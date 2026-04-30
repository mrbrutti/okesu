package agent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DaemonState is the on-disk state persisted between daemon restarts.
// It is written atomically to stateDir/<name>/state.json.
type DaemonState struct {
	mu sync.Mutex `json:"-"`

	// Dedup cache: maps content-hash → expiry timestamp (Unix ms).
	Dedup map[string]int64 `json:"dedup,omitempty"`
	// TickCount is the total number of ticks executed across all restarts.
	TickCount int64 `json:"tick_count"`
	// ErrorCount is the total number of ticks that ended with an error.
	ErrorCount int64 `json:"error_count"`
	// LastTickAt is the UTC time the most recent tick started.
	LastTickAt time.Time `json:"last_tick_at,omitempty"`
	// CurrentInterval is the last computed adaptive-schedule interval.
	// Persisted so backoff state survives daemon restarts; restored on
	// LoadState. Zero means "use IntervalMin" (fresh start).
	CurrentInterval time.Duration `json:"current_interval,omitempty"`
}

// LoadState reads state from stateDir/<name>/state.json.
// Returns a fresh empty state if the file is absent or unparseable.
func LoadState(stateDir, name string) *DaemonState {
	s := &DaemonState{Dedup: make(map[string]int64)}
	if stateDir == "" || name == "" {
		return s
	}
	data, err := os.ReadFile(stateFilePath(stateDir, name))
	if err != nil {
		return s
	}
	if err := json.Unmarshal(data, s); err != nil {
		return &DaemonState{Dedup: make(map[string]int64)}
	}
	if s.Dedup == nil {
		s.Dedup = make(map[string]int64)
	}
	return s
}

// SaveState writes state atomically to stateDir/<name>/state.json.
func SaveState(stateDir, name string, s *DaemonState) error {
	if stateDir == "" || name == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Join(stateDir, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("state mkdir: %w", err)
	}

	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("state marshal: %w", err)
	}

	tmp := stateFilePath(stateDir, name) + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return fmt.Errorf("state write: %w", err)
	}
	if err := os.Rename(tmp, stateFilePath(stateDir, name)); err != nil {
		return fmt.Errorf("state rename: %w", err)
	}
	return nil
}

// IsDuplicate reports whether text was seen within the dedup TTL window.
func (s *DaemonState) IsDuplicate(text string, ttl time.Duration) bool {
	if ttl <= 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	h := contentHash(text)
	exp, ok := s.Dedup[h]
	if !ok {
		return false
	}
	return time.Now().UnixMilli() < exp
}

// AddToDedup records text in the dedup cache with an expiry of now+ttl.
func (s *DaemonState) AddToDedup(text string, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	h := contentHash(text)
	s.Dedup[h] = time.Now().Add(ttl).UnixMilli()
}

// PruneDedup removes expired entries from the dedup cache.
// Call once per tick to keep the cache from growing unbounded.
func (s *DaemonState) PruneDedup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UnixMilli()
	for h, exp := range s.Dedup {
		if now >= exp {
			delete(s.Dedup, h)
		}
	}
}

// RecordTick updates TickCount, ErrorCount, and LastTickAt and saves state.
// hadError should be true when the tick's agentic loop returned an error.
func (s *DaemonState) RecordTick(stateDir, name string, hadError bool) {
	s.mu.Lock()
	s.TickCount++
	if hadError {
		s.ErrorCount++
	}
	s.LastTickAt = time.Now().UTC()
	s.mu.Unlock()
	_ = SaveState(stateDir, name, s)
}

// SetCurrentInterval records the adaptive scheduler's current interval
// and persists it so backoff state survives a daemon restart. Called by
// the daemon loop after each tick once the scheduler has been updated.
//
// Skip-if-unchanged: a daimon at the ceiling sees the same interval
// every tick, and there's no need to fsync state on every one. We only
// write when the value actually moves (during the backoff ramp-up,
// or on reset to min after a finding/error).
func (s *DaemonState) SetCurrentInterval(stateDir, name string, d time.Duration) {
	s.mu.Lock()
	if s.CurrentInterval == d {
		s.mu.Unlock()
		return
	}
	s.CurrentInterval = d
	s.mu.Unlock()
	_ = SaveState(stateDir, name, s)
}

func stateFilePath(stateDir, name string) string {
	return filepath.Join(stateDir, name, "state.json")
}

func contentHash(text string) string {
	h := sha256.Sum256([]byte(text))
	return fmt.Sprintf("%x", h[:8]) // 64-bit prefix is enough for dedup
}
