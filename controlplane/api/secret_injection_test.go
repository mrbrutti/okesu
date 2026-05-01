package api

import (
	"strings"
	"testing"

	"github.com/section9labs/okesu/agent"
	"github.com/section9labs/okesu/controlplane/db"
)

// helper — open a fresh test store. Mirrors the pattern in db tests
// but lives in api/ so we can call attachEnvSecrets directly.
func openTempAPIStore(t *testing.T) *db.Store {
	t.Helper()
	path := t.TempDir() + "/cp.db"
	s, err := db.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestAttachEnvSecrets_InjectsMatchingBindings — happy path. A node
// with env=prod label, plus a PROD_API_KEY env_var secret bound to
// `env=prod, scope=agent_run`, ends up in payload.Env.
func TestAttachEnvSecrets_InjectsMatchingBindings(t *testing.T) {
	st := openTempAPIStore(t)
	mk := []byte(strings.Repeat("k", 32))

	// Direct meta write so MasterKeyFromMeta resolves cleanly.
	// session_hmac_key is base64-encoded in production; we mirror
	// that encoding via the public MetaSet helper.
	if err := writeMasterKey(st, mk); err != nil {
		t.Fatalf("seed master key: %v", err)
	}

	// Node + label.
	res, _ := st.Exec(`INSERT INTO nodes (name, hostname, ssh_user, ssh_port, status) VALUES ('prod-1','prod-1','root',22,'ready')`)
	nodeID, _ := res.LastInsertId()
	_ = st.SetNodeLabel(nodeID, "env", "prod")

	// Secret + binding.
	mkBytes, _ := st.MasterKeyFromMeta()
	sec, err := st.CreateSecret(db.SecretInsert{
		Name:  "PROD_API_KEY",
		Kind:  db.SecretKindEnvVar,
		Value: "sk-prod-secret",
	}, mkBytes)
	if err != nil {
		t.Fatalf("create secret: %v", err)
	}
	if err := st.AddSecretBinding(sec.ID, "env=prod", db.SecretScopeAgentRun); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// Inject and verify.
	payload := &agent.JobPayload{Prompt: "do thing"}
	attachEnvSecrets(st, nodeID, db.SecretScopeAgentRun, payload)
	if payload.Env == nil {
		t.Fatalf("Env not populated")
	}
	if got := payload.Env["PROD_API_KEY"]; got != "sk-prod-secret" {
		t.Errorf("PROD_API_KEY = %q, want sk-prod-secret", got)
	}
}

// TestAttachEnvSecrets_NoLabelsNoLeak — a node without matching
// labels gets no env block, even if a binding exists for prod.
func TestAttachEnvSecrets_NoLabelsNoLeak(t *testing.T) {
	st := openTempAPIStore(t)
	mk := []byte(strings.Repeat("k", 32))
	if err := writeMasterKey(st, mk); err != nil {
		t.Fatalf("seed master: %v", err)
	}
	res, _ := st.Exec(`INSERT INTO nodes (name, hostname, ssh_user, ssh_port, status) VALUES ('stage-1','stage-1','root',22,'ready')`)
	stageID, _ := res.LastInsertId()
	_ = st.SetNodeLabel(stageID, "env", "staging")

	mkBytes, _ := st.MasterKeyFromMeta()
	sec, _ := st.CreateSecret(db.SecretInsert{
		Name: "PROD_KEY", Kind: db.SecretKindEnvVar, Value: "v",
	}, mkBytes)
	_ = st.AddSecretBinding(sec.ID, "env=prod", db.SecretScopeAgentRun)

	payload := &agent.JobPayload{}
	attachEnvSecrets(st, stageID, db.SecretScopeAgentRun, payload)
	if len(payload.Env) != 0 {
		t.Errorf("expected empty Env on stage node, got %v", payload.Env)
	}
}

// TestAttachEnvSecrets_NilStoreSafe — the helper degrades gracefully
// when the store is missing (e.g. test harnesses, federation
// short-circuits). Caller should not panic.
func TestAttachEnvSecrets_NilStoreSafe(t *testing.T) {
	payload := &agent.JobPayload{}
	attachEnvSecrets(nil, 1, db.SecretScopeAgentRun, payload)
	if len(payload.Env) != 0 {
		t.Errorf("expected unchanged Env, got %v", payload.Env)
	}
}

// writeMasterKey seeds the meta row that MasterKeyFromMeta reads.
// The auth Manager bootstraps this on first boot in production; for
// tests we drop it directly. Stored as base64 to match the auth
// manager's encoding.
func writeMasterKey(s *db.Store, key []byte) error {
	// MasterKeyFromMeta reads the base64-encoded value from
	// meta('session_hmac_key'). MetaSet is the public setter.
	return s.MetaSet("session_hmac_key", encodeB64(key))
}

func encodeB64(b []byte) string {
	const tab = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var out strings.Builder
	for i := 0; i < len(b); i += 3 {
		var n uint32
		var pad int
		if i+2 < len(b) {
			n = uint32(b[i])<<16 | uint32(b[i+1])<<8 | uint32(b[i+2])
		} else if i+1 < len(b) {
			n = uint32(b[i])<<16 | uint32(b[i+1])<<8
			pad = 1
		} else {
			n = uint32(b[i]) << 16
			pad = 2
		}
		out.WriteByte(tab[(n>>18)&0x3F])
		out.WriteByte(tab[(n>>12)&0x3F])
		if pad < 2 {
			out.WriteByte(tab[(n>>6)&0x3F])
		} else {
			out.WriteByte('=')
		}
		if pad < 1 {
			out.WriteByte(tab[n&0x3F])
		} else {
			out.WriteByte('=')
		}
	}
	return out.String()
}
