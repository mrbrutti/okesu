package orchestrator

import (
	"strings"
	"testing"
)

// fakeApplier is the minimal ActionApplier impl used by the
// auto-lesson tests. Records the calls that hit each method so
// the test asserts which side-effects fired.
type fakeApplier struct {
	statusUpdates   []string // each entry: "<finding_id>:<status>:<reason>"
	severitySets    []string // "<finding_id>:<severity>:<reason>"
	lessons         []string // "<agent>:<text>"
	findingToAgent  map[int64]string
	findingErr      error
}

func (a *fakeApplier) UpdateFindingStatus(findingID int64, status, reason string, runID int64, stepID string) error {
	a.statusUpdates = append(a.statusUpdates, fmtT(findingID, status, reason))
	return nil
}
func (a *fakeApplier) AddFindingTag(findingID int64, tag, reason string, runID int64, stepID string) error {
	return nil
}
func (a *fakeApplier) RemoveFindingTag(findingID int64, tag, reason string, runID int64, stepID string) error {
	return nil
}
func (a *fakeApplier) SetFindingSeverityOverride(findingID int64, severity, reason string, runID int64, stepID string) error {
	a.severitySets = append(a.severitySets, fmtT(findingID, severity, reason))
	return nil
}
func (a *fakeApplier) LinkRunToFinding(findingID, runID int64, stepID, reason string) error {
	return nil
}
func (a *fakeApplier) RecordAgentLesson(agentName, text string, runID int64, stepID string) error {
	a.lessons = append(a.lessons, agentName+":"+text)
	return nil
}
func (a *fakeApplier) EscalateRun(runID int64, reason, severity string) error  { return nil }
func (a *fakeApplier) EnrichIOC(iocID, runID int64, stepID string) error       { return nil }

// FindingAgentLookup impl — controls whether auto-lessons fire.
func (a *fakeApplier) FindingAgent(findingID int64) (string, error) {
	if a.findingErr != nil {
		return "", a.findingErr
	}
	return a.findingToAgent[findingID], nil
}

func fmtT(id int64, a, b string) string {
	return joinNonEmpty(":", []string{itoa(id), a, b})
}

func itoa(i int64) string {
	// strconv.FormatInt is overkill for the tiny ids the tests use.
	if i == 0 {
		return "0"
	}
	var b []byte
	neg := i < 0
	if neg {
		i = -i
	}
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func joinNonEmpty(sep string, parts []string) string {
	keep := parts[:0]
	for _, p := range parts {
		if p != "" {
			keep = append(keep, p)
		}
	}
	return strings.Join(keep, sep)
}

// TestAutoLesson_OnFalsePositive checks that closing a finding as
// false_positive with a reason auto-records a lesson on the
// originating agent (not the orchestration step's agent).
func TestAutoLesson_OnFalsePositive(t *testing.T) {
	app := &fakeApplier{
		findingToAgent: map[int64]string{42: "edr"},
	}
	e := &Engine{applier: app}
	step := StepSpec{ID: "classify", Agent: "investigator"}

	err := e.dispatchAction(7, step, Action{
		Kind:      ActionUpdateFindingStatus,
		FindingID: 42,
		Status:    "false_positive",
		Reason:    "matches known noise pattern: cron retry loop",
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(app.statusUpdates) != 1 {
		t.Fatalf("status updates = %v, want 1 entry", app.statusUpdates)
	}
	if len(app.lessons) != 1 {
		t.Fatalf("lessons = %v, want 1 auto-lesson", app.lessons)
	}
	got := app.lessons[0]
	if !strings.HasPrefix(got, "edr:auto:") {
		t.Errorf("lesson recorded on wrong agent or wrong shape: %q (want prefix 'edr:auto:')", got)
	}
	if !strings.Contains(got, "matches known noise pattern") {
		t.Errorf("lesson body missing reason: %q", got)
	}
}

// TestAutoLesson_OnSeverityDrop checks that dropping a finding's
// severity to INFO/LOW with reasoning auto-records a lesson.
// Promotions to HIGHER severity should NOT.
func TestAutoLesson_OnSeverityDrop(t *testing.T) {
	app := &fakeApplier{
		findingToAgent: map[int64]string{42: "instance-threat"},
	}
	e := &Engine{applier: app}
	step := StepSpec{ID: "classify", Agent: "investigator"}

	// Drop to INFO → auto-lesson
	if err := e.dispatchAction(7, step, Action{
		Kind:      ActionSetFindingSeverityOverride,
		FindingID: 42,
		Severity:  "INFO",
		Reason:    "scanner is internal monitoring, not exfil",
	}); err != nil {
		t.Fatalf("INFO dispatch: %v", err)
	}
	if len(app.lessons) != 1 {
		t.Fatalf("after INFO drop: lessons = %v, want 1", app.lessons)
	}

	// Promote to CRITICAL → no auto-lesson (correctly emitted before)
	if err := e.dispatchAction(8, step, Action{
		Kind:      ActionSetFindingSeverityOverride,
		FindingID: 42,
		Severity:  "CRITICAL",
		Reason:    "actually a real exploit",
	}); err != nil {
		t.Fatalf("CRITICAL dispatch: %v", err)
	}
	if len(app.lessons) != 1 {
		t.Fatalf("after CRITICAL promotion: lessons = %v, want still 1 (no lesson on promotion)", app.lessons)
	}
}

// TestAutoLesson_NoEmittingAgent silently skips when the finding
// has no agent stamped (e.g. operator-created finding).
func TestAutoLesson_NoEmittingAgent(t *testing.T) {
	app := &fakeApplier{
		findingToAgent: map[int64]string{}, // no mapping
	}
	e := &Engine{applier: app}
	step := StepSpec{ID: "classify", Agent: "investigator"}

	err := e.dispatchAction(7, step, Action{
		Kind:      ActionUpdateFindingStatus,
		FindingID: 42,
		Status:    "false_positive",
		Reason:    "operator-created finding",
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(app.lessons) != 0 {
		t.Errorf("lessons = %v, want 0 (no emitting agent → no lesson)", app.lessons)
	}
}

// TestAutoLesson_EmptyReasonNoLesson — silence is signal too.
// Without a reason there's nothing useful to teach the agent, so
// don't pollute its lesson slots with empty entries.
func TestAutoLesson_EmptyReasonNoLesson(t *testing.T) {
	app := &fakeApplier{findingToAgent: map[int64]string{42: "edr"}}
	e := &Engine{applier: app}
	step := StepSpec{ID: "classify", Agent: "investigator"}

	if err := e.dispatchAction(7, step, Action{
		Kind:      ActionUpdateFindingStatus,
		FindingID: 42,
		Status:    "false_positive",
		Reason:    "",
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(app.lessons) != 0 {
		t.Errorf("lessons = %v, want 0 (empty reason → no lesson)", app.lessons)
	}
}
