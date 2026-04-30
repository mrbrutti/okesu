package db

import "testing"

func TestTransportConfigReferences_Empty(t *testing.T) {
	s := openTempStore(t)
	tcID := mustCreateTransportConfig(t, s, "test")
	refs, err := s.TransportConfigReferences(tcID)
	if err != nil {
		t.Fatalf("TransportConfigReferences: %v", err)
	}
	if !refs.IsEmpty() {
		t.Errorf("expected empty refs; got %+v", refs)
	}
}

func TestDeleteTransportConfig_Empty(t *testing.T) {
	s := openTempStore(t)
	tcID := mustCreateTransportConfig(t, s, "test")
	if err := s.DeleteTransportConfig(tcID); err != nil {
		t.Fatalf("DeleteTransportConfig: %v", err)
	}
}

func mustCreateTransportConfig(t *testing.T, s *Store, name string) int64 {
	t.Helper()
	tc := TransportConfig{
		Name:     name,
		Kind:     "s3",
		Bucket:   "test-bucket",
		Endpoint: "https://test.example",
		UseSSL:   true,
	}
	id, err := s.CreateTransportConfig(tc)
	if err != nil {
		t.Fatalf("CreateTransportConfig: %v", err)
	}
	return id
}
