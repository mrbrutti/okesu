// Event sink — daimon-emitted Events are batched and uploaded as
// NDJSON objects under cp/<cp-id>/nodes/<node-id>/events/. Replaces
// the HTTPS webhook for nodes whose transport is set to "s3".
//
// Findings (a subtype of Event in the existing schema) get their own
// findings/ prefix so the CP scanner can ingest them with different
// retention than general events.

package s3transport

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/section9labs/okesu/agent"
)

// EventSinkConfig parameters the batched uploader.
type EventSinkConfig struct {
	Client   *Client
	CPID     string
	NodeID   int64

	FlushInterval time.Duration
	FlushMax      int
}

// EventSink batches incoming agent.Events and flushes them as one
// NDJSON object per flush. Implements the same Send shape the
// existing WebhookSink does, so the daemon can swap one for the
// other based on transport config.
type EventSink struct {
	cfg EventSinkConfig

	mu  sync.Mutex
	buf []agent.Event
	t   *time.Timer
	ctx context.Context
}

// NewEventSink constructs the sink. ctx scopes the flush goroutine.
func NewEventSink(ctx context.Context, cfg EventSinkConfig) *EventSink {
	if cfg.FlushInterval == 0 {
		cfg.FlushInterval = 15 * time.Second
	}
	if cfg.FlushMax == 0 {
		cfg.FlushMax = 50
	}
	s := &EventSink{cfg: cfg, ctx: ctx}
	s.t = time.AfterFunc(cfg.FlushInterval, s.tick)
	return s
}

// Send queues one event. Triggers an immediate flush if the buffer
// is full.
func (s *EventSink) Send(e agent.Event) error {
	s.mu.Lock()
	s.buf = append(s.buf, e)
	full := len(s.buf) >= s.cfg.FlushMax
	s.mu.Unlock()
	if full {
		s.flushNow()
	}
	return nil
}

// Flush forces an immediate write. Used at daemon shutdown.
func (s *EventSink) Flush() error {
	s.flushNow()
	return nil
}

func (s *EventSink) tick() {
	s.flushNow()
	s.t.Reset(s.cfg.FlushInterval)
}

func (s *EventSink) flushNow() {
	s.mu.Lock()
	if len(s.buf) == 0 {
		s.mu.Unlock()
		return
	}
	batch := s.buf
	s.buf = nil
	s.mu.Unlock()

	// One NDJSON object per flush. Findings are written separately
	// (one object each) so the scanner can fan them into the
	// findings table without parsing every event.
	var ndjson []byte
	var findings []agent.Event
	for _, e := range batch {
		if e.Type == "finding" {
			findings = append(findings, e)
			continue
		}
		b, err := json.Marshal(e)
		if err != nil {
			continue
		}
		ndjson = append(ndjson, b...)
		ndjson = append(ndjson, '\n')
	}

	now := time.Now().UTC()
	dayPrefix := fmt.Sprintf("%04d-%02d-%02d/", now.Year(), now.Month(), now.Day())

	if len(ndjson) > 0 {
		key := NodePrefix(s.cfg.CPID, s.cfg.NodeID) + DirEvents + dayPrefix + genObjectName() + ".ndjson"
		_ = s.cfg.Client.Put(s.ctx, key, ndjson, "application/x-ndjson")
	}

	for _, f := range findings {
		body, err := json.Marshal(f)
		if err != nil {
			continue
		}
		key := NodePrefix(s.cfg.CPID, s.cfg.NodeID) + DirFindings + genObjectName() + ".json"
		_ = s.cfg.Client.Put(s.ctx, key, body, "application/json")
	}
}

// Close stops the timer and force-flushes. Safe to call multiple times.
func (s *EventSink) Close() {
	if s.t != nil {
		s.t.Stop()
	}
	s.flushNow()
}
