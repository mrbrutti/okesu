package ports

import "context"

// PubSub is fire-and-forget broadcast — no persistence, no replay, no
// guaranteed delivery. Used for SSE fan-out (live event feed for the UI),
// run subscriber notifications, and similar "every connected operator
// should hear about this NOW" use cases.
//
// Adapters: redis (managed Redis on OCI / ElastiCache / Cloud Memorystore),
// inprocess (channel-backed; single-CP dev).
//
// Subscriber semantics
//
//   - Subscribers receive a copy of every Publish call after they
//     registered. Messages published before Subscribe are NOT replayed.
//   - Slow subscribers DROP messages rather than block the publisher.
//     SSE clients that fall behind reconnect and miss the gap; they'll
//     pull the missed events via the GET /api/events history endpoint
//     instead. The publisher MUST stay fast — this is hot-path code.
//   - Channels close when the subscriber's cancel func is called OR
//     when the adapter is shut down.
//
// This contract intentionally diverges from Queue: PubSub is for
// real-time UI signals where stale data is worse than missing data.
// Queue is for durable async pipelines where every message matters.
type PubSub interface {
	// Subscribe registers an interest in `topic`. Returns a receive-only
	// channel and a cancel func that unregisters and closes it. The
	// channel is buffered; full = drop.
	Subscribe(ctx context.Context, topic string) (<-chan []byte, func(), error)

	// Publish broadcasts msg to every subscriber of topic. Non-blocking
	// from the caller's perspective even if subscribers are slow.
	Publish(ctx context.Context, topic string, msg []byte) error

	// Close shuts down all subscriptions and frees resources.
	Close() error
}
