package db

import (
	"testing"
)

func TestAddIOCRelationship_Idempotent(t *testing.T) {
	s := openTempStore(t)
	a, _, _ := s.UpsertIOC(&IOCUpsert{Kind: "domain", Value: "evil.example.com", NormalizedValue: "evil.example.com"})
	b, _, _ := s.UpsertIOC(&IOCUpsert{Kind: "ipv4", Value: "1.2.3.4", NormalizedValue: "1.2.3.4"})
	if err := s.AddIOCRelationship(&IOCRelationshipInsert{
		SubjectID: a, Predicate: "resolves-to", ObjectID: b, Source: "enrichment", Confidence: "high",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddIOCRelationship(&IOCRelationshipInsert{
		SubjectID: a, Predicate: "resolves-to", ObjectID: b, Source: "enrichment", Confidence: "high",
	}); err != nil {
		t.Errorf("expected idempotent; got %v", err)
	}
	got, _ := s.ListIOCRelationships(a)
	if len(got) != 1 {
		t.Errorf("expected 1; got %d", len(got))
	}
}

func TestAddIOCRelationship_RejectsUnknownPredicate(t *testing.T) {
	s := openTempStore(t)
	a, _, _ := s.UpsertIOC(&IOCUpsert{Kind: "sha256", Value: "x", NormalizedValue: "x"})
	b, _, _ := s.UpsertIOC(&IOCUpsert{Kind: "sha256", Value: "y", NormalizedValue: "y"})
	if err := s.AddIOCRelationship(&IOCRelationshipInsert{
		SubjectID: a, Predicate: "ate-for-breakfast", ObjectID: b,
	}); err == nil {
		t.Errorf("expected error for unknown predicate")
	}
}

func TestListIOCRelationships_ReturnsBothDirections(t *testing.T) {
	s := openTempStore(t)
	a, _, _ := s.UpsertIOC(&IOCUpsert{Kind: "domain", Value: "a", NormalizedValue: "a"})
	b, _, _ := s.UpsertIOC(&IOCUpsert{Kind: "ipv4", Value: "1.1.1.1", NormalizedValue: "1.1.1.1"})
	s.AddIOCRelationship(&IOCRelationshipInsert{SubjectID: a, Predicate: "resolves-to", ObjectID: b})
	got, _ := s.ListIOCRelationships(b)
	if len(got) != 1 {
		t.Errorf("expected ListIOCRelationships(b) to surface incoming edge; got %d", len(got))
	}
}
