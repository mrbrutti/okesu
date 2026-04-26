package controlplane

import (
	"context"

	"github.com/section9labs/okesu/controlplane/adapters/inprocess"
	"github.com/section9labs/okesu/controlplane/ports"
)

// Broadcaster fans out raw JSONL event lines to a set of subscribers.
// Used by the SSE endpoint and the webhook receiver.
//
// Subscribers receive new events on a buffered channel. Slow subscribers
// drop messages rather than blocking the publisher.
//
// As of Phase 8a Broadcaster is a thin adapter over a ports.PubSub.
// The default factory NewBroadcaster wires the in-process channel-based
// adapter so behaviour is unchanged. Phase 8d swaps in Redis when
// CP runs in a multi-replica deployment.
type Broadcaster struct {
	pubsub ports.PubSub
	topic  string
}

// liveTopic is the canonical topic name for live events. Constants kept
// stringly-typed so different PubSub adapters (especially Redis) get a
// stable channel name.
const liveTopic = "events.live"

// NewBroadcaster constructs an empty Broadcaster backed by an in-process
// PubSub adapter. Same behaviour as the pre-Phase-8 implementation — the
// indirection just makes the boundary explicit.
func NewBroadcaster() *Broadcaster {
	return NewBroadcasterWith(inprocess.NewPubSub())
}

// NewBroadcasterWith wraps the given PubSub. Used in Phase 8d wiring
// when the CP needs to share fanout state across replicas via Redis.
func NewBroadcasterWith(p ports.PubSub) *Broadcaster {
	return &Broadcaster{pubsub: p, topic: liveTopic}
}

// Subscribe returns a buffered channel that receives a copy of every
// Publish call. The returned cancel func unregisters the subscriber and
// closes the channel; always defer it.
func (b *Broadcaster) Subscribe() (<-chan []byte, func()) {
	ch, cancel, err := b.pubsub.Subscribe(context.Background(), b.topic)
	if err != nil {
		// Adapter shutdown is the only realistic error here; fall back
		// to a closed channel + no-op cancel so callers can keep their
		// existing API contract.
		closed := make(chan []byte)
		close(closed)
		return closed, func() {}
	}
	return ch, cancel
}

// Publish sends line to every subscriber. Subscribers whose buffer is
// full drop the message (no blocking).
func (b *Broadcaster) Publish(line []byte) {
	_ = b.pubsub.Publish(context.Background(), b.topic, line)
}
