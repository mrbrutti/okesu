package agent

import (
	"strings"
	"testing"
)

func TestPrependLessons_NoneNoChange(t *testing.T) {
	got := PrependLessons("base prompt", nil)
	if got != "base prompt" {
		t.Errorf("expected pass-through; got %q", got)
	}
}

func TestPrependLessons_AddsHeaderAndBullets(t *testing.T) {
	got := PrependLessons("base prompt", []string{"first lesson", "second lesson"})
	if !strings.HasPrefix(got, "## Lessons from prior runs") {
		t.Errorf("expected lessons header at start; got %q", got)
	}
	if !strings.Contains(got, "first lesson") || !strings.Contains(got, "second lesson") {
		t.Errorf("missing lessons in output: %q", got)
	}
	if !strings.HasSuffix(got, "base prompt") {
		t.Errorf("base prompt should be suffix; got %q", got)
	}
}

func TestPrependLessons_TrimsWhitespaceOnlyEntries(t *testing.T) {
	// Whitespace-only lessons are useless — drop them rather than
	// emit empty bullets.
	got := PrependLessons("base", []string{"real lesson", "   ", ""})
	if strings.Count(got, "- ") != 1 {
		t.Errorf("expected 1 bullet (whitespace-only filtered); got: %q", got)
	}
}
