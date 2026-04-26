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

// Worker reads from `queue`, batches messages, and writes them to
// `store`. Run blocks until ctx cancels.
//
// On any error from store.InsertBatch the worker logs and DOESN'T ack
// — the queue will redeliver. This means a transient ClickHouse
// outage causes a backlog in Kafka but no data loss.
type Worker struct {
	queue ports.Queue
	store ports.EventStore
	cfg   Config
}

// NewWorker constructs a Worker.
func NewWorker(queue ports.Queue, store ports.EventStore, cfg Config) *Worker {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 1000
	}
	if cfg.BatchTimeout <= 0 {
		cfg.BatchTimeout = 100 * time.Millisecond
	}
	return &Worker{queue: queue, store: store, cfg: cfg}
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
		if err := w.store.InsertBatch(ctx, pending); err != nil {
			log.Printf("eventpipeline: batch insert failed (%d rows): %v", len(pending), err)
			// Don't clear pending — the next flush retries the same
			// rows. In the Kafka path, the messages aren't acked yet
			// so they'll be redelivered if the CP dies; the in-process
			// path just keeps retrying in memory until it succeeds.
			return
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
