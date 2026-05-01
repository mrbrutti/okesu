package db

import (
	"testing"
	"time"
)

func TestFeedsConsent_DefaultUngrantedThenSet(t *testing.T) {
	st := openTempStore(t)

	at, err := st.GetFeedsConsentGrantedAt()
	if err != nil {
		t.Fatalf("get default: %v", err)
	}
	if at != nil {
		t.Fatalf("expected unset, got %v", at)
	}

	now := time.Now().UTC().Truncate(time.Second)
	if err := st.SetFeedsConsentGrantedAt(now); err != nil {
		t.Fatalf("set: %v", err)
	}

	got, err := st.GetFeedsConsentGrantedAt()
	if err != nil {
		t.Fatalf("get after set: %v", err)
	}
	if got == nil {
		t.Fatalf("expected non-nil after set")
	}
	if !got.Equal(now) {
		t.Fatalf("expected %v got %v", now, *got)
	}
}

func TestFeedsConsent_SetBeforeGet(t *testing.T) {
	st := openTempStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	if err := st.SetFeedsConsentGrantedAt(now); err != nil {
		t.Fatalf("set on fresh store: %v", err)
	}
	got, err := st.GetFeedsConsentGrantedAt()
	if err != nil {
		t.Fatalf("get after set: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil after set on fresh store")
	}
	if !got.Equal(now) {
		t.Fatalf("expected %v got %v", now, *got)
	}
}
