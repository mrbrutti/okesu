package feeds

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/section9labs/okesu/controlplane/db"
)

type fakeRefresher struct {
	count int32
	ids   []int64
}

func (f *fakeRefresher) RefreshOne(ctx context.Context, fc *db.FeedConfig) error {
	atomic.AddInt32(&f.count, 1)
	f.ids = append(f.ids, fc.ID)
	return nil
}

func TestScheduler_RefreshNowJumpsQueue(t *testing.T) {
	st := newTestStore(t)
	if err := st.SetFeedsConsentGrantedAt(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	id, err := st.InsertFeedConfig(&db.FeedConfigInsert{
		Slug: "f1", Name: "F1", Kind: "single_file",
		URL: "https://x", Parser: "yara",
		RefreshIntervalSeconds: 86400, Enabled: true,
	})
	if err != nil { t.Fatal(err) }

	r := &fakeRefresher{}
	sch := NewScheduler(st, r, 50*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sch.Run(ctx)

	if err := sch.RefreshNow(id); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&r.count) >= 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected at least one refresh, got %d", atomic.LoadInt32(&r.count))
}

func TestScheduler_RefusesWhenConsentMissing(t *testing.T) {
	st := newTestStore(t)
	id, err := st.InsertFeedConfig(&db.FeedConfigInsert{
		Slug: "f1", Name: "F1", Kind: "single_file",
		URL: "https://x", Parser: "yara",
		RefreshIntervalSeconds: 86400, Enabled: true,
	})
	if err != nil { t.Fatal(err) }
	r := &fakeRefresher{}
	sch := NewScheduler(st, r, 50*time.Millisecond)
	if err := sch.RefreshNow(id); err == nil {
		t.Fatal("expected error when consent not granted")
	}
}

func TestScheduler_TickRespectsConsentGate(t *testing.T) {
	// A scheduler with no consent should never call RefreshOne even
	// when there's an enabled, never-refreshed feed. Run for ~150ms
	// and confirm count stays 0.
	st := newTestStore(t)
	_, err := st.InsertFeedConfig(&db.FeedConfigInsert{
		Slug: "f1", Name: "F1", Kind: "single_file",
		URL: "https://x", Parser: "yara",
		RefreshIntervalSeconds: 86400, Enabled: true,
	})
	if err != nil { t.Fatal(err) }
	r := &fakeRefresher{}
	sch := NewScheduler(st, r, 50*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	sch.Run(ctx) // blocks until ctx expires
	if atomic.LoadInt32(&r.count) != 0 {
		t.Fatalf("expected 0 refreshes when consent missing, got %d", r.count)
	}
}

func TestScheduler_TickDispatchesDueFeeds(t *testing.T) {
	st := newTestStore(t)
	if err := st.SetFeedsConsentGrantedAt(time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	// Two feeds: one enabled, never refreshed; one disabled.
	enabledID, _ := st.InsertFeedConfig(&db.FeedConfigInsert{
		Slug: "enabled", Name: "Enabled", Kind: "single_file",
		URL: "https://x", Parser: "yara",
		RefreshIntervalSeconds: 86400, Enabled: true,
	})
	_, _ = st.InsertFeedConfig(&db.FeedConfigInsert{
		Slug: "disabled", Name: "Disabled", Kind: "single_file",
		URL: "https://x", Parser: "yara",
		RefreshIntervalSeconds: 86400, Enabled: false,
	})

	r := &fakeRefresher{}
	sch := NewScheduler(st, r, 50*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	sch.Run(ctx) // blocks until ctx expires

	// At least one refresh of `enabled`; never `disabled`.
	if atomic.LoadInt32(&r.count) < 1 {
		t.Fatalf("expected ≥1 enabled refresh, got %d", r.count)
	}
	for _, id := range r.ids {
		if id != enabledID {
			t.Fatalf("disabled feed should not be refreshed; got %d", id)
		}
	}
}
