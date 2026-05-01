package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// FeedConfig is the read shape returned by GetFeedConfig / ListFeedConfigs.
type FeedConfig struct {
	ID                     int64
	Slug                   string
	Name                   string
	Kind                   string // "single_file" | "git"
	URL                    string
	Subpath                string
	Parser                 string
	AuthCredentialID       sql.NullInt64
	RefreshIntervalSeconds int
	Enabled                bool
	InstalledFromRegistry  bool
	LastRefreshAt          sql.NullTime
	LastRefreshStatus      string
	LastRefreshError       string
	LastRefreshEntryCount  int
	Source                 string         // "local" | "federated_from_parent"
	ParentCPID             sql.NullString // populated only when Source = federated_from_parent
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// FeedConfigInsert is the write shape for InsertFeedConfig. Slug must be
// unique; the caller is responsible for picking a stable identifier.
type FeedConfigInsert struct {
	Slug                   string
	Name                   string
	Kind                   string
	URL                    string
	Subpath                string
	Parser                 string
	AuthCredentialID       *int64
	RefreshIntervalSeconds int
	Enabled                bool
	InstalledFromRegistry  bool
	Source                 string // optional; "local" if empty. Federation poller sets "federated_from_parent".
	ParentCPID             string // optional; required when Source = "federated_from_parent"
}

// FeedConfigPatch is the partial-update shape for UpdateFeedConfig. Nil
// pointers leave the field alone; non-nil overwrites.
type FeedConfigPatch struct {
	Name                   *string
	URL                    *string
	Subpath                *string
	Parser                 *string
	// AuthCredentialID: nil leaves the column alone; a non-nil &sql.NullInt64
	// with Valid=false clears it; with Valid=true sets it to Int64.
	AuthCredentialID       *sql.NullInt64
	RefreshIntervalSeconds *int
	Enabled                *bool
}

// InsertFeedConfig inserts a new feed config row and returns its id.
func (s *Store) InsertFeedConfig(in *FeedConfigInsert) (int64, error) {
	if in.Slug == "" || in.Kind == "" || in.URL == "" || in.Parser == "" {
		return 0, fmt.Errorf("InsertFeedConfig: slug, kind, url, parser are required")
	}
	if in.RefreshIntervalSeconds <= 0 {
		in.RefreshIntervalSeconds = 86400
	}
	source := in.Source
	if source == "" {
		source = "local"
	}
	res, err := s.Exec(`
		INSERT INTO ioc_feeds
		  (slug, name, kind, url, subpath, parser, auth_credential_id,
		   refresh_interval_seconds, enabled, installed_from_registry,
		   source, parent_cp_id,
		   created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		in.Slug, in.Name, in.Kind, in.URL, nullable(in.Subpath), in.Parser,
		nullableInt64Ptr(in.AuthCredentialID),
		in.RefreshIntervalSeconds, boolToInt(in.Enabled), boolToInt(in.InstalledFromRegistry),
		source, nullable(in.ParentCPID))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetFeedConfig returns the feed config with the given id, or an error if
// not found.
func (s *Store) GetFeedConfig(id int64) (*FeedConfig, error) {
	row := s.QueryRow(`SELECT `+feedSelectCols+` FROM ioc_feeds WHERE id = ?`, id)
	return scanFeedConfig(row)
}

// GetFeedConfigBySlug returns the feed config with the given slug, or an
// error if not found.
func (s *Store) GetFeedConfigBySlug(slug string) (*FeedConfig, error) {
	row := s.QueryRow(`SELECT `+feedSelectCols+` FROM ioc_feeds WHERE slug = ?`, slug)
	return scanFeedConfig(row)
}

// ListFeedConfigs returns all feed configs ordered by name ascending.
func (s *Store) ListFeedConfigs() ([]FeedConfig, error) {
	rows, err := s.Query(`SELECT ` + feedSelectCols + ` FROM ioc_feeds ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FeedConfig
	for rows.Next() {
		fc, err := scanFeedConfig(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *fc)
	}
	return out, rows.Err()
}

// UpdateFeedConfig applies a partial update to the feed config with the
// given id. Nil fields in the patch are left unchanged.
func (s *Store) UpdateFeedConfig(id int64, p *FeedConfigPatch) error {
	sets := []string{"updated_at = CURRENT_TIMESTAMP"}
	args := []any{}
	if p.Name != nil {
		sets = append(sets, "name = ?")
		args = append(args, *p.Name)
	}
	if p.URL != nil {
		sets = append(sets, "url = ?")
		args = append(args, *p.URL)
	}
	if p.Subpath != nil {
		sets = append(sets, "subpath = ?")
		args = append(args, nullable(*p.Subpath))
	}
	if p.Parser != nil {
		sets = append(sets, "parser = ?")
		args = append(args, *p.Parser)
	}
	if p.AuthCredentialID != nil {
		if !p.AuthCredentialID.Valid {
			sets = append(sets, "auth_credential_id = NULL")
		} else {
			sets = append(sets, "auth_credential_id = ?")
			args = append(args, p.AuthCredentialID.Int64)
		}
	}
	if p.RefreshIntervalSeconds != nil {
		sets = append(sets, "refresh_interval_seconds = ?")
		args = append(args, *p.RefreshIntervalSeconds)
	}
	if p.Enabled != nil {
		sets = append(sets, "enabled = ?")
		args = append(args, boolToInt(*p.Enabled))
	}
	if len(sets) == 1 {
		// nothing to update besides the timestamp sentinel — no-op
		return nil
	}
	args = append(args, id)
	q := `UPDATE ioc_feeds SET ` + strings.Join(sets, ", ") + ` WHERE id = ?`
	res, err := s.Exec(q, args...)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("feed not found")
	}
	return nil
}

// MarkFeedRefresh records the outcome of a refresh attempt. status is
// "ok" or "error"; errMsg is empty on success, truncated to 1 KB on
// error; entryCount is the number of rows produced (0 on error). at is
// the wall-clock time the refresh completed.
//
// Unlike UpdateFeedConfig / DeleteFeedConfig, this does NOT check
// RowsAffected: the worker may race with an uninstall, and silently
// succeeding when the feed was just deleted is intentional (the next
// refresh attempt simply won't be scheduled).
func (s *Store) MarkFeedRefresh(id int64, status, errMsg string, entryCount int, at *time.Time) error {
	if len(errMsg) > 1024 {
		errMsg = errMsg[:1024]
	}
	_, err := s.Exec(`
		UPDATE ioc_feeds
		   SET last_refresh_at = ?, last_refresh_status = ?, last_refresh_error = ?,
		       last_refresh_entry_count = ?, updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`,
		at, status, nullable(errMsg), entryCount, id)
	return err
}

// DeleteFeedConfig removes the feed config with the given id. Returns an
// error if the row is not found.
func (s *Store) DeleteFeedConfig(id int64) error {
	res, err := s.Exec(`DELETE FROM ioc_feeds WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("feed not found")
	}
	return nil
}

const feedSelectCols = `
	id, slug, name, kind, url, COALESCE(subpath, ''), parser,
	auth_credential_id, refresh_interval_seconds,
	enabled, installed_from_registry,
	last_refresh_at, COALESCE(last_refresh_status, ''),
	COALESCE(last_refresh_error, ''), COALESCE(last_refresh_entry_count, 0),
	COALESCE(source, 'local'), parent_cp_id,
	created_at, updated_at`

func scanFeedConfig(r rowScanner) (*FeedConfig, error) {
	var fc FeedConfig
	var enabledInt, fromRegInt int
	if err := r.Scan(
		&fc.ID, &fc.Slug, &fc.Name, &fc.Kind, &fc.URL, &fc.Subpath, &fc.Parser,
		&fc.AuthCredentialID, &fc.RefreshIntervalSeconds,
		&enabledInt, &fromRegInt,
		&fc.LastRefreshAt, &fc.LastRefreshStatus,
		&fc.LastRefreshError, &fc.LastRefreshEntryCount,
		&fc.Source, &fc.ParentCPID,
		&fc.CreatedAt, &fc.UpdatedAt,
	); err != nil {
		return nil, err
	}
	fc.Enabled = enabledInt != 0
	fc.InstalledFromRegistry = fromRegInt != 0
	return &fc, nil
}

// nullableInt64Ptr converts a *int64 to a SQL-compatible value: nil
// produces a SQL NULL, non-nil dereferences the pointer.
func nullableInt64Ptr(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

// FederationFeedConfig is the wire shape the parent CP exposes via
// /api/v1/federation/feeds. Mirrors the parent's FeedConfig minus
// secret-bearing fields (auth_credential_id is not federated; child
// CPs that need auth must configure their own credential and override
// locally).
type FederationFeedConfig struct {
	Slug                   string
	Name                   string
	Kind                   string
	URL                    string
	Subpath                string
	Parser                 string
	RefreshIntervalSeconds int
	Enabled                bool
}

// SetFeedConfigsFromFederation reconciles the local ioc_feeds table
// against the parent's authoritative list. For each parent feed:
//   - If the local slug is absent → insert with source = "federated_from_parent".
//   - If present AND source = "federated_from_parent" → update fields
//     in place (config drift); leaves last_refresh_* alone since the
//     child runs its own refreshes.
//   - If present AND source = "local" → leave alone (operator's
//     override takes precedence).
//
// For each local row with source = "federated_from_parent" whose slug
// is NOT in the parent's list, the feed ID is returned in toUninstall.
// The caller (federation poller) is responsible for calling
// feeds.Reconcile + DeleteFeedConfig for each entry in toUninstall —
// the same path used by UninstallFeedHandler.
//
// Returns counts: inserted, updated, and the list of feed IDs the
// caller must uninstall via the normal Reconcile-then-DeleteFeedConfig
// flow.
func (s *Store) SetFeedConfigsFromFederation(parentCPID string, parent []FederationFeedConfig) (inserted, updated int, toUninstall []int64, err error) {
	tx, txErr := s.Begin()
	if txErr != nil {
		return 0, 0, nil, txErr
	}
	defer tx.Rollback()

	// Build lookup map for the parent's feed set.
	parentBySlug := make(map[string]FederationFeedConfig, len(parent))
	for _, pf := range parent {
		parentBySlug[pf.Slug] = pf
	}

	// Query existing local rows: slug, id, source.
	rows, queryErr := tx.Query(`SELECT slug, id, source FROM ioc_feeds`)
	if queryErr != nil {
		return 0, 0, nil, queryErr
	}
	type localRow struct {
		id     int64
		source string
	}
	localSlugs := make(map[string]localRow)
	federatedSlugs := make(map[string]localRow)
	for rows.Next() {
		var slug, src string
		var id int64
		if scanErr := rows.Scan(&slug, &id, &src); scanErr != nil {
			rows.Close()
			return 0, 0, nil, scanErr
		}
		lr := localRow{id: id, source: src}
		localSlugs[slug] = lr
		if src == "federated_from_parent" {
			federatedSlugs[slug] = lr
		}
	}
	rows.Close()
	if rowsErr := rows.Err(); rowsErr != nil {
		return 0, 0, nil, rowsErr
	}

	// Reconcile parent feeds against local state.
	for _, pf := range parent {
		lr, exists := localSlugs[pf.Slug]
		if !exists {
			// Insert new federated row.
			interval := pf.RefreshIntervalSeconds
			if interval <= 0 {
				interval = 86400
			}
			_, insErr := tx.Exec(`
				INSERT INTO ioc_feeds
				  (slug, name, kind, url, subpath, parser,
				   refresh_interval_seconds, enabled,
				   source, parent_cp_id,
				   created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'federated_from_parent', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
				pf.Slug, pf.Name, pf.Kind, pf.URL, nullable(pf.Subpath), pf.Parser,
				interval, boolToInt(pf.Enabled), parentCPID)
			if insErr != nil {
				return 0, 0, nil, insErr
			}
			inserted++
		} else if lr.source == "federated_from_parent" {
			// Update config columns; leave last_refresh_* alone.
			interval := pf.RefreshIntervalSeconds
			if interval <= 0 {
				interval = 86400
			}
			_, updErr := tx.Exec(`
				UPDATE ioc_feeds SET
					name = ?, kind = ?, url = ?, subpath = ?, parser = ?,
					refresh_interval_seconds = ?, enabled = ?,
					parent_cp_id = ?, updated_at = CURRENT_TIMESTAMP
				WHERE slug = ? AND source = 'federated_from_parent'`,
				pf.Name, pf.Kind, pf.URL, nullable(pf.Subpath), pf.Parser,
				interval, boolToInt(pf.Enabled), parentCPID, pf.Slug)
			if updErr != nil {
				return 0, 0, nil, updErr
			}
			updated++
		}
		// else: source = "local" — operator override; leave untouched.
	}

	// Build toUninstall: federated rows not in the parent's list.
	for slug, lr := range federatedSlugs {
		if _, inParent := parentBySlug[slug]; !inParent {
			toUninstall = append(toUninstall, lr.id)
		}
	}

	if commitErr := tx.Commit(); commitErr != nil {
		return 0, 0, nil, commitErr
	}
	return inserted, updated, toUninstall, nil
}
