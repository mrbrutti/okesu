// Package redispubsub implements ports.PubSub against Redis (or any
// Redis-compatible service: OCI Cache, AWS ElastiCache, Cloud
// Memorystore, KeyDB, Dragonfly).
//
// The CP-side wiring (controlplane/broadcast.go) accepts any
// ports.PubSub, so swapping from the in-process adapter to Redis is a
// config decision: --pubsub redis://host:6379/0 picks this adapter.
//
// Why Redis: when the CP runs as multiple replicas behind a load
// balancer, an event posted to webhook on replica A must reach the
// SSE-stream endpoint that's serving an operator on replica B. Redis
// PUBLISH/SUBSCRIBE handles this with no operational fuss; the message
// path crosses replicas in a few milliseconds.
//
// Subscriber semantics match the in-process adapter:
//   - Each Subscribe call gets its own buffered channel.
//   - Slow subscribers DROP messages (PubSub is fire-and-forget — see
//     the ports.PubSub doc; durability is the Queue's job).
//   - Cancel func + ctx-cancel both unregister cleanly.
package redispubsub

import (
	"context"
	"fmt"
	"sync"

	"github.com/redis/go-redis/v9"

	"github.com/section9labs/okesu/controlplane/ports"
)

// Adapter satisfies ports.PubSub backed by a Redis client.
type Adapter struct {
	client *redis.Client

	mu     sync.Mutex
	closed bool
}

// New connects to Redis at the given URL (e.g. "redis://host:6379/0")
// and verifies connectivity with a Ping. The Adapter is shared across
// many Subscribe calls; each call opens its own Redis pubsub channel
// behind the scenes.
func New(ctx context.Context, redisURL string) (*Adapter, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("redispubsub: parse url: %w", err)
	}
	c := redis.NewClient(opts)
	if err := c.Ping(ctx).Err(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("redispubsub: ping: %w", err)
	}
	return &Adapter{client: c}, nil
}

const subscriberBuffer = 32

// Subscribe registers an interest in `topic` and returns a buffered
// receive-only channel + cancel func.
func (a *Adapter) Subscribe(ctx context.Context, topic string) (<-chan []byte, func(), error) {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil, nil, ports.ErrShutdown
	}
	a.mu.Unlock()

	// One Redis subscription per Subscribe call. The CP typically has
	// O(few hundred) live subscribers — the SSE endpoints in operator
	// browsers — which Redis handles trivially.
	sub := a.client.Subscribe(ctx, topic)
	if _, err := sub.Receive(ctx); err != nil {
		_ = sub.Close()
		return nil, nil, fmt.Errorf("redispubsub: subscribe: %w", err)
	}

	out := make(chan []byte, subscriberBuffer)
	closed := make(chan struct{})

	// Bridge Redis's *Message channel onto our []byte channel. Drop
	// when the consumer is slow (matches in-process semantics + the
	// ports.PubSub contract).
	go func() {
		defer close(out)
		ch := sub.Channel()
		for {
			select {
			case <-closed:
				return
			case <-ctx.Done():
				return
			case m, ok := <-ch:
				if !ok {
					return
				}
				select {
				case out <- []byte(m.Payload):
				default:
					// drop on slow consumer
				}
			}
		}
	}()

	cancel := func() {
		select {
		case <-closed:
		default:
			close(closed)
		}
		_ = sub.Close()
	}

	// Wire ctx cancellation to also tear down — caller may rely on
	// either path.
	go func() {
		<-ctx.Done()
		cancel()
	}()

	return out, cancel, nil
}

// Publish broadcasts msg to every subscriber on `topic`.
func (a *Adapter) Publish(ctx context.Context, topic string, msg []byte) error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return ports.ErrShutdown
	}
	a.mu.Unlock()
	return a.client.Publish(ctx, topic, msg).Err()
}

// Close releases the underlying Redis client.
func (a *Adapter) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	a.mu.Unlock()
	return a.client.Close()
}

// Compile-time assertion.
var _ ports.PubSub = (*Adapter)(nil)
