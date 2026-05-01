package feeds

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/section9labs/okesu/controlplane/db"
)

// Refresher is implemented by the worker that knows how to fetch +
// parse + reconcile a single feed. Defined as an interface so the
// scheduler is unit-testable without spinning up real HTTP / git.
type Refresher interface {
	RefreshOne(ctx context.Context, fc *db.FeedConfig) error
}

// Scheduler periodically scans ioc_feeds and dispatches refreshes for
// any enabled feed whose interval has elapsed. Manual "Refresh now"
// requests jump the queue. The scheduler is a singleton in the CP —
// server.go calls Run once on a background goroutine.
type Scheduler struct {
	store    *db.Store
	worker   Refresher
	tick     time.Duration
	manualCh chan int64
}

// NewScheduler constructs a Scheduler. tick is the loop interval —
// production uses time.Minute; tests use 50ms.
func NewScheduler(store *db.Store, worker Refresher, tick time.Duration) *Scheduler {
	return &Scheduler{
		store:    store,
		worker:   worker,
		tick:     tick,
		manualCh: make(chan int64, 16),
	}
}

// RefreshNow enqueues a manual refresh for one feed. Returns an error
// when consent has not been granted (the scheduler also checks this
// in its loop, but the API call needs to fail loud immediately when
// consent is missing).
func (s *Scheduler) RefreshNow(feedID int64) error {
	at, err := s.store.GetFeedsConsentGrantedAt()
	if err != nil {
		return err
	}
	if at == nil {
		return errors.New("feeds consent not granted")
	}
	select {
	case s.manualCh <- feedID:
		return nil
	default:
		return errors.New("manual refresh queue full")
	}
}

// Run drives the scheduler until ctx is cancelled. Safe to call once
// per CP. Blocking — caller is expected to invoke from a goroutine.
func (s *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(s.tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-s.manualCh:
			s.refresh(ctx, id)
		case <-t.C:
			s.dispatchDue(ctx)
		}
	}
}

func (s *Scheduler) dispatchDue(ctx context.Context) {
	at, err := s.store.GetFeedsConsentGrantedAt()
	if err != nil || at == nil {
		// Refuse silently; the API/UI surfaces "consent missing" elsewhere.
		return
	}
	feeds, err := s.store.ListFeedConfigs()
	if err != nil {
		return
	}
	now := time.Now().UTC()
	for _, fc := range feeds {
		if !fc.Enabled {
			continue
		}
		due := time.Time{}
		if fc.LastRefreshAt.Valid {
			due = fc.LastRefreshAt.Time.Add(time.Duration(fc.RefreshIntervalSeconds) * time.Second)
		}
		if !fc.LastRefreshAt.Valid || !now.Before(due) {
			s.refresh(ctx, fc.ID)
		}
	}
}

func (s *Scheduler) refresh(ctx context.Context, id int64) {
	fc, err := s.store.GetFeedConfig(id)
	if err != nil {
		log.Printf("feeds.scheduler: get %d: %v", id, err)
		return
	}
	if !fc.Enabled {
		return
	}
	if err := s.worker.RefreshOne(ctx, fc); err != nil {
		log.Printf("feeds.scheduler: refresh %s: %v", fc.Slug, err)
	}
}
