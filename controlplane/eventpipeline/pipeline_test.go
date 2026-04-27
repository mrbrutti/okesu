package eventpipeline

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/section9labs/okesu/controlplane/adapters/inprocess"
	"github.com/section9labs/okesu/controlplane/ports"
)

// fakeEventStore captures InsertBatch / Insert calls so we can assert
// the worker split findings out of the batch path correctly.
type fakeEventStore struct {
	mu       sync.Mutex
	batches  [][]ports.EventRecord
	singles  []ports.EventRecord
	nextID   int64
}

func (f *fakeEventStore) Insert(_ context.Context, e ports.EventRecord) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	e.ID = f.nextID
	f.singles = append(f.singles, e)
	return f.nextID, nil
}
func (f *fakeEventStore) InsertBatch(_ context.Context, evs []ports.EventRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batches = append(f.batches, append([]ports.EventRecord(nil), evs...))
	return nil
}
func (f *fakeEventStore) Recent(_ context.Context, _ int, _ int64) ([]ports.EventRecord, error) {
	return nil, nil
}

// TestWorker_SplitsFindingsFromBatch verifies that finding events go
// through the per-row Insert path (so each one can capture an event_id
// for projection) while non-finding events use the batched fast path.
//
// This is the core of the Phase 8c.next2 contract: the eventpipeline
// worker is now responsible for both event persistence and finding
// projection, and it must keep the per-row vs batched split correct.
func TestWorker_SplitsFindingsFromBatch(t *testing.T) {
	q := inprocess.NewQueue()
	defer q.Close()

	es := &fakeEventStore{}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// store=nil → finding projection skipped (we only test the
	// EventStore split here; projection itself needs a *db.Store).
	w := NewWorker(q, es, nil, Config{BatchSize: 100, BatchTimeout: 50 * time.Millisecond})
	go func() { _ = w.Run(ctx) }()

	// Give Subscribe a tick to register the in-process channel before
	// the first Publish lands. (Inprocess Publish doesn't buffer for
	// groups created later, mirroring Kafka semantics.)
	time.Sleep(20 * time.Millisecond)

	publish := func(e ports.EventRecord) {
		b, _ := json.Marshal(e)
		_ = q.Publish(ctx, TopicEventsRaw, b)
	}
	publish(ports.EventRecord{Ts: 1, Type: "tick_done", Agent: "edr", Host: "h1"})
	publish(ports.EventRecord{Ts: 2, Type: "finding", Agent: "edr", Host: "h1", Severity: "HIGH", Title: "X", RawJSON: `{"resource":"pid:1"}`})
	publish(ports.EventRecord{Ts: 3, Type: "tick_done", Agent: "edr", Host: "h1"})

	// Wait for one flush cycle.
	time.Sleep(150 * time.Millisecond)

	es.mu.Lock()
	defer es.mu.Unlock()

	// Non-findings batched together.
	if len(es.batches) == 0 {
		t.Fatalf("expected at least one batch, got 0")
	}
	var batched int
	for _, b := range es.batches {
		batched += len(b)
		for _, e := range b {
			if e.Type == "finding" {
				t.Errorf("finding %q ended up in batched path; should have been per-row", e.Title)
			}
		}
	}
	if batched != 2 {
		t.Errorf("expected 2 non-finding events batched, got %d", batched)
	}

	// Findings went per-row.
	if len(es.singles) != 1 {
		t.Errorf("expected 1 finding via per-row Insert, got %d", len(es.singles))
	}
	if len(es.singles) == 1 && es.singles[0].Type != "finding" {
		t.Errorf("per-row Insert got non-finding event: %+v", es.singles[0])
	}
}

func TestParseFindingFields(t *testing.T) {
	ev := ports.EventRecord{
		Ts:      1700000000,
		Agent:   "edr",
		Host:    "h1",
		Severity: "HIGH",
		Title:   "PERSISTENT (TICK 87): Memfd process",
		RawJSON: `{"resource":"pid:1337","evidence":"e","dedup_key":"d","category":"process","process_pid":1337,"path":"/usr/bin/x"}`,
	}
	fi, err := parseFindingFields(ev)
	if err != nil {
		t.Fatal(err)
	}
	if fi.ProcessPID != 1337 {
		t.Errorf("ProcessPID = %d, want 1337", fi.ProcessPID)
	}
	if fi.Resource != "pid:1337" {
		t.Errorf("Resource = %q", fi.Resource)
	}
	if fi.Path != "/usr/bin/x" {
		t.Errorf("Path = %q", fi.Path)
	}
	if fi.Title == ev.Title {
		t.Errorf("Title should be normalized; got unchanged %q", fi.Title)
	}
}
