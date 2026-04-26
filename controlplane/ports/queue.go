package ports

import "context"

// Queue is a durable message stream — at-least-once delivery, partitioned
// by topic. Used for decoupling webhook ingest from event persistence and
// for any future high-volume async pipeline.
//
// Adapters: kafka (OCI Streaming, MSK, Confluent, Redpanda for dev),
// inprocess (channel-backed; tests + single-instance dev).
//
// Topic semantics
//
//   - Topics are named with dotted lowercase: "events.raw", "findings.ingest".
//   - Producers don't create topics implicitly — the operator pre-provisions
//     them via Terraform (or for in-process, the adapter creates lazily).
//   - Each Subscribe call creates an independent consumer; the adapter
//     manages whatever group/offset semantics are appropriate.
//
// Failure modes
//
//   - Publish errors mean "definitely not delivered." Caller decides retry
//     vs error response to the user.
//   - A handler returning an error means redeliver later (the adapter
//     decides the timing — Kafka uses commit offsets; in-process retries
//     after a short delay). The handler should be idempotent.
type Queue interface {
	// Publish sends one message to topic. Blocks until the broker
	// acknowledges, or returns ctx.Err().
	Publish(ctx context.Context, topic string, msg []byte) error

	// Subscribe registers a handler for topic. The handler is invoked
	// for each message; returning nil acks, returning an error triggers
	// adapter-specific redelivery. Subscription runs until ctx cancels.
	//
	// The `group` arg names the consumer group. Multiple subscribers in
	// the same group share the topic's partitions (parallel scale-out);
	// different groups each see every message.
	Subscribe(ctx context.Context, topic, group string, handler func(ctx context.Context, msg []byte) error) error

	// Close releases any background workers held by the adapter.
	Close() error
}
