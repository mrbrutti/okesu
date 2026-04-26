// Package jobs provides an in-memory registry for long-running deploy jobs.
//
// Jobs are ephemeral: they live until the CP restarts. Each job has a bounded
// log buffer (recent N lines) and a per-job channel for live tailing via SSE.
package jobs

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// Status enumerates the lifecycle of a job.
type Status string

const (
	StatusRunning Status = "running"
	StatusOk      Status = "succeeded"
	StatusFailed  Status = "failed"
)

// Job is one tracked deploy.
type Job struct {
	ID         string
	NodeID     int64
	Type       string // "deploy"
	Status     Status
	Error      string
	StartedAt  time.Time
	FinishedAt time.Time

	mu       sync.Mutex
	lines    []string         // bounded ring of log lines
	subs     map[chan string]struct{}
	finished chan struct{}    // closed when job ends
}

// Registry is a process-wide store of in-flight and recently completed jobs.
type Registry struct {
	mu     sync.RWMutex
	jobs   map[string]*Job
	maxLog int
}

// New constructs an empty Registry. logBuffer is the per-job line cap.
func New(logBuffer int) *Registry {
	if logBuffer <= 0 {
		logBuffer = 500
	}
	return &Registry{
		jobs:   make(map[string]*Job),
		maxLog: logBuffer,
	}
}

// Create registers a new running job and returns it.
func (r *Registry) Create(jobType string, nodeID int64) *Job {
	id, _ := randomID()
	j := &Job{
		ID:        id,
		NodeID:    nodeID,
		Type:      jobType,
		Status:    StatusRunning,
		StartedAt: time.Now().UTC(),
		subs:      map[chan string]struct{}{},
		finished:  make(chan struct{}),
	}
	r.mu.Lock()
	r.jobs[id] = j
	r.mu.Unlock()
	return j
}

// Get returns a job by ID, or nil.
func (r *Registry) Get(id string) *Job {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.jobs[id]
}

// ListByNode returns recent jobs for a node, newest first.
func (r *Registry) ListByNode(nodeID int64) []*Job {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Job, 0)
	for _, j := range r.jobs {
		if j.NodeID == nodeID {
			out = append(out, j)
		}
	}
	// Sort newest first.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].StartedAt.Before(out[j].StartedAt); j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// Append adds a log line to the job and fans out to subscribers.
// Lines past maxLog are dropped from the ring head.
func (j *Job) Append(line string) {
	j.mu.Lock()
	if len(j.lines) >= 500 {
		j.lines = j.lines[len(j.lines)-499:]
	}
	j.lines = append(j.lines, line)
	subs := make([]chan string, 0, len(j.subs))
	for ch := range j.subs {
		subs = append(subs, ch)
	}
	j.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- line:
		default:
			// drop
		}
	}
}

// Lines returns a snapshot copy of the log ring.
func (j *Job) Lines() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]string, len(j.lines))
	copy(out, j.lines)
	return out
}

// Subscribe registers a channel that receives subsequent log lines until the
// returned cancel func is called.
func (j *Job) Subscribe() (<-chan string, func()) {
	ch := make(chan string, 64)
	j.mu.Lock()
	j.subs[ch] = struct{}{}
	j.mu.Unlock()
	return ch, func() {
		j.mu.Lock()
		if _, ok := j.subs[ch]; ok {
			delete(j.subs, ch)
			close(ch)
		}
		j.mu.Unlock()
	}
}

// Finished returns a channel that's closed when the job ends.
func (j *Job) Finished() <-chan struct{} { return j.finished }

// Complete marks the job done. Pass err == nil for success.
func (j *Job) Complete(err error) {
	j.mu.Lock()
	if err != nil {
		j.Status = StatusFailed
		j.Error = err.Error()
	} else {
		j.Status = StatusOk
	}
	j.FinishedAt = time.Now().UTC()

	// Close all subscriber channels.
	for ch := range j.subs {
		close(ch)
	}
	j.subs = nil
	close(j.finished)
	j.mu.Unlock()
}

func randomID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
