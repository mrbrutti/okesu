// Child-side write-pipe server. Polls bucket prefixes for incoming
// directives, dispatches to per-kind handlers, writes responses.
//
// Bucket layout the Server consumes:
//
//   cp/<parent>/outbound/<self>/req/<id>.json   — read on tick
//   cp/<self>/outbound/<parent>/resp/<id>.json  — written when done
//
// Multi-parent: self can have N parents. The Server lists every
// `cp/*/outbound/<self>/req/` prefix on each tick, picks up new
// requests, and writes responses back to the corresponding
// `cp/<self>/outbound/<that-parent>/resp/`. Operators don't need
// to enumerate parent IDs anywhere — the bucket layout is the
// discovery mechanism.
//
// Idempotency: Server keeps an in-memory map of executed[request_id]
// → cached response. A re-issued directive (parent's first response
// got dropped, it retries) returns the cached response without
// re-running the handler. Map entries TTL out at 1h to bound memory.

package s3rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/section9labs/okesu/agent/s3transport"
)

// Handler executes a single kind of directive. Returns the body
// the parent will see + an HTTP-style status code (200 / 400 / etc).
// Handlers should NOT panic on bad input; return a 400 with a
// descriptive Error in the Response instead.
type Handler func(ctx context.Context, req Request) Response

// Server is the child-side dispatcher. New + Register + Run.
type Server struct {
	cli       *s3transport.Client
	selfID    string
	handlers  map[string]Handler
	interval  time.Duration

	mu        sync.Mutex
	executed  map[string]executedEntry
}

type executedEntry struct {
	resp Response
	at   time.Time
}

// New constructs a Server. Run starts the polling loop; Register
// adds handlers; both must complete before Run.
func New(cli *s3transport.Client, selfID string) (*Server, error) {
	if cli == nil {
		return nil, errors.New("s3rpc: server requires non-nil s3transport.Client")
	}
	selfID = strings.Trim(selfID, "/")
	if selfID == "" {
		return nil, errors.New("s3rpc: selfID required")
	}
	return &Server{
		cli:      cli,
		selfID:   selfID,
		handlers: map[string]Handler{},
		interval: DefaultServerPollInterval,
		executed: map[string]executedEntry{},
	}, nil
}

// Register installs a Handler under a kind. Panics on duplicate
// kind — that's a programming error, not a runtime condition.
func (s *Server) Register(kind string, h Handler) {
	if _, dup := s.handlers[kind]; dup {
		panic("s3rpc: duplicate handler for kind " + kind)
	}
	s.handlers[kind] = h
}

// Run blocks polling until ctx is cancelled.
func (s *Server) Run(ctx context.Context) {
	t := time.NewTicker(s.interval)
	defer t.Stop()
	s.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}

// tick lists every cp/*/outbound/<self>/req/ prefix and processes
// any unseen request objects.
func (s *Server) tick(ctx context.Context) {
	// We list under the umbrella "cp/" because the bucket can host
	// many CPs. The minio client's List walks all keys under that
	// prefix; we filter to the ones matching our self id +
	// /outbound/.../req/ shape. Limit is generous; a busy parent
	// might queue dozens of directives between ticks.
	suffix := fmt.Sprintf("/outbound/%s/req/", s.selfID)
	objs, err := s.cli.List(ctx, "cp/", 5000)
	if err != nil {
		log.Printf("s3rpc server: list: %v", err)
		return
	}
	for _, o := range objs {
		key := o.Key
		if !strings.Contains(key, suffix) || !strings.HasSuffix(key, ".json") {
			continue
		}
		// Extract parent id from the key: cp/<parent>/outbound/<self>/req/<id>.json
		parentID := parentIDFromReqKey(key, s.selfID)
		if parentID == "" {
			log.Printf("s3rpc server: malformed key %q (skipping)", key)
			continue
		}
		s.processOne(ctx, parentID, key)
	}
	s.gcExecuted()
}

// processOne reads one req object, dispatches, writes the response.
// Idempotent: a request_id we've already executed returns the
// cached response without re-running.
func (s *Server) processOne(ctx context.Context, parentID, reqKey string) {
	body, err := s.cli.GetBytes(ctx, reqKey)
	if err != nil {
		// Could be a 404 if a sibling tick already consumed +
		// deleted it. Not actionable; skip.
		return
	}
	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		log.Printf("s3rpc server: parse %s: %v", reqKey, err)
		return
	}
	if req.RequestID == "" || req.Kind == "" {
		log.Printf("s3rpc server: %s missing request_id/kind", reqKey)
		return
	}

	// Idempotency check.
	s.mu.Lock()
	prev, seen := s.executed[req.RequestID]
	s.mu.Unlock()
	var resp Response
	if seen {
		resp = prev.resp
	} else {
		h, ok := s.handlers[req.Kind]
		if !ok {
			resp = Response{
				RequestID:   req.RequestID,
				Kind:        req.Kind,
				CompletedAt: time.Now().UTC(),
				Status:      "error",
				HTTPStatus:  501,
				Error:       fmt.Sprintf("no handler registered for kind %q", req.Kind),
			}
		} else {
			resp = h(ctx, req)
			// Defensive — handlers might forget to set these.
			resp.RequestID = req.RequestID
			resp.Kind = req.Kind
			if resp.CompletedAt.IsZero() {
				resp.CompletedAt = time.Now().UTC()
			}
			if resp.Status == "" {
				if resp.HTTPStatus == 0 || (resp.HTTPStatus >= 200 && resp.HTTPStatus < 300) {
					resp.Status = "ok"
				} else {
					resp.Status = "error"
				}
			}
		}
		s.mu.Lock()
		s.executed[req.RequestID] = executedEntry{resp: resp, at: time.Now()}
		s.mu.Unlock()
	}

	respBody, err := json.Marshal(resp)
	if err != nil {
		log.Printf("s3rpc server: marshal resp for %s: %v", req.RequestID, err)
		return
	}
	respKey := fmt.Sprintf("cp/%s/outbound/%s/resp/%s.json", s.selfID, parentID, req.RequestID)
	if err := s.cli.Put(ctx, respKey, respBody, "application/json"); err != nil {
		log.Printf("s3rpc server: put resp %s: %v", respKey, err)
		return
	}
}

// gcExecuted prunes idempotency cache entries older than 1h. Called
// after each tick — cheap because the map is small.
func (s *Server) gcExecuted() {
	cutoff := time.Now().Add(-1 * time.Hour)
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, e := range s.executed {
		if e.at.Before(cutoff) {
			delete(s.executed, id)
		}
	}
}

// parentIDFromReqKey peels the parent CP id out of a key like
// 'cp/<parent>/outbound/<self>/req/<id>.json'. Returns "" on a
// shape mismatch.
func parentIDFromReqKey(key, selfID string) string {
	key = strings.Trim(key, "/")
	parts := strings.Split(key, "/")
	// Expect: cp / parent / outbound / self / req / id.json
	if len(parts) != 6 {
		return ""
	}
	if parts[0] != "cp" || parts[2] != "outbound" || parts[3] != selfID || parts[4] != "req" {
		return ""
	}
	return parts[1]
}
