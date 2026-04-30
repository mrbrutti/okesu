// Package s3publisher is the child-side half of the S3-dead-drop
// federation transport (Phase A).
//
// A child CP that has no inbound HTTPS path (sitting behind NAT, in a
// strict egress-only network, or — like the dev lab — running in a
// public cloud while the parent is on a developer's laptop) can
// federate up by writing introspect snapshots to a known bucket
// prefix on a tick. The parent's s3reader picks them up.
//
// Bucket layout, on the convention the s3reader expects:
//
//   <BucketPrefix>/introspect.json   — last full snapshot, overwritten
//   <BucketPrefix>/heartbeat.json    — { "ts": "..." } for freshness
//
// BucketPrefix is `cp/<this-child-cp-id>/outbound/<parent-cp-id>/`,
// supplied at construction. The publisher knows nothing about
// findings/daimons/nodes yet — those land in Phase A.2 once the
// introspect-only round-trip is proven.
//
// One Publisher per parent peer. Most child CPs federate up to a
// single parent, but the design allows fan-out.

package s3publisher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/section9labs/okesu/agent/s3transport"
)

// Config bundles everything the publisher needs to talk to the
// bucket + know what to put in each manifest. Mirrors the s3scanner's
// ClientConfig fields where possible.
type Config struct {
	Bucket       string
	Endpoint     string
	Region       string
	UseSSL       bool
	AccessKey    string
	SecretKey    string
	BucketPrefix string        // 'cp/<self>/outbound/<parent>/' — must end with '/'
	Interval     time.Duration // tick cadence; defaults to 30s when zero
}

// IntrospectFn returns the JSON snapshot the publisher should write
// on each tick. Provided by the caller (server.go wires it to the
// existing CPIntrospect logic) so this package stays free of
// imports from controlplane/api.
type IntrospectFn func(ctx context.Context) ([]byte, error)

// Asset is one named blob to publish per tick. The publisher writes
// {Path} to {BucketPrefix}{Path} with Render's bytes as the body.
// Phase A.2 uses this to publish findings/daimons/nodes alongside
// introspect — a tiny extension of the same primitive.
type Asset struct {
	// Path within the bucket prefix, e.g. "findings.json".
	Path string
	// Render produces the bytes for this asset. Errors abort just
	// this asset's publish; other assets in the same tick still try.
	Render func(ctx context.Context) ([]byte, error)
	// ContentType for the bucket object; defaults to application/json.
	ContentType string
}

// Publisher writes the child CP's introspect snapshot to the bucket on
// a tick. Run blocks until ctx is cancelled.
type Publisher struct {
	client     *s3transport.Client
	prefix     string
	introspect IntrospectFn
	assets     []Asset
	interval   time.Duration

	mu   sync.Mutex
	last time.Time
	err  error
}

// New connects to the bucket and returns a Publisher ready to Run.
// Returns an error on bad config or unreachable bucket — the caller
// should log + skip rather than crash, so an offline bucket doesn't
// take the CP down.
func New(ctx context.Context, cfg Config, fn IntrospectFn, assets ...Asset) (*Publisher, error) {
	if cfg.Bucket == "" || cfg.Endpoint == "" {
		return nil, errors.New("s3publisher: bucket + endpoint required")
	}
	if !strings.HasSuffix(cfg.BucketPrefix, "/") {
		cfg.BucketPrefix += "/"
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Second
	}
	cli, err := s3transport.NewClient(ctx, s3transport.ClientConfig{
		Bucket:    cfg.Bucket,
		Endpoint:  cfg.Endpoint,
		Region:    cfg.Region,
		UseSSL:    cfg.UseSSL,
		AccessKey: cfg.AccessKey,
		SecretKey: cfg.SecretKey,
	})
	if err != nil {
		return nil, err
	}
	return &Publisher{
		client:     cli,
		prefix:     cfg.BucketPrefix,
		introspect: fn,
		assets:     assets,
		interval:   cfg.Interval,
	}, nil
}

// Run blocks publishing until ctx is cancelled. First write happens
// immediately so the parent's first poll can find a fresh manifest
// without waiting a full Interval.
func (p *Publisher) Run(ctx context.Context) {
	t := time.NewTicker(p.interval)
	defer t.Stop()
	p.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.tick(ctx)
		}
	}
}

// LastError surfaces the most recent publish failure for ops surfaces
// (e.g. the CP's /api/about endpoint can include "federation S3:
// last_error=…"). Empty when the last tick succeeded.
func (p *Publisher) LastError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

// LastPublishedAt is the wall-clock of the most recent successful
// write. Useful for the same /api/about surface as LastError.
func (p *Publisher) LastPublishedAt() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.last
}

// tick performs one publish cycle: introspect → bucket, plus any
// extra Assets. Errors are logged + cached on the Publisher; we
// don't propagate because the loop must keep running across
// transient bucket outages. A failure on one asset doesn't abort
// the rest of the tick — the introspect snapshot is the most
// important and is published first.
func (p *Publisher) tick(ctx context.Context) {
	body, err := p.introspect(ctx)
	if err != nil {
		p.recordErr("introspect: " + err.Error())
		return
	}
	// introspect.json — full snapshot, overwritten each tick.
	if err := p.client.Put(ctx, p.prefix+"introspect.json", body, "application/json"); err != nil {
		p.recordErr("put introspect.json: " + err.Error())
		return
	}
	// heartbeat.json — small object the parent uses for freshness
	// without re-parsing introspect.json.
	hb, _ := json.Marshal(struct {
		TS time.Time `json:"ts"`
	}{TS: time.Now().UTC()})
	if err := p.client.Put(ctx, p.prefix+"heartbeat.json", hb, "application/json"); err != nil {
		p.recordErr("put heartbeat.json: " + err.Error())
		return
	}
	// Phase A.2 extra assets — findings.json today, daimons/nodes
	// follow in A.3. Each asset can fail independently; we log but
	// don't abort the tick (the introspect that gates HealthyPeers
	// is already on bucket).
	for _, a := range p.assets {
		body, err := a.Render(ctx)
		if err != nil {
			log.Printf("s3publisher %s: render %s: %v", p.prefix, a.Path, err)
			continue
		}
		ct := a.ContentType
		if ct == "" {
			ct = "application/json"
		}
		if err := p.client.Put(ctx, p.prefix+a.Path, body, ct); err != nil {
			log.Printf("s3publisher %s: put %s: %v", p.prefix, a.Path, err)
		}
	}
	p.recordOK()
}

func (p *Publisher) recordOK() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.last = time.Now().UTC()
	p.err = nil
}

func (p *Publisher) recordErr(msg string) {
	log.Printf("s3publisher %s: %s", p.prefix, msg)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.err = errors.New(msg)
}

// nopReader / bytesReader — minor adapter so the s3transport.Put
// signature (which takes []byte) lines up with our internal flow
// without needing a separate buffer copy. Currently unused but kept
// for the Phase A.2 streaming-findings path.
var _ = bytes.NewReader
