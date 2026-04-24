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
	// LastTickAt is the UTC time the most recent tick started.
	LastTickAt time.Time `json:"last_tick_at,omitempty"`
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

// RecordTick updates TickCount and LastTickAt and saves state.
func (s *DaemonState) RecordTick(stateDir, name string) {
	s.mu.Lock()
	s.TickCount++
	s.LastTickAt = time.Now().UTC()
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
