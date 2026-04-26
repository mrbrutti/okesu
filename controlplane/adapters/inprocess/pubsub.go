package inprocess

import (
	"context"
	"sync"

	"github.com/section9labs/okesu/controlplane/ports"
)

// PubSub is the in-process channel-backed PubSub adapter.
//
// Topology: per-topic, an unbounded list of subscriber channels. Each
// subscriber gets its own buffered channel; full = drop (the contract
// for ports.PubSub). Designed to mirror the existing Broadcaster's
// semantics so swapping it in is behaviour-preserving.
type PubSub struct {
	mu     sync.Mutex
	subs   map[string]map[chan []byte]struct{} // topic → set of subscribers
	closed bool
}

// NewPubSub returns an empty PubSub.
func NewPubSub() *PubSub {
	return &PubSub{subs: make(map[string]map[chan []byte]struct{})}
}

const pubsubBuffer = 32

// Subscribe returns a receive channel + cancel func for the given topic.
func (p *PubSub) Subscribe(ctx context.Context, topic string) (<-chan []byte, func(), error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, nil, ports.ErrShutdown
	}
	ch := make(chan []byte, pubsubBuffer)
	if p.subs[topic] == nil {
		p.subs[topic] = make(map[chan []byte]struct{})
	}
	p.subs[topic][ch] = struct{}{}
	p.mu.Unlock()

	cancel := func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if set, ok := p.subs[topic]; ok {
			if _, has := set[ch]; has {
				delete(set, ch)
				close(ch)
				if len(set) == 0 {
					delete(p.subs, topic)
				}
			}
		}
	}

	// Auto-cancel on ctx done so callers can `defer cancel()` AND rely
	// on context cancellation for cleanup.
	if ctx != nil {
		go func() {
			<-ctx.Done()
			cancel()
		}()
	}

	return ch, cancel, nil
}

// Publish broadcasts msg to every subscriber on topic. Drops on slow.
func (p *PubSub) Publish(_ context.Context, topic string, msg []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ports.ErrShutdown
	}
	cp := append([]byte(nil), msg...)
	for ch := range p.subs[topic] {
		select {
		case ch <- cp:
		default:
			// drop — slow subscriber
		}
	}
	return nil
}

// Close shuts down all subscriptions.
func (p *PubSub) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	for _, set := range p.subs {
		for ch := range set {
			close(ch)
		}
	}
	p.subs = nil
	return nil
}
