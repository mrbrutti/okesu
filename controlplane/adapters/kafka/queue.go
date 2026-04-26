// Package kafka implements ports.Queue against any Kafka-compatible
// broker — production targets:
//   - OCI Streaming (Kafka API on the streaming-pool endpoint)
//   - AWS MSK / MSK Serverless
//   - Confluent Cloud
//   - Self-managed Apache Kafka
//   - Redpanda (the dev choice; same API surface, single binary)
//
// segmentio/kafka-go is the right level of abstraction here: simpler
// than franz-go for our use case, no codegen, single dependency.
//
// Design notes:
//   - One Writer per producer (the CP webhook handler holds one,
//     reuses across requests). Lazy-create on first Publish.
//   - One Reader per Subscribe call. The consumer-group coordination
//     happens server-side; segmentio-kafka-go handles offset commits
//     with sensible defaults (CommitInterval: 1s).
//   - Message handler returning err means the message stays uncommitted
//     and gets redelivered when the consumer restarts. Idempotent
//     handlers are required (same as ports.Queue contract).
package kafka

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sync"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/plain"

	"github.com/section9labs/okesu/controlplane/ports"
)

// Config bundles the connection params. SASL/TLS off for dev; on for
// any managed offering (OCI Streaming, MSK, Confluent require both).
type Config struct {
	Brokers []string // host:port pairs
	// SASL credentials. OCI Streaming uses
	//   user: <tenancy>/<username>/<stream-pool-ocid>
	//   password: <auth-token>
	SASLUsername string
	SASLPassword string
	UseTLS       bool
}

// Adapter is the shared Kafka client used for both Publish and
// Subscribe. Multiple producers / consumers can share one Adapter.
type Adapter struct {
	cfg Config

	mu      sync.Mutex
	writers map[string]*kafkago.Writer // topic → writer
	readers []*kafkago.Reader
	closed  bool
}

// New constructs an Adapter. Doesn't dial yet — the first Publish or
// Subscribe call triggers connection.
func New(cfg Config) (*Adapter, error) {
	if len(cfg.Brokers) == 0 {
		return nil, errors.New("kafka: brokers required")
	}
	return &Adapter{cfg: cfg, writers: map[string]*kafkago.Writer{}}, nil
}

func (a *Adapter) dialer() *kafkago.Dialer {
	d := &kafkago.Dialer{
		Timeout:   10 * time.Second,
		DualStack: true,
	}
	if a.cfg.UseTLS {
		d.TLS = &tls.Config{} //nolint:gosec  // TODO: pin CA when we wire OCI ca.crt
	}
	if a.cfg.SASLUsername != "" {
		d.SASLMechanism = plain.Mechanism{
			Username: a.cfg.SASLUsername,
			Password: a.cfg.SASLPassword,
		}
	}
	return d
}

// Publish sends one message to topic. Lazy-creates a Writer on first
// call per topic.
func (a *Adapter) Publish(ctx context.Context, topic string, msg []byte) error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return ports.ErrShutdown
	}
	w, ok := a.writers[topic]
	if !ok {
		w = &kafkago.Writer{
			Addr:                   kafkago.TCP(a.cfg.Brokers...),
			Topic:                  topic,
			Balancer:               &kafkago.Hash{},
			AllowAutoTopicCreation: true,
			Async:                  false, // synchronous ack; webhook handler returns 202 only after commit
			Transport: &kafkago.Transport{
				TLS:  tlsConfigOrNil(a.cfg.UseTLS),
				SASL: saslOrNil(a.cfg.SASLUsername, a.cfg.SASLPassword),
			},
		}
		a.writers[topic] = w
	}
	a.mu.Unlock()
	return w.WriteMessages(ctx, kafkago.Message{Value: append([]byte(nil), msg...)})
}

// Subscribe consumes from topic with the given consumer group.
func (a *Adapter) Subscribe(ctx context.Context, topic, group string, handler func(ctx context.Context, msg []byte) error) error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return ports.ErrShutdown
	}
	r := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:        a.cfg.Brokers,
		Topic:          topic,
		GroupID:        group,
		MinBytes:       1,
		MaxBytes:       10 * 1024 * 1024,
		CommitInterval: time.Second,
		Dialer:         a.dialer(),
	})
	a.readers = append(a.readers, r)
	a.mu.Unlock()

	defer func() {
		_ = r.Close()
	}()

	for {
		m, err := r.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("kafka fetch: %w", err)
		}
		if err := handler(ctx, m.Value); err != nil {
			// Don't commit — message will be redelivered. This matches
			// the at-least-once contract of ports.Queue.
			continue
		}
		if err := r.CommitMessages(ctx, m); err != nil {
			return fmt.Errorf("kafka commit: %w", err)
		}
	}
}

// Close releases every writer + reader.
func (a *Adapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil
	}
	a.closed = true
	for _, w := range a.writers {
		_ = w.Close()
	}
	for _, r := range a.readers {
		_ = r.Close()
	}
	a.writers = nil
	a.readers = nil
	return nil
}

func tlsConfigOrNil(use bool) *tls.Config {
	if !use {
		return nil
	}
	return &tls.Config{} //nolint:gosec
}

func saslOrNil(user, pass string) plain.Mechanism {
	if user == "" {
		return plain.Mechanism{}
	}
	return plain.Mechanism{Username: user, Password: pass}
}

// Compile-time assertion.
var _ ports.Queue = (*Adapter)(nil)
