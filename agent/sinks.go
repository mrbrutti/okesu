package agent

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

// Sink is the interface implemented by all output destinations.
// Write receives a fully-marshalled JSONL line (no trailing newline).
// Close flushes and releases resources; safe to call more than once.
type Sink interface {
	Write(line []byte) error
	Close() error
}

// FilteredSink wraps any Sink and only forwards events whose type is in the
// allowed set. An empty allowed set means all events are forwarded.
type FilteredSink struct {
	inner   Sink
	allowed map[EventType]struct{}
}

// NewFilteredSink wraps inner with an event-type filter. eventTypes is the
// allow-list; pass nil or empty to forward everything.
func NewFilteredSink(inner Sink, eventTypes []string) *FilteredSink {
	if len(eventTypes) == 0 {
		return &FilteredSink{inner: inner} // nil map = pass all
	}
	m := make(map[EventType]struct{}, len(eventTypes))
	for _, t := range eventTypes {
		m[EventType(t)] = struct{}{}
	}
	return &FilteredSink{inner: inner, allowed: m}
}

func (f *FilteredSink) Write(line []byte) error {
	if len(f.allowed) == 0 {
		return f.inner.Write(line)
	}
	var ev struct {
		Type EventType `json:"type"`
	}
	if err := json.Unmarshal(line, &ev); err != nil {
		// Unparseable — pass through to avoid silent drops.
		return f.inner.Write(line)
	}
	if _, ok := f.allowed[ev.Type]; !ok {
		return nil // filtered out
	}
	return f.inner.Write(line)
}

func (f *FilteredSink) Close() error { return f.inner.Close() }

// StdoutSink writes JSONL events to os.Stdout with a mutex.
type StdoutSink struct {
	mu sync.Mutex
}

func (s *StdoutSink) Write(line []byte) error {
	s.mu.Lock()
	_, err := fmt.Printf("%s\n", line)
	s.mu.Unlock()
	return err
}

func (s *StdoutSink) Close() error { return nil }

// JSONLFileSink appends JSONL events to a rotating file via FileBuffer.
type JSONLFileSink struct {
	fb *FileBuffer
}

// NewJSONLFileSink creates a file sink. maxBytes=0 disables rotation.
func NewJSONLFileSink(path string, maxBytes int64) (*JSONLFileSink, error) {
	fb, err := NewFileBuffer(path, maxBytes)
	if err != nil {
		return nil, err
	}
	return &JSONLFileSink{fb: fb}, nil
}

func (s *JSONLFileSink) Write(line []byte) error { return s.fb.Write(line) }
func (s *JSONLFileSink) Close() error            { return s.fb.Close() }

// WebhookSink delivers JSONL events to an HTTP endpoint with:
//   - HMAC-SHA256 signing (X-Okesu-Signature header) when secret != ""
//   - Identity headers: X-Okesu-Agent, X-Okesu-Host, X-Okesu-Timestamp
//   - Exponential backoff retry (up to maxRetries attempts)
//   - In-memory ring buffer so callers are never blocked by network latency
type WebhookSink struct {
	url        string
	secret     string
	agentName  string
	host       string
	maxRetries int
	buf        *MemoryBuffer
	queue      chan []byte
	client     *http.Client
	wg         sync.WaitGroup
	once       sync.Once
	stopCh     chan struct{}
}

// WebhookTLSOptions tunes the TLS verification behavior of a webhook sink.
type WebhookTLSOptions struct {
	// CACertPEM is an additional CA bundle (PEM-encoded) to trust for the
	// webhook URL. Empty means "system roots only".
	CACertPEM []byte
	// InsecureSkipVerify disables TLS verification entirely. Dev-only.
	InsecureSkipVerify bool
}

// NewWebhookSink starts the background delivery goroutine.
// agentName and host are included in every request as identity headers.
func NewWebhookSink(url, secret, agentName, host string, maxRetries, bufCap int) *WebhookSink {
	return NewWebhookSinkWithTLS(url, secret, agentName, host, maxRetries, bufCap, WebhookTLSOptions{})
}

// NewWebhookSinkWithTLS is NewWebhookSink with explicit TLS configuration.
func NewWebhookSinkWithTLS(url, secret, agentName, host string, maxRetries, bufCap int, opts WebhookTLSOptions) *WebhookSink {
	if maxRetries <= 0 {
		maxRetries = 3
	}
	tr := &http.Transport{
		TLSClientConfig: buildWebhookTLSConfig(opts),
	}
	s := &WebhookSink{
		url:        url,
		secret:     secret,
		agentName:  agentName,
		host:       host,
		maxRetries: maxRetries,
		buf:        NewMemoryBuffer(bufCap),
		queue:      make(chan []byte, 512),
		client:     &http.Client{Timeout: 10 * time.Second, Transport: tr},
		stopCh:     make(chan struct{}),
	}
	s.wg.Add(1)
	go s.deliver()
	return s
}

func buildWebhookTLSConfig(opts WebhookTLSOptions) *tls.Config {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if opts.InsecureSkipVerify {
		cfg.InsecureSkipVerify = true //nolint:gosec — explicitly opted-in by config
	}
	if len(opts.CACertPEM) > 0 {
		pool := x509.NewCertPool()
		if pool.AppendCertsFromPEM(opts.CACertPEM) {
			cfg.RootCAs = pool
		}
	}
	return cfg
}

func (s *WebhookSink) Write(line []byte) error {
	cp := make([]byte, len(line))
	copy(cp, line)
	s.buf.Push(cp)
	select {
	case s.queue <- cp:
	default:
		// Queue full — event dropped from delivery (still in ring buffer for replay).
	}
	return nil
}

func (s *WebhookSink) Close() error {
	s.once.Do(func() { close(s.stopCh) })
	s.wg.Wait()
	return nil
}

func (s *WebhookSink) deliver() {
	defer s.wg.Done()
	for {
		select {
		case line := <-s.queue:
			s.send(line)
		case <-s.stopCh:
			// Drain remaining queued events before stopping.
			for {
				select {
				case line := <-s.queue:
					s.send(line)
				default:
					return
				}
			}
		}
	}
}

func (s *WebhookSink) send(line []byte) {
	for attempt := range s.maxRetries {
		if err := s.post(line); err == nil {
			return
		}
		if attempt < s.maxRetries-1 {
			time.Sleep(retryDelay(attempt))
		}
	}
}

func (s *WebhookSink) post(line []byte) error {
	req, err := http.NewRequest(http.MethodPost, s.url, bytes.NewReader(line))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	req.Header.Set("X-Okesu-Agent", s.agentName)
	req.Header.Set("X-Okesu-Host", s.host)
	req.Header.Set("X-Okesu-Timestamp", fmt.Sprintf("%d", time.Now().UnixMilli()))
	if s.secret != "" {
		mac := hmac.New(sha256.New, []byte(s.secret))
		mac.Write(line)
		req.Header.Set("X-Okesu-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("webhook %s: HTTP %d", s.url, resp.StatusCode)
	}
	return nil
}

// FanoutSink writes each event to all registered sinks in parallel.
// Write returns the first non-nil error but does not short-circuit — all
// sinks always receive every event.
type FanoutSink struct {
	sinks []Sink
}

// NewFanoutSink wraps the given sinks.
func NewFanoutSink(sinks ...Sink) *FanoutSink { return &FanoutSink{sinks: sinks} }

func (f *FanoutSink) Write(line []byte) error {
	var wg sync.WaitGroup
	errs := make([]error, len(f.sinks))
	for i, s := range f.sinks {
		wg.Add(1)
		go func(idx int, sk Sink) {
			defer wg.Done()
			errs[idx] = sk.Write(line)
		}(i, s)
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

func (f *FanoutSink) Close() error {
	var first error
	for _, s := range f.sinks {
		if err := s.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// -- Global sink registry --------------------------------------------------

var (
	globalSinkMu sync.RWMutex
	globalSink   Sink = &StdoutSink{} // default: stdout only
)

// SetGlobalSink replaces the active output sink.
func SetGlobalSink(s Sink) {
	globalSinkMu.Lock()
	globalSink = s
	globalSinkMu.Unlock()
}

// emitToSink marshals e and writes it to the active global sink.
func emitToSink(e Event) {
	e.Ts = time.Now().UnixMilli()
	b, err := json.Marshal(e)
	if err != nil {
		fmt.Fprintf(os.Stderr, "jsonl marshal error: %v\n", err)
		return
	}
	globalSinkMu.RLock()
	sink := globalSink
	globalSinkMu.RUnlock()
	if err := sink.Write(b); err != nil {
		fmt.Fprintf(os.Stderr, "sink write error: %v\n", err)
	}
}

// BuildSinks constructs a FanoutSink (or simpler sink) from OutputDef slice.
// agentName and host are used to populate webhook identity headers.
// Always includes stdout unless the list is non-empty and no stdout entry is
// present. Each sink is wrapped in FilteredSink if Events is non-empty.
func BuildSinks(outputs []OutputDef, agentName, host string) (Sink, error) {
	if len(outputs) == 0 {
		return &StdoutSink{}, nil
	}

	var sinks []Sink
	hasStdout := false
	for _, o := range outputs {
		var raw Sink
		switch o.Type {
		case "stdout":
			hasStdout = true
			raw = &StdoutSink{}
		case "file":
			fs, err := NewJSONLFileSink(o.Path, o.MaxBytes)
			if err != nil {
				return nil, fmt.Errorf("file sink %q: %w", o.Path, err)
			}
			raw = fs
		case "webhook":
			tlsOpts := WebhookTLSOptions{
				InsecureSkipVerify: o.InsecureSkipVerify,
			}
			if o.CACertFile != "" {
				if pem, err := os.ReadFile(o.CACertFile); err == nil {
					tlsOpts.CACertPEM = pem
				} else {
					return nil, fmt.Errorf("webhook ca cert %q: %w", o.CACertFile, err)
				}
			}
			raw = NewWebhookSinkWithTLS(o.URL, o.Secret, agentName, host, o.Retries, o.BufferCap, tlsOpts)
		default:
			return nil, fmt.Errorf("unknown sink type %q", o.Type)
		}
		// Wrap with event filter if specified.
		if len(o.Events) > 0 {
			sinks = append(sinks, NewFilteredSink(raw, o.Events))
		} else {
			sinks = append(sinks, raw)
		}
	}
	if !hasStdout {
		sinks = append([]Sink{&StdoutSink{}}, sinks...)
	}
	if len(sinks) == 1 {
		return sinks[0], nil
	}
	return NewFanoutSink(sinks...), nil
}
