package tunnel

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Conn represents a live tunnel connection from one Node.
// It is safe for concurrent calls.
type Conn struct {
	NodeName    string
	ConnectedAt time.Time

	// send pushes a frame to the node. Returns an error if the connection
	// is closed.
	send func(*Frame) error

	mu        sync.Mutex
	runs      map[string]*runSubscriber          // active runs we're forwarding output for
	probes    map[string]chan *ProbeReplyPayload // pending metadata probes by ProbeID
	closed    bool
	closeOnce sync.Once
}

// runSubscriber receives lines + an exit signal for a single in-flight run.
type runSubscriber struct {
	lines chan LinePayload
	exit  chan ExitPayload
}

// SendRun asks the node to start a run and returns channels that receive
// the streaming output. The caller MUST drain `exit` and call Cleanup when done
// to release the run from the registry.
func (c *Conn) SendRun(p RunPayload) (lines <-chan LinePayload, exit <-chan ExitPayload, cleanup func(), err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, nil, nil, errors.New("tunnel closed")
	}
	if c.runs == nil {
		c.runs = make(map[string]*runSubscriber)
	}
	if _, exists := c.runs[p.RunID]; exists {
		c.mu.Unlock()
		return nil, nil, nil, fmt.Errorf("run %q already active", p.RunID)
	}
	sub := &runSubscriber{
		lines: make(chan LinePayload, 64),
		exit:  make(chan ExitPayload, 1),
	}
	c.runs[p.RunID] = sub
	c.mu.Unlock()

	cleanup = func() {
		c.mu.Lock()
		if existing, ok := c.runs[p.RunID]; ok && existing == sub {
			delete(c.runs, p.RunID)
			// Close channels if nothing else will. Use safety helpers
			// below so double-close from server side is safe.
			safeClose(sub.lines, sub.exit)
		}
		c.mu.Unlock()
	}

	if err := c.send(&Frame{Type: MsgRun, Run: &p}); err != nil {
		cleanup()
		return nil, nil, nil, fmt.Errorf("send run: %w", err)
	}
	return sub.lines, sub.exit, cleanup, nil
}

// Cancel asks the node to stop a run.
func (c *Conn) Cancel(runID string) error {
	return c.send(&Frame{Type: MsgCancel, Cancel: &CancelPayload{RunID: runID}})
}

// SendProbe asks the node for fresh metadata and waits up to `timeout` for
// a reply. Used by the "Refresh metadata" action on the Nodes page —
// avoids the SSH-credential roundtrip and is sub-second on a connected
// node. Returns ctx.Err() if the context fires first, ErrProbeTimeout
// otherwise.
func (c *Conn) SendProbe(ctx context.Context, timeout time.Duration) (*ProbeReplyPayload, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("tunnel closed")
	}
	if c.probes == nil {
		c.probes = make(map[string]chan *ProbeReplyPayload)
	}
	id := newProbeID()
	ch := make(chan *ProbeReplyPayload, 1)
	c.probes[id] = ch
	c.mu.Unlock()

	cleanup := func() {
		c.mu.Lock()
		delete(c.probes, id)
		c.mu.Unlock()
	}
	defer cleanup()

	if err := c.send(&Frame{Type: MsgProbe, Probe: &ProbePayload{ProbeID: id}}); err != nil {
		return nil, fmt.Errorf("send probe: %w", err)
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	select {
	case reply := <-ch:
		return reply, nil
	case <-time.After(timeout):
		return nil, ErrProbeTimeout
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ErrProbeTimeout is returned by SendProbe when the node didn't reply in time.
var ErrProbeTimeout = errors.New("probe timed out")

func newProbeID() string {
	// Tiny ID — collision-resistant within a short-lived in-flight set.
	return fmt.Sprintf("p-%d", time.Now().UnixNano())
}

// Ping sends a keepalive.
func (c *Conn) Ping() error {
	return c.send(&Frame{Type: MsgPing})
}

// dispatch forwards an incoming frame to whichever run is interested.
// Used by the WS read loop.
func (c *Conn) dispatch(f *Frame) {
	switch f.Type {
	case MsgLine:
		if f.Line == nil {
			return
		}
		c.mu.Lock()
		sub := c.runs[f.Line.RunID]
		c.mu.Unlock()
		if sub != nil {
			select {
			case sub.lines <- *f.Line:
			default:
				// drop on slow consumer
			}
		}
	case MsgExit:
		if f.Exit == nil {
			return
		}
		c.mu.Lock()
		sub := c.runs[f.Exit.RunID]
		c.mu.Unlock()
		if sub != nil {
			select {
			case sub.exit <- *f.Exit:
			default:
			}
			safeClose(sub.lines, sub.exit)
		}
	case MsgProbeReply:
		if f.ProbeReply == nil {
			return
		}
		c.mu.Lock()
		ch, ok := c.probes[f.ProbeReply.ProbeID]
		if ok {
			delete(c.probes, f.ProbeReply.ProbeID)
		}
		c.mu.Unlock()
		if ok {
			select {
			case ch <- f.ProbeReply:
			default:
			}
		}
	}
}

// Close marks the connection closed and aborts all in-flight runs with an
// "exit code -1, tunnel closed" event so consumers don't hang.
func (c *Conn) Close() {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		runs := c.runs
		c.runs = nil
		c.mu.Unlock()
		for id, sub := range runs {
			select {
			case sub.exit <- ExitPayload{RunID: id, Code: -1, Error: "tunnel closed"}:
			default:
			}
			safeClose(sub.lines, sub.exit)
		}
	})
}

// safeClose closes channels exactly once, swallowing the panic on second close.
func safeClose(lines chan LinePayload, exit chan ExitPayload) {
	defer func() { _ = recover() }()
	close(lines)
	close(exit)
}

// Registry tracks live tunnel connections by node name.
//
// Only ONE connection per node at a time. A second connect for the same name
// closes the prior connection (typical when the node reconnects after a
// transient failure).
type Registry struct {
	mu    sync.RWMutex
	conns map[string]*Conn
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{conns: map[string]*Conn{}}
}

// Add registers a new connection, evicting any prior connection for the
// same node. Returns the new Conn.
func (r *Registry) Add(nodeName string, send func(*Frame) error) *Conn {
	c := &Conn{
		NodeName:    nodeName,
		ConnectedAt: time.Now().UTC(),
		send:        send,
		runs:        map[string]*runSubscriber{},
	}
	r.mu.Lock()
	if prior, ok := r.conns[nodeName]; ok {
		go prior.Close()
	}
	r.conns[nodeName] = c
	r.mu.Unlock()
	return c
}

// Remove drops a connection from the registry, but only if it's still the
// active one for that node (prevents removing a fresh reconnect).
func (r *Registry) Remove(c *Conn) {
	r.mu.Lock()
	if existing, ok := r.conns[c.NodeName]; ok && existing == c {
		delete(r.conns, c.NodeName)
	}
	r.mu.Unlock()
	c.Close()
}

// Get returns the active connection for nodeName, or nil.
func (r *Registry) Get(nodeName string) *Conn {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.conns[nodeName]
}

// Names returns the names of currently connected nodes.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.conns))
	for name := range r.conns {
		out = append(out, name)
	}
	return out
}

// keepalive periodically pings every connection. Run in a background goroutine.
func (r *Registry) Keepalive(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.mu.RLock()
			conns := make([]*Conn, 0, len(r.conns))
			for _, c := range r.conns {
				conns = append(conns, c)
			}
			r.mu.RUnlock()
			for _, c := range conns {
				_ = c.Ping()
			}
		}
	}
}
