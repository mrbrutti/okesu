package controlplane

import (
	"sync"
)

// Broadcaster fans out raw JSONL event lines to a set of subscribers.
// Used by the SSE endpoint and the webhook receiver.
//
// Subscribers receive new events on a buffered channel. Slow subscribers
// drop messages rather than blocking the publisher.
type Broadcaster struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

// NewBroadcaster constructs an empty Broadcaster.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subs: make(map[chan []byte]struct{})}
}

// Subscribe returns a buffered channel that receives a copy of every Publish
// call. The returned cancel func unregisters the subscriber and closes the
// channel; always defer it.
func (b *Broadcaster) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 32)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		if _, ok := b.subs[ch]; ok {
			delete(b.subs, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
}

// Publish sends line to every subscriber. Subscribers whose buffer is full
// drop the message (no blocking).
func (b *Broadcaster) Publish(line []byte) {
	cp := make([]byte, len(line))
	copy(cp, line)
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- cp:
		default:
			// drop
		}
	}
}
