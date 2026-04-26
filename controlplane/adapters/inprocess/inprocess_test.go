package inprocess_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/section9labs/okesu/controlplane/adapters/inprocess"
	"github.com/section9labs/okesu/controlplane/ports"
)

// TestPubSubBasic verifies subscribe/publish/cancel semantics that the
// SSE fanout depends on.
func TestPubSubBasic(t *testing.T) {
	p := inprocess.NewPubSub()
	t.Cleanup(func() { _ = p.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, sub1Cancel, err := p.Subscribe(ctx, "events.live")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if err := p.Publish(ctx, "events.live", []byte("hello")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case msg := <-ch:
		if string(msg) != "hello" {
			t.Fatalf("got %q, want hello", string(msg))
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message")
	}

	sub1Cancel()
	// Channel should now be closed.
	if _, ok := <-ch; ok {
		t.Fatal("expected closed channel after cancel")
	}
}

// TestPubSubFanout — multiple subscribers each see a copy.
func TestPubSubFanout(t *testing.T) {
	p := inprocess.NewPubSub()
	t.Cleanup(func() { _ = p.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const N = 5
	subs := make([]<-chan []byte, N)
	for i := 0; i < N; i++ {
		ch, _, err := p.Subscribe(ctx, "t")
		if err != nil {
			t.Fatalf("sub %d: %v", i, err)
		}
		subs[i] = ch
	}

	if err := p.Publish(ctx, "t", []byte("msg")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	for i, ch := range subs {
		select {
		case msg := <-ch:
			if string(msg) != "msg" {
				t.Fatalf("sub %d got %q", i, string(msg))
			}
		case <-time.After(time.Second):
			t.Fatalf("sub %d timeout", i)
		}
	}
}

// TestQueueRoundTrip — a publish lands in the handler.
func TestQueueRoundTrip(t *testing.T) {
	q := inprocess.NewQueue()
	t.Cleanup(func() { _ = q.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got := make(chan []byte, 1)
	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = q.Subscribe(ctx, "events.raw", "ingest", func(_ context.Context, msg []byte) error {
			got <- msg
			return nil
		})
	}()

	// Give the subscriber a moment to register.
	time.Sleep(50 * time.Millisecond)

	if err := q.Publish(ctx, "events.raw", []byte("payload")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case msg := <-got:
		if string(msg) != "payload" {
			t.Fatalf("got %q", string(msg))
		}
	case <-time.After(time.Second):
		t.Fatal("handler never invoked")
	}

	cancel()
	wg.Wait()
}

// TestQueueRedeliveryOnError — handler returning err causes redelivery.
func TestQueueRedeliveryOnError(t *testing.T) {
	q := inprocess.NewQueue()
	t.Cleanup(func() { _ = q.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var attempts int32
	done := make(chan struct{})
	go func() {
		_ = q.Subscribe(ctx, "events.raw", "ingest", func(_ context.Context, msg []byte) error {
			n := atomic.AddInt32(&attempts, 1)
			if n < 3 {
				return ports.ErrShutdown // arbitrary non-nil
			}
			close(done)
			return nil
		})
	}()
	time.Sleep(50 * time.Millisecond)
	_ = q.Publish(ctx, "events.raw", []byte("x"))

	select {
	case <-done:
		if atomic.LoadInt32(&attempts) < 3 {
			t.Fatal("expected at least 3 redeliveries")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler never succeeded after retries")
	}
}
