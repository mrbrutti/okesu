// Package db provides the relational state store for the Control Plane.
//
// Two backends are supported, selected by the DSN passed to Open:
//   - sqlite — file path (e.g. "./cp.db") or "sqlite://..."
//   - postgres — "postgres://user:pass@host:port/db?sslmode=..."
//
// Phase 8b lands the connection-time foundation: dialect detection,
// driver selection, and a Store that knows which backend it's running
// against. The schema migration port is incremental — sqlite remains
// the production-ready backend, postgres is wired up but its
// migrations are tracked in a separate migrations/postgres/ directory
// that is filled in as we exercise the queries. Running against
// postgres today connects cleanly but applying migrations beyond the
// init blocks will fail until that directory is populated. Operators
// stick with sqlite until the port is complete; the abstraction lets
// us land the OCI Database PostgreSQL deployment shape in a series of
// small, reviewable commits rather than one giant SQL drop.
package db

import (
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // postgres
	_ "modernc.org/sqlite"             // sqlite
)

// Dialect identifies the SQL flavour the Store is running against.
// Drives placeholder rewriting, autoincrement syntax, and migration
// directory selection.
type Dialect string

const (
	DialectSQLite   Dialect = "sqlite"
	DialectPostgres Dialect = "postgres"
)

// detectDialect classifies the DSN. SQLite is the default for anything
// that doesn't have a recognised URL scheme; matches the historical
// "the DSN is a file path" behaviour.
func detectDialect(dsn string) Dialect {
	switch {
	case strings.HasPrefix(dsn, "postgres://"),
		strings.HasPrefix(dsn, "postgresql://"):
		return DialectPostgres
	case strings.HasPrefix(dsn, "sqlite://"):
		return DialectSQLite
	default:
		// File path → sqlite (legacy default).
		return DialectSQLite
	}
}

// Migration files are embedded per-dialect. The Go-side migration
// runner reads from sqliteMigrations or postgresMigrations based on
// the Store's Dialect. Both lists are kept in lockstep — every SQLite
// migration N has a Postgres counterpart at the same N — so the
// schema_migrations version tracking works the same way regardless of
// backend.
//
// Adding a new migration: drop the file in BOTH migrations/sqlite/
// and migrations/postgres/, add an embed directive + slice entry to
// each side, increment the bootstrap probe in applyMigrations.

//go:embed migrations/sqlite/001_init.sql
var sqliteM001 string

//go:embed migrations/sqlite/002_agents.sql
var sqliteM002 string

//go:embed migrations/sqlite/003_findings.sql
var sqliteM003 string

//go:embed migrations/sqlite/004_nodes.sql
var sqliteM004 string

//go:embed migrations/sqlite/005_audit_log.sql
var sqliteM005 string

//go:embed migrations/sqlite/006_known_hosts.sql
var sqliteM006 string

//go:embed migrations/sqlite/007_daemon_binaries.sql
var sqliteM007 string

//go:embed migrations/sqlite/008_notifications.sql
var sqliteM008 string

//go:embed migrations/sqlite/009_api_tokens.sql
var sqliteM009 string

//go:embed migrations/sqlite/010_runs.sql
var sqliteM010 string

//go:embed migrations/sqlite/011_finding_attributes.sql
var sqliteM011 string

//go:embed migrations/sqlite/012_agents_composite_key.sql
var sqliteM012 string

//go:embed migrations/sqlite/013_finding_status.sql
var sqliteM013 string

//go:embed migrations/sqlite/014_node_daemon_hostname.sql
var sqliteM014 string

//go:embed migrations/sqlite/015_finding_severity_override.sql
var sqliteM015 string

//go:embed migrations/sqlite/016_run_finding_link.sql
var sqliteM016 string

//go:embed migrations/sqlite/017_node_metadata.sql
var sqliteM017 string

//go:embed migrations/sqlite/018_agent_definition_hash.sql
var sqliteM018 string

//go:embed migrations/sqlite/019_agent_definition_version.sql
var sqliteM019 string

//go:embed migrations/sqlite/020_node_auto_update_paused.sql
var sqliteM020 string

//go:embed migrations/sqlite/021_cp_meta.sql
var sqliteM021 string

//go:embed migrations/sqlite/022_federation_peers.sql
var sqliteM022 string

//go:embed migrations/sqlite/023_orchestrations.sql
var sqliteM023 string

//go:embed migrations/sqlite/024_orchestration_triggers.sql
var sqliteM024 string

//go:embed migrations/sqlite/025_node_jobs.sql
var sqliteM025 string

//go:embed migrations/sqlite/026_s3_transport.sql
var sqliteM026 string

//go:embed migrations/sqlite/027_finding_edits.sql
var sqliteM027 string

//go:embed migrations/sqlite/028_orchestration_step_data.sql
var sqliteM028 string

//go:embed migrations/sqlite/029_cp_bootstrap_tokens.sql
var sqliteM029 string


//go:embed migrations/sqlite/030_cloud_credentials.sql
var sqliteM030 string

//go:embed migrations/sqlite/031_cp_provisions.sql
var sqliteM031 string

//go:embed migrations/sqlite/032_cost_guardrails.sql
var sqliteM032 string

//go:embed migrations/sqlite/033_transport_endpoint_split.sql
var sqliteM033 string

//go:embed migrations/sqlite/034_iocs.sql
var sqliteM034 string

//go:embed migrations/sqlite/035_finding_clusters_and_lessons.sql
var sqliteM035 string

//go:embed migrations/sqlite/036_investigations_and_subtype.sql
var sqliteM036 string

//go:embed migrations/sqlite/037_federation_s3_transport.sql
var sqliteM037 string

//go:embed migrations/sqlite/038_ioc_enrichments_and_relationships.sql
var sqliteM038 string

//go:embed migrations/sqlite/039_step_node_dispatches.sql
var sqliteM039 string

var sqliteMigrations = []string{
	sqliteM001, sqliteM002, sqliteM003,
	sqliteM004, sqliteM005, sqliteM006, sqliteM007,
	sqliteM008, sqliteM009, sqliteM010, sqliteM011, sqliteM012,
	sqliteM013, sqliteM014, sqliteM015, sqliteM016, sqliteM017,
	sqliteM018, sqliteM019, sqliteM020, sqliteM021, sqliteM022,
	sqliteM023, sqliteM024, sqliteM025, sqliteM026, sqliteM027,

	sqliteM028, sqliteM029, sqliteM030, sqliteM031, sqliteM032, sqliteM033, sqliteM034,
	sqliteM035, sqliteM036, sqliteM037, sqliteM038, sqliteM039,
}

//go:embed migrations/postgres/001_init.sql
var pgM001 string

//go:embed migrations/postgres/002_agents.sql
var pgM002 string

//go:embed migrations/postgres/003_findings.sql
var pgM003 string

//go:embed migrations/postgres/004_nodes.sql
var pgM004 string

//go:embed migrations/postgres/005_audit_log.sql
var pgM005 string

//go:embed migrations/postgres/006_known_hosts.sql
var pgM006 string

//go:embed migrations/postgres/007_daemon_binaries.sql
var pgM007 string

//go:embed migrations/postgres/008_notifications.sql
var pgM008 string

//go:embed migrations/postgres/009_api_tokens.sql
var pgM009 string

//go:embed migrations/postgres/010_runs.sql
var pgM010 string

//go:embed migrations/postgres/011_finding_attributes.sql
var pgM011 string

//go:embed migrations/postgres/012_agents_composite_key.sql
var pgM012 string

//go:embed migrations/postgres/013_finding_status.sql
var pgM013 string

//go:embed migrations/postgres/014_node_daemon_hostname.sql
var pgM014 string

//go:embed migrations/postgres/015_finding_severity_override.sql
var pgM015 string

//go:embed migrations/postgres/016_run_finding_link.sql
var pgM016 string

//go:embed migrations/postgres/017_node_metadata.sql
var pgM017 string

//go:embed migrations/postgres/018_agent_definition_hash.sql
var pgM018 string

//go:embed migrations/postgres/019_agent_definition_version.sql
var pgM019 string

//go:embed migrations/postgres/020_node_auto_update_paused.sql
var pgM020 string

//go:embed migrations/postgres/021_cp_meta.sql
var pgM021 string

//go:embed migrations/postgres/022_federation_peers.sql
var pgM022 string

//go:embed migrations/postgres/023_orchestrations.sql
var pgM023 string

//go:embed migrations/postgres/024_orchestration_triggers.sql
var pgM024 string

//go:embed migrations/postgres/025_node_jobs.sql
var pgM025 string

//go:embed migrations/postgres/026_s3_transport.sql
var pgM026 string

//go:embed migrations/postgres/027_finding_edits.sql
var pgM027 string

//go:embed migrations/postgres/028_orchestration_step_data.sql
var pgM028 string

//go:embed migrations/postgres/029_cp_bootstrap_tokens.sql
var pgM029 string


//go:embed migrations/postgres/030_cloud_credentials.sql
var pgM030 string

//go:embed migrations/postgres/031_cp_provisions.sql
var pgM031 string

//go:embed migrations/postgres/032_cost_guardrails.sql
var pgM032 string

//go:embed migrations/postgres/033_transport_endpoint_split.sql
var pgM033 string

//go:embed migrations/postgres/034_iocs.sql
var pgM034 string

//go:embed migrations/postgres/035_finding_clusters_and_lessons.sql
var pgM035 string

//go:embed migrations/postgres/036_investigations_and_subtype.sql
var pgM036 string

//go:embed migrations/postgres/037_federation_s3_transport.sql
var pgM037 string

//go:embed migrations/postgres/038_ioc_enrichments_and_relationships.sql
var pgM038 string

//go:embed migrations/postgres/039_step_node_dispatches.sql
var pgM039 string

var postgresMigrations = []string{
	pgM001, pgM002, pgM003,
	pgM004, pgM005, pgM006, pgM007,
	pgM008, pgM009, pgM010, pgM011, pgM012,
	pgM013, pgM014, pgM015, pgM016, pgM017,
	pgM018, pgM019, pgM020, pgM021, pgM022,

	pgM023, pgM024, pgM025, pgM026, pgM027, pgM028, pgM029, pgM030, pgM031, pgM032, pgM033, pgM034,
	pgM035, pgM036, pgM037, pgM038, pgM039,
}

// migrationsForDialect returns the embedded list matching the dialect.
func migrationsForDialect(d Dialect) []string {
	if d == DialectPostgres {
		return postgresMigrations
	}
	return sqliteMigrations
}

// Store wraps a *sql.DB with helpers used across the controlplane package.
//
// Dialect records which backend we're running against so call sites that
// need dialect-specific SQL (rare — most queries are portable) can branch.
type Store struct {
	*sql.DB
	path    string
	Dialect Dialect
}

// Open creates (or opens) the database at the given DSN, applies
// migrations, and returns a ready-to-use Store.
//
// DSN forms:
//   - "./cp.db"                              — sqlite file path (legacy, default)
//   - "sqlite:///abs/path.db"                — explicit sqlite scheme
//   - "postgres://user:pass@host:5432/cpdb"  — managed Postgres
//
// For SQLite, the parent directory is created if missing — matches the
// legacy "you can point this at any path" UX. For Postgres we only
// connect; provisioning is the operator's responsibility.
func Open(dsn string) (*Store, error) {
	dialect := detectDialect(dsn)
	switch dialect {
	case DialectSQLite:
		return openSQLite(dsn)
	case DialectPostgres:
		return openPostgres(dsn)
	default:
		return nil, fmt.Errorf("unrecognised db dialect for dsn %q", dsn)
	}
}

func openSQLite(dsn string) (*Store, error) {
	// sqlite:// → file path (strip scheme); plain path → use as-is.
	path := dsn
	path = strings.TrimPrefix(path, "sqlite://")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("mkdir db parent: %w", err)
	}

	connDSN := path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	conn, err := sql.Open("sqlite", connDSN)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	conn.SetMaxOpenConns(1) // SQLite serializes writes anyway; keep it simple.
	conn.SetConnMaxLifetime(0)

	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}

	if err := applyMigrations(conn, DialectSQLite); err != nil {
		return nil, err
	}

	return &Store{DB: conn, path: path, Dialect: DialectSQLite}, nil
}

// openPostgres dials a managed Postgres instance via the pgx stdlib
// driver, applies the postgres-flavoured migrations, and returns a
// Store ready for query.
//
// The migration set lives in migrations/postgres/ and is the
// per-statement Postgres equivalent of the SQLite set. Schema-history
// version tracking in schema_migrations works identically — both
// backends read the same migration numbers.
func openPostgres(dsn string) (*Store, error) {
	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	// Generous pool — Postgres handles many writers concurrently, unlike
	// SQLite. Operators tune via DSN params (`pool_max_conns=...`); we
	// just set safe defaults.
	conn.SetMaxOpenConns(20)
	conn.SetMaxIdleConns(4)
	conn.SetConnMaxLifetime(30 * time.Minute)

	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	if err := applyMigrations(conn, DialectPostgres); err != nil {
		return nil, fmt.Errorf("apply postgres migrations: %w", err)
	}
	return &Store{DB: conn, path: dsn, Dialect: DialectPostgres}, nil
}

// Path returns the DSN the Store was opened at. The name is historical —
// "path" only made sense for the SQLite-only era.
func (s *Store) Path() string { return s.path }

// applyMigrations runs each numbered migration once, tracking applied
// versions in `schema_migrations`. Idempotent across restarts: skipping an
// already-applied migration is what makes ALTER TABLE / DROP TABLE-style
// migrations safe to ship.
//
// Bootstrap for pre-tracking-era databases: if `schema_migrations` is
// brand new but the `meta` table exists (created by migration 001), we
// probe for each migration's signature object and mark it applied if its
// effect is already present. That way upgrading a CP from before this
// commit doesn't re-run schema-altering migrations against tables that
// already have the new shape.
func applyMigrations(db *sql.DB, dialect Dialect) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	var applied int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		return err
	}
	// Bootstrap probes only run against SQLite — they reference
	// PRAGMA table_info / sqlite_master which Postgres doesn't have.
	// Postgres deployments are new-fleet by definition (no legacy
	// pre-tracking-era DB on the wire) so bootstrap is a no-op there.
	if applied == 0 && dialect == DialectSQLite && tableExists(db, "meta") {
		bootstrap := []struct {
			version int
			probe   func() bool
		}{
			{1,  func() bool { return tableExists(db, "meta") }},
			{2,  func() bool { return tableExists(db, "agents") }},
			{3,  func() bool { return tableExists(db, "findings") }},
			{4,  func() bool { return tableExists(db, "nodes") }},
			{5,  func() bool { return tableExists(db, "audit_log") }},
			{6,  func() bool { return tableExists(db, "known_hosts") }},
			{7,  func() bool { return tableExists(db, "daemon_binaries") }},
			{8,  func() bool { return tableExists(db, "notification_channels") }},
			{9,  func() bool { return tableExists(db, "api_tokens") }},
			{10, func() bool { return tableExists(db, "runs") }},
			{11, func() bool { return columnExists(db, "findings", "category") }},
			{12, func() bool { return agentsHasCompositePK(db) }},
			{13, func() bool { return columnExists(db, "findings", "status") }},
			{14, func() bool { return columnExists(db, "nodes", "daemon_hostname") }},
			{15, func() bool { return columnExists(db, "findings", "operator_severity") && tableExists(db, "finding_severity_rules") }},
			{16, func() bool { return columnExists(db, "runs", "finding_id") }},
			{17, func() bool { return columnExists(db, "nodes", "kernel_release") }},
			{18, func() bool { return columnExists(db, "agents", "current_definition_hash") }},
		}
		for _, m := range bootstrap {
			if !m.probe() {
				continue
			}
			if _, err := db.Exec(`INSERT OR IGNORE INTO schema_migrations (version) VALUES (?)`, m.version); err != nil {
				return fmt.Errorf("bootstrap migration tracking: %w", err)
			}
		}
	}

	checkSQL, insertSQL := dialectSQL(dialect)
	for i, stmt := range migrationsForDialect(dialect) {
		version := i + 1
		var n int64
		if err := db.QueryRow(checkSQL, version).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("apply migration %03d: %w", version, err)
		}
		if _, err := db.Exec(insertSQL, version); err != nil {
			return fmt.Errorf("record migration %03d: %w", version, err)
		}
	}
	return nil
}

// dialectSQL returns the SELECT-version-exists and INSERT-version SQL
// strings that the migration runner needs, with placeholders rewritten
// for the target dialect ($1 for postgres, ? for sqlite).
func dialectSQL(d Dialect) (check, insert string) {
	if d == DialectPostgres {
		return `SELECT COUNT(*) FROM schema_migrations WHERE version = $1`,
			`INSERT INTO schema_migrations (version) VALUES ($1) ON CONFLICT DO NOTHING`
	}
	return `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`,
		`INSERT INTO schema_migrations (version) VALUES (?)`
}

func tableExists(db *sql.DB, name string) bool {
	var n int64
	_ = db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name = ?`, name,
	).Scan(&n)
	return n > 0
}

func columnExists(db *sql.DB, table, col string) bool {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return false
		}
		if name == col {
			return true
		}
	}
	return false
}

// agentsHasCompositePK detects whether migration 012 has been applied —
// the rebuilt table makes both `name` and `host` part of the primary key.
func agentsHasCompositePK(db *sql.DB) bool {
	rows, err := db.Query(`PRAGMA table_info(agents)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	pkCount := 0
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return false
		}
		if pk > 0 {
			pkCount++
		}
	}
	return pkCount >= 2
}

// MetaGet reads a value from the meta table. Returns "" if absent.
func (s *Store) MetaGet(key string) (string, error) {
	var v string
	err := s.QueryRow("SELECT value FROM meta WHERE key = ?", key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// MetaSet writes a key/value pair, replacing any prior value for the key.
func (s *Store) MetaSet(key, value string) error {
	_, err := s.Exec(`
		INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	return err
}

// User is the persisted user row.
type User struct {
	ID           int64
	Email        string
	PasswordHash sql.NullString
	Role         string
	CreatedAt    time.Time
}

// UserByEmail returns the user with the given email, or sql.ErrNoRows.
func (s *Store) UserByEmail(email string) (*User, error) {
	u := &User{}
	err := s.QueryRow(
		`SELECT id, email, password_hash, role, created_at FROM users WHERE email = ?`,
		email,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// UserByID returns the user with the given id.
func (s *Store) UserByID(id int64) (*User, error) {
	u := &User{}
	err := s.QueryRow(
		`SELECT id, email, password_hash, role, created_at FROM users WHERE id = ?`,
		id,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// CreateUser inserts a new user and returns its id.
func (s *Store) CreateUser(email, passwordHash, role string) (int64, error) {
	res, err := s.Exec(
		`INSERT INTO users (email, password_hash, role) VALUES (?, ?, ?)`,
		email, passwordHash, role,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpsertSSOUser creates a user with the given email if absent, or updates
// the user's role to match the IDP. password_hash stays NULL — SSO users
// cannot log in with a password. Returns the (possibly updated) user.
func (s *Store) UpsertSSOUser(email, role string) (*User, error) {
	_, err := s.Exec(`
		INSERT INTO users (email, password_hash, role)
		VALUES (?, NULL, ?)
		ON CONFLICT(email) DO UPDATE SET role = excluded.role
	`, email, role)
	if err != nil {
		return nil, err
	}
	return s.UserByEmail(email)
}

// ListUsers returns every user, ordered by created_at descending.
func (s *Store) ListUsers() ([]*User, error) {
	rows, err := s.Query(`
		SELECT id, email, password_hash, role, created_at
		FROM users ORDER BY created_at DESC, id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u := &User{}
		if err := rows.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UpdateUserRole sets the role on a user.
func (s *Store) UpdateUserRole(id int64, role string) error {
	_, err := s.Exec(`UPDATE users SET role = ? WHERE id = ?`, role, id)
	return err
}

// UpdateUserPassword sets a new bcrypt hash on a user.
func (s *Store) UpdateUserPassword(id int64, passwordHash string) error {
	_, err := s.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, id)
	return err
}

// DeleteUser removes a user. Cascades to sessions via FK; audit rows have
// actor_id ON DELETE SET NULL, so history is preserved.
func (s *Store) DeleteUser(id int64) error {
	_, err := s.Exec(`DELETE FROM users WHERE id = ?`, id)
	return err
}

// CountUsersByRole returns the number of users with the given role. Used
// to prevent removing the last admin.
func (s *Store) CountUsersByRole(role string) (int, error) {
	var n int
	err := s.QueryRow(`SELECT COUNT(*) FROM users WHERE role = ?`, role).Scan(&n)
	return n, err
}

// ── Sessions ─────────────────────────────────────────────────────────────────

// SessionInfo is a session row enriched with relative metadata for the UI.
type SessionInfo struct {
	ID        string
	ExpiresAt time.Time
	CreatedAt time.Time
}

// ListUserSessions returns every active (non-expired) session for a user,
// most recent first.
func (s *Store) ListUserSessions(userID int64) ([]*SessionInfo, error) {
	rows, err := s.Query(`
		SELECT id, expires_at, created_at FROM sessions
		WHERE user_id = ? AND expires_at > CURRENT_TIMESTAMP
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*SessionInfo
	for rows.Next() {
		si := &SessionInfo{}
		if err := rows.Scan(&si.ID, &si.ExpiresAt, &si.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, si)
	}
	return out, rows.Err()
}

// DeleteUserSessionsExcept revokes every session for a user except the one
// passed in (typically the current session). Used for "sign out everywhere
// else".
func (s *Store) DeleteUserSessionsExcept(userID int64, keepID string) error {
	_, err := s.Exec(`DELETE FROM sessions WHERE user_id = ? AND id != ?`, userID, keepID)
	return err
}

// CreateSession inserts a session row.
func (s *Store) CreateSession(id string, userID int64, expiresAt time.Time) error {
	_, err := s.Exec(
		`INSERT INTO sessions (id, user_id, expires_at) VALUES (?, ?, ?)`,
		id, userID, expiresAt,
	)
	return err
}

// SessionUser returns the user associated with a non-expired session, or
// sql.ErrNoRows if the session is missing or expired.
func (s *Store) SessionUser(sessionID string) (*User, error) {
	u := &User{}
	err := s.QueryRow(`
		SELECT u.id, u.email, u.password_hash, u.role, u.created_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.id = ? AND s.expires_at > CURRENT_TIMESTAMP
	`, sessionID).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// DeleteSession removes a session row.
func (s *Store) DeleteSession(id string) error {
	_, err := s.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// PruneSessions deletes expired sessions. Call periodically.
func (s *Store) PruneSessions() error {
	_, err := s.Exec(`DELETE FROM sessions WHERE expires_at <= CURRENT_TIMESTAMP`)
	return err
}

// Event is the persisted event row.
type Event struct {
	ID         int64
	Ts         int64
	Type       string
	Agent      sql.NullString
	Host       sql.NullString
	Severity   sql.NullString
	Title      sql.NullString
	RawJSON    string
	ReceivedAt time.Time
}

// InsertEvent stores an event and returns its id.
func (s *Store) InsertEvent(e *Event) (int64, error) {
	res, err := s.Exec(`
		INSERT INTO events (ts, type, agent, host, severity, title, raw_json)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, e.Ts, e.Type, e.Agent, e.Host, e.Severity, e.Title, e.RawJSON)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// RecentEvents returns up to limit events ordered by timestamp descending.
// When beforeTs > 0, only events strictly older than that timestamp are
// returned — used as a cursor for the UI's infinite-scroll pagination.
// (Cursor by ts is more robust than offset against the SSE stream
// prepending new events at the top during pagination.)
func (s *Store) RecentEvents(limit int, beforeTs int64) ([]*Event, error) {
	return s.RecentEventsFiltered("", "", limit, beforeTs)
}

// RecentEventsFiltered is RecentEvents with agent/host equality
// constraints pushed into the SQL WHERE so a daimon-detail query
// doesn't scan the whole fleet's recent events to find the few
// rows belonging to one daimon. Empty filters fall through to the
// unfiltered path.
//
// Limit is capped at 5000 here (vs 1000 for unfiltered Recent)
// because the filtered query can return up to that many rows for
// one busy daimon's history without wasting bandwidth on rows
// nobody asked for.
func (s *Store) RecentEventsFiltered(agent, host string, limit int, beforeTs int64) ([]*Event, error) {
	if limit <= 0 {
		limit = 100
	}
	if agent == "" && host == "" {
		if limit > 1000 {
			limit = 1000
		}
	} else if limit > 5000 {
		limit = 5000
	}
	q := `SELECT id, ts, type, agent, host, severity, title, raw_json, received_at
		  FROM events`
	var conds []string
	var args []any
	if agent != "" {
		conds = append(conds, "agent = ?")
		args = append(args, agent)
	}
	if host != "" {
		conds = append(conds, "host = ?")
		args = append(args, host)
	}
	if beforeTs > 0 {
		conds = append(conds, "ts < ?")
		args = append(args, beforeTs)
	}
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += ` ORDER BY ts DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Event
	for rows.Next() {
		e := &Event{}
		if err := rows.Scan(
			&e.ID, &e.Ts, &e.Type, &e.Agent, &e.Host,
			&e.Severity, &e.Title, &e.RawJSON, &e.ReceivedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
