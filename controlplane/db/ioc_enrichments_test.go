package db

import (
	"testing"
	"time"
)

func TestUpsertIOCEnrichment_InsertsRow(t *testing.T) {
	s := openTempStore(t)
	iocID, _, _ := s.UpsertIOC(&IOCUpsert{Kind: "sha256", Value: "abc", NormalizedValue: "abc"})
	id, err := s.UpsertIOCEnrichment(&IOCEnrichmentInsert{
		IOCID:     iocID,
		Adapter:   "virustotal",
		Verdict:   "malicious",
		Score:     85,
		RawJSON:   `{"vt":"raw"}`,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("UpsertIOCEnrichment: %v", err)
	}
	if id == 0 {
		t.Errorf("expected non-zero id")
	}
}

func TestGetIOCEnrichment_ByIOCAndAdapter(t *testing.T) {
	s := openTempStore(t)
	iocID, _, _ := s.UpsertIOC(&IOCUpsert{Kind: "ipv4", Value: "1.2.3.4", NormalizedValue: "1.2.3.4"})
	s.UpsertIOCEnrichment(&IOCEnrichmentInsert{
		IOCID:     iocID,
		Adapter:   "abuseipdb",
		Verdict:   "suspicious",
		Score:     60,
		RawJSON:   `{}`,
		ExpiresAt: time.Now().Add(time.Hour),
	})
	got, err := s.GetIOCEnrichment(iocID, "abuseipdb")
	if err != nil {
		t.Fatalf("GetIOCEnrichment: %v", err)
	}
	if got.Verdict != "suspicious" || got.Score != 60 {
		t.Errorf("unexpected: %+v", got)
	}
}

func TestUpsertIOCEnrichment_OverwritesPriorRow(t *testing.T) {
	s := openTempStore(t)
	iocID, _, _ := s.UpsertIOC(&IOCUpsert{Kind: "sha256", Value: "abc", NormalizedValue: "abc"})
	s.UpsertIOCEnrichment(&IOCEnrichmentInsert{
		IOCID: iocID, Adapter: "virustotal", Verdict: "clean", Score: 0,
		RawJSON: `{"v":1}`, ExpiresAt: time.Now().Add(time.Hour),
	})
	s.UpsertIOCEnrichment(&IOCEnrichmentInsert{
		IOCID: iocID, Adapter: "virustotal", Verdict: "malicious", Score: 90,
		RawJSON: `{"v":2}`, ExpiresAt: time.Now().Add(time.Hour),
	})
	got, _ := s.GetIOCEnrichment(iocID, "virustotal")
	if got.Verdict != "malicious" || got.Score != 90 {
		t.Errorf("expected upsert to overwrite; got %+v", got)
	}
}

func TestGetFreshIOCEnrichment_ExpiredReturnsError(t *testing.T) {
	s := openTempStore(t)
	iocID, _, _ := s.UpsertIOC(&IOCUpsert{Kind: "sha256", Value: "abc", NormalizedValue: "abc"})
	s.UpsertIOCEnrichment(&IOCEnrichmentInsert{
		IOCID: iocID, Adapter: "virustotal", Verdict: "clean",
		RawJSON: `{}`, ExpiresAt: time.Now().Add(-1 * time.Hour),
	})
	_, err := s.GetFreshIOCEnrichment(iocID, "virustotal")
	if err == nil {
		t.Errorf("expected error for expired row")
	}
}
