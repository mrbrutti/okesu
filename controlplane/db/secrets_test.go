package db

import (
	"errors"
	"strings"
	"testing"
)

// secretsMasterKey returns a deterministic 32-byte test key. Real
// CPs read this via store.MasterKeyFromMeta which seeds itself on
// first boot; for unit tests we just use a fixed value.
func secretsMasterKey() []byte {
	return []byte(strings.Repeat("k", 32))
}

// TestSecrets_CRUDAndEncryption — happy path. Plaintext goes in
// sealed; comes out only via GetSecretValue with the same master.
func TestSecrets_CRUDAndEncryption(t *testing.T) {
	st := openTempStore(t)
	mk := secretsMasterKey()

	created, err := st.CreateSecret(SecretInsert{
		Name:        "ANTHROPIC_API_KEY",
		Kind:        SecretKindEnvVar,
		Description: "Production daimon key",
		Value:       "sk-ant-secret-value",
	}, mk)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Name != "ANTHROPIC_API_KEY" {
		t.Errorf("name = %q", created.Name)
	}

	// Plaintext round-trips.
	got, err := st.GetSecretValue(created.ID, mk)
	if err != nil {
		t.Fatalf("get value: %v", err)
	}
	if got != "sk-ant-secret-value" {
		t.Errorf("plaintext = %q", got)
	}

	// Wrong master key → AEAD fails open.
	bad := []byte(strings.Repeat("x", 32))
	if _, err := st.GetSecretValue(created.ID, bad); err == nil {
		t.Errorf("should fail with wrong master key")
	}

	// Rotate value.
	if err := st.UpdateSecretValue(created.ID, "sk-ant-rotated", mk); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	got, _ = st.GetSecretValue(created.ID, mk)
	if got != "sk-ant-rotated" {
		t.Errorf("after rotate, got %q", got)
	}

	// List + delete.
	all, _ := st.ListSecrets()
	if len(all) != 1 {
		t.Errorf("list should have 1, got %d", len(all))
	}
	if err := st.DeleteSecret(created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	all, _ = st.ListSecrets()
	if len(all) != 0 {
		t.Errorf("after delete, list = %d", len(all))
	}
}

// TestSecrets_Validation — kind enum + duplicate name + missing
// value all surface clean errors.
func TestSecrets_Validation(t *testing.T) {
	st := openTempStore(t)
	mk := secretsMasterKey()

	// Empty value rejected.
	if _, err := st.CreateSecret(SecretInsert{Name: "n", Kind: SecretKindAPIKey}, mk); err == nil {
		t.Errorf("empty value should error")
	}

	// Bad kind rejected.
	if _, err := st.CreateSecret(SecretInsert{Name: "n", Kind: "weird", Value: "x"}, mk); err == nil {
		t.Errorf("bad kind should error")
	}

	// Duplicate name → typed error.
	if _, err := st.CreateSecret(SecretInsert{Name: "shared", Kind: SecretKindEnvVar, Value: "v1"}, mk); err != nil {
		t.Fatalf("first: %v", err)
	}
	_, err := st.CreateSecret(SecretInsert{Name: "shared", Kind: SecretKindEnvVar, Value: "v2"}, mk)
	if !errors.Is(err, ErrSecretNameTaken) {
		t.Errorf("expected ErrSecretNameTaken, got %v", err)
	}
}

// TestSecretBindings_Resolver — a secret with a scoped binding
// matches the right node, doesn't leak to mismatched nodes, and
// the wildcard 'any' scope satisfies any scope filter.
func TestSecretBindings_Resolver(t *testing.T) {
	st := openTempStore(t)
	mk := secretsMasterKey()

	prod := seedNode(t, st, "prod-a")
	stage := seedNode(t, st, "stage-b")
	_ = st.SetNodeLabel(prod, "env", "prod")
	_ = st.SetNodeLabel(stage, "env", "staging")

	// Two secrets: a prod-only env var and a CP-wide SSH key.
	prodKey, err := st.CreateSecret(SecretInsert{
		Name: "PROD_API_KEY", Kind: SecretKindEnvVar, Value: "prod-secret",
	}, mk)
	if err != nil {
		t.Fatalf("create prod: %v", err)
	}
	allKey, err := st.CreateSecret(SecretInsert{
		Name: "ALL_SSH", Kind: SecretKindSSHKey, Value: "-----BEGIN PRIVATE KEY-----",
	}, mk)
	if err != nil {
		t.Fatalf("create all: %v", err)
	}

	// Bind: prod env var → env=prod, agent_run scope.
	if err := st.AddSecretBinding(prodKey.ID, "env=prod", SecretScopeAgentRun); err != nil {
		t.Fatalf("bind prod: %v", err)
	}
	// Bind: ssh key → CP-wide (empty selector), node scope.
	if err := st.AddSecretBinding(allKey.ID, "", SecretScopeNode); err != nil {
		t.Fatalf("bind all: %v", err)
	}

	// Idempotent re-add (UNIQUE on the tuple).
	if err := st.AddSecretBinding(prodKey.ID, "env=prod", SecretScopeAgentRun); err != nil {
		t.Errorf("duplicate add should be no-op: %v", err)
	}

	// Bad selector rejected at authoring time.
	if err := st.AddSecretBinding(prodKey.ID, "env=pr od", SecretScopeAny); err == nil {
		t.Errorf("invalid selector should error")
	}

	// Bad scope rejected.
	if err := st.AddSecretBinding(prodKey.ID, "env=prod", "weird"); err == nil {
		t.Errorf("bad scope should error")
	}

	// Resolver: prod node + agent_run scope → both secrets.
	// (env_var matches via env=prod; ssh_key matches via empty
	// selector + the wildcard-any scope filter — actually no, ssh
	// is scope=node, so won't surface for an agent_run query.)
	got, err := st.ListSecretsForNode(prod, SecretScopeAgentRun, "")
	if err != nil {
		t.Fatalf("resolve prod/agent_run: %v", err)
	}
	if len(got) != 1 || got[0].Secret.Name != "PROD_API_KEY" {
		t.Errorf("prod/agent_run expected [PROD_API_KEY], got %+v", names(got))
	}

	// Stage node + agent_run scope → no matches.
	got, _ = st.ListSecretsForNode(stage, SecretScopeAgentRun, "")
	if len(got) != 0 {
		t.Errorf("stage/agent_run should be empty, got %+v", names(got))
	}

	// Prod node + node scope → only the SSH key.
	got, _ = st.ListSecretsForNode(prod, SecretScopeNode, "")
	if len(got) != 1 || got[0].Secret.Name != "ALL_SSH" {
		t.Errorf("prod/node expected [ALL_SSH], got %+v", names(got))
	}

	// Kind filter — agent_run + env_var → only env vars.
	got, _ = st.ListSecretsForNode(prod, SecretScopeAgentRun, SecretKindEnvVar)
	if len(got) != 1 || got[0].Secret.Kind != SecretKindEnvVar {
		t.Errorf("kind filter expected env_var only, got %+v", names(got))
	}

	// Add an 'any' scope binding → satisfies both scope queries.
	wide, _ := st.CreateSecret(SecretInsert{
		Name: "AUDIT_TOKEN", Kind: SecretKindAPIKey, Value: "v",
	}, mk)
	if err := st.AddSecretBinding(wide.ID, "", SecretScopeAny); err != nil {
		t.Fatalf("bind any: %v", err)
	}
	got, _ = st.ListSecretsForNode(stage, SecretScopeAgentRun, "")
	if len(got) != 1 || got[0].Secret.Name != "AUDIT_TOKEN" {
		t.Errorf("stage/agent_run should now include AUDIT_TOKEN, got %+v", names(got))
	}
}

func names(rs []ResolvedSecret) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Secret.Name
	}
	return out
}
