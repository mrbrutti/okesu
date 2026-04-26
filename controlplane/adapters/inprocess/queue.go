// Package inprocess holds Queue + PubSub adapters that don't touch any
// external service — channels in memory, single CP only. The dev default,
// the unit-test default, and a fine choice for small deployments where
// adding Kafka/Redis would be operational overkill.
//
// At-least-once delivery is best-effort: a CP restart drops in-flight
// messages. If you need durability past a process restart, use the
// kafka adapter (or Postgres LISTEN/NOTIFY for moderate scale).
package inprocess

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/section9labs/okesu/controlplane/ports"
)

// Queue is the in-process channel-backed Queue adapter.
//
// Each (topic, group) pair gets one buffered channel; multiple
// subscribers in the same group share that channel (load balancing).
// Different groups each have their own channel for the same topic.
//
// Buffer size is generous (1024) so a slow consumer doesn't immediately
// drop messages, but does eventually if persistently behind. A real
// Kafka adapter would back-pressure differently.
type Queue struct {
	mu     sync.Mutex
	queues map[string]chan []byte // key = topic + "|" + group
	closed bool
}

// NewQueue returns an empty in-process Queue.
func NewQueue() *Queue {
	return &Queue{queues: make(map[string]chan []byte)}
}

const queueBuffer = 1024

func qkey(topic, group string) string { return topic + "|" + group }

// Publish ensures every group with a subscriber on `topic` receives msg.
// Groups created later don't see this message — same as Kafka semantics.
func (q *Queue) Publish(_ context.Context, topic string, msg []byte) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ports.ErrShutdown
	}
	cp := append([]byte(nil), msg...)
	for k, ch := range q.queues {
		// Match all groups subscribed to this topic.
		if len(k) > len(topic)+1 && k[:len(topic)+1] == topic+"|" {
			select {
			case ch <- cp:
			default:
				// Buffer full → drop. This is the "behind on consumption"
				// signal; the consumer will catch up but lose this msg.
				// A real Kafka adapter would persist instead.
			}
		}
	}
	return nil
}

// Subscribe registers a handler. Runs until ctx cancels.
func (q *Queue) Subscribe(ctx context.Context, topic, group string, handler func(ctx context.Context, msg []byte) error) error {
	if topic == "" || group == "" {
		return errors.New("queue: topic and group are required")
	}
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return ports.ErrShutdown
	}
	k := qkey(topic, group)
	ch, ok := q.queues[k]
	if !ok {
		ch = make(chan []byte, queueBuffer)
		q.queues[k] = ch
	}
	q.mu.Unlock()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-ch:
			if !ok {
				return ports.ErrShutdown
			}
			// Run handler with the same ctx so cancellation propagates.
			if err := handler(ctx, msg); err != nil {
				// Adapter-specific redelivery: requeue with a small
				// backoff so a busted handler doesn't spin.
				go func(m []byte) {
					time.Sleep(500 * time.Millisecond)
					q.mu.Lock()
					if q.closed {
						q.mu.Unlock()
						return
					}
					select {
					case ch <- m:
					default:
						// drop — already overflowed
					}
					q.mu.Unlock()
				}(msg)
			}
		}
	}
}

// Close drains and shuts down all queues.
func (q *Queue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return nil
	}
	q.closed = true
	for _, ch := range q.queues {
		close(ch)
	}
	q.queues = nil
	return nil
}
