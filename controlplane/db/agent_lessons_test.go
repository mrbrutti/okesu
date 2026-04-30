package db

import (
	"testing"
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
