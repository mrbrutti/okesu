// Meeting executor — Phase 22.3.
//
// A "meeting" step runs N participants sequentially, then a single
// synthesizer that distils the conversation into one
// orchestration_result finding (subtype=meeting_minutes). Each
// participant's prompt is the step's templated body prefaced with a
// "## Conversation so far" section showing every prior speaker's
// output tail; the synthesizer additionally sees each participant's
// structured findings and gets a directive instructing it to emit
// the meeting_minutes finding rather than chiming in as another
// participant.
//
// The function returns the synthesizer's DispatchResult so the
// engine's existing post-step distillation (result_json, output
// tail, action application) keeps working uniformly with default-
// kind steps. Findings on the synthesizer's result are post-
// processed here:
//
//   - any orchestration_result without an explicit Subtype is set to
//     "meeting_minutes" so the dashboard pickup is deterministic;
//   - when step.WarBridge is true, every finding gets the "war-bridge"
//     tag so the operator UI's banner picks it up.
//
// We intentionally do NOT touch participants' findings — those go
// into the run's findings list as-is, and the synthesizer's output
// is what downstream steps key off via {{stepN.result}}.

package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// participantOutput is the per-participant snapshot the prompt
// builders read. Private to this file so it can't leak into other
// engine paths by accident.
type participantOutput struct {
	Agent      string
	OutputTail string
	Findings   []DispatchedFinding
}

// runMeetingStep executes a kind=meeting step and returns the
// synthesizer's DispatchResult (the engine treats it as the step's
// canonical result for distillation + binding purposes).
//
// `spec` and `step` come from the engine's run loop so we can resolve
// the effective timeout + dispatch mode through the same helpers
// every other step path uses; passing them in keeps this file from
// having to know how the engine loaded the spec.
func (e *Engine) runMeetingStep(
	ctx context.Context,
	spec *Spec,
	step StepSpec,
	cpSel string,
	env Env,
) (DispatchResult, error) {
	if step.Meeting == nil {
		return DispatchResult{}, fmt.Errorf("meeting step %q has no meeting block", step.ID)
	}
	mtg := step.Meeting

	// TODO(phase-22.x): MeetingSpec.ContextWindow is parsed and
	// validated ("trigger" | "trigger+last_5_findings") but not yet
	// consumed. v1 always behaves as "trigger". Implementing
	// "trigger+last_5_findings" requires querying the agent's recent
	// findings via the data resolver and prepending them to each
	// participant's prompt — defer until a real orchestration needs it.
	_ = mtg.ContextWindow

	timeout := spec.EffectiveTimeout(&step)
	dispatchMode := spec.EffectiveDispatch(&step)

	// One dispatch helper — the engine has already timeout-wrapped
	// `ctx` for the step, so each participant + the synthesizer
	// share the same overall step deadline. If we ever want
	// per-participant deadlines, that's a spec change first.
	dispatchOne := func(agent, prompt string) (DispatchResult, error) {
		return e.dispatcher.Dispatch(ctx, DispatchRequest{
			StepID:       step.ID,
			AgentName:    agent,
			CPInstanceID: cpSel,
			Prompt:       prompt,
			Inputs:       step.Inputs,
			Timeout:      timeout,
			DispatchMode: dispatchMode,
		})
	}

	conversation := make([]participantOutput, 0, len(mtg.Participants))
	for _, p := range mtg.Participants {
		prompt := buildParticipantPrompt(step.Prompt, env, conversation)
		res, err := dispatchOne(p, prompt)
		if err != nil {
			return DispatchResult{}, fmt.Errorf("meeting participant %s: %w", p, err)
		}
		conversation = append(conversation, participantOutput{
			Agent:      p,
			OutputTail: res.OutputTail,
			Findings:   res.Findings,
		})
	}

	synthPrompt := buildSynthesizerPrompt(step.Prompt, env, conversation, step.WarBridge)
	res, err := dispatchOne(mtg.Synthesizer, synthPrompt)
	if err != nil {
		return DispatchResult{}, fmt.Errorf("meeting synthesizer %s: %w", mtg.Synthesizer, err)
	}
	// Defensive in-memory fallback for downstream env-binding: if the
	// agent forgot to set subtype/tags in its JSONL, patch the parsed
	// DispatchedFinding so subsequent steps that read {{<step>.findings}}
	// see consistent values. The DB row itself depends on the agent's
	// JSONL — that's what eventpipeline ingests. The synthesizer prompt
	// (buildSynthesizerPrompt) carries the explicit instructions so a
	// well-behaved LLM emits subtype + tags itself.
	for i := range res.Findings {
		f := &res.Findings[i]
		if f.Category == OrchestrationResultCategory && f.Subtype == "" {
			f.Subtype = "meeting_minutes"
		}
		if step.WarBridge {
			f.Tags = appendTagOnce(f.Tags, "war-bridge")
		}
	}
	return res, nil
}

// buildParticipantPrompt renders the step's prompt template against
// env and prepends a "Conversation so far" section if any prior
// participants have spoken. Render errors are not fatal — we fall
// back to the unrendered prompt body so a typo in a template
// reference doesn't drop the whole meeting; the agent will still see
// the conversation and can reason about what's missing.
func buildParticipantPrompt(stepPrompt string, env Env, prior []participantOutput) string {
	var b strings.Builder
	if len(prior) > 0 {
		b.WriteString("## Conversation so far\n\n")
		for _, p := range prior {
			b.WriteString("### " + p.Agent + "\n\n")
			b.WriteString(p.OutputTail)
			b.WriteString("\n\n")
		}
		b.WriteString("---\n\n")
	}
	rendered, err := Render(stepPrompt, env)
	if err != nil {
		b.WriteString(stepPrompt)
	} else {
		b.WriteString(rendered)
	}
	return b.String()
}

// buildSynthesizerPrompt is similar but adds:
//   - the "Conversation transcript" header instead of "so far" so the
//     synthesizer reads it as a closed corpus, not an open thread;
//   - structured outputs from each participant rendered as JSON so the
//     synthesizer can quote them precisely;
//   - a final "Synthesize" directive so the LLM emits a
//     meeting_minutes orchestration_result rather than chiming in;
//   - explicit subtype + tag instructions so the agent's JSONL output
//     carries the right values into the eventpipeline → DB write
//     (the in-memory mutation in runMeetingStep is a fallback for the
//     env-binding path; the DB row depends on what the agent emits).
func buildSynthesizerPrompt(stepPrompt string, env Env, conv []participantOutput, warBridge bool) string {
	var b strings.Builder
	b.WriteString("## Conversation transcript\n\n")
	for _, p := range conv {
		b.WriteString("### " + p.Agent + "\n\n")
		b.WriteString(p.OutputTail)
		b.WriteString("\n\n")
		if len(p.Findings) > 0 {
			b.WriteString("Structured outputs:\n")
			for _, f := range p.Findings {
				if blob, err := json.Marshal(f); err == nil {
					b.WriteString(string(blob))
					b.WriteString("\n")
				}
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("---\n\n")
	rendered, err := Render(stepPrompt, env)
	if err != nil {
		b.WriteString(stepPrompt)
	} else {
		b.WriteString(rendered)
	}
	b.WriteString("\n\nSynthesize the conversation above into a single orchestration_result ")
	b.WriteString("finding. Set `subtype` to `\"meeting_minutes\"` so the dashboard renders ")
	b.WriteString("it as meeting output. Include in `attributes`: agenda (one-liner), positions ")
	b.WriteString("(per participant, one sentence each), action_items (array of strings), and ")
	b.WriteString("decision (string).")
	if warBridge {
		b.WriteString(" Also set `tags: [\"war-bridge\"]` on the finding — this signals ")
		b.WriteString("immediate operator attention and surfaces the result in the dashboard's ")
		b.WriteString("red banner. Do not skip this tag.")
	}
	return b.String()
}

// appendTagOnce adds tag to a comma-separated tags string, dedup'd.
// Mirrors how the rest of the codebase stores tags on a finding row;
// we intentionally don't import a tag util here to keep this file
// free of cross-package dependencies.
func appendTagOnce(tags, tag string) string {
	if tags == "" {
		return tag
	}
	for _, t := range strings.Split(tags, ",") {
		if strings.TrimSpace(t) == tag {
			return tags
		}
	}
	return tags + "," + tag
}

