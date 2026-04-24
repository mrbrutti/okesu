package agent

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
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
//   - Exponential backoff retry (up to maxRetries attempts)
//   - In-memory ring buffer so callers are never blocked by network latency
type WebhookSink struct {
	url        string
	secret     string
	maxRetries int
	buf        *MemoryBuffer
	queue      chan []byte
	client     *http.Client
	wg         sync.WaitGroup
	once       sync.Once
	stopCh     chan struct{}
}

// NewWebhookSink starts the background delivery goroutine.
func NewWebhookSink(url, secret string, maxRetries, bufCap int) *WebhookSink {
	if maxRetries <= 0 {
		maxRetries = 3
	}
	s := &WebhookSink{
		url:        url,
		secret:     secret,
		maxRetries: maxRetries,
		buf:        NewMemoryBuffer(bufCap),
		queue:      make(chan []byte, 512),
		client:     &http.Client{Timeout: 10 * time.Second},
		stopCh:     make(chan struct{}),
	}
	s.wg.Add(1)
	go s.deliver()
	return s
}

func (s *WebhookSink) Write(line []byte) error {
	cp := make([]byte, len(line))
	copy(cp, line)
	s.buf.Push(cp)
	select {
	case s.queue <- cp:
	default:
		// Queue full — event dropped from delivery (still in ring buffer).
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
// Call before the first Emit; not safe to call concurrently with Emit.
func SetGlobalSink(s Sink) {
	globalSinkMu.Lock()
	globalSink = s
	globalSinkMu.Unlock()
}

// emitToSink marshals e and writes it to the active global sink.
// It replaces the direct fmt.Printf in Emit when sinks are configured.
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

// BuildSinks constructs a FanoutSink (or plain StdoutSink) from OutputDef slice.
// Always includes stdout unless the list is non-empty and no stdout entry is present.
func BuildSinks(outputs []OutputDef) (Sink, error) {
	if len(outputs) == 0 {
		return &StdoutSink{}, nil
	}

	var sinks []Sink
	hasStdout := false
	for _, o := range outputs {
		switch o.Type {
		case "stdout":
			hasStdout = true
			sinks = append(sinks, &StdoutSink{})
		case "file":
			fs, err := NewJSONLFileSink(o.Path, o.MaxBytes)
			if err != nil {
				return nil, fmt.Errorf("file sink %q: %w", o.Path, err)
			}
			sinks = append(sinks, fs)
		case "webhook":
			sinks = append(sinks, NewWebhookSink(o.URL, o.Secret, o.Retries, o.BufferCap))
		default:
			return nil, fmt.Errorf("unknown sink type %q", o.Type)
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
