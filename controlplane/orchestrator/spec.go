// Package orchestrator parses, validates, and executes orchestration
// specs — YAML files that chain agent runs together.
//
// This file owns the *language*: the Spec type, the YAML parse, and
// the structural validator. It deliberately doesn't know how to
// execute anything — that's the engine's job. Splitting them this way
// lets the API surface (CRUD endpoints, library list) parse + display
// specs without dragging in the dispatch / federation machinery.
//
// See docs/orchestrations.md for the operator-facing language
// reference. The validator here is the canonical source of truth for
// what the docs allow.
package orchestrator

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// SpecVersion is the language version this CP understands. Specs with
// a higher version are rejected at parse time so an operator gets a
// clear error rather than silent misbehaviour after a CP downgrade.
const SpecVersion = 1

// Spec is the parsed orchestration definition. The `Body` field holds
// the markdown that follows the YAML frontmatter — operator notes,
// not part of the language. The engine never reads it.
type Spec struct {
	Name        string              `yaml:"name"`
	Description string              `yaml:"description"`
	Version     int                 `yaml:"version,omitempty"`
	Trigger     TriggerSpec         `yaml:"trigger,omitempty"`
	Inputs      map[string]InputDef `yaml:"inputs,omitempty"`
	Defaults    StepDefaults        `yaml:"defaults,omitempty"`
	Steps       []StepSpec          `yaml:"steps"`

	// Body is the markdown body after the closing `---`. Stored on the
	// spec so the library detail panel can render documentation.
	Body string `yaml:"-"`
}

type TriggerSpec struct {
	On     string `yaml:"on,omitempty"` // manual | finding | cron — defaults to manual
	Filter string `yaml:"filter,omitempty"`
	Cron   string `yaml:"cron,omitempty"`
}

type InputDef struct {
	Type     string   `yaml:"type"`              // string | int | bool
	Required bool     `yaml:"required,omitempty"`
	Default  any      `yaml:"default,omitempty"`
	Enum     []string `yaml:"enum,omitempty"`
}

type StepDefaults struct {
	Timeout         Duration `yaml:"timeout,omitempty"`
	ContinueOnError bool     `yaml:"continue_on_error,omitempty"`
	CP              string   `yaml:"cp,omitempty"`
	// Dispatch is the default runtime for steps that don't override
	// it: "" (auto, default), "tunnel", or "jobs". See spec.go
	// EffectiveDispatch for resolution.
	Dispatch string `yaml:"dispatch,omitempty"`
}

type StepSpec struct {
	ID    string `yaml:"id"`
	Agent string `yaml:"agent"`
	// Node targets one host. Mutually exclusive with Nodes (the
	// fan-out variant). Either field can be templated; the engine
	// resolves at run time.
	Node string `yaml:"node,omitempty"`
	// Nodes targets a set of hosts and runs the step on every one
	// in parallel. Each host gets its own dispatched Run; the engine
	// aggregates outputs into the step's `result.byNode` map and the
	// flat `findings` list, so chained templates can read either the
	// combined view or per-node details.
	Nodes  []string       `yaml:"nodes,omitempty"`
	CP     string         `yaml:"cp,omitempty"`
	Prompt string         `yaml:"prompt"`
	Inputs map[string]any `yaml:"inputs,omitempty"`
	Timeout Duration `yaml:"timeout,omitempty"`
	// ContinueOnError uses a pointer so we can distinguish "unset"
	// (inherit from defaults) from "explicitly false" (override the
	// default's `true`).
	ContinueOnError *bool  `yaml:"continue_on_error,omitempty"`
	Approval        string `yaml:"approval,omitempty"` // "" | "required"
	When            string `yaml:"when,omitempty"`
	// Dispatch overrides the orchestration's default runtime for
	// just this step: "" (auto), "tunnel" (require + auto-start
	// if needed), "jobs" (pull queue). When set to "tunnel" but no
	// tunnel is connected, the engine sends a start_tunnel job to
	// the host's jobs runtime and waits for the tunnel to register.
	Dispatch string `yaml:"dispatch,omitempty"`

	// Actions is the allowlist of CP-side mutations this step is
	// permitted to request. The agent's orchestration_result emits
	// an `actions:` array; the engine cross-checks each entry's
	// kind against this list before applying. Empty list = the step
	// can request nothing (default deny). See ActionKind* constants.
	Actions []string `yaml:"actions,omitempty"`

	// Data declares structured CP-side data the engine should fetch
	// and bind into the prompt template before dispatch. Each map
	// entry becomes a `{{data.<name>}}` binding visible to the
	// prompt + when expressions. Replaces the old "have the agent
	// curl /api/findings" pattern: the engine handles auth + rate
	// limiting + audit, the agent's prompt stays focused on the
	// reasoning step.
	//
	// Example:
	//
	//   data:
	//     findings:
	//       query: findings.list
	//       params: { state: queue, severity: [INFO, LOW], limit: 50 }
	//     summary:
	//       query: findings.summary
	//
	// See orchestrator.DataResolver and the api-package query
	// registry for the supported queries.
	Data map[string]DataSource `yaml:"data,omitempty"`

	// Phase 22.3 — step kind. Default "" = legacy agent dispatch (today's
	// behavior). "meeting" runs the participants sequentially with the
	// Meeting block's config; the engine's dispatch path branches on this.
	Kind string `yaml:"kind,omitempty"`

	// Meeting carries the meeting-specific configuration when Kind == "meeting".
	Meeting *MeetingSpec `yaml:"meeting,omitempty"`

	// WarBridge marks a meeting step's resulting meeting_minutes finding
	// as needing immediate operator attention. Tagged "war-bridge" so the
	// dashboard renders the red banner. Only meaningful when Kind == "meeting".
	WarBridge bool `yaml:"war_bridge,omitempty"`
}

// MeetingSpec is the configuration for a meeting-kind step. Each
// participant is dispatched sequentially against the trigger payload
// plus all prior participants' outputs; the synthesizer receives the
// full conversation and emits a meeting_minutes finding.
type MeetingSpec struct {
	Participants []string `yaml:"participants"`
	Synthesizer  string   `yaml:"synthesizer"`
	// ContextWindow declares what context the participants see beyond
	// the trigger. v1 supports "trigger" (just the triggering finding)
	// and "trigger+last_5_findings" (also include the agent's 5 most
	// recent findings). Empty = "trigger".
	ContextWindow string `yaml:"context_window,omitempty"`
}

// DataSource is a single declarative read in a step's `data:` block.
// Query is a registered handler name (e.g. "findings.list"); Params is
// a free-form map the handler validates. Both are persisted to the
// step record on dispatch so a run can be replayed against the same
// data the agent originally saw.
type DataSource struct {
	Query  string         `yaml:"query"`
	Params map[string]any `yaml:"params,omitempty"`
}

// EffectiveNodes returns the resolved set of node targets for a step,
// regardless of whether the operator wrote `node:` or `nodes:`. An
// empty result means the step is a local-CP-only step (no node
// dispatch — used for steps that just manipulate CP state).
func (s *StepSpec) EffectiveNodes() []string {
	if len(s.Nodes) > 0 {
		return s.Nodes
	}
	if s.Node != "" {
		return []string{s.Node}
	}
	return nil
}

// Duration accepts Go-style duration strings ("5m", "30s", "1h"). The
// custom unmarshal exists so the YAML stays human-readable; the engine
// just calls time.Duration(d) when scheduling timeouts.
type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return err
	}
	if s == "" {
		return nil
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("bad duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) String() string { return time.Duration(d).String() }

// Parse takes the full orchestration file (frontmatter + optional
// markdown body) and returns the validated Spec. The body is stripped
// off the YAML and stashed on Spec.Body for the library UI.
func Parse(content string) (*Spec, error) {
	fm, body := splitFrontmatter(content)
	if fm == "" {
		return nil, errors.New("orchestration: missing YAML frontmatter — wrap the spec in `---` blocks")
	}
	var spec Spec
	if err := yaml.Unmarshal([]byte(fm), &spec); err != nil {
		return nil, fmt.Errorf("orchestration: parse frontmatter: %w", err)
	}
	if spec.Version == 0 {
		spec.Version = SpecVersion
	}
	spec.Body = body
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	return &spec, nil
}

// Validate checks the spec for structural issues that would make it
// unrunnable. The engine assumes Validate has already returned nil.
//
// What we check:
//   - name + description present, name slug-shaped
//   - spec version supported
//   - at least one step
//   - step ids unique, slug-shaped, non-empty agent + prompt
//   - approval is empty or "required"
//   - trigger.on in the allowed set; cron requires `cron`; finding
//     requires `filter`
//   - templated strings (prompt, node, when) only reference step ids
//     that already exist in the linear order — forward references
//     would always resolve to empty in v1's sequential execution
//
// What we DON'T check (engine territory):
//   - whether the named agent exists in the agent library
//   - whether the node selector resolves to a real node
//   - whether the CP id is reachable
//   - whether the `when` expression is satisfiable
func (s *Spec) Validate() error {
	if s.Name == "" {
		return errors.New("orchestration: name is required")
	}
	if !nameRE.MatchString(s.Name) {
		return fmt.Errorf("orchestration: name %q must match %s", s.Name, nameRE.String())
	}
	if s.Description == "" {
		return errors.New("orchestration: description is required")
	}
	if s.Version != SpecVersion {
		return fmt.Errorf("orchestration: unsupported spec version %d (this CP supports %d)", s.Version, SpecVersion)
	}
	if len(s.Steps) == 0 {
		return errors.New("orchestration: at least one step is required")
	}

	// Trigger validation.
	if s.Trigger.On == "" {
		s.Trigger.On = "manual"
	}
	switch s.Trigger.On {
	case "manual":
		// nothing else required
	case "finding":
		if s.Trigger.Filter == "" {
			return errors.New("trigger.on=finding requires trigger.filter")
		}
	case "cron":
		if s.Trigger.Cron == "" {
			return errors.New("trigger.on=cron requires trigger.cron")
		}
	default:
		return fmt.Errorf("trigger.on must be manual|finding|cron, got %q", s.Trigger.On)
	}

	// Inputs validation.
	for name, def := range s.Inputs {
		if !inputNameRE.MatchString(name) {
			return fmt.Errorf("inputs.%s: name must match %s", name, inputNameRE.String())
		}
		switch def.Type {
		case "string", "int", "bool":
		case "":
			return fmt.Errorf("inputs.%s: type is required", name)
		default:
			return fmt.Errorf("inputs.%s: type must be string|int|bool, got %q", name, def.Type)
		}
	}

	// Step validation. seenIDs maps step id -> idx of definition. The
	// templated-reference check uses it to forbid forward refs.
	seenIDs := map[string]int{}
	for i, st := range s.Steps {
		if st.ID == "" {
			return fmt.Errorf("step %d: id is required", i)
		}
		if !stepIDRE.MatchString(st.ID) {
			return fmt.Errorf("step %d: id %q must match %s", i, st.ID, stepIDRE.String())
		}
		if prev, dup := seenIDs[st.ID]; dup {
			return fmt.Errorf("step %d: id %q duplicates step %d", i, st.ID, prev)
		}
		// Phase 22.3 — kind-aware validation. Default-kind steps still
		// require a top-level agent + prompt; meeting steps require a
		// meeting block (participants + synthesizer) and forbid the
		// top-level agent (which would silently be ignored).
		switch st.Kind {
		case "", "agent":
			if st.Agent == "" {
				return fmt.Errorf("step %q: agent is required for default-kind steps", st.ID)
			}
			if st.Prompt == "" {
				return fmt.Errorf("step %q: prompt is required", st.ID)
			}
		case "meeting":
			if st.Meeting == nil {
				return fmt.Errorf("step %q: kind=meeting requires a meeting: block", st.ID)
			}
			if len(st.Meeting.Participants) == 0 {
				return fmt.Errorf("step %q: meeting requires at least one participant", st.ID)
			}
			if st.Meeting.Synthesizer == "" {
				return fmt.Errorf("step %q: meeting requires a synthesizer", st.ID)
			}
			if st.Agent != "" {
				return fmt.Errorf("step %q: meeting steps must not declare a top-level agent (use participants/synthesizer)", st.ID)
			}
			switch st.Meeting.ContextWindow {
			case "", "trigger", "trigger+last_5_findings":
				// ok
			default:
				return fmt.Errorf("step %q: meeting.context_window must be empty|trigger|trigger+last_5_findings, got %q", st.ID, st.Meeting.ContextWindow)
			}
		default:
			return fmt.Errorf("step %q: unknown kind %q (allowed: agent, meeting)", st.ID, st.Kind)
		}
		if st.Approval != "" && st.Approval != "required" {
			return fmt.Errorf("step %q: approval must be 'required' or omitted, got %q", st.ID, st.Approval)
		}
		if st.Node != "" && len(st.Nodes) > 0 {
			return fmt.Errorf("step %q: use either `node:` (single) or `nodes:` (fan-out), not both", st.ID)
		}
		if st.Dispatch != "" && st.Dispatch != "auto" && st.Dispatch != "tunnel" && st.Dispatch != "jobs" {
			return fmt.Errorf("step %q: dispatch must be auto|tunnel|jobs or omitted, got %q", st.ID, st.Dispatch)
		}
		// `actions:` allowlist — every entry must be a registered
		// kind. Operators see the full list in the error so they
		// can fix the typo without grepping the source.
		for _, k := range st.Actions {
			if !IsAllowedActionKind(k) {
				return fmt.Errorf("step %q: actions[%q] not a registered kind (allowed: %v)", st.ID, k, AllowedActionKinds)
			}
		}
		// `data:` block — bind names must match the same identifier
		// shape we use elsewhere; the query name uses the
		// "namespace.method" convention and is checked at run time
		// against the data resolver's registry.
		for name, src := range st.Data {
			if !inputNameRE.MatchString(name) {
				return fmt.Errorf("step %q: data.%s: name must match %s", st.ID, name, inputNameRE.String())
			}
			if !dataQueryRE.MatchString(src.Query) {
				return fmt.Errorf("step %q: data.%s.query %q must match %s", st.ID, name, src.Query, dataQueryRE.String())
			}
		}
		// Forward-reference check: a step cannot template-reference a
		// later step (in linear v1, that's always empty).
		for _, field := range []struct{ name, value string }{
			{"prompt", st.Prompt},
			{"node", st.Node},
			{"when", st.When},
		} {
			if field.value == "" {
				continue
			}
			if err := validateTemplateRefs(field.value, seenIDs); err != nil {
				return fmt.Errorf("step %q.%s: %w", st.ID, field.name, err)
			}
		}
		seenIDs[st.ID] = i
	}

	return nil
}

// LookupStep returns the step with the given id, or nil. Convenience
// for the engine + UI when they have a step id and need its spec.
func (s *Spec) LookupStep(id string) *StepSpec {
	for i := range s.Steps {
		if s.Steps[i].ID == id {
			return &s.Steps[i]
		}
	}
	return nil
}

// EffectiveTimeout returns the step's timeout, falling back to the
// orchestration-level default, or 0 (no limit) if neither is set.
func (s *Spec) EffectiveTimeout(step *StepSpec) time.Duration {
	if step.Timeout != 0 {
		return time.Duration(step.Timeout)
	}
	return time.Duration(s.Defaults.Timeout)
}

// EffectiveContinueOnError applies the inheritance rule: explicit
// step value wins, otherwise default, otherwise false.
func (s *Spec) EffectiveContinueOnError(step *StepSpec) bool {
	if step.ContinueOnError != nil {
		return *step.ContinueOnError
	}
	return s.Defaults.ContinueOnError
}

// EffectiveCP applies the inheritance rule for CP selectors. The
// engine resolves the special values ("local", "*", "from-step-N.cp")
// — Validate just checks the value is non-empty after inheritance.
func (s *Spec) EffectiveCP(step *StepSpec) string {
	if step.CP != "" {
		return step.CP
	}
	if s.Defaults.CP != "" {
		return s.Defaults.CP
	}
	return "local"
}

// EffectiveDispatch resolves the runtime preference: per-step wins,
// then orchestration default, then "" (engine picks). Node-level
// preference is layered on top by the engine itself, since the spec
// can't see the node row at parse time.
func (s *Spec) EffectiveDispatch(step *StepSpec) string {
	if step.Dispatch != "" {
		return step.Dispatch
	}
	return s.Defaults.Dispatch
}

// ── helpers ──────────────────────────────────────────────────────────

var (
	// Slug constraints picked to match the existing daimon/agent file
	// naming convention so an orchestration named `post-finding-deep-dive`
	// looks at home next to a daimon named `instance-threat`.
	nameRE      = regexp.MustCompile(`^[a-z][a-z0-9-]*[a-z0-9]$`)
	stepIDRE    = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	inputNameRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	// dataQueryRE matches "namespace.method" handler identifiers
	// (e.g. "findings.list", "orchestration-runs.list"). The dot
	// separator is required to make the registry surface obvious
	// at a glance.
	dataQueryRE = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)+$`)

	// Captures `{{stepID.field…}}` references inside templated strings.
	// Only the leading identifier matters for the forward-ref check;
	// the engine's evaluator handles the rest.
	templateRefRE = regexp.MustCompile(`\{\{\s*([a-z][a-z0-9_-]*)\b`)
)

// splitFrontmatter peels a leading `---\n…\n---\n` YAML block off the
// content, returning (frontmatter, body). Mirrors the helper in
// agent_library.go but lives here so this package can stay free of
// api-package imports.
func splitFrontmatter(content string) (frontmatter, body string) {
	const sep = "---"
	t := strings.TrimLeft(content, " \t\r\n")
	if !strings.HasPrefix(t, sep) {
		return "", content
	}
	rest := t[len(sep):]
	rest = strings.TrimLeft(rest, " \t")
	if !strings.HasPrefix(rest, "\n") && !strings.HasPrefix(rest, "\r\n") {
		return "", content
	}
	rest = strings.TrimLeft(rest, "\r\n")
	end := strings.Index(rest, "\n"+sep)
	if end < 0 {
		// Try CRLF fallback.
		end = strings.Index(rest, "\r\n"+sep)
		if end < 0 {
			return "", content
		}
	}
	frontmatter = rest[:end]
	tail := strings.TrimLeft(rest[end:], "\r\n")
	tail = strings.TrimPrefix(tail, sep)
	body = strings.TrimLeft(tail, " \t\r\n")
	return frontmatter, body
}

// validateTemplateRefs rejects references to step ids that haven't
// been defined yet (typo or forward ref). Reserved bindings —
// `trigger`, `defaults`, `inputs` — pass through.
func validateTemplateRefs(s string, seen map[string]int) error {
	matches := templateRefRE.FindAllStringSubmatch(s, -1)
	for _, m := range matches {
		ref := m[1]
		switch ref {
		case "trigger", "defaults", "inputs", "data":
			continue
		}
		if _, ok := seen[ref]; !ok {
			return fmt.Errorf("template references step %q which is not defined yet (or doesn't exist)", ref)
		}
	}
	return nil
}
