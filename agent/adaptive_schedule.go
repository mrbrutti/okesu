package agent

import "time"

// adaptiveSchedule grows the inter-tick delay geometrically on quiet
// ticks and resets to IntervalMin on errors or new findings. Capped at
// IntervalMax. Concurrency: tied to a single daemon goroutine; not
// independently goroutine-safe.
type adaptiveSchedule struct {
	min, max time.Duration
	current  time.Duration
	growth   float64
}

// newAdaptiveSchedule returns a scheduler configured from cfg. When
// IntervalMin and IntervalMax are equal (or only IntervalMin is set),
// the scheduler degenerates to a fixed-interval timer — every recordTick
// leaves `current` at min.
func newAdaptiveSchedule(cfg DaemonConfig) *adaptiveSchedule {
	min := cfg.IntervalMin
	max := cfg.IntervalMax
	if min <= 0 {
		min = 60 * time.Second
	}
	if max <= 0 || max < min {
		max = min
	}
	return &adaptiveSchedule{min: min, max: max, current: min, growth: 1.5}
}

// recordTick updates the current interval based on the outcome of the
// just-completed tick. New findings or errors snap back to min; quiet
// ticks grow the delay geometrically (×1.5) up to max.
func (s *adaptiveSchedule) recordTick(producedFinding, hadError bool) {
	if producedFinding || hadError {
		s.current = s.min
		return
	}
	next := time.Duration(float64(s.current) * s.growth)
	if next > s.max {
		next = s.max
	}
	if next < s.min {
		next = s.min
	}
	s.current = next
}

// next returns the time of the next tick, given the current time.
func (s *adaptiveSchedule) next(t time.Time) time.Time {
	return t.Add(s.current)
}

// atCeiling reports whether the scheduler has reached its IntervalMax.
// Used by the daemon loop to log a one-line marker so operators can see
// when an agent has gone fully idle.
func (s *adaptiveSchedule) atCeiling() bool {
	return s.max > s.min && s.current >= s.max
}

// restore seeds `current` from a persisted DaemonState value, clamping
// to the configured [min, max] range. Zero (the default for a fresh
// state file) means "use min".
func (s *adaptiveSchedule) restore(persisted time.Duration) {
	if persisted <= 0 {
		s.current = s.min
		return
	}
	if persisted < s.min {
		s.current = s.min
		return
	}
	if persisted > s.max {
		s.current = s.max
		return
	}
	s.current = persisted
}
