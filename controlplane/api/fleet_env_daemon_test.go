package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFleetEnvDaemon_ReturnsPlaintext(t *testing.T) {
	st := newSeededTestStore(t)
	body := `{"anthropic_api_key":"sk-ant-secret","openai_api_key":"sk-oai-secret"}`
	FleetEnvPut(st, nil)(httptest.NewRecorder(), httptest.NewRequest("PUT", "/api/fleet-env", strings.NewReader(body)))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/fleet/env", nil)
	FleetEnvDaemon(st)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var got fleetEnvWithKeysJSON
	json.NewDecoder(rec.Body).Decode(&got)
	if got.AnthropicAPIKey != "sk-ant-secret" || got.OpenAIAPIKey != "sk-oai-secret" {
		t.Errorf("plaintext mismatch: %+v", got)
	}
	if got.Version != 1 {
		t.Errorf("Version = %d, want 1", got.Version)
	}
}

func TestFleetEnvDaemon_EmptyWhenUnset(t *testing.T) {
	st := newSeededTestStore(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/fleet/env", nil)
	FleetEnvDaemon(st)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var got fleetEnvWithKeysJSON
	json.NewDecoder(rec.Body).Decode(&got)
	if got.AnthropicAPIKey != "" || got.OpenAIAPIKey != "" {
		t.Errorf("expected empty plaintext; got %+v", got)
	}
	if got.Version != 0 {
		t.Errorf("expected version 0; got %d", got.Version)
	}
}

func TestFleetEnvFederation_SameShape(t *testing.T) {
	st := newSeededTestStore(t)
	body := `{"anthropic_api_key":"sk-fed-test"}`
	FleetEnvPut(st, nil)(httptest.NewRecorder(), httptest.NewRequest("PUT", "/api/fleet-env", strings.NewReader(body)))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/federation/fleet-env", nil)
	FleetEnvFederation(st)(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var got fleetEnvWithKeysJSON
	json.NewDecoder(rec.Body).Decode(&got)
	if got.AnthropicAPIKey != "sk-fed-test" {
		t.Errorf("Anthropic = %q", got.AnthropicAPIKey)
	}
}
