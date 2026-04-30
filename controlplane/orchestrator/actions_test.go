package orchestrator

import "testing"

func TestParseActions_ReflectWithLessons(t *testing.T) {
	in := []any{
		map[string]any{"kind": "reflect_with_lessons", "lessons": []any{"keep grep tight", "validate before delete"}},
	}
	got, err := ParseActions(in)
	if err != nil {
		t.Fatalf("ParseActions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 action; got %d", len(got))
	}
	if got[0].Kind != ActionReflectWithLessons {
		t.Errorf("kind = %q, want %q", got[0].Kind, ActionReflectWithLessons)
	}
	if len(got[0].Lessons) != 2 {
		t.Errorf("expected 2 lessons; got %v", got[0].Lessons)
	}
}

func TestParseActions_ReflectWithLessons_RejectsEmpty(t *testing.T) {
	in := []any{
		map[string]any{"kind": "reflect_with_lessons", "lessons": []any{}},
	}
	if _, err := ParseActions(in); err == nil {
		t.Errorf("expected error for empty lessons")
	}
}
