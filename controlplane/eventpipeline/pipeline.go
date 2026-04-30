// Package eventpipeline owns the async event-ingest path: webhook
// handler enqueues raw event lines, this package's worker consumes
// from the queue and batches them into the EventStore.
//
// In dev (--queue=inprocess --events-store=sqlite) the pipeline is a
// goroutine that handles the in-memory queue and writes to SQLite.
// Same code, same shape, just different adapters wired in.
//
// In production (--queue=kafka --events-store=clickhouse) the worker
// is one of many — Kafka does consumer-group load balancing across
// CP replicas, each replica running this same loop. Failures
// (ClickHouse unreachable, etc.) keep the message uncommitted in
// Kafka so a healthy replica picks it up. This is the resilience
// story we wanted: a single CP going down doesn't lose events.
package eventpipeline

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/section9labs/okesu/agent"
	"github.com/section9labs/okesu/controlplane/db"
	"github.com/section9labs/okesu/controlplane/ports"
)

// Topic for raw events. Lowercase dotted convention — same as Kafka
// shop convention.
const TopicEventsRaw = "events.raw"

// Group is the consumer group the worker registers under. Multiple
// CPs running this worker share the partition load via this group.
const ConsumerGroup = "okesu-event-ingest"

// Config tunes batching. Defaults are tuned for ClickHouse — flush
// every 100ms or when 1000 rows accumulate, whichever comes first.
// SQLite-backed deployments are fine with the same defaults; the
// per-batch overhead is cheap there too.
type Config struct {
	BatchSize    int           // max rows per insert; 0 → 1000
	BatchTimeout time.Duration // max wait before flush; 0 → 100ms
}

// Worker reads from `queue`, batches messages, and writes them to the
// EventStore. Run blocks until ctx cancels.
//
// Phase 8c.next2 — the worker also projects finding events into the
// relational state Store. The split is: events go through batched
// EventStore.InsertBatch (cheap, columnar, append-only); findings go
// through per-row Store.InsertFinding so each finding gets its
// generated event_id from a single-row Insert. Findings are typically
// a tiny fraction of total event volume so the per-row cost is fine.
//
// On any error from EventStore.InsertBatch the worker logs and
// DOESN'T ack — the queue redelivers. Transient ClickHouse outages
// produce a Kafka backlog but no data loss. Finding projection errors
// are logged but don't block the batch ack: the underlying event is
// already durable; the finding can be re-projected later if needed
// (Phase 13's known-issues feed reads from the events table).
type Worker struct {
	queue      ports.Queue
	eventStore ports.EventStore
	store      *db.Store
	cfg        Config

	// onFindingProjected fires after a finding row has landed in the
	// DB. Used by the orchestrator's auto-trigger probe to evaluate
	// `trigger.on=finding` orchestrations against the new finding.
	// Optional — nil callback is a no-op. Wired in server.go.
	onFindingProjected func(FindingProjectedEvent)
}

// FindingProjectedEvent is the payload passed to onFindingProjected.
// Decoupled from db.Finding so callers (the orchestrator's hook
// belongs to the api package) don't have to import db just to
// receive the event.
type FindingProjectedEvent struct {
	FindingID  int64
	EventID    int64
	Severity   string
	Title      string
	Agent      string
	Host       string
	Category   string
	DedupKey   string
	Resource   string
	Attributes map[string]any
}

// SetFindingHook installs (or replaces) the post-projection callback.
// Safe to call before Run() — there's no concurrent access until the
// worker starts processing batches.
func (w *Worker) SetFindingHook(fn func(FindingProjectedEvent)) {
	w.onFindingProjected = fn
}

// NewWorker constructs a Worker. Pass `store` nil to skip finding
// projection (e.g. read-only replicas, or tests that only care about
// the events firehose).
func NewWorker(queue ports.Queue, eventStore ports.EventStore, store *db.Store, cfg Config) *Worker {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 1000
	}
	if cfg.BatchTimeout <= 0 {
		cfg.BatchTimeout = 100 * time.Millisecond
	}
	return &Worker{queue: queue, eventStore: eventStore, store: store, cfg: cfg}
}

// Run starts the worker loop. Blocks until ctx cancels or the queue
// returns a fatal error.
func (w *Worker) Run(ctx context.Context) error {
	pending := make([]ports.EventRecord, 0, w.cfg.BatchSize)
	flush := make(chan struct{}, 1)

	// Time-based flush. Sends a flush signal when BatchTimeout elapses
	// even if pending is below BatchSize — keeps live events visible
	// without operators having to wait for a full batch.
	go func() {
		t := time.NewTicker(w.cfg.BatchTimeout)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				select {
				case flush <- struct{}{}:
				default:
				}
			}
		}
	}()

	doFlush := func() {
		if len(pending) == 0 {
			return
		}
		// Split findings out so we can capture each one's event_id
		// from a single-row Insert and link it into the findings
		// table. Non-findings go through the batched fast path.
		var (
			findings   []ports.EventRecord
			nonFinding = pending[:0:cap(pending)]
		)
		for _, e := range pending {
			if e.Type == "finding" {
				findings = append(findings, e)
			} else {
				nonFinding = append(nonFinding, e)
			}
		}
		if len(nonFinding) > 0 {
			if err := w.eventStore.InsertBatch(ctx, nonFinding); err != nil {
				log.Printf("eventpipeline: batch insert failed (%d rows): %v", len(nonFinding), err)
				// Don't clear pending — the next flush retries the
				// same rows. In the Kafka path, the messages aren't
				// acked yet so they'll be redelivered if the CP dies;
				// the in-process path retries in memory.
				return
			}
		}
		for _, ev := range findings {
			id, err := w.eventStore.Insert(ctx, ev)
			if err != nil {
				log.Printf("eventpipeline: finding event insert failed: %v", err)
				return // leave pending intact for retry
			}
			if w.store == nil {
				continue
			}
			fi, perr := parseFindingFields(ev)
			if perr != nil {
				log.Printf("eventpipeline: finding projection parse failed: %v (event_id=%d, skipping projection)", perr, id)
				continue
			}
			fi.EventID = id
			fid, ferr := w.store.InsertFinding(fi)
			if ferr != nil {
				// Event is durable; finding row failed to project.
				// Log and continue — don't redeliver, that would
				// double-insert the underlying event.
				log.Printf("eventpipeline: finding projection failed (event_id=%d): %v", id, ferr)
				continue
			}
			// Tier-0 dedup closure: when a new finding lands sharing
			// a dedup_key with older opens, supersede the siblings so
			// the operator queue stays small. Engine-level: no LLM
			// in the loop, deterministic, free. The Audit + run-link
			// rows make the rolled-up siblings easy to find later.
			if fi.DedupKey != "" {
				if n, err := w.store.SupersedeOpenDedups(fid, fi.DedupKey); err != nil {
					log.Printf("eventpipeline: supersede on dedup_key %q: %v", fi.DedupKey, err)
				} else if n > 0 {
					log.Printf("eventpipeline: rolled up %d sibling finding(s) by dedup_key %q (kept #%d)", n, fi.DedupKey, fid)
				}
			}
			if w.onFindingProjected != nil {
				// Best-effort: any panic in the callback is
				// recovered so a buggy hook can't kill the worker.
				func() {
					defer func() {
						if r := recover(); r != nil {
							log.Printf("eventpipeline: onFindingProjected panic: %v", r)
						}
					}()
					w.onFindingProjected(FindingProjectedEvent{
						FindingID:  fid,
						EventID:    id,
						Severity:   fi.Severity,
						Title:      fi.Title,
						Agent:      ev.Agent,
						Host:       ev.Host,
						Category:   fi.Category,
						DedupKey:   fi.DedupKey,
						Resource:   fi.Resource,
						Attributes: decodeAttributes(fi.Attributes),
					})
				}()
			}
		}
		pending = pending[:0]
	}

	// The Subscribe call registers our handler; messages flow into the
	// goroutine. We don't return from Subscribe until ctx cancels.
	// Goroutine accumulates into pending; the time-flush goroutine
	// triggers periodic emission via the flush channel.
	handlerErr := make(chan error, 1)
	go func() {
		handlerErr <- w.queue.Subscribe(ctx, TopicEventsRaw, ConsumerGroup,
			func(_ context.Context, msg []byte) error {
				var e ports.EventRecord
				if err := json.Unmarshal(msg, &e); err != nil {
					// Malformed event — drop. Returning err would just
					// redeliver the same broken message forever.
					log.Printf("eventpipeline: bad message: %v (skipping)", err)
					return nil
				}
				pending = append(pending, e)
				if len(pending) >= w.cfg.BatchSize {
					select {
					case flush <- struct{}{}:
					default:
					}
				}
				return nil
			})
	}()

	for {
		select {
		case <-ctx.Done():
			doFlush() // best-effort final flush
			return ctx.Err()
		case <-flush:
			doFlush()
		case err := <-handlerErr:
			doFlush()
			return err
		}
	}
}

// findingFields mirrors the optional fields the daemon attaches to a
// finding-type event. Re-parsed from RawJSON because ports.EventRecord
// only carries the indexed fields; the firehose RawJSON is the source
// of truth for everything else.
type findingFields struct {
	Resource        string          `json:"resource"`
	Evidence        string          `json:"evidence"`
	DedupKey        string          `json:"dedup_key"`
	Category        string          `json:"category"`
	ProcessPID      int64           `json:"process_pid"`
	ProcessName     string          `json:"process_name"`
	Path            string          `json:"path"`
	NetworkEndpoint string          `json:"network_endpoint"`
	CVE             string          `json:"cve"`
	Tags            string          `json:"tags"`
	Attributes      json.RawMessage `json:"attributes"`
	// Phase 22.3 — finding subtype emitted by orchestration agents
	// (e.g. "hypothesis", "meeting_minutes"). Drives UI rendering
	// hooks. NULL/empty = no special rendering.
	Subtype string `json:"subtype"`
}

// decodeAttributes turns a finding's JSON-encoded attributes string
// (as stored on the Finding row) into a Go map. Best-effort: an
// undecodable string yields an empty map so the trigger probe still
// runs against the rest of the finding fields.
func decodeAttributes(s string) map[string]any {
	if s == "" {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}

func parseFindingFields(ev ports.EventRecord) (*db.FindingInsert, error) {
	var f findingFields
	if ev.RawJSON != "" {
		if err := json.Unmarshal([]byte(ev.RawJSON), &f); err != nil {
			return nil, err
		}
	}
	return &db.FindingInsert{
		Ts:              ev.Ts,
		Agent:           ev.Agent,
		Host:            ev.Host,
		Severity:        ev.Severity,
		Title:           agent.NormalizeFindingTitle(ev.Title),
		Resource:        f.Resource,
		Evidence:        f.Evidence,
		DedupKey:        f.DedupKey,
		RawJSON:         ev.RawJSON,
		Category:        f.Category,
		ProcessPID:      f.ProcessPID,
		ProcessName:     f.ProcessName,
		Path:            f.Path,
		NetworkEndpoint: f.NetworkEndpoint,
		CVE:             f.CVE,
		Tags:            f.Tags,
		Attributes:      string(f.Attributes),
		Subtype:         f.Subtype,
	}, nil
}
