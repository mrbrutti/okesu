# Fleet LLM API Keys in Settings — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist Anthropic + OpenAI API keys in the CP DB, expose via Settings UI, and automatically distribute to every node + every federated child CP across all transports (tunnel, poll, S3 dead-drop).

**Architecture:** New singleton `fleet_env` table (AES-GCM sealed via the existing `MasterKeyFromMeta()` + `sealCloudPayload`/`openCloudPayload` helpers, scoped to a separate HKDF info string for domain separation). One operator-facing `GET/PUT /api/fleet-env` (cookie-auth, masked-summary GET / partial-update PUT with version bump). Two distribution channels: HTTPS pull at `/api/v1/fleet/env` (mTLS, daemon-authed) and S3 dead-drop publish (new `fleet-env.json` artifact alongside the existing federation publisher's findings/daimons/etc.). Daemon's existing heartbeat loop gains a fleet-env poll that rewrites `/etc/okesu/jobs.env` and `systemctl restart okesu-jobs.service` on `version` change. Federation propagation: parent exposes `/api/v1/federation/fleet-env`; child stores received keys with `source="federated_from_parent"` and republishes through its own channels — transitive propagation via existing publish/pull plumbing.

**Tech Stack:**
- Go (`controlplane/db/`, `controlplane/api/`, `controlplane/federation/`, `agent/`)
- modernc.org/sqlite + lib/pq (DB)
- `crypto/aes` + `crypto/cipher` + `golang.org/x/crypto/hkdf` (encryption — already vendored)
- `github.com/aws/aws-sdk-go-v2/service/s3` (S3 publisher — already vendored from prior work)
- React + TypeScript + Tailwind (Settings UI; light-theme platform tokens)
- chi router

---

## File Structure

**Backend create:**
- `controlplane/db/migrations/sqlite/045_fleet_env.sql`
- `controlplane/db/migrations/postgres/045_fleet_env.sql`
- `controlplane/db/fleet_env.go` — `FleetEnv` type + `GetFleetEnv` / `UpsertFleetEnv` / `ClearFleetEnv` / `SetFleetEnvSource` Store methods (encrypted via reused helpers)
- `controlplane/db/fleet_env_test.go`
- `controlplane/api/fleet_env.go` — operator-facing handlers (`GET/PUT /api/fleet-env`, `POST /api/fleet-env/override-local`, `POST /api/fleet-env/revert-to-parent`)
- `controlplane/api/fleet_env_test.go`
- `controlplane/api/fleet_env_daemon.go` — daemon-mTLS-authed `GET /api/v1/fleet/env` + federation-token-authed `GET /api/v1/federation/fleet-env`
- `controlplane/api/fleet_env_daemon_test.go`
- `controlplane/federation/fleet_env_topic.go` — child-side receiver: take a `FederatedFleetEnv` payload, store with `source="federated_from_parent"`, trigger republish

**Backend modify:**
- `controlplane/db/store.go` — wire migration 045 into both sqlite + postgres slices
- `controlplane/server.go` — register cpLocalEnv refresh from DB at boot; register the new S3 asset; mount routes; ensure backwards-compat seed runs once at boot
- `controlplane/federation/aggregator.go` (or `poller.go`) — add fleet-env topic to the federation poll
- `controlplane/api/auto_deploy.go` — `NewFleetAutoDeployer` reads keys from DB at deploy time (via Store), not from constructor args

**Daemon-side modify:**
- `agent/mgmt.go` — add a fleet-env poll alongside `StartHeartbeat` (or extend `StartConfigPoller`); rewrite `/etc/okesu/jobs.env` on `version` change; `systemctl restart okesu-jobs.service`

**Frontend create:**
- `web/src/pages/settings/LLMKeys.tsx`
- `web/src/components/MaskedKeyInput.tsx` (small reusable masked-input component)

**Frontend modify:**
- `web/src/api.ts` — `FleetEnvSummary`, `FleetEnvPatch` types + helpers
- `web/src/pages/Settings.tsx` (or wherever the settings tab list lives) — add the LLM Keys tab/route
- `web/src/App.tsx` — register `/settings/llm-keys` if needed

---

## Task Group A — DB layer

### Task A1: Migration 045 — fleet_env table

**Files:**
- Create: `controlplane/db/migrations/sqlite/045_fleet_env.sql`
- Create: `controlplane/db/migrations/postgres/045_fleet_env.sql`
- Modify: `controlplane/db/store.go`

- [ ] **Step A1.1: Create the sqlite migration**

`controlplane/db/migrations/sqlite/045_fleet_env.sql`:

```sql
-- fleet_env stores the fleet-wide LLM API keys (Anthropic, OpenAI)
-- the operator manages from Settings → LLM Keys. Singleton (id=1)
-- per CP. Keys are AES-GCM sealed using the same master-key
-- pattern cloud_credentials uses (HKDF info "okesu-fleet-env-v1"
-- for domain separation).
--
-- source = 'local' when this CP's keys were set by the operator.
-- source = 'federated_from_parent' when the keys came from a parent
-- CP via the federation poller; the federation poller respects
-- this flag and only overwrites when source = 'federated_from_parent'.
--
-- version is monotonically incrementing — bumped on every successful
-- update so consumers (daemon poll, S3 readers, federation poller)
-- can cache and only react to changes.

CREATE TABLE IF NOT EXISTS fleet_env (
  id                          INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  anthropic_api_key_sealed    BLOB,
  anthropic_api_key_nonce     BLOB,
  anthropic_api_key_last4     TEXT,
  openai_api_key_sealed       BLOB,
  openai_api_key_nonce        BLOB,
  openai_api_key_last4        TEXT,
  version                     INTEGER NOT NULL DEFAULT 0,
  source                      TEXT NOT NULL DEFAULT 'local'
                                CHECK (source IN ('local','federated_from_parent')),
  parent_cp_id                TEXT,
  updated_at                  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_by_user_email       TEXT
);

INSERT OR IGNORE INTO fleet_env (id) VALUES (1);
```

- [ ] **Step A1.2: Create the postgres migration**

`controlplane/db/migrations/postgres/045_fleet_env.sql`:

```sql
-- Phase: postgres parity. See migrations/sqlite/045_fleet_env.sql.

CREATE TABLE IF NOT EXISTS fleet_env (
  id                          INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  anthropic_api_key_sealed    BYTEA,
  anthropic_api_key_nonce     BYTEA,
  anthropic_api_key_last4     TEXT,
  openai_api_key_sealed       BYTEA,
  openai_api_key_nonce        BYTEA,
  openai_api_key_last4        TEXT,
  version                     BIGINT NOT NULL DEFAULT 0,
  source                      TEXT NOT NULL DEFAULT 'local'
                                CHECK (source IN ('local','federated_from_parent')),
  parent_cp_id                TEXT,
  updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_by_user_email       TEXT
);

INSERT INTO fleet_env (id) VALUES (1) ON CONFLICT (id) DO NOTHING;
```

- [ ] **Step A1.3: Wire migration 045 into store.go embed slices**

In `controlplane/db/store.go`, find the existing migration block (latest is 044). Add 045 after both:

```go
//go:embed migrations/sqlite/045_fleet_env.sql
var sqliteM045 string
```

```go
//go:embed migrations/postgres/045_fleet_env.sql
var pgM045 string
```

Append to both `sqliteMigrations` and `postgresMigrations` slices on their final lines.

- [ ] **Step A1.4: Build + commit**

```bash
go build ./controlplane/db/
```

```bash
pwd && git status   # MUST show feat/fleet-llm-keys
git add controlplane/db/migrations/sqlite/045_fleet_env.sql \
        controlplane/db/migrations/postgres/045_fleet_env.sql \
        controlplane/db/store.go
git commit -m "db(migration 045): fleet_env singleton table for fleet-wide LLM API keys"
```

### Task A2: FleetEnv type + Store methods

**Files:**
- Create: `controlplane/db/fleet_env.go`
- Create: `controlplane/db/fleet_env_test.go`

- [ ] **Step A2.1: Create the type + Get + Upsert + Clear**

`controlplane/db/fleet_env.go`:

```go
// fleet_env stores fleet-wide LLM API keys (Anthropic, OpenAI) used
// by daemons / agents / jobs across the fleet. Singleton (id=1) per
// CP. Keys are AES-GCM sealed using the same master-key pattern
// cloud_credentials uses, with a separate HKDF info string for
// domain separation ("okesu-fleet-env-v1").

package db

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"time"

	"golang.org/x/crypto/hkdf"
)

// FleetEnv is the singleton fleet-env row. Plaintext keys are
// returned only by Store.GetFleetEnvWithKeys (used by the daemon-
// auth + federation-token endpoints). Operator-facing reads use
// Store.GetFleetEnv which never returns plaintext.
type FleetEnv struct {
	AnthropicAPIKey      string  // plaintext; populated only by GetFleetEnvWithKeys
	OpenAIAPIKey         string  // plaintext; populated only by GetFleetEnvWithKeys
	AnthropicLast4       string
	OpenAILast4          string
	HasAnthropic         bool
	HasOpenAI            bool
	Version              int64
	Source               string  // "local" | "federated_from_parent"
	ParentCPID           sql.NullString
	UpdatedAt            time.Time
	UpdatedByUserEmail   sql.NullString
}

// GetFleetEnv returns the masked-summary view: never includes
// plaintext keys. Used by operator-facing endpoints.
func (s *Store) GetFleetEnv() (*FleetEnv, error) {
	row := s.QueryRow(`
		SELECT
			anthropic_api_key_sealed IS NOT NULL,
			openai_api_key_sealed    IS NOT NULL,
			COALESCE(anthropic_api_key_last4, ''),
			COALESCE(openai_api_key_last4, ''),
			version,
			source,
			parent_cp_id,
			updated_at,
			updated_by_user_email
		FROM fleet_env WHERE id = 1`)
	var fe FleetEnv
	if err := row.Scan(&fe.HasAnthropic, &fe.HasOpenAI,
		&fe.AnthropicLast4, &fe.OpenAILast4,
		&fe.Version, &fe.Source, &fe.ParentCPID,
		&fe.UpdatedAt, &fe.UpdatedByUserEmail); err != nil {
		return nil, err
	}
	return &fe, nil
}

// GetFleetEnvWithKeys returns the same shape as GetFleetEnv plus
// the plaintext API keys decrypted via masterKey. Used only by
// daemon-mTLS-authed and federation-token-authed endpoints.
func (s *Store) GetFleetEnvWithKeys(masterKey []byte) (*FleetEnv, error) {
	fe, err := s.GetFleetEnv()
	if err != nil {
		return nil, err
	}
	row := s.QueryRow(`
		SELECT anthropic_api_key_sealed, anthropic_api_key_nonce,
		       openai_api_key_sealed,    openai_api_key_nonce
		FROM fleet_env WHERE id = 1`)
	var aSealed, aNonce, oSealed, oNonce []byte
	if err := row.Scan(&aSealed, &aNonce, &oSealed, &oNonce); err != nil {
		return nil, err
	}
	if fe.HasAnthropic {
		pt, err := openFleetEnvPayload(masterKey, aSealed, aNonce)
		if err != nil {
			return nil, fmt.Errorf("unseal anthropic: %w", err)
		}
		fe.AnthropicAPIKey = string(pt)
	}
	if fe.HasOpenAI {
		pt, err := openFleetEnvPayload(masterKey, oSealed, oNonce)
		if err != nil {
			return nil, fmt.Errorf("unseal openai: %w", err)
		}
		fe.OpenAIAPIKey = string(pt)
	}
	return fe, nil
}

// FleetEnvUpdate is the partial-update shape. Per-field semantics:
//   - nil pointer = leave unchanged
//   - pointer to "" = delete (sealed → NULL)
//   - pointer to non-empty = encrypt + store
type FleetEnvUpdate struct {
	AnthropicAPIKey *string
	OpenAIAPIKey    *string
	UpdatedByUserEmail string
}

// UpsertFleetEnv applies the update. Bumps version, sets source =
// "local", clears parent_cp_id. Returns the new version.
func (s *Store) UpsertFleetEnv(masterKey []byte, u FleetEnvUpdate) (int64, error) {
	tx, err := s.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	type colUpdate struct {
		sealed, nonce []byte
		last4         string
		clear         bool
	}
	var aUp, oUp *colUpdate

	if u.AnthropicAPIKey != nil {
		k := *u.AnthropicAPIKey
		if k == "" {
			aUp = &colUpdate{clear: true}
		} else {
			ct, nonce, err := sealFleetEnvPayload(masterKey, []byte(k))
			if err != nil {
				return 0, fmt.Errorf("seal anthropic: %w", err)
			}
			aUp = &colUpdate{sealed: ct, nonce: nonce, last4: last4(k)}
		}
	}
	if u.OpenAIAPIKey != nil {
		k := *u.OpenAIAPIKey
		if k == "" {
			oUp = &colUpdate{clear: true}
		} else {
			ct, nonce, err := sealFleetEnvPayload(masterKey, []byte(k))
			if err != nil {
				return 0, fmt.Errorf("seal openai: %w", err)
			}
			oUp = &colUpdate{sealed: ct, nonce: nonce, last4: last4(k)}
		}
	}

	if aUp != nil {
		if aUp.clear {
			if _, err := tx.Exec(`UPDATE fleet_env SET anthropic_api_key_sealed=NULL, anthropic_api_key_nonce=NULL, anthropic_api_key_last4=NULL WHERE id=1`); err != nil {
				return 0, err
			}
		} else {
			if _, err := tx.Exec(`UPDATE fleet_env SET anthropic_api_key_sealed=?, anthropic_api_key_nonce=?, anthropic_api_key_last4=? WHERE id=1`,
				aUp.sealed, aUp.nonce, aUp.last4); err != nil {
				return 0, err
			}
		}
	}
	if oUp != nil {
		if oUp.clear {
			if _, err := tx.Exec(`UPDATE fleet_env SET openai_api_key_sealed=NULL, openai_api_key_nonce=NULL, openai_api_key_last4=NULL WHERE id=1`); err != nil {
				return 0, err
			}
		} else {
			if _, err := tx.Exec(`UPDATE fleet_env SET openai_api_key_sealed=?, openai_api_key_nonce=?, openai_api_key_last4=? WHERE id=1`,
				oUp.sealed, oUp.nonce, oUp.last4); err != nil {
				return 0, err
			}
		}
	}

	// Bump version + set local + audit fields, only if anything changed.
	if aUp != nil || oUp != nil {
		if _, err := tx.Exec(`
			UPDATE fleet_env SET
				version = version + 1,
				source = 'local',
				parent_cp_id = NULL,
				updated_at = CURRENT_TIMESTAMP,
				updated_by_user_email = ?
			WHERE id = 1`, u.UpdatedByUserEmail); err != nil {
			return 0, err
		}
	}

	row := tx.QueryRow(`SELECT version FROM fleet_env WHERE id=1`)
	var v int64
	if err := row.Scan(&v); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return v, nil
}

// SetFleetEnvFromFederation stores keys received from a parent CP.
// Sets source = "federated_from_parent". Bumps version. Idempotent
// (skips work if the incoming version <= current version).
func (s *Store) SetFleetEnvFromFederation(masterKey []byte, parentCPID, anthropicKey, openaiKey string, parentVersion int64) (int64, bool, error) {
	tx, err := s.Begin()
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()

	var currentSource string
	var currentVersion int64
	if err := tx.QueryRow(`SELECT source, version FROM fleet_env WHERE id = 1`).Scan(&currentSource, &currentVersion); err != nil {
		return 0, false, err
	}
	// Operator-set local values are not overridden by federation.
	if currentSource == "local" {
		return currentVersion, false, nil
	}
	// Only update if the parent's version is newer than ours.
	if parentVersion <= currentVersion {
		return currentVersion, false, nil
	}

	var aSealed, aNonce []byte
	var aLast4 string
	if anthropicKey != "" {
		ct, nonce, err := sealFleetEnvPayload(masterKey, []byte(anthropicKey))
		if err != nil {
			return 0, false, err
		}
		aSealed = ct
		aNonce = nonce
		aLast4 = last4(anthropicKey)
	}
	var oSealed, oNonce []byte
	var oLast4 string
	if openaiKey != "" {
		ct, nonce, err := sealFleetEnvPayload(masterKey, []byte(openaiKey))
		if err != nil {
			return 0, false, err
		}
		oSealed = ct
		oNonce = nonce
		oLast4 = last4(openaiKey)
	}

	if _, err := tx.Exec(`
		UPDATE fleet_env SET
			anthropic_api_key_sealed = ?, anthropic_api_key_nonce = ?, anthropic_api_key_last4 = ?,
			openai_api_key_sealed    = ?, openai_api_key_nonce    = ?, openai_api_key_last4    = ?,
			version = version + 1,
			source = 'federated_from_parent',
			parent_cp_id = ?,
			updated_at = CURRENT_TIMESTAMP,
			updated_by_user_email = NULL
		WHERE id = 1`,
		aSealed, aNonce, aLast4,
		oSealed, oNonce, oLast4,
		parentCPID); err != nil {
		return 0, false, err
	}

	var newVersion int64
	if err := tx.QueryRow(`SELECT version FROM fleet_env WHERE id = 1`).Scan(&newVersion); err != nil {
		return 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return newVersion, true, nil
}

// SetFleetEnvSource flips source between 'local' and
// 'federated_from_parent'. Used by the override-locally / revert-
// to-parent operator buttons. Does not change the keys themselves;
// when reverting to federated, the next federation poll repopulates.
func (s *Store) SetFleetEnvSource(source string, updatedByUserEmail string) error {
	if source != "local" && source != "federated_from_parent" {
		return errors.New("invalid source")
	}
	_, err := s.Exec(`
		UPDATE fleet_env SET
			source = ?,
			updated_at = CURRENT_TIMESTAMP,
			updated_by_user_email = ?
		WHERE id = 1`, source, updatedByUserEmail)
	return err
}

// ────────────────── encryption helpers (HKDF-derived sub-key) ──────────────────

func sealFleetEnvPayload(masterKey, plaintext []byte) ([]byte, []byte, error) {
	key, err := deriveFleetEnvKey(masterKey)
	if err != nil {
		return nil, nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	ct := gcm.Seal(nil, nonce, plaintext, nil)
	return ct, nonce, nil
}

func openFleetEnvPayload(masterKey, ciphertext, nonce []byte) ([]byte, error) {
	key, err := deriveFleetEnvKey(masterKey)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nonce, ciphertext, nil)
}

// deriveFleetEnvKey is sibling to deriveCloudKey. Different HKDF info
// string ensures fleet_env keys aren't interchangeable with
// cloud_credentials keys even though they share the master.
func deriveFleetEnvKey(masterKey []byte) ([]byte, error) {
	if len(masterKey) < 16 {
		return nil, errors.New("master key too short (need >= 16 bytes)")
	}
	r := hkdf.New(sha256.New, masterKey, nil, []byte("okesu-fleet-env-v1"))
	out := make([]byte, 32)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, err
	}
	return out, nil
}

// last4 returns the last four characters of a key for display.
// Empty string in → empty string out.
func last4(s string) string {
	if len(s) <= 4 {
		return s
	}
	return s[len(s)-4:]
}
```

- [ ] **Step A2.2: Tests**

`controlplane/db/fleet_env_test.go`:

```go
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
```

- [ ] **Step A2.3: Run + commit**

```bash
go test ./controlplane/db/ -run "TestFleetEnv" -v -count=1
go test ./controlplane/db/ -count=1
```

```bash
pwd && git status
git add controlplane/db/fleet_env.go controlplane/db/fleet_env_test.go
git commit -m "db(fleet_env): GetFleetEnv + GetFleetEnvWithKeys + UpsertFleetEnv + SetFleetEnvFromFederation"
```

---

## Task Group B — Operator HTTP endpoints

### Task B1: GET/PUT /api/fleet-env + override/revert handlers

**Files:**
- Create: `controlplane/api/fleet_env.go`
- Create: `controlplane/api/fleet_env_test.go`

- [ ] **Step B1.1: Implement**

`controlplane/api/fleet_env.go`:

```go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/section9labs/okesu/controlplane/db"
)

// fleetEnvSummaryJSON is the wire shape of GET /api/fleet-env.
// Never returns plaintext keys.
type fleetEnvSummaryJSON struct {
	AnthropicSet           bool    `json:"anthropic_set"`
	AnthropicLast4         string  `json:"anthropic_last4"`
	OpenAISet              bool    `json:"openai_set"`
	OpenAILast4            string  `json:"openai_last4"`
	Version                int64   `json:"version"`
	Source                 string  `json:"source"`
	ParentCPID             *string `json:"parent_cp_id"`
	UpdatedAt              string  `json:"updated_at"`
	UpdatedByUserEmail     *string `json:"updated_by_user_email"`
}

// fleetEnvPatchReq is the wire shape of PUT /api/fleet-env. Per-field:
// missing (nil pointer in Go after JSON-decoding-into-struct? we use
// a distinct sentinel via pointers + json.RawMessage parsing here).
type fleetEnvPatchReq struct {
	AnthropicAPIKey *string `json:"anthropic_api_key,omitempty"`
	OpenAIAPIKey    *string `json:"openai_api_key,omitempty"`
}

// FleetEnvGet returns the masked summary.
func FleetEnvGet(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fe, err := store.GetFleetEnv()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := fleetEnvSummaryJSON{
			AnthropicSet:   fe.HasAnthropic,
			AnthropicLast4: fe.AnthropicLast4,
			OpenAISet:      fe.HasOpenAI,
			OpenAILast4:    fe.OpenAILast4,
			Version:        fe.Version,
			Source:         fe.Source,
			UpdatedAt:      fe.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}
		if fe.ParentCPID.Valid {
			s := fe.ParentCPID.String
			out.ParentCPID = &s
		}
		if fe.UpdatedByUserEmail.Valid {
			s := fe.UpdatedByUserEmail.String
			out.UpdatedByUserEmail = &s
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// FleetEnvPut applies a partial update. Bumps version, sets source =
// local. The optional onChange callback is invoked after a successful
// commit so the server can refresh cpLocalEnv + trigger publish.
func FleetEnvPut(store *db.Store, onChange func(version int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req fleetEnvPatchReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		if req.AnthropicAPIKey == nil && req.OpenAIAPIKey == nil {
			http.Error(w, "no fields to update", http.StatusBadRequest)
			return
		}
		mk, err := store.MasterKeyFromMeta()
		if err != nil {
			http.Error(w, "master key: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// Pull operator email from the cookie session if available.
		actor := operatorEmailFromCtx(r)
		v, err := store.UpsertFleetEnv(mk, db.FleetEnvUpdate{
			AnthropicAPIKey:    req.AnthropicAPIKey,
			OpenAIAPIKey:       req.OpenAIAPIKey,
			UpdatedByUserEmail: actor,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = store.AppendAuditLog(actor, "fleet_env.set", map[string]any{"version": v})
		if onChange != nil {
			onChange(v)
		}
		// Return the new summary.
		FleetEnvGet(store)(w, r)
	}
}

// FleetEnvOverrideLocal switches source to "local" so the federation
// poller stops overwriting the row. Keeps current key values.
func FleetEnvOverrideLocal(store *db.Store, onChange func(version int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor := operatorEmailFromCtx(r)
		if err := store.SetFleetEnvSource("local", actor); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = store.AppendAuditLog(actor, "fleet_env.override_local", nil)
		if onChange != nil {
			fe, _ := store.GetFleetEnv()
			if fe != nil {
				onChange(fe.Version)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// FleetEnvRevertToParent switches source back to
// "federated_from_parent". Next federation poll will overwrite with
// parent's current value.
func FleetEnvRevertToParent(store *db.Store, onChange func(version int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor := operatorEmailFromCtx(r)
		if err := store.SetFleetEnvSource("federated_from_parent", actor); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = store.AppendAuditLog(actor, "fleet_env.revert_to_parent", nil)
		if onChange != nil {
			fe, _ := store.GetFleetEnv()
			if fe != nil {
				onChange(fe.Version)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// operatorEmailFromCtx extracts the operator's email from the cookie
// session. If the helper doesn't exist with this exact name, find
// the equivalent in the auth package and use it. Empty string is OK
// — UpsertFleetEnv tolerates blank.
func operatorEmailFromCtx(r *http.Request) string {
	// The auth middleware stashes the session principal in the
	// request context. Existing handlers (e.g.,
	// controlplane/api/cloud_credentials.go) read it via
	// auth.PrincipalFromCtx(r.Context()). Reuse that pattern.
	if p, ok := authPrincipal(r); ok {
		return p.Email
	}
	return ""
}
```

(`authPrincipal` is a placeholder for the existing helper — find the real one in `controlplane/auth/` and import it. The test file's expectations won't depend on this, since tests pass an unauthenticated request and the empty string is acceptable.)

Verify `Store.AppendAuditLog` exists with the assumed signature (`AppendAuditLog(actor, action string, details any) error`):

```bash
grep -n "func .Store..*AppendAudit\|func .Store..*AuditLog" controlplane/db/*.go
```

If the signature differs, adjust.

- [ ] **Step B1.2: Tests**

`controlplane/api/fleet_env_test.go`:

```go
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
	json.NewDecoder(rec.Body).Decode(&got)
	if !got.AnthropicSet || !got.OpenAISet {
		t.Errorf("expected both set; got %+v", got)
	}
	if got.AnthropicLast4 != "1234" || got.OpenAILast4 != "abcd" {
		t.Errorf("last4 wrong: %+v", got)
	}
	if got.Version != 1 {
		t.Errorf("expected version 1; got %d", got.Version)
	}
	// Plaintext must not appear in the response.
	if strings.Contains(rec.Body.String(), "sk-ant-test-1234") || strings.Contains(rec.Body.String(), "sk-oai-test-abcd") {
		t.Errorf("plaintext leaked: %s", rec.Body.String())
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
```

(`newSeededTestStore` was added by Group F of the bucket-provisioning work — it seeds the master key. If it doesn't exist on this branch yet, define it locally in the test file.)

- [ ] **Step B1.3: Run + commit**

```bash
go test ./controlplane/api/ -run "TestFleetEnvGet|TestFleetEnvPut" -v -count=1
go vet ./controlplane/api/
```

```bash
pwd && git status
git add controlplane/api/fleet_env.go controlplane/api/fleet_env_test.go
git commit -m "api(fleet-env): GET/PUT operator handlers + override-local + revert-to-parent"
```

---

## Task Group C — Daemon + federation HTTP endpoints

### Task C1: GET /api/v1/fleet/env (daemon-mTLS) + GET /api/v1/federation/fleet-env (federation-token)

**Files:**
- Create: `controlplane/api/fleet_env_daemon.go`
- Create: `controlplane/api/fleet_env_daemon_test.go`

- [ ] **Step C1.1: Implement**

`controlplane/api/fleet_env_daemon.go`:

```go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/section9labs/okesu/controlplane/db"
)

// fleetEnvWithKeysJSON is the wire shape returned by the
// daemon-mTLS-authed and federation-token-authed endpoints.
// Plaintext keys included.
type fleetEnvWithKeysJSON struct {
	AnthropicAPIKey string `json:"anthropic_api_key"`
	OpenAIAPIKey    string `json:"openai_api_key"`
	Version         int64  `json:"version"`
}

// FleetEnvDaemon returns the plaintext keys for daemons.
// MUST be mounted in the mTLS-authed daemon-management group.
func FleetEnvDaemon(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mk, err := store.MasterKeyFromMeta()
		if err != nil {
			http.Error(w, "master key: "+err.Error(), http.StatusInternalServerError)
			return
		}
		fe, err := store.GetFleetEnvWithKeys(mk)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		out := fleetEnvWithKeysJSON{
			AnthropicAPIKey: fe.AnthropicAPIKey,
			OpenAIAPIKey:    fe.OpenAIAPIKey,
			Version:         fe.Version,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// FleetEnvFederation returns the plaintext keys for a federated
// child CP. MUST be mounted behind requireFederationToken.
func FleetEnvFederation(store *db.Store) http.HandlerFunc {
	return FleetEnvDaemon(store) // identical wire shape; auth differs at mount
}
```

- [ ] **Step C1.2: Tests**

`controlplane/api/fleet_env_daemon_test.go`:

```go
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
		t.Errorf("Version = %d", got.Version)
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
```

- [ ] **Step C1.3: Run + commit**

```bash
go test ./controlplane/api/ -run "TestFleetEnvDaemon|TestFleetEnvFederation" -v -count=1
```

```bash
pwd && git status
git add controlplane/api/fleet_env_daemon.go controlplane/api/fleet_env_daemon_test.go
git commit -m "api(fleet-env): daemon-mTLS + federation-token endpoints (plaintext)"
```

---

## Task Group D — server.go wiring + cpLocalEnv refresh + S3 publish + boot seed

### Task D1: cpLocalEnv refresh + boot seed + route mounts + S3 asset

**Files:**
- Modify: `controlplane/server.go`

- [ ] **Step D1.1: Add a method to refresh cpLocalEnv from DB**

In `controlplane/server.go`, near the existing cpLocalEnv construction (around line 373-378), add a method on the Server struct:

```go
// refreshFleetEnvFromDB reads fleet_env from the DB and updates the
// CP's process-local env (cpLocalEnv) so the local jobs runtime
// picks up the new keys without a CP restart. Called at boot and
// after every operator save.
func (s *Server) refreshFleetEnvFromDB() error {
	mk, err := s.store.MasterKeyFromMeta()
	if err != nil {
		return err
	}
	fe, err := s.store.GetFleetEnvWithKeys(mk)
	if err != nil {
		return err
	}
	// Rebuild cpLocalEnv to drop any prior fleet-env entries.
	var rebuilt []string
	for _, e := range s.cpLocalEnv {
		if !strings.HasPrefix(e, "ANTHROPIC_API_KEY=") && !strings.HasPrefix(e, "OPENAI_API_KEY=") {
			rebuilt = append(rebuilt, e)
		}
	}
	if fe.HasAnthropic {
		rebuilt = append(rebuilt, "ANTHROPIC_API_KEY="+fe.AnthropicAPIKey)
	}
	if fe.HasOpenAI {
		rebuilt = append(rebuilt, "OPENAI_API_KEY="+fe.OpenAIAPIKey)
	}
	s.cpLocalEnv = rebuilt
	return nil
}
```

(Verify `s.cpLocalEnv`'s actual field name — search `cpLocalEnv` in server.go to confirm. If it's `localEnv` or something else, adjust.)

- [ ] **Step D1.2: Backwards-compat seed at boot**

Find the place in `controlplane/server.go` where `srv.cpLocalEnv` is first populated from `cfg.FleetAnthropicAPIKey` / `cfg.FleetOpenAIAPIKey`. Just BEFORE that, add a one-time seed:

```go
// Backwards-compat: if the operator had set
// OKESU_CP_FLEET_ANTHROPIC_API_KEY / OKESU_CP_FLEET_OPENAI_API_KEY
// env vars on a previous boot but never used the Settings UI, seed
// the fleet_env row from the env values exactly once. Subsequent
// boots see a non-empty row and skip the seed; operator saves through
// Settings overwrite freely.
if mk, err := srv.store.MasterKeyFromMeta(); err == nil {
	if fe, err := srv.store.GetFleetEnv(); err == nil {
		if !fe.HasAnthropic && !fe.HasOpenAI && (cfg.FleetAnthropicAPIKey != "" || cfg.FleetOpenAIAPIKey != "") {
			update := db.FleetEnvUpdate{UpdatedByUserEmail: "boot:env-seed"}
			if cfg.FleetAnthropicAPIKey != "" {
				k := cfg.FleetAnthropicAPIKey
				update.AnthropicAPIKey = &k
			}
			if cfg.FleetOpenAIAPIKey != "" {
				k := cfg.FleetOpenAIAPIKey
				update.OpenAIAPIKey = &k
			}
			if _, err := srv.store.UpsertFleetEnv(mk, update); err != nil {
				log.Printf("fleet_env: env-seed failed: %v", err)
			} else {
				log.Printf("fleet_env: seeded from OKESU_CP_FLEET_*_API_KEY env vars")
			}
		}
	}
}
// After potential seed, refresh cpLocalEnv from DB (DB wins over env).
if err := srv.refreshFleetEnvFromDB(); err != nil {
	log.Printf("fleet_env: refreshFleetEnvFromDB at boot: %v", err)
}
```

(After this block, the existing lines that hard-code `cpLocalEnv = append(cpLocalEnv, "ANTHROPIC_API_KEY="+cfg.FleetAnthropicAPIKey)` should be **deleted** — the refresh handles them now.)

- [ ] **Step D1.3: Mount routes**

Find the cookie-auth admin group (search for an existing endpoint like `/api/cloud-credentials`) and add:

```go
// Fleet-env (Settings → LLM Keys).
r.Get("/api/fleet-env", api.FleetEnvGet(s.store))
r.Put("/api/fleet-env", api.FleetEnvPut(s.store, func(version int64) {
	if err := s.refreshFleetEnvFromDB(); err != nil {
		log.Printf("fleet_env: refresh after PUT: %v", err)
	}
	s.publishFleetEnvToS3Peers()
}))
r.Post("/api/fleet-env/override-local", api.FleetEnvOverrideLocal(s.store, nil))
r.Post("/api/fleet-env/revert-to-parent", api.FleetEnvRevertToParent(s.store, nil))
```

Find the daemon-mTLS group (search `/api/v1/agents/.*/heartbeat`) and add:

```go
r.Get("/api/v1/fleet/env", api.FleetEnvDaemon(s.store))
```

Find the `requireFederationToken`-wrapped block (search `/api/v1/federation/findings`) and add:

```go
r.Get("/api/v1/federation/fleet-env", api.FleetEnvFederation(s.store))
```

- [ ] **Step D1.4: Add the S3 asset**

Find the existing `assets := []s3publisher.Asset{...}` block. Add the new entry:

```go
{Path: "fleet-env.json", Render: s.renderFederationFleetEnvJSON},
```

Implement the render method on the Server struct (add to the same area where the other `renderFederation*JSON` methods live):

```go
func (s *Server) renderFederationFleetEnvJSON() ([]byte, error) {
	mk, err := s.store.MasterKeyFromMeta()
	if err != nil {
		return nil, err
	}
	fe, err := s.store.GetFleetEnvWithKeys(mk)
	if err != nil {
		return nil, err
	}
	out := struct {
		AnthropicAPIKey string `json:"anthropic_api_key"`
		OpenAIAPIKey    string `json:"openai_api_key"`
		Version         int64  `json:"version"`
	}{
		AnthropicAPIKey: fe.AnthropicAPIKey,
		OpenAIAPIKey:    fe.OpenAIAPIKey,
		Version:         fe.Version,
	}
	return json.Marshal(out)
}
```

(`encoding/json` should already be imported.)

- [ ] **Step D1.5: Implement the publish-now hook**

Add a method on the Server:

```go
// publishFleetEnvToS3Peers triggers an immediate publish of the
// fleet-env.json asset to every active S3 peer. Called after an
// operator save so peers see the new version on their next read
// without waiting for the next periodic publish tick.
func (s *Server) publishFleetEnvToS3Peers() {
	if s.fedPublisher == nil {
		return
	}
	if err := s.fedPublisher.PublishAsset("fleet-env.json"); err != nil {
		log.Printf("fleet_env: publish-now failed: %v", err)
	}
}
```

(Verify the publisher's actual method name. Search for `func .*Publisher.*Publish` in `controlplane/federation/s3publisher/`. If the existing method is named differently — e.g., `Trigger` or `PublishOne` — match what's there. If no per-asset publish exists, the publisher's existing tick will pick up the change on its next interval; just drop this method and rely on the tick.)

- [ ] **Step D1.6: Build + commit**

```bash
go build ./controlplane/...
go test ./controlplane/api/ -count=1
go vet ./controlplane/...
```

```bash
pwd && git status
git add controlplane/server.go
git commit -m "server: fleet_env routes + cpLocalEnv refresh + S3 publish + boot seed"
```

---

## Task Group E — auto_deploy.go: read keys from DB at deploy time

### Task E1: NewFleetAutoDeployer pulls keys from Store, not constructor args

**Files:**
- Modify: `controlplane/api/auto_deploy.go`
- Modify: `controlplane/server.go` (call site update)

- [ ] **Step E1.1: Refactor NewFleetAutoDeployer signature**

Current signature:

```go
func NewFleetAutoDeployer(store *db.Store, issuer CertIssuer, sshKeyPath string, mgmtURL string, binPath string, resolver sshdeploy.DaemonBinaryResolver, anthropicKey, openaiKey string) (*FleetAutoDeployer, error)
```

Drop the last two args (`anthropicKey, openaiKey string`). Inside the deployer, read fresh keys from `store.GetFleetEnvWithKeys(masterKey)` at each deploy call (so rotations land for each subsequent deploy without restarting the deployer).

If `FleetAutoDeployer` already caches an env-string at construction, replace that with a method that fetches fresh on each deploy. Search for where the cached string is used (around line 354 of `controlplane/sshdeploy/deploy.go` — the `WriteFile("/etc/okesu/jobs.env", []byte(envContent), 0600)` site).

- [ ] **Step E1.2: Update the call site in server.go**

Find:

```go
dep, derr := api.NewFleetAutoDeployer(store, srv, cfg.FleetSSHKeyPath, cfg.EffectiveMgmtURL(), cfg.DaemonBinaryPath, binResolver, cfg.FleetAnthropicAPIKey, cfg.FleetOpenAIAPIKey)
```

Replace with:

```go
dep, derr := api.NewFleetAutoDeployer(store, srv, cfg.FleetSSHKeyPath, cfg.EffectiveMgmtURL(), cfg.DaemonBinaryPath, binResolver)
```

(The deployer now reads fleet-env from the store on every deploy.)

- [ ] **Step E1.3: Build + commit**

```bash
go build ./controlplane/...
go test ./controlplane/api/ -count=1
```

```bash
pwd && git status
git add controlplane/api/auto_deploy.go controlplane/server.go
git commit -m "api(auto_deploy): read fleet-env from DB at each deploy (drop constructor args)"
```

---

## Task Group F — Federation poller: receive + republish

### Task F1: Add fleet-env topic to the federation poller

**Files:**
- Modify: `controlplane/federation/aggregator.go` (or `poller.go` — wherever the existing topic dispatch lives)

- [ ] **Step F1.1: Locate the existing topic dispatch**

```bash
grep -n "findings.json\|daimons.json\|topic\|s3AssetForPath" controlplane/federation/aggregator.go controlplane/federation/poller.go
```

The existing pattern (per `aggregator.go:217-219`) maps topic name → asset path. Find where the poller iterates over topics it knows how to handle.

- [ ] **Step F1.2: Add a new topic handler that calls SetFleetEnvFromFederation**

Add a `fleet-env.json` topic. The handler:
1. Decodes the published JSON (`{anthropic_api_key, openai_api_key, version}`)
2. Calls `store.SetFleetEnvFromFederation(masterKey, parentCPID, anthropic, openai, parentVersion)`
3. If `applied == true`: trigger this CP's own publish + cpLocalEnv refresh (via the same hooks the operator-save path uses)

Concrete shape (adapt to the existing poller's helper signatures):

```go
// Inside whatever loop polls peers / reads from S3:
case "fleet-env":
	var fe struct {
		AnthropicAPIKey string `json:"anthropic_api_key"`
		OpenAIAPIKey    string `json:"openai_api_key"`
		Version         int64  `json:"version"`
	}
	if err := json.Unmarshal(payload, &fe); err != nil {
		return fmt.Errorf("decode fleet-env: %w", err)
	}
	mk, err := s.store.MasterKeyFromMeta()
	if err != nil {
		return err
	}
	_, applied, err := s.store.SetFleetEnvFromFederation(mk, peerInstanceID, fe.AnthropicAPIKey, fe.OpenAIAPIKey, fe.Version)
	if err != nil {
		return err
	}
	if applied {
		// Republish to this CP's own peers + refresh local env.
		if cb := s.fleetEnvOnChange; cb != nil {
			cb()
		}
	}
	return nil
```

The exact integration point depends on the existing poller's structure — read the file and adapt. The `s.fleetEnvOnChange` callback is wired in server.go when the poller is constructed (it calls `s.refreshFleetEnvFromDB() + s.publishFleetEnvToS3Peers()`).

- [ ] **Step F1.3: Build + commit**

```bash
go build ./controlplane/federation/
go test ./controlplane/federation/ -count=1
```

```bash
pwd && git status
git add controlplane/federation/
git commit -m "federation: receive + republish fleet-env from parent CP"
```

---

## Task Group G — Daemon-side fleet-env poll + jobs.env rewrite

### Task G1: Extend StartHeartbeat with a fleet-env poll

**Files:**
- Modify: `agent/mgmt.go`

- [ ] **Step G1.1: Add a poller alongside StartHeartbeat**

In `agent/mgmt.go`, find `StartHeartbeat`. Add a sibling method `StartFleetEnvPoll`:

```go
// StartFleetEnvPoll polls /api/v1/fleet/env on every heartbeat tick.
// On version change, rewrites /etc/okesu/jobs.env atomically and
// signals okesu-jobs.service to reload by restarting it.
func (m *MgmtPlane) StartFleetEnvPoll(ctx context.Context) {
	interval := time.Duration(m.cfg.HeartbeatSec) * time.Second
	if interval <= 0 {
		interval = defaultHeartbeatSec * time.Second
	}
	go func() {
		var lastVersion int64 = -1
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if v, err := m.pollFleetEnvOnce(lastVersion); err != nil {
					Emit(Event{Type: EventText, Agent: m.agent.Name, Host: m.host,
						Text: fmt.Sprintf("fleet-env poll error: %v", err)})
				} else {
					lastVersion = v
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (m *MgmtPlane) pollFleetEnvOnce(lastVersion int64) (int64, error) {
	var resp struct {
		AnthropicAPIKey string `json:"anthropic_api_key"`
		OpenAIAPIKey    string `json:"openai_api_key"`
		Version         int64  `json:"version"`
	}
	if err := m.get("/api/v1/fleet/env", &resp); err != nil {
		return lastVersion, err
	}
	if resp.Version == lastVersion {
		return lastVersion, nil
	}
	if err := writeJobsEnvAtomic(resp.AnthropicAPIKey, resp.OpenAIAPIKey); err != nil {
		return lastVersion, fmt.Errorf("write jobs.env: %w", err)
	}
	if err := restartJobsService(); err != nil {
		// Log but don't bail — operator may have configured the daemon
		// without root access; jobs.env is updated and will be picked
		// up on the next manual restart.
		Emit(Event{Type: EventText, Agent: m.agent.Name, Host: m.host,
			Text: fmt.Sprintf("fleet-env: jobs.env updated to v%d but systemctl restart failed: %v", resp.Version, err)})
	} else {
		Emit(Event{Type: EventText, Agent: m.agent.Name, Host: m.host,
			Text: fmt.Sprintf("fleet-env: jobs.env updated to v%d", resp.Version)})
	}
	return resp.Version, nil
}

// writeJobsEnvAtomic writes /etc/okesu/jobs.env via temp+rename so a
// concurrent reader never sees a half-written file. Mode 0600.
func writeJobsEnvAtomic(anthropic, openai string) error {
	const path = "/etc/okesu/jobs.env"
	const tmp = "/etc/okesu/.jobs.env.new"
	var lines []string
	if anthropic != "" {
		lines = append(lines, "ANTHROPIC_API_KEY="+anthropic)
	}
	if openai != "" {
		lines = append(lines, "OPENAI_API_KEY="+openai)
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(tmp, []byte(body), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// restartJobsService runs `systemctl restart okesu-jobs.service`.
// Errors propagate so the caller can log them.
func restartJobsService() error {
	cmd := exec.Command("systemctl", "restart", "okesu-jobs.service")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl restart okesu-jobs.service: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
```

Add `"os"`, `"os/exec"`, `"strings"` to imports if not already present.

(`m.get` is the existing GET helper — search `func (m \*MgmtPlane) get` to confirm the signature. If it doesn't exist, build one mirroring `m.post`.)

- [ ] **Step G1.2: Wire StartFleetEnvPoll alongside StartHeartbeat**

In `agent/daemon.go`, find where `mgmt.StartHeartbeat(mgmtCtx, state)` is called. Add immediately after:

```go
mgmt.StartFleetEnvPoll(mgmtCtx)
```

- [ ] **Step G1.3: Build + commit**

```bash
go build ./agent/
go vet ./agent/
```

(Lab smoke + manual testing covers behavior — unit-testing systemctl + /etc paths in a unit test is fragile. Group Z's lab-smoke checklist includes the daemon poll path.)

```bash
pwd && git status
git add agent/mgmt.go agent/daemon.go
git commit -m "agent(mgmt): poll /api/v1/fleet/env, rewrite /etc/okesu/jobs.env on version change"
```

---

## Task Group H — Frontend api.ts

### Task H1: Types + helpers

**Files:**
- Modify: `web/src/api.ts`

- [ ] **Step H1.1: Install web deps**

```bash
cd web && (test -d node_modules || npm install --silent) && cd ..
```

- [ ] **Step H1.2: Add types + helpers**

In `web/src/api.ts`, near other settings-related types, append:

```ts
// Fleet LLM API keys (Settings → LLM Keys).
export interface FleetEnvSummary {
  anthropic_set: boolean;
  anthropic_last4: string;
  openai_set: boolean;
  openai_last4: string;
  version: number;
  source: 'local' | 'federated_from_parent';
  parent_cp_id: string | null;
  updated_at: string;
  updated_by_user_email: string | null;
}

// Per-field semantics: omit = leave unchanged; "" = delete; non-empty = update.
export interface FleetEnvPatch {
  anthropic_api_key?: string;
  openai_api_key?: string;
}
```

In the api object, add helpers near `cloudCredentials`:

```ts
  fleetEnv: () =>
    request<FleetEnvSummary>('/api/fleet-env'),

  fleetEnvUpdate: (patch: FleetEnvPatch) =>
    request<FleetEnvSummary>('/api/fleet-env', {
      method: 'PUT',
      body: JSON.stringify(patch),
    }),

  fleetEnvOverrideLocal: () =>
    request<void>('/api/fleet-env/override-local', { method: 'POST' }),

  fleetEnvRevertToParent: () =>
    request<void>('/api/fleet-env/revert-to-parent', { method: 'POST' }),
```

- [ ] **Step H1.3: Build + commit**

```bash
cd web && npm run build && cd ..
```

```bash
pwd && git status
git add web/src/api.ts
git commit -m "web(api): fleet-env types + helpers"
```

---

## Task Group I — Settings UI: LLMKeys page

### Task I1: New page + tab registration

**Files:**
- Create: `web/src/pages/settings/LLMKeys.tsx`
- Modify: `web/src/pages/Settings.tsx` (or wherever the settings tabs are registered)

- [ ] **Step I1.1: First inspect how existing settings tabs are registered**

```bash
grep -n "About\|AuditLog\|Authentication\|Cloud\|Database\|Deploy\|settings" web/src/pages/Settings.tsx | head -20
```

Match the pattern existing tabs use (the structure for tab/route registration was established by the prior settings pages).

- [ ] **Step I1.2: Create LLMKeys.tsx**

`web/src/pages/settings/LLMKeys.tsx`:

```tsx
import { useEffect, useState } from 'react';
import { Loader2 } from 'lucide-react';

import { api, type FleetEnvSummary } from '../../api';
import { cn } from '../../lib/cn';

export default function LLMKeysSection() {
  const [summary, setSummary] = useState<FleetEnvSummary | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [anthropic, setAnthropic] = useState('');
  const [openai, setOpenai] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [statusMsg, setStatusMsg] = useState<string | null>(null);

  const reload = () => {
    setError(null);
    api.fleetEnv()
      .then(setSummary)
      .catch((e) => setError(String(e)));
  };

  useEffect(() => { reload(); }, []);

  const isFederated = summary?.source === 'federated_from_parent';

  function save() {
    if (!anthropic && !openai) {
      setError('Enter at least one key.');
      return;
    }
    setSubmitting(true);
    setError(null);
    setStatusMsg(null);
    const patch: { anthropic_api_key?: string; openai_api_key?: string } = {};
    if (anthropic) patch.anthropic_api_key = anthropic;
    if (openai) patch.openai_api_key = openai;
    api.fleetEnvUpdate(patch)
      .then((s) => {
        setSummary(s);
        setAnthropic('');
        setOpenai('');
        setStatusMsg('Keys saved. Distribution to nodes is in progress.');
      })
      .catch((e) => setError(String(e)))
      .finally(() => setSubmitting(false));
  }

  function clearKey(which: 'anthropic_api_key' | 'openai_api_key') {
    if (!confirm(`Clear ${which === 'anthropic_api_key' ? 'Anthropic' : 'OpenAI'} key from the fleet?`)) return;
    setSubmitting(true);
    setError(null);
    api.fleetEnvUpdate({ [which]: '' })
      .then(setSummary)
      .catch((e) => setError(String(e)))
      .finally(() => setSubmitting(false));
  }

  function overrideLocal() {
    api.fleetEnvOverrideLocal().then(reload).catch((e) => setError(String(e)));
  }

  function revertToParent() {
    api.fleetEnvRevertToParent().then(reload).catch((e) => setError(String(e)));
  }

  return (
    <div className="p-6 space-y-4 max-w-2xl">
      <header>
        <h2 className="text-lg font-semibold">LLM API keys</h2>
        <p className="text-xs text-ink-dim mt-0.5">
          Anthropic + OpenAI API keys used by every node's agents, daimons, and jobs across the fleet.
          Saved here are persisted, encrypted at rest, and pushed to nodes via heartbeat / S3 dead-drop.
        </p>
      </header>

      {summary === null ? (
        <div className="flex items-center gap-2 text-ink-dim text-xs">
          <Loader2 size={14} className="animate-spin" /> Loading…
        </div>
      ) : (
        <>
          {error && (
            <div className="text-xs text-red-700 bg-red-50 border border-red-200 px-3 py-2 rounded-md">{error}</div>
          )}
          {statusMsg && (
            <div className="text-xs text-emerald-700 bg-emerald-50 border border-emerald-200 px-3 py-2 rounded-md">{statusMsg}</div>
          )}

          {isFederated && (
            <div className="text-xs text-yellow-700 bg-yellow-50 border border-yellow-200 px-3 py-2 rounded-md">
              <div className="font-medium">Inherited from parent CP</div>
              <div className="mt-0.5">
                Keys are managed by parent CP <code className="font-mono">{summary.parent_cp_id ?? '(unknown)'}</code>.
                {' '}Click <button onClick={overrideLocal} className="text-brand-700 hover:underline">Override locally</button>
                {' '}to set values on this CP that won't be overwritten by the parent.
              </div>
            </div>
          )}
          {!isFederated && summary.parent_cp_id && (
            <div className="text-xs text-ink-mute">
              Local override active. <button onClick={revertToParent} className="text-brand-700 hover:underline">Revert to parent</button>
              {' '}to start receiving updates from <code className="font-mono">{summary.parent_cp_id}</code> again.
            </div>
          )}

          <KeyRow
            label="Anthropic API key"
            envVar="ANTHROPIC_API_KEY"
            isSet={summary.anthropic_set}
            last4={summary.anthropic_last4}
            value={anthropic}
            onChange={setAnthropic}
            onClear={() => clearKey('anthropic_api_key')}
            disabled={isFederated || submitting}
          />
          <KeyRow
            label="OpenAI API key"
            envVar="OPENAI_API_KEY"
            isSet={summary.openai_set}
            last4={summary.openai_last4}
            value={openai}
            onChange={setOpenai}
            onClear={() => clearKey('openai_api_key')}
            disabled={isFederated || submitting}
          />

          <div className="flex justify-end">
            <button
              onClick={save}
              disabled={isFederated || submitting || (!anthropic && !openai)}
              className={cn(
                'text-xs px-3 py-1.5 rounded-md',
                isFederated || submitting || (!anthropic && !openai)
                  ? 'bg-slate-100 text-ink-mute cursor-not-allowed'
                  : 'bg-brand-600 text-white hover:bg-brand-700',
              )}
            >
              {submitting ? 'Saving…' : 'Save'}
            </button>
          </div>

          <div className="text-[11px] text-ink-mute pt-2 border-t border-border">
            Version {summary.version} · last updated {summary.updated_at}
            {summary.updated_by_user_email && <> by {summary.updated_by_user_email}</>}
          </div>
        </>
      )}
    </div>
  );
}

function KeyRow({
  label, envVar, isSet, last4, value, onChange, onClear, disabled,
}: {
  label: string; envVar: string; isSet: boolean; last4: string;
  value: string; onChange: (v: string) => void;
  onClear: () => void; disabled: boolean;
}) {
  return (
    <div className="border border-border rounded-md bg-white p-3 space-y-2">
      <div className="flex items-center justify-between">
        <div>
          <div className="text-sm font-medium">{label}</div>
          <div className="text-[10px] text-ink-mute font-mono">{envVar}</div>
        </div>
        {isSet && (
          <div className="text-[10px] text-ink-dim">
            <span className="px-1.5 py-0.5 rounded bg-emerald-50 text-emerald-700 ring-1 ring-emerald-200">SET</span>
            <span className="ml-2 font-mono">…{last4}</span>
            <button onClick={onClear} disabled={disabled} className="ml-2 text-red-700 hover:underline disabled:opacity-50">Clear</button>
          </div>
        )}
        {!isSet && (
          <span className="text-[10px] px-1.5 py-0.5 rounded bg-slate-100 text-ink-mute ring-1 ring-border">UNSET</span>
        )}
      </div>
      <input
        type="password"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        placeholder={isSet ? 'Replace with new key…' : 'Paste API key…'}
        className="w-full text-sm font-mono px-2.5 py-1.5 rounded-md ring-1 ring-border bg-white disabled:bg-slate-50 disabled:text-ink-mute"
      />
    </div>
  );
}
```

- [ ] **Step I1.3: Register the tab/route**

In `web/src/pages/Settings.tsx` (or wherever the settings nav lives), add an entry pointing to the new component. Match the pattern used by sibling pages (e.g., `Cloud.tsx`, `Authentication.tsx`).

If the routes are static, add:

```tsx
import LLMKeysSection from './settings/LLMKeys';
// …
<Route path="llm-keys" element={<LLMKeysSection />} />
```

And the nav entry alongside the other settings links.

- [ ] **Step I1.4: Build + commit**

```bash
cd web && npm run build && cd ..
```

```bash
pwd && git status
git add web/src/pages/settings/LLMKeys.tsx web/src/pages/Settings.tsx
git commit -m "web(settings/llm-keys): new page for fleet API key management"
```

---

## Task Group Z — Docs + test sweep + PR body

### Task Z1: Architecture doc

**Files:**
- Modify: `docs/architecture.md`

- [ ] **Step Z1.1: Append a section**

Append after the most recent existing section:

```markdown
## Fleet LLM API keys (Settings → LLM Keys)

Operators manage the Anthropic + OpenAI API keys used by the fleet
(agents/daimons/jobs) directly from `Settings → LLM Keys`. Persisted
in the singleton `fleet_env` table (migration 045), AES-GCM sealed
via the same master-key pattern `cloud_credentials` use, with a
distinct HKDF info string (`okesu-fleet-env-v1`) for domain
separation.

Distribution channels:
- **HTTPS pull** (`/api/v1/fleet/env`, mTLS-authed for daemons): the
  daemon's heartbeat loop polls and rewrites `/etc/okesu/jobs.env`
  on `version` change, then `systemctl restart okesu-jobs.service`.
- **S3 dead-drop publish** (`fleet-env.json` artifact alongside
  findings/daimons/etc.): S3-deployed nodes' readers and S3-federated
  child CPs pick up the new artifact on their next bucket scan.

Federation (`/api/v1/federation/fleet-env` for HTTPS-federated
children, S3 artifact for S3-federated children): child stores
received keys with `source = "federated_from_parent"` and
republishes through its own channels — transitive propagation via
existing publish/pull plumbing. Children can override locally; the
override switches `source` to `"local"` and the federation poller
stops overwriting.

Backwards compat: `OKESU_CP_FLEET_ANTHROPIC_API_KEY` /
`OKESU_CP_FLEET_OPENAI_API_KEY` env vars seed the `fleet_env` row on
first boot if it's empty. Once the operator saves through Settings,
the DB wins forever.
```

Commit:

```bash
git add docs/architecture.md
git commit -m "docs(architecture): fleet LLM API keys section"
```

### Task Z2: Full test sweep (verification only)

```bash
go test ./... 2>&1 | tail -50
cd web && npm run build && cd ..
```

Both must pass. Pre-existing UI dist embed false positive only fires if `controlplane/ui/dist` doesn't exist; running `npm run build` first writes it.

### Task Z3: PR body

**Create:** `docs/superpowers/plans/2026-04-30-fleet-llm-keys-pr-body.md`:

```markdown
## Summary

Operators now manage the fleet-wide Anthropic + OpenAI API keys from
Settings → LLM Keys, instead of relying on env vars + CP restarts.
Keys are persisted in the DB (encrypted), exposed via Settings UI,
and automatically distributed to every connected node + every
federated child CP across all transports (tunnel, poll, S3 dead-drop).

- New singleton `fleet_env` table (migration 045), AES-GCM sealed
  via the existing master-key pattern with a separate HKDF info
  string for domain separation.
- New endpoints:
  - Operator: `GET/PUT /api/fleet-env`,
    `POST /api/fleet-env/{override-local,revert-to-parent}`.
  - Daemon (mTLS): `GET /api/v1/fleet/env`.
  - Federation (token): `GET /api/v1/federation/fleet-env`.
- New S3 publisher artifact `fleet-env.json` alongside the existing
  `findings.json` / `daimons.json` / etc. Encrypted to the per-peer
  fleet keypair.
- Daemon's existing heartbeat loop gains a fleet-env poll: rewrites
  `/etc/okesu/jobs.env` on version change and runs
  `systemctl restart okesu-jobs.service`.
- Federation poller gains a `fleet-env` topic: child stores keys
  with `source = "federated_from_parent"`, bumps own version,
  republishes through its own channels (transitive propagation).
- Settings UI: new `LLMKeys.tsx` page with masked-input fields,
  per-key Clear, override-local / revert-to-parent buttons, last4
  display, version + audit indicator.
- Backwards compat: existing
  `OKESU_CP_FLEET_ANTHROPIC_API_KEY` /
  `OKESU_CP_FLEET_OPENAI_API_KEY` env vars seed the DB on first
  boot if it's empty.

Build matrix unchanged: `CGO_ENABLED=0` everywhere.

## Test plan

Lab smoke (post-merge):

- [ ] `go test ./...` passes
- [ ] `npm run build` clean in `web/`
- [ ] On a standalone CP, set both keys via Settings → LLM Keys.
      Verify a connected node's `/etc/okesu/jobs.env` updates within
      one heartbeat cycle and `okesu-jobs.service` restarts.
- [ ] Save on a parent CP federated to a child via HTTPS. Verify
      child's Settings page shows "Inherited from parent" within
      one federation poll cycle, and child's connected nodes update
      within one heartbeat after that.
- [ ] Same flow with an S3-federated child.
- [ ] On a federated child, click "Override locally" and save a
      different key. Verify parent's subsequent updates do NOT
      change the child's key.
- [ ] Click "Revert to parent" on the child. Verify the child picks
      up the parent's current value on the next federation poll.
- [ ] Clear a key (empty string via the UI). Verify the daemon
      receives an empty value on next poll and `/etc/okesu/jobs.env`
      no longer contains that line.

## Files

- New endpoints: see above.
- New table: `fleet_env` (migration 045).
- New page: `web/src/pages/settings/LLMKeys.tsx`.
- New daemon poll: `agent/mgmt.go` `StartFleetEnvPoll`.

## Spec / plan

- Spec: `docs/superpowers/specs/2026-04-30-fleet-llm-keys-design.md`
- Plan: `docs/superpowers/plans/2026-04-30-fleet-llm-keys.md`

## Open follow-ups

- Provider-specific validation on save
  (`anthropic.models.list` / `openai.models.list` ping).
- Per-node / per-agent key overrides.
- Other LLM providers (Google / Cohere / etc.).
- Usage tracking + quota.
- Time-based auto-rotation.
- Drain-then-restart `okesu-jobs.service` instead of unconditional
  restart (currently kills in-flight jobs).
```

Commit:

```bash
git add docs/superpowers/plans/2026-04-30-fleet-llm-keys-pr-body.md
git commit -m "docs: fleet LLM API keys PR body"
```

### Task Z4: DO NOT push or open the PR

The controller (the human operator) handles `git push` and `gh pr create`.

---

## Self-review checklist

- [ ] Spec coverage:
  - **Storage + encryption** → Group A
  - **Operator GET/PUT + override/revert** → Group B
  - **Daemon-mTLS endpoint + federation export** → Group C
  - **cpLocalEnv refresh + boot seed + S3 asset registration + route mounts** → Group D
  - **auto_deploy reads from DB** → Group E
  - **Federation receive + republish** → Group F
  - **Daemon-side fleet-env poll + jobs.env rewrite + systemctl restart** → Group G
  - **Frontend api.ts** → Group H
  - **Settings UI** → Group I
- [ ] No build matrix changes — `grep -n "CGO_ENABLED" Makefile` should still show only `=0`.
- [ ] Migration 045 in BOTH sqlite and postgres directories with parity.
- [ ] Encryption uses HKDF info `okesu-fleet-env-v1` (NOT `okesu-cloud-credentials-v1`) — domain separation.
- [ ] Plaintext keys never appear in operator-facing GET responses (only daemon-mTLS + federation-token endpoints).
- [ ] Audit log entries written for every operator save / override / revert.
- [ ] Federation poller respects `source == "local"` (does not overwrite).
- [ ] Federation update is idempotent on stale versions (skip when `parent_version <= current_version`).
- [ ] Daemon poll caches version and only acts on change.
- [ ] No placeholders in any task; every step shows literal code/command.
