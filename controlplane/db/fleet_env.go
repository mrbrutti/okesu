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
	AnthropicAPIKey    string // plaintext; populated only by GetFleetEnvWithKeys
	OpenAIAPIKey       string // plaintext; populated only by GetFleetEnvWithKeys
	AnthropicLast4     string
	OpenAILast4        string
	HasAnthropic       bool
	HasOpenAI          bool
	Version            int64
	Source             string // "local" | "federated_from_parent"
	ParentCPID         sql.NullString
	UpdatedAt          time.Time
	UpdatedByUserEmail sql.NullString
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
	AnthropicAPIKey    *string
	OpenAIAPIKey       *string
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
	var currentUpdatedBy sql.NullString
	if err := tx.QueryRow(`SELECT source, version, updated_by_user_email FROM fleet_env WHERE id = 1`).Scan(&currentSource, &currentVersion, &currentUpdatedBy); err != nil {
		return 0, false, err
	}
	// Operator-set local values are not overridden by federation. The
	// migration default is source='local', version=0 — that "empty"
	// state still lets federation seed the row (per spec: poller
	// updates when source=federated OR row is empty). Once the
	// operator types a key in Settings, version > 0 and this guard
	// keeps their value untouched.
	//
	// Exception: a row that's still on the boot-env seed
	// (updated_by_user_email = "boot:env-seed") is treated as
	// not-yet-pinned even though version > 0. The CP started with
	// environment-supplied keys but the operator hasn't actually said
	// "use this." Federation pushes win in that case so a parent's
	// update propagates to fresh children without manual
	// override-flipping. Once an operator explicitly PUTs from the UI
	// (updated_by = a real email), the guard locks back in.
	bootSeeded := currentUpdatedBy.Valid && currentUpdatedBy.String == "boot:env-seed"
	if !bootSeeded && currentSource == "local" && currentVersion > 0 {
		return currentVersion, false, nil
	}
	// Version-monotonicity skip — but only when comparing against a
	// row from the SAME provenance. A boot-seeded row's version=1
	// shouldn't block a parent push at version=4; that's the bug the
	// boot-seed exception fixes. After the first apply, the row's
	// source flips to federated_from_parent and this guard kicks in.
	if !bootSeeded && parentVersion <= currentVersion {
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
			version = ?,
			source = 'federated_from_parent',
			parent_cp_id = ?,
			updated_at = CURRENT_TIMESTAMP,
			updated_by_user_email = NULL
		WHERE id = 1`,
		aSealed, aNonce, aLast4,
		oSealed, oNonce, oLast4,
		parentVersion, parentCPID); err != nil {
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
