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

func TestListIOCRelationshipsByKVPaired(t *testing.T) {
	s := openTempStore(t)
	aID, _, _ := s.UpsertIOC(&IOCUpsert{Kind: "domain", Value: "evil.com", NormalizedValue: "evil.com"})
	bID, _, _ := s.UpsertIOC(&IOCUpsert{Kind: "ipv4", Value: "1.2.3.4", NormalizedValue: "1.2.3.4"})
	if err := s.AddIOCRelationship(&IOCRelationshipInsert{
		SubjectID: aID, Predicate: "resolves-to", ObjectID: bID, Source: "agent",
	}); err != nil {
		t.Fatalf("AddIOCRelationship: %v", err)
	}

	rels, err := s.ListIOCRelationshipsByKVPaired("domain", "evil.com")
	if err != nil {
		t.Fatalf("ListIOCRelationshipsByKVPaired: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 edge; got %d", len(rels))
	}
	r := rels[0]
	if r.SubjectKind != "domain" || r.SubjectValue != "evil.com" ||
		r.Predicate != "resolves-to" || r.ObjectKind != "ipv4" || r.ObjectValue != "1.2.3.4" {
		t.Errorf("edge shape wrong: %+v", r)
	}

	// Querying the object side should also return the same edge.
	relsB, err := s.ListIOCRelationshipsByKVPaired("ipv4", "1.2.3.4")
	if err != nil {
		t.Fatalf("ListIOCRelationshipsByKVPaired (object side): %v", err)
	}
	if len(relsB) != 1 || relsB[0].SubjectKind != "domain" {
		t.Errorf("object-side query missing edge or shape wrong: %+v", relsB)
	}
}
