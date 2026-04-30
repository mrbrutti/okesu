// agent_lessons KV — per-agent persistent notes that the daemon
// prepends to its system prompt on the next tick.
//
// The KV is bounded: each insert prunes older rows beyond
// MaxAgentLessonsPerAgent so the table can't grow unbounded under a
// chatty agent. Daemons typically read the top N (10) on tick prep.
//
// See migration 035 for the schema; design rationale lives in
// docs/superpowers/specs/2026-04-29-threatcaddy-borrows-phasing-design.md
// (Phase 22.2 §"Agent reflection / lessons KV").

package db

import (
	"fmt"
	"unicode/utf8"
)

// AgentLesson is a single per-agent lesson record. Returned by
// ListAgentLessons; daemons concatenate the top-N for prompt prep.
type AgentLesson struct {
	ID                  int64
	AgentName           string
	Text                string
	OrchestrationRunID  int64
	OrchestrationStepID string
}

// MaxAgentLessonChars is the per-lesson cap. Longer text is truncated
// rather than rejected so an over-eager agent doesn't drop its lesson
// entirely.
const MaxAgentLessonChars = 200

// MaxAgentLessonsPerAgent is the per-agent retention cap. Insert path
// keeps oldest-evicted; daemons typically read the top 10.
const MaxAgentLessonsPerAgent = 10

// RecordAgentLesson appends a lesson and prunes older entries beyond
// the per-agent cap.
//
// Insert + prune are two separate Exec calls (not transactional).
// If the prune fails after a successful insert, the table can briefly
// hold cap+1 rows; the next successful RecordAgentLesson reconciles.
// Daemon reads use LIMIT 10 regardless, so the cap+1 state is invisible
// to the consumer.
func (s *Store) RecordAgentLesson(agent, text string, runID int64, stepID string) error {
	if agent == "" {
		return fmt.Errorf("RecordAgentLesson: agent is required")
	}
	if text == "" {
		return fmt.Errorf("RecordAgentLesson: text is required")
	}
	text = truncateUTF8(text, MaxAgentLessonChars)
	if _, err := s.Exec(`
		INSERT INTO agent_lessons (agent_name, lesson_text, orchestration_run_id, orchestration_step_id)
		VALUES (?, ?, ?, ?)`,
		agent, text, nullableInt64(runID), nullable(stepID)); err != nil {
		return err
	}
	return s.PruneAgentLessons(agent, MaxAgentLessonsPerAgent)
}

// truncateUTF8 returns s clipped to at most maxBytes bytes without
// splitting a multi-byte rune. We start at the byte cap and walk
// backward until the prefix is valid UTF-8 — at most 3 bytes of
// scan-back since UTF-8 runes are at most 4 bytes. Plain `s[:maxBytes]`
// would persist invalid bytes that downstream model APIs reject; the
// daemon prepends these into a system prompt verbatim.
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	clipped := s[:maxBytes]
	for !utf8.ValidString(clipped) && len(clipped) > 0 {
		clipped = clipped[:len(clipped)-1]
	}
	return clipped
}

// ListAgentLessons returns the agent's most recent lessons, newest first.
// ORDER BY (created_at DESC, id DESC) so ties on tied timestamps —
// possible on sqlite's second-resolution timestamps — resolve by
// monotonic id rather than relying on insertion order luck.
func (s *Store) ListAgentLessons(agent string, limit int) ([]AgentLesson, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	rows, err := s.Query(`
		SELECT id, agent_name, lesson_text,
		       COALESCE(orchestration_run_id, 0), COALESCE(orchestration_step_id, '')
		FROM agent_lessons
		WHERE agent_name = ?
		ORDER BY created_at DESC, id DESC
		LIMIT ?`, agent, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentLesson
	for rows.Next() {
		var l AgentLesson
		if err := rows.Scan(&l.ID, &l.AgentName, &l.Text, &l.OrchestrationRunID, &l.OrchestrationStepID); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// PruneAgentLessons deletes all but the newest `keep` lessons for the
// given agent. Same ordering as ListAgentLessons so the keep set is
// consistent.
func (s *Store) PruneAgentLessons(agent string, keep int) error {
	if keep <= 0 {
		keep = MaxAgentLessonsPerAgent
	}
	_, err := s.Exec(`
		DELETE FROM agent_lessons
		WHERE agent_name = ?
		  AND id NOT IN (
		    SELECT id FROM agent_lessons
		    WHERE agent_name = ?
		    ORDER BY created_at DESC, id DESC
		    LIMIT ?
		  )`, agent, agent, keep)
	return err
}
