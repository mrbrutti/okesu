package db

import (
	"database/sql"
	"strings"
	"time"
)

// ── Channels ────────────────────────────────────────────────────────────────

const (
	ChannelTypeSlack   = "slack"
	ChannelTypeEmail   = "email"
	ChannelTypeWebhook = "webhook"
)

// NotificationChannel is a typed delivery destination.
type NotificationChannel struct {
	ID              int64
	Name            string
	Type            string
	Config          string // JSON
	Enabled         bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
	CreatedByEmail  sql.NullString
}

// CreateChannel inserts a channel. configJSON should be the JSON-encoded
// type-specific settings (see migration comments for shapes per type).
func (s *Store) CreateChannel(name, chanType, configJSON, createdBy string) (int64, error) {
	res, err := s.Exec(`
		INSERT INTO notification_channels (name, type, config, created_by_email)
		VALUES (?, ?, ?, ?)
	`, name, chanType, configJSON, nullable(createdBy))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateChannel(id int64, name, configJSON string, enabled bool) error {
	_, err := s.Exec(`
		UPDATE notification_channels
		SET name = ?, config = ?, enabled = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, name, configJSON, boolToInt(enabled), id)
	return err
}

func (s *Store) DeleteChannel(id int64) error {
	_, err := s.Exec(`DELETE FROM notification_channels WHERE id = ?`, id)
	return err
}

func (s *Store) ChannelByID(id int64) (*NotificationChannel, error) {
	c := &NotificationChannel{}
	var enabled int
	err := s.QueryRow(`
		SELECT id, name, type, config, enabled, created_at, updated_at, created_by_email
		FROM notification_channels WHERE id = ?
	`, id).Scan(
		&c.ID, &c.Name, &c.Type, &c.Config, &enabled,
		&c.CreatedAt, &c.UpdatedAt, &c.CreatedByEmail,
	)
	if err != nil {
		return nil, err
	}
	c.Enabled = enabled != 0
	return c, nil
}

func (s *Store) ListChannels() ([]*NotificationChannel, error) {
	rows, err := s.Query(`
		SELECT id, name, type, config, enabled, created_at, updated_at, created_by_email
		FROM notification_channels ORDER BY id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*NotificationChannel
	for rows.Next() {
		c := &NotificationChannel{}
		var enabled int
		if err := rows.Scan(
			&c.ID, &c.Name, &c.Type, &c.Config, &enabled,
			&c.CreatedAt, &c.UpdatedAt, &c.CreatedByEmail,
		); err != nil {
			return nil, err
		}
		c.Enabled = enabled != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// ── Rules ──────────────────────────────────────────────────────────────────

// NotificationRule routes findings to a channel.
type NotificationRule struct {
	ID             int64
	Name           string
	ChannelID      int64
	MinSeverity    string
	AgentSubstring sql.NullString
	HostSubstring  sql.NullString
	Enabled        bool
	CreatedAt      time.Time
}

func (s *Store) CreateRule(r *NotificationRule) (int64, error) {
	res, err := s.Exec(`
		INSERT INTO notification_rules (name, channel_id, min_severity, agent_substring, host_substring, enabled)
		VALUES (?, ?, ?, ?, ?, ?)
	`, r.Name, r.ChannelID, strings.ToUpper(r.MinSeverity),
		nullable(r.AgentSubstring.String), nullable(r.HostSubstring.String),
		boolToInt(r.Enabled))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateRule(r *NotificationRule) error {
	_, err := s.Exec(`
		UPDATE notification_rules
		SET name = ?, channel_id = ?, min_severity = ?,
		    agent_substring = ?, host_substring = ?, enabled = ?
		WHERE id = ?
	`, r.Name, r.ChannelID, strings.ToUpper(r.MinSeverity),
		nullable(r.AgentSubstring.String), nullable(r.HostSubstring.String),
		boolToInt(r.Enabled), r.ID)
	return err
}

func (s *Store) DeleteRule(id int64) error {
	_, err := s.Exec(`DELETE FROM notification_rules WHERE id = ?`, id)
	return err
}

func (s *Store) ListRules() ([]*NotificationRule, error) {
	rows, err := s.Query(`
		SELECT id, name, channel_id, min_severity, agent_substring, host_substring, enabled, created_at
		FROM notification_rules ORDER BY id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*NotificationRule
	for rows.Next() {
		r := &NotificationRule{}
		var enabled int
		if err := rows.Scan(
			&r.ID, &r.Name, &r.ChannelID, &r.MinSeverity,
			&r.AgentSubstring, &r.HostSubstring, &enabled, &r.CreatedAt,
		); err != nil {
			return nil, err
		}
		r.Enabled = enabled != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// EnabledRulesWithChannels returns every enabled rule joined with its
// channel. Used by the worker to decide where a finding should go.
type EnabledRule struct {
	Rule    NotificationRule
	Channel NotificationChannel
}

func (s *Store) EnabledRulesWithChannels() ([]*EnabledRule, error) {
	rows, err := s.Query(`
		SELECT r.id, r.name, r.channel_id, r.min_severity, r.agent_substring, r.host_substring, r.enabled, r.created_at,
		       c.id, c.name, c.type, c.config, c.enabled, c.created_at, c.updated_at, c.created_by_email
		FROM notification_rules r
		JOIN notification_channels c ON c.id = r.channel_id
		WHERE r.enabled = 1 AND c.enabled = 1
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*EnabledRule
	for rows.Next() {
		er := &EnabledRule{}
		var rEnabled, cEnabled int
		if err := rows.Scan(
			&er.Rule.ID, &er.Rule.Name, &er.Rule.ChannelID, &er.Rule.MinSeverity,
			&er.Rule.AgentSubstring, &er.Rule.HostSubstring, &rEnabled, &er.Rule.CreatedAt,
			&er.Channel.ID, &er.Channel.Name, &er.Channel.Type, &er.Channel.Config,
			&cEnabled, &er.Channel.CreatedAt, &er.Channel.UpdatedAt, &er.Channel.CreatedByEmail,
		); err != nil {
			return nil, err
		}
		er.Rule.Enabled = rEnabled != 0
		er.Channel.Enabled = cEnabled != 0
		out = append(out, er)
	}
	return out, rows.Err()
}

// ── Deliveries ─────────────────────────────────────────────────────────────

// NotificationDelivery is one send attempt.
type NotificationDelivery struct {
	ID         int64
	RuleID     sql.NullInt64
	ChannelID  sql.NullInt64
	FindingID  sql.NullInt64
	Severity   sql.NullString
	Title      sql.NullString
	Status     string
	Attempt    int
	Error      sql.NullString
	CreatedAt  time.Time
	FinishedAt sql.NullTime
}

func (s *Store) CreateDelivery(ruleID, channelID, findingID int64, severity, title string) (int64, error) {
	var fid sql.NullInt64
	if findingID > 0 {
		fid = sql.NullInt64{Int64: findingID, Valid: true}
	}
	res, err := s.Exec(`
		INSERT INTO notification_deliveries (rule_id, channel_id, finding_id, severity, title, status)
		VALUES (?, ?, ?, ?, ?, 'pending')
	`, ruleID, channelID, fid, nullable(severity), nullable(title))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateDeliveryAttempt(id int64, attempt int) error {
	_, err := s.Exec(`UPDATE notification_deliveries SET attempt = ? WHERE id = ?`, attempt, id)
	return err
}

func (s *Store) FinishDelivery(id int64, status, errMsg string) error {
	_, err := s.Exec(`
		UPDATE notification_deliveries
		SET status = ?, error = ?, finished_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, status, nullable(errMsg), id)
	return err
}

// ListDeliveries returns recent deliveries, newest first.
func (s *Store) ListDeliveries(limit int) ([]*NotificationDelivery, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.Query(`
		SELECT id, rule_id, channel_id, finding_id, severity, title,
		       status, attempt, error, created_at, finished_at
		FROM notification_deliveries
		ORDER BY id DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*NotificationDelivery
	for rows.Next() {
		d := &NotificationDelivery{}
		if err := rows.Scan(
			&d.ID, &d.RuleID, &d.ChannelID, &d.FindingID,
			&d.Severity, &d.Title, &d.Status, &d.Attempt, &d.Error,
			&d.CreatedAt, &d.FinishedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// boolToInt is a helper for SQLite which uses 0/1 for booleans.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
