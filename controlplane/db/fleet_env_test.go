package db

import (
	"testing"
)

func mustMasterKey(t *testing.T) []byte {
	t.Helper()
	return make([]byte, 32) // zeroes are fine for tests; HKDF will derive a stable sub-key
}

func TestFleetEnv_GetEmpty(t *testing.T) {
	s := openTempStore(t)
	fe, err := s.GetFleetEnv()
	if err != nil {
		t.Fatalf("GetFleetEnv: %v", err)
	}
	if fe.HasAnthropic || fe.HasOpenAI {
		t.Errorf("expected both keys empty on fresh DB; got %+v", fe)
	}
	if fe.Version != 0 {
		t.Errorf("expected version 0; got %d", fe.Version)
	}
	if fe.Source != "local" {
		t.Errorf("expected source=local; got %q", fe.Source)
	}
}

func TestFleetEnv_UpsertAndGetWithKeys(t *testing.T) {
	s := openTempStore(t)
	mk := mustMasterKey(t)
	a := "sk-ant-test-1234567890"
	o := "sk-openai-test-abcdefghij"
	v, err := s.UpsertFleetEnv(mk, FleetEnvUpdate{
		AnthropicAPIKey:    &a,
		OpenAIAPIKey:       &o,
		UpdatedByUserEmail: "ops@example.com",
	})
	if err != nil {
		t.Fatalf("UpsertFleetEnv: %v", err)
	}
	if v != 1 {
		t.Errorf("version after first upsert = %d, want 1", v)
	}

	fe, err := s.GetFleetEnvWithKeys(mk)
	if err != nil {
		t.Fatalf("GetFleetEnvWithKeys: %v", err)
	}
	if fe.AnthropicAPIKey != a || fe.OpenAIAPIKey != o {
		t.Errorf("keys did not round-trip: %+v", fe)
	}
	if fe.AnthropicLast4 != "7890" || fe.OpenAILast4 != "ghij" {
		t.Errorf("last4 wrong: anthropic=%q openai=%q", fe.AnthropicLast4, fe.OpenAILast4)
	}
}

func TestFleetEnv_PartialUpdate(t *testing.T) {
	s := openTempStore(t)
	mk := mustMasterKey(t)
	a, o := "sk-ant-1", "sk-oai-1"
	if _, err := s.UpsertFleetEnv(mk, FleetEnvUpdate{AnthropicAPIKey: &a, OpenAIAPIKey: &o}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Only update Anthropic; OpenAI should be unchanged.
	a2 := "sk-ant-2"
	if _, err := s.UpsertFleetEnv(mk, FleetEnvUpdate{AnthropicAPIKey: &a2}); err != nil {
		t.Fatalf("update: %v", err)
	}
	fe, _ := s.GetFleetEnvWithKeys(mk)
	if fe.AnthropicAPIKey != "sk-ant-2" {
		t.Errorf("Anthropic = %q, want sk-ant-2", fe.AnthropicAPIKey)
	}
	if fe.OpenAIAPIKey != "sk-oai-1" {
		t.Errorf("OpenAI changed unexpectedly: %q", fe.OpenAIAPIKey)
	}
}

func TestFleetEnv_DeleteEmptyString(t *testing.T) {
	s := openTempStore(t)
	mk := mustMasterKey(t)
	a := "sk-ant-1"
	if _, err := s.UpsertFleetEnv(mk, FleetEnvUpdate{AnthropicAPIKey: &a}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	empty := ""
	if _, err := s.UpsertFleetEnv(mk, FleetEnvUpdate{AnthropicAPIKey: &empty}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	fe, _ := s.GetFleetEnv()
	if fe.HasAnthropic {
		t.Errorf("expected Anthropic cleared; got %+v", fe)
	}
}

func TestFleetEnv_VersionMonotonic(t *testing.T) {
	s := openTempStore(t)
	mk := mustMasterKey(t)
	a := "sk-1"
	for i := int64(1); i <= 3; i++ {
		k := a + string(rune('0'+i))
		if v, err := s.UpsertFleetEnv(mk, FleetEnvUpdate{AnthropicAPIKey: &k}); err != nil {
			t.Fatalf("update %d: %v", i, err)
		} else if v != i {
			t.Errorf("version after update %d = %d, want %d", i, v, i)
		}
	}
}

func TestFleetEnv_FederatedSet_OnlyWhenSourceIsFederated(t *testing.T) {
	s := openTempStore(t)
	mk := mustMasterKey(t)
	// Operator sets keys locally first.
	a := "sk-local"
	if _, err := s.UpsertFleetEnv(mk, FleetEnvUpdate{AnthropicAPIKey: &a}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Federation tries to push a different key — should be a no-op.
	_, applied, err := s.SetFleetEnvFromFederation(mk, "parent-cp-1", "sk-from-parent", "sk-oai-from-parent", 99)
	if err != nil {
		t.Fatalf("SetFleetEnvFromFederation: %v", err)
	}
	if applied {
		t.Errorf("expected federated update to skip when source=local")
	}
	fe, _ := s.GetFleetEnvWithKeys(mk)
	if fe.AnthropicAPIKey != "sk-local" {
		t.Errorf("local key was overwritten: %q", fe.AnthropicAPIKey)
	}
}

func TestFleetEnv_FederatedSet_SeedsEmptyRow(t *testing.T) {
	// Migration default leaves the singleton row at source='local',
	// version=0 — the "empty" state. Federation must be allowed to
	// seed it without the operator first toggling source manually
	// from the UI; otherwise a freshly-bootstrapped child CP never
	// auto-receives keys from its parent.
	s := openTempStore(t)
	mk := mustMasterKey(t)
	pre, _ := s.GetFleetEnv()
	if pre.Source != "local" || pre.Version != 0 {
		t.Fatalf("setup: source=%q version=%d, want local/0", pre.Source, pre.Version)
	}
	_, applied, err := s.SetFleetEnvFromFederation(mk, "parent-cp-1", "sk-seed", "sk-oai-seed", 7)
	if err != nil {
		t.Fatalf("SetFleetEnvFromFederation: %v", err)
	}
	if !applied {
		t.Fatalf("expected federation to seed empty row, but applied=false")
	}
	fe, _ := s.GetFleetEnvWithKeys(mk)
	if fe.Source != "federated_from_parent" {
		t.Errorf("Source = %q, want federated_from_parent", fe.Source)
	}
	if fe.Version != 7 {
		t.Errorf("Version = %d, want 7", fe.Version)
	}
	if fe.AnthropicAPIKey != "sk-seed" || fe.OpenAIAPIKey != "sk-oai-seed" {
		t.Errorf("keys not stored: anthropic=%q openai=%q", fe.AnthropicAPIKey, fe.OpenAIAPIKey)
	}
}

func TestFleetEnv_FederatedSet_WhenFederated(t *testing.T) {
	s := openTempStore(t)
	mk := mustMasterKey(t)
	if err := s.SetFleetEnvSource("federated_from_parent", ""); err != nil {
		t.Fatalf("SetFleetEnvSource: %v", err)
	}
	_, applied, err := s.SetFleetEnvFromFederation(mk, "parent-cp-1", "sk-from-parent", "", 5)
	if err != nil {
		t.Fatalf("SetFleetEnvFromFederation: %v", err)
	}
	if !applied {
		t.Errorf("expected federated update to apply")
	}
	fe, _ := s.GetFleetEnvWithKeys(mk)
	if fe.AnthropicAPIKey != "sk-from-parent" {
		t.Errorf("Anthropic = %q", fe.AnthropicAPIKey)
	}
	if fe.Source != "federated_from_parent" {
		t.Errorf("Source = %q", fe.Source)
	}
	if !fe.ParentCPID.Valid || fe.ParentCPID.String != "parent-cp-1" {
		t.Errorf("ParentCPID = %+v", fe.ParentCPID)
	}
}

func TestFleetEnv_FederatedSet_OlderVersionSkipped(t *testing.T) {
	s := openTempStore(t)
	mk := mustMasterKey(t)
	if err := s.SetFleetEnvSource("federated_from_parent", ""); err != nil {
		t.Fatalf("SetFleetEnvSource: %v", err)
	}
	// First federated update at version 5.
	if _, _, err := s.SetFleetEnvFromFederation(mk, "p1", "sk-v5", "", 5); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Stale federated update at version 4 — should be skipped.
	_, applied, err := s.SetFleetEnvFromFederation(mk, "p1", "sk-v4", "", 4)
	if err != nil {
		t.Fatalf("stale: %v", err)
	}
	if applied {
		t.Errorf("stale version should have been skipped")
	}
	fe, _ := s.GetFleetEnvWithKeys(mk)
	if fe.AnthropicAPIKey != "sk-v5" {
		t.Errorf("expected v5 key preserved; got %q", fe.AnthropicAPIKey)
	}
}

func TestFleetEnv_OverrideLocal(t *testing.T) {
	s := openTempStore(t)
	mk := mustMasterKey(t)
	// Start federated.
	_ = s.SetFleetEnvSource("federated_from_parent", "")
	if _, _, err := s.SetFleetEnvFromFederation(mk, "p1", "sk-from-parent", "", 1); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Override locally.
	if err := s.SetFleetEnvSource("local", "ops@example.com"); err != nil {
		t.Fatalf("SetFleetEnvSource: %v", err)
	}
	fe, _ := s.GetFleetEnvWithKeys(mk)
	if fe.Source != "local" {
		t.Errorf("Source = %q", fe.Source)
	}
	// Subsequent federation push must be ignored.
	_, applied, _ := s.SetFleetEnvFromFederation(mk, "p1", "sk-different", "", 99)
	if applied {
		t.Errorf("federation should not overwrite local override")
	}
	fe2, _ := s.GetFleetEnvWithKeys(mk)
	if fe2.AnthropicAPIKey != "sk-from-parent" {
		t.Errorf("override should preserve last federated value, got %q", fe2.AnthropicAPIKey)
	}
}
