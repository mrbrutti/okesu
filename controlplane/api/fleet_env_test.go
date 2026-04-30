package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFleetEnvGet_Empty(t *testing.T) {
	st := newSeededTestStore(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/fleet-env", nil)
	FleetEnvGet(st)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var got fleetEnvSummaryJSON
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.AnthropicSet || got.OpenAISet {
		t.Errorf("expected unset; got %+v", got)
	}
	if got.Version != 0 {
		t.Errorf("expected version 0; got %d", got.Version)
	}
	if got.Source != "local" {
		t.Errorf("expected source=local; got %q", got.Source)
	}
}

func TestFleetEnvPut_SetsKeysAndBumpsVersion(t *testing.T) {
	st := newSeededTestStore(t)
	body := `{"anthropic_api_key":"sk-ant-test-1234","openai_api_key":"sk-oai-test-abcd"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/fleet-env", strings.NewReader(body))
	FleetEnvPut(st, nil)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got fleetEnvSummaryJSON
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.AnthropicSet || !got.OpenAISet {
		t.Errorf("expected both set; got %+v", got)
	}
	if got.AnthropicLast4 != "1234" || got.OpenAILast4 != "abcd" {
		t.Errorf("last4 wrong: %+v", got)
	}
	if got.Version != 1 {
		t.Errorf("expected version 1; got %d", got.Version)
	}
	// Plaintext must NOT appear in the response.
	bodyStr := rec.Body.String()
	if strings.Contains(bodyStr, "sk-ant-test-1234") || strings.Contains(bodyStr, "sk-oai-test-abcd") {
		t.Errorf("plaintext leaked: %s", bodyStr)
	}
}

func TestFleetEnvPut_OnChangeCalled(t *testing.T) {
	st := newSeededTestStore(t)
	called := int64(0)
	body := `{"anthropic_api_key":"sk-ant-1"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/fleet-env", strings.NewReader(body))
	FleetEnvPut(st, func(v int64) { called = v })(rec, req)
	if called != 1 {
		t.Errorf("onChange not called with version 1; got %d", called)
	}
}

func TestFleetEnvPut_NoFieldsRejected(t *testing.T) {
	st := newSeededTestStore(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/fleet-env", bytes.NewReader([]byte(`{}`)))
	FleetEnvPut(st, nil)(rec, req)
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestFleetEnvPut_DeleteViaEmptyString(t *testing.T) {
	st := newSeededTestStore(t)
	// Seed.
	body1 := `{"anthropic_api_key":"sk-ant-1"}`
	FleetEnvPut(st, nil)(httptest.NewRecorder(), httptest.NewRequest("PUT", "/api/fleet-env", strings.NewReader(body1)))
	// Delete via empty string.
	body2 := `{"anthropic_api_key":""}`
	rec := httptest.NewRecorder()
	FleetEnvPut(st, nil)(rec, httptest.NewRequest("PUT", "/api/fleet-env", strings.NewReader(body2)))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var got fleetEnvSummaryJSON
	json.NewDecoder(rec.Body).Decode(&got)
	if got.AnthropicSet {
		t.Errorf("expected Anthropic cleared; got %+v", got)
	}
}

func TestFleetEnvOverrideLocal(t *testing.T) {
	st := newSeededTestStore(t)
	// Seed via federation path so initial source = federated.
	if err := st.SetFleetEnvSource("federated_from_parent", ""); err != nil {
		t.Fatalf("SetFleetEnvSource: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/fleet-env/override-local", nil)
	FleetEnvOverrideLocal(st, nil)(rec, req)
	if rec.Code != 204 {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	fe, _ := st.GetFleetEnv()
	if fe.Source != "local" {
		t.Errorf("Source = %q, want local", fe.Source)
	}
}

func TestFleetEnvRevertToParent(t *testing.T) {
	st := newSeededTestStore(t)
	// Default source = local; revert flips to federated.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/fleet-env/revert-to-parent", nil)
	FleetEnvRevertToParent(st, nil)(rec, req)
	if rec.Code != 204 {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	fe, _ := st.GetFleetEnv()
	if fe.Source != "federated_from_parent" {
		t.Errorf("Source = %q, want federated_from_parent", fe.Source)
	}
}
