package db

import (
	"database/sql"
	"time"
)

// Agent is a registered daemon agent. Identity is (Name, Host) — the same
// agent name running on different hosts is two distinct rows.
type Agent struct {
	Name             string
	Host             string
	Provider         sql.NullString
	Model            sql.NullString
	Version          sql.NullString // okesu binary version
	RegisteredAt     time.Time
	LastHeartbeatAt  sql.NullTime
	LastTickCount    int64
	DesiredMaxTurns  sql.NullInt64
	DesiredEffort    sql.NullString
	DesiredSuspended bool
	ConfigUpdatedAt  sql.NullTime
	// CurrentDefinitionHash is the sha256 the daemon reports it has
	// loaded. Compared against the CP's canonical hash to surface drift
	// on the Daimon Library page.
	CurrentDefinitionHash sql.NullString
	// DefinitionVersion is the operator-set version label from the
	// daimon file's YAML frontmatter (e.g. "2", "v3"). Reported by the
	// daemon at every heartbeat alongside the hash. Human-readable
	// counterpart to the opaque hash.
	DefinitionVersion sql.NullString
}

// UpsertAgentRegistration creates or updates an agent on registration.
// Identity is (name, host) — preserves desired config across re-registrations
// of the same daemon, but a different host registers as a new row.
func (s *Store) UpsertAgentRegistration(name, host, provider, model, version, definitionVersion string) error {
	if host == "" {
		host = "(unknown)"
	}
	_, err := s.Exec(`
		INSERT INTO agents (name, host, provider, model, version, definition_version, registered_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(name, host) DO UPDATE SET
			provider           = excluded.provider,
			model              = excluded.model,
			version            = excluded.version,
			definition_version = excluded.definition_version,
			registered_at      = CURRENT_TIMESTAMP
	`, name, host, provider, model, version, definitionVersion)
	return err
}

// RecordHeartbeat updates the last_heartbeat_at and last_tick_count for an
// agent identified by (name, host). Auto-creates the row if the agent
// hasn't registered yet — common for daemons whose first poll arrives
// before their registration round-trip completes.
//
// definitionHash is the sha256 the daemon reports it has loaded; empty
// preserves the existing value. definitionVersion is the operator-set
// label from the daimon file's frontmatter; empty also preserves.
func (s *Store) RecordHeartbeat(name, host string, tickCount int64, definitionHash, definitionVersion string) error {
	if host == "" {
		host = "(unknown)"
	}

	// Build the SET clause dynamically so we don't clobber existing
	// values when the daemon doesn't report them. Hash and version both
	// have legacy-empty semantics: the daemon may be older than Phase 7b
	// (no hash) or pre-version-frontmatter (no version).
	setParts := []string{
		"last_heartbeat_at = CURRENT_TIMESTAMP",
		"last_tick_count = ?",
	}
	args := []any{tickCount}
	if definitionHash != "" {
		setParts = append(setParts, "current_definition_hash = ?")
		args = append(args, definitionHash)
	}
	if definitionVersion != "" {
		setParts = append(setParts, "definition_version = ?")
		args = append(args, definitionVersion)
	}
	args = append(args, name, host)

	res, err := s.Exec(`
		UPDATE agents SET `+joinComma(setParts)+`
		WHERE name = ? AND host = ?
	`, args...)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows > 0 {
		return nil
	}

	// Insert path — agent's first heartbeat before its registration arrived.
	_, err = s.Exec(`
		INSERT INTO agents (name, host, last_heartbeat_at, last_tick_count, current_definition_hash, definition_version)
		VALUES (?, ?, CURRENT_TIMESTAMP, ?, ?, ?)
		ON CONFLICT(name, host) DO UPDATE SET
			last_heartbeat_at       = CURRENT_TIMESTAMP,
			last_tick_count         = excluded.last_tick_count,
			current_definition_hash = COALESCE(NULLIF(excluded.current_definition_hash, ''), agents.current_definition_hash),
			definition_version      = COALESCE(NULLIF(excluded.definition_version, ''), agents.definition_version)
	`, name, host, tickCount, definitionHash, definitionVersion)
	return err
}

// joinComma is a tiny helper kept package-local to avoid pulling strings
// into a file that doesn't otherwise need it.
func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}

// AgentByNameHost returns the row for one (name, host) pair.
func (s *Store) AgentByNameHost(name, host string) (*Agent, error) {
	a := &Agent{}
	err := s.QueryRow(`
		SELECT name, host, provider, model, version,
		       registered_at, last_heartbeat_at, last_tick_count,
		       desired_max_turns, desired_effort, desired_suspended,
		       config_updated_at, current_definition_hash, definition_version
		FROM agents WHERE name = ? AND host = ?
	`, name, host).Scan(
		&a.Name, &a.Host, &a.Provider, &a.Model, &a.Version,
		&a.RegisteredAt, &a.LastHeartbeatAt, &a.LastTickCount,
		&a.DesiredMaxTurns, &a.DesiredEffort, &a.DesiredSuspended,
		&a.ConfigUpdatedAt, &a.CurrentDefinitionHash, &a.DefinitionVersion,
	)
	if err != nil {
		return nil, err
	}
	return a, nil
}

// AgentByName returns the most-recently-heartbeat row for `name` across
// all hosts. Provided for backward-compat with code paths that still
// identify agents by name only (e.g. config-poll endpoint, where the
// daemon's mTLS cert CN is the agent name and there's no host hint).
//
// New code should prefer AgentByNameHost when the host is known.
func (s *Store) AgentByName(name string) (*Agent, error) {
	a := &Agent{}
	err := s.QueryRow(`
		SELECT name, host, provider, model, version,
		       registered_at, last_heartbeat_at, last_tick_count,
		       desired_max_turns, desired_effort, desired_suspended,
		       config_updated_at, current_definition_hash, definition_version
		FROM agents
		WHERE name = ?
		ORDER BY (last_heartbeat_at IS NULL), last_heartbeat_at DESC
		LIMIT 1
	`, name).Scan(
		&a.Name, &a.Host, &a.Provider, &a.Model, &a.Version,
		&a.RegisteredAt, &a.LastHeartbeatAt, &a.LastTickCount,
		&a.DesiredMaxTurns, &a.DesiredEffort, &a.DesiredSuspended,
		&a.ConfigUpdatedAt, &a.CurrentDefinitionHash, &a.DefinitionVersion,
	)
	if err != nil {
		return nil, err
	}
	return a, nil
}

// ListAgents returns agents ordered by last heartbeat descending.
// One row per (name, host) pair. Pages via limit + offset; the UI's
// infinite-scroll calls it with limit=N, offset=loaded. limit=0 falls
// back to 1000 — large enough for almost any console while bounding
// worst-case memory.
func (s *Store) ListAgents(limit, offset int) ([]*Agent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.Query(`
		SELECT name, host, provider, model, version,
		       registered_at, last_heartbeat_at, last_tick_count,
		       desired_max_turns, desired_effort, desired_suspended,
		       config_updated_at, current_definition_hash, definition_version
		FROM agents
		ORDER BY (last_heartbeat_at IS NULL), last_heartbeat_at DESC, name, host
		LIMIT ? OFFSET ?
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Agent
	for rows.Next() {
		a := &Agent{}
		if err := rows.Scan(
			&a.Name, &a.Host, &a.Provider, &a.Model, &a.Version,
			&a.RegisteredAt, &a.LastHeartbeatAt, &a.LastTickCount,
			&a.DesiredMaxTurns, &a.DesiredEffort, &a.DesiredSuspended,
			&a.ConfigUpdatedAt, &a.CurrentDefinitionHash, &a.DefinitionVersion,
		); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetDesiredConfig updates the desired runtime overrides for an agent.
// Pass nil pointers to leave a field unchanged; empty/zero values clear
// the override. Applies to ALL hosts running the named agent — the desired
// config is logically per-agent-name (operators tune the agent, not a
// specific instance), so an `edr` MaxTurns change rolls to every host.
func (s *Store) SetDesiredConfig(name string, maxTurns *int64, effort *string, suspended *bool) error {
	q := "UPDATE agents SET config_updated_at = CURRENT_TIMESTAMP"
	args := []any{}
	if maxTurns != nil {
		if *maxTurns == 0 {
			q += ", desired_max_turns = NULL"
		} else {
			q += ", desired_max_turns = ?"
			args = append(args, *maxTurns)
		}
	}
	if effort != nil {
		if *effort == "" {
			q += ", desired_effort = NULL"
		} else {
			q += ", desired_effort = ?"
			args = append(args, *effort)
		}
	}
	if suspended != nil {
		q += ", desired_suspended = ?"
		v := 0
		if *suspended {
			v = 1
		}
		args = append(args, v)
	}
	q += " WHERE name = ?"
	args = append(args, name)
	_, err := s.Exec(q, args...)
	return err
}
