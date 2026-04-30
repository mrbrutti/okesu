package db

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRecordAgentLesson_InsertsRow(t *testing.T) {
	s := openTempStore(t)
	if err := s.RecordAgentLesson("investigator", "be specific about evidence", 0, ""); err != nil {
		t.Fatalf("RecordAgentLesson: %v", err)
	}
	got, err := s.ListAgentLessons("investigator", 10)
	if err != nil {
		t.Fatalf("ListAgentLessons: %v", err)
	}
	if len(got) != 1 || got[0].Text != "be specific about evidence" {
		t.Errorf("expected one lesson with text; got %+v", got)
	}
}

func TestListAgentLessons_NewestFirst(t *testing.T) {
	s := openTempStore(t)
	for _, txt := range []string{"first", "second", "third"} {
		if err := s.RecordAgentLesson("investigator", txt, 0, ""); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.ListAgentLessons("investigator", 10)
	if len(got) != 3 || got[0].Text != "third" {
		t.Errorf("expected newest-first; got %+v", got)
	}
}

func TestPruneAgentLessons_KeepsTopN(t *testing.T) {
	s := openTempStore(t)
	for i := 0; i < 15; i++ {
		s.RecordAgentLesson("investigator", "lesson", 0, "")
	}
	if err := s.PruneAgentLessons("investigator", 10); err != nil {
		t.Fatalf("PruneAgentLessons: %v", err)
	}
	got, _ := s.ListAgentLessons("investigator", 100)
	if len(got) != 10 {
		t.Errorf("expected 10 after prune; got %d", len(got))
	}
}

func TestRecordAgentLesson_TruncatesOversizeText(t *testing.T) {
	s := openTempStore(t)
	long := make([]byte, 500)
	for i := range long {
		long[i] = 'x'
	}
	if err := s.RecordAgentLesson("investigator", string(long), 0, ""); err != nil {
		t.Fatalf("RecordAgentLesson: %v", err)
	}
	got, _ := s.ListAgentLessons("investigator", 1)
	if len(got[0].Text) != 200 {
		t.Errorf("expected lesson truncated to 200; got %d chars", len(got[0].Text))
	}
}

func TestRecordAgentLesson_TruncatesUTF8Cleanly(t *testing.T) {
	s := openTempStore(t)
	// "é" is 2 UTF-8 bytes. Repeating it produces a string that, if
	// naively sliced at byte 200, would split the rune at the boundary
	// and leave invalid bytes. We assert the persisted text is valid UTF-8
	// and ≤ 200 bytes — the truncation must clip back to a rune boundary.
	long := strings.Repeat("é", 200) // 400 bytes
	if err := s.RecordAgentLesson("investigator", long, 0, ""); err != nil {
		t.Fatalf("RecordAgentLesson: %v", err)
	}
	got, _ := s.ListAgentLessons("investigator", 1)
	if len(got[0].Text) > 200 {
		t.Errorf("expected ≤200 bytes; got %d", len(got[0].Text))
	}
	if !utf8.ValidString(got[0].Text) {
		t.Errorf("truncation produced invalid UTF-8: %q", got[0].Text)
	}
}
