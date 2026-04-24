package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// MemoryBuffer is a thread-safe ring buffer that holds the N most recent
// raw JSONL lines. Used by the webhook sink to replay events after reconnect.
type MemoryBuffer struct {
	mu   sync.Mutex
	buf  [][]byte
	head int
	size int
	cap  int
}

// NewMemoryBuffer creates a ring buffer with the given capacity.
func NewMemoryBuffer(capacity int) *MemoryBuffer {
	if capacity <= 0 {
		capacity = 256
	}
	return &MemoryBuffer{buf: make([][]byte, capacity), cap: capacity}
}

// Push appends a raw JSONL line, evicting the oldest entry when full.
func (m *MemoryBuffer) Push(line []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(line))
	copy(cp, line)
	m.buf[m.head] = cp
	m.head = (m.head + 1) % m.cap
	if m.size < m.cap {
		m.size++
	}
}

// Snapshot returns all buffered lines in insertion order.
func (m *MemoryBuffer) Snapshot() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]byte, m.size)
	start := (m.head - m.size + m.cap) % m.cap
	for i := range m.size {
		out[i] = m.buf[(start+i)%m.cap]
	}
	return out
}

// FileBuffer appends JSONL lines to a rotating log file.
// Rotation: when the file exceeds maxBytes it is renamed to <path>.1 and a
// new file is opened. Only one backup is kept.
type FileBuffer struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	f        *os.File
	written  int64
}

// NewFileBuffer opens (or creates) path for append writes.
// maxBytes controls rotation size; 0 means no rotation.
func NewFileBuffer(path string, maxBytes int64) (*FileBuffer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("filebuffer mkdir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("filebuffer open: %w", err)
	}
	fi, _ := f.Stat()
	var written int64
	if fi != nil {
		written = fi.Size()
	}
	return &FileBuffer{path: path, maxBytes: maxBytes, f: f, written: written}, nil
}

// Write appends line+"\n" and rotates if needed.
func (fb *FileBuffer) Write(line []byte) error {
	fb.mu.Lock()
	defer fb.mu.Unlock()

	if fb.maxBytes > 0 && fb.written+int64(len(line))+1 > fb.maxBytes {
		if err := fb.rotate(); err != nil {
			return err
		}
	}

	n, err := fmt.Fprintf(fb.f, "%s\n", line)
	fb.written += int64(n)
	return err
}

func (fb *FileBuffer) rotate() error {
	fb.f.Close()
	backup := fb.path + ".1"
	_ = os.Remove(backup)
	_ = os.Rename(fb.path, backup)
	f, err := os.OpenFile(fb.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("filebuffer rotate: %w", err)
	}
	fb.f = f
	fb.written = 0
	return nil
}

// Close flushes and closes the underlying file.
func (fb *FileBuffer) Close() error {
	fb.mu.Lock()
	defer fb.mu.Unlock()
	return fb.f.Close()
}

// OutputDef describes one output sink configured in an agent file.
type OutputDef struct {
	Type     string `yaml:"type"`               // "stdout" | "file" | "webhook"
	Path     string `yaml:"path,omitempty"`     // file sink: path to JSONL log
	MaxBytes int64  `yaml:"maxBytes,omitempty"` // file sink: rotation threshold
	URL      string `yaml:"url,omitempty"`      // webhook sink: endpoint URL
	Secret   string `yaml:"secret,omitempty"`   // webhook sink: HMAC-SHA256 signing secret
	Retries  int    `yaml:"retries,omitempty"`  // webhook sink: max delivery attempts
	BufferCap int   `yaml:"bufferCap,omitempty"` // webhook sink: in-memory ring buffer size
}

// retryDelay returns an exponential back-off delay for retry attempt n (0-indexed).
func retryDelay(n int) time.Duration {
	base := 500 * time.Millisecond
	for range n {
		base *= 2
	}
	if base > 30*time.Second {
		return 30 * time.Second
	}
	return base
}
