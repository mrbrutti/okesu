package agent

import (
	"testing"
	"time"
)

func TestAdaptiveSchedule_BacksOffOnIdle(t *testing.T) {
	cfg := DaemonConfig{IntervalMin: 30 * time.Second, IntervalMax: 600 * time.Second}
	sched := newAdaptiveSchedule(cfg)
	sched.recordTick(false /* findings */, false /* err */)
	sched.recordTick(false, false)
	sched.recordTick(false, false)
	if sched.current <= cfg.IntervalMin {
		t.Errorf("expected interval to grow; got %v", sched.current)
	}
}

func TestAdaptiveSchedule_ResetsOnFinding(t *testing.T) {
	cfg := DaemonConfig{IntervalMin: 30 * time.Second, IntervalMax: 600 * time.Second}
	sched := newAdaptiveSchedule(cfg)
	for i := 0; i < 5; i++ {
		sched.recordTick(false, false)
	}
	prev := sched.current
	sched.recordTick(true, false)
	if sched.current >= prev {
		t.Errorf("expected reset on finding; was %v, now %v", prev, sched.current)
	}
	if sched.current != cfg.IntervalMin {
		t.Errorf("expected reset to IntervalMin; got %v", sched.current)
	}
}

func TestAdaptiveSchedule_CapsAtMax(t *testing.T) {
	cfg := DaemonConfig{IntervalMin: 30 * time.Second, IntervalMax: 90 * time.Second}
	sched := newAdaptiveSchedule(cfg)
	for i := 0; i < 100; i++ {
		sched.recordTick(false, false)
	}
	if sched.current > cfg.IntervalMax {
		t.Errorf("interval %v exceeds max %v", sched.current, cfg.IntervalMax)
	}
}

func TestAdaptiveSchedule_ResetsOnError(t *testing.T) {
	cfg := DaemonConfig{IntervalMin: 30 * time.Second, IntervalMax: 600 * time.Second}
	sched := newAdaptiveSchedule(cfg)
	for i := 0; i < 5; i++ {
		sched.recordTick(false, false)
	}
	prev := sched.current
	sched.recordTick(false, true)
	if sched.current >= prev {
		t.Errorf("expected reset on error; was %v, now %v", prev, sched.current)
	}
	if sched.current != cfg.IntervalMin {
		t.Errorf("expected reset to IntervalMin on error; got %v", sched.current)
	}
}

func TestAdaptiveSchedule_FixedIntervalCompat(t *testing.T) {
	// When IntervalMin == IntervalMax, behavior is identical to a fixed interval.
	cfg := DaemonConfig{IntervalMin: 60 * time.Second, IntervalMax: 60 * time.Second}
	sched := newAdaptiveSchedule(cfg)
	for i := 0; i < 10; i++ {
		sched.recordTick(false, false)
		if sched.current != 60*time.Second {
			t.Fatalf("fixed-interval drifted: got %v after tick %d", sched.current, i)
		}
	}
}

func TestAdaptiveSchedule_MaxBelowMinNormalizesToFixed(t *testing.T) {
	// max < min is operator typo territory. Constructor should normalize
	// max=min and behave as a fixed-interval timer rather than growing
	// past the (lower) max into invalid territory.
	cfg := DaemonConfig{IntervalMin: 60 * time.Second, IntervalMax: 30 * time.Second}
	sched := newAdaptiveSchedule(cfg)
	if sched.max != 60*time.Second {
		t.Fatalf("expected max normalized to min (60s); got %v", sched.max)
	}
	for range 10 {
		sched.recordTick(false, false)
	}
	if sched.current != 60*time.Second {
		t.Errorf("expected fixed-interval behavior at min; got %v", sched.current)
	}
}
