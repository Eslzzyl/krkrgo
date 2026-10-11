package runtime

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// TimerScale stores milliseconds in the 16.16 fixed-point representation used
// by TimerIntf. A fixed-point deadline makes fractional intervals deterministic
// and avoids host clock floating-point drift.
const TimerScale uint64 = 1 << 16

const (
	// MaxTimers bounds registered native timers. Timer callbacks are separately
	// bounded by EventQueue's pending-event limit and each timer's capacity.
	MaxTimers        = MaxPendingEvents
	maxTimerCatchup  = 40
	maxTimerCapacity = 65535
)

// Timer is a registered one-shot-at-a-time repeating timer. Its callback is
// represented by EventQueue entries; the headless runtime intentionally leaves
// callback invocation to the session dispatcher.
type Timer struct {
	id       uint64
	interval uint64 // 16.16 milliseconds
	next     uint64 // 16.16 deadline
	enabled  bool
	capacity int32
	mode     Priority
	owner    *TimerRegistry
}

func newTimer(id uint64, owner *TimerRegistry) *Timer {
	return &Timer{id: id, interval: 1000, capacity: 6, owner: owner}
}

// NewTimer creates an unregistered timer. Register it with a TimerRegistry to
// produce callback events. An omitted ID defaults to zero for callers that
// assign identity at registration time.
func NewTimer(ids ...uint64) *Timer {
	var id uint64
	if len(ids) != 0 {
		id = ids[0]
	}
	return newTimer(id, nil)
}

func (t *Timer) ID() uint64 {
	if t == nil {
		return 0
	}
	return t.id
}
func (t *Timer) Enabled() bool { return t != nil && t.enabled }
func (t *Timer) IntervalTicks() uint64 {
	if t == nil {
		return 0
	}
	return t.interval
}
func (t *Timer) NextDeadlineTicks() uint64 {
	if t == nil {
		return 0
	}
	return t.next
}
func (t *Timer) Capacity() int32 {
	if t == nil {
		return 0
	}
	return t.capacity
}
func (t *Timer) Mode() Priority {
	if t == nil {
		return PriorityNormal
	}
	return t.mode
}
func (t *Timer) Interval() float64 {
	if t == nil {
		return 0
	}
	return float64(t.interval) / float64(TimerScale)
}

// SetInterval sets a fixed-point interval from milliseconds. Fractional values
// are rounded to the nearest fixed-point unit, matching the native conversion.
// Changing an enabled timer cancels queued callbacks and starts a fresh period
// from the registry clock.
func (t *Timer) SetInterval(intervalMS float64) error {
	ticks, err := timerTicks(intervalMS)
	if err != nil {
		return err
	}
	if t == nil {
		return errors.New("nil timer")
	}
	if t.owner != nil {
		return t.owner.setInterval(t.id, ticks)
	}
	t.interval = ticks
	return nil
}

// SetIntervalTicks sets an interval already represented in 16.16 units.
func (t *Timer) SetIntervalTicks(ticks uint64) error {
	if t == nil {
		return errors.New("nil timer")
	}
	if t.owner != nil {
		return t.owner.setInterval(t.id, ticks)
	}
	t.interval = ticks
	return nil
}

func (t *Timer) SetEnabled(enabled bool) error {
	if t == nil {
		return errors.New("nil timer")
	}
	if t.owner != nil {
		return t.owner.setEnabled(t.id, enabled)
	}
	t.enabled = enabled
	return nil
}

func (t *Timer) SetCapacity(capacity int32) error {
	if t == nil {
		return errors.New("nil timer")
	}
	t.capacity = capacity
	return nil
}

func (t *Timer) SetMode(mode int) error {
	if t == nil {
		return errors.New("nil timer")
	}
	t.mode = normalizePriority(mode)
	return nil
}

func timerTicks(intervalMS float64) (uint64, error) {
	if math.IsNaN(intervalMS) || math.IsInf(intervalMS, 0) || intervalMS < 0 {
		return 0, errors.New("invalid timer interval")
	}
	scaled := intervalMS*float64(TimerScale) + 0.5
	if scaled >= float64(math.MaxUint64) {
		return 0, errors.New("Timer clock range exceeded")
	}
	return uint64(scaled), nil
}

func clockTicks(timeMS uint64) (uint64, error) {
	if timeMS > math.MaxUint64/TimerScale {
		return 0, errors.New("Timer clock range exceeded")
	}
	return timeMS * TimerScale, nil
}

// TimerRegistry owns timers and emits their deferred events into an
// EventQueue. IDs are kept sorted so timer processing is independent of Go map
// iteration order.
type TimerRegistry struct {
	events    *EventQueue
	timers    map[uint64]*Timer
	ids       []uint64
	nowMS     uint64
	lax       bool
	maxTimers int
}

// TimerManager is the longer descriptive name used by host integrations.
type TimerManager = TimerRegistry

func NewTimerRegistry(events *EventQueue) *TimerRegistry {
	if events == nil {
		events = NewEventQueue()
	}
	return &TimerRegistry{events: events, timers: make(map[uint64]*Timer), maxTimers: MaxTimers}
}
func NewTimerManager(events *EventQueue) *TimerRegistry { return NewTimerRegistry(events) }

type timerStateSnapshot struct {
	timer    *Timer
	id       uint64
	interval uint64
	next     uint64
	enabled  bool
	capacity int32
	mode     Priority
}

type timerRegistrySnapshot struct {
	states    []timerStateSnapshot
	ids       []uint64
	nowMS     uint64
	lax       bool
	maxTimers int
}

func (r *TimerRegistry) snapshot() timerRegistrySnapshot {
	if r == nil {
		return timerRegistrySnapshot{}
	}
	snapshot := timerRegistrySnapshot{ids: append([]uint64(nil), r.ids...), nowMS: r.nowMS, lax: r.lax, maxTimers: r.maxTimers, states: make([]timerStateSnapshot, 0, len(r.timers))}
	for id, timer := range r.timers {
		snapshot.states = append(snapshot.states, timerStateSnapshot{timer: timer, id: id, interval: timer.interval, next: timer.next, enabled: timer.enabled, capacity: timer.capacity, mode: timer.mode})
	}
	return snapshot
}

func (r *TimerRegistry) restore(snapshot timerRegistrySnapshot) {
	if r == nil {
		return
	}
	r.nowMS = snapshot.nowMS
	r.lax = snapshot.lax
	r.maxTimers = snapshot.maxTimers
	r.ids = append(r.ids[:0], snapshot.ids...)
	r.timers = make(map[uint64]*Timer, len(snapshot.states))
	for _, state := range snapshot.states {
		timer := state.timer
		if timer == nil {
			timer = &Timer{}
		}
		*timer = Timer{id: state.id, interval: state.interval, next: state.next, enabled: state.enabled, capacity: state.capacity, mode: state.mode, owner: r}
		r.timers[state.id] = timer
	}
}

func (r *TimerRegistry) ensureInit() {
	if r.events == nil {
		r.events = NewEventQueue()
	}
	if r.timers == nil {
		r.timers = make(map[uint64]*Timer)
	}
	if r.maxTimers == 0 {
		r.maxTimers = MaxTimers
	}
}

func (r *TimerRegistry) SetMaxTimers(limit int) error {
	r.ensureInit()
	if limit <= 0 || limit < len(r.timers) {
		return fmt.Errorf("invalid timer limit %d", limit)
	}
	r.maxTimers = limit
	return nil
}

// Register creates a timer with the native defaults. Duplicate IDs are
// rejected so cancellation cannot accidentally affect a replacement timer.
func (r *TimerRegistry) Register(id uint64) (*Timer, error) {
	r.ensureInit()
	if _, exists := r.timers[id]; exists {
		return nil, errors.New("timer is already registered")
	}
	if len(r.timers) >= r.maxTimers {
		return nil, errors.New("timer registration limit exceeded")
	}
	t := newTimer(id, r)
	r.timers[id] = t
	r.ids = append(r.ids, id)
	sort.Slice(r.ids, func(i, j int) bool { return r.ids[i] < r.ids[j] })
	return t, nil
}

// RegisterTimer adds a preconfigured unregistered timer to the registry.
func (r *TimerRegistry) RegisterTimer(t *Timer) error {
	if t == nil {
		return errors.New("nil timer")
	}
	r.ensureInit()
	if _, exists := r.timers[t.id]; exists {
		return errors.New("timer is already registered")
	}
	if len(r.timers) >= r.maxTimers {
		return errors.New("timer registration limit exceeded")
	}
	if t.owner != nil {
		return errors.New("timer is already registered")
	}
	t.owner = r
	r.timers[t.id] = t
	r.ids = append(r.ids, t.id)
	sort.Slice(r.ids, func(i, j int) bool { return r.ids[i] < r.ids[j] })
	return nil
}

func (r *TimerRegistry) Lookup(id uint64) (*Timer, bool) {
	if r == nil {
		return nil, false
	}
	t, ok := r.timers[id]
	return t, ok
}
func (r *TimerRegistry) Timer(id uint64) *Timer { t, _ := r.Lookup(id); return t }
func (r *TimerRegistry) EventQueue() *EventQueue {
	if r == nil {
		return nil
	}
	return r.events
}
func (r *TimerRegistry) Now() uint64 {
	if r == nil {
		return 0
	}
	return r.nowMS
}
func (r *TimerRegistry) Lax() bool { return r != nil && r.lax }
func (r *TimerRegistry) SetLax(lax bool) {
	if r != nil {
		r.lax = lax
	}
}
func (r *TimerRegistry) SetLaxTimer(lax bool)     { r.SetLax(lax) }
func (r *TimerRegistry) SetLaxTimerMode(lax bool) { r.SetLax(lax) }

// Unregister disables and cancels all pending callbacks for id.
func (r *TimerRegistry) Unregister(id uint64) {
	if r == nil || r.timers == nil {
		return
	}
	if _, ok := r.timers[id]; !ok {
		return
	}
	delete(r.timers, id)
	r.events.Cancel(id, EventTimer)
	for i, candidate := range r.ids {
		if candidate == id {
			copy(r.ids[i:], r.ids[i+1:])
			r.ids = r.ids[:len(r.ids)-1]
			break
		}
	}
}

func (r *TimerRegistry) setInterval(id uint64, ticks uint64) error {
	t := r.timers[id]
	if t == nil {
		return errors.New("timer is not registered")
	}
	t.interval = ticks
	if t.enabled {
		r.events.Cancel(id, EventTimer)
		now, err := clockTicks(r.nowMS)
		if err != nil {
			return err
		}
		t.next = now + t.interval // native fixed-width tick arithmetic wraps
	}
	return nil
}

func (r *TimerRegistry) SetInterval(id uint64, intervalMS float64) error {
	ticks, err := timerTicks(intervalMS)
	if err != nil {
		return err
	}
	return r.setInterval(id, ticks)
}
func (r *TimerRegistry) SetIntervalTicks(id, ticks uint64) error { return r.setInterval(id, ticks) }

func (r *TimerRegistry) setEnabled(id uint64, enabled bool) error {
	t := r.timers[id]
	if t == nil {
		return errors.New("timer is not registered")
	}
	t.enabled = enabled
	if enabled {
		now, err := clockTicks(r.nowMS)
		if err != nil {
			return err
		}
		t.next = now + t.interval
	} else {
		r.events.Cancel(id, EventTimer)
	}
	return nil
}
func (r *TimerRegistry) SetEnabled(id uint64, enabled bool) error { return r.setEnabled(id, enabled) }
func (r *TimerRegistry) SetCapacity(id uint64, capacity int32) error {
	t := r.timers[id]
	if t == nil {
		return errors.New("timer is not registered")
	}
	t.capacity = capacity
	return nil
}
func (r *TimerRegistry) SetMode(id uint64, mode int) error {
	t := r.timers[id]
	if t == nil {
		return errors.New("timer is not registered")
	}
	return t.SetMode(mode)
}

// Advance samples timers at a deterministic host clock. A timer due by more
// than 40 periods collapses to one callback and starts its next period from
// the current time; otherwise each missed period contributes one callback up
// to the timer's queue capacity. The exact deadline is intentionally not due
// until the following sample, matching the native thread's late-wakeup check.
func (r *TimerRegistry) Advance(timeMS uint64) error {
	r.ensureInit()
	if timeMS < r.nowMS {
		return errors.New("clock cannot move backwards")
	}
	now, err := clockTicks(timeMS)
	if err != nil {
		return err
	}
	for _, id := range r.ids {
		t := r.timers[id]
		if t == nil || !t.enabled || t.interval == 0 || t.next >= now {
			continue
		}
		missed := (now - t.next) / t.interval
		if missed != ^uint64(0) {
			missed++
		}
		count := missed
		if missed > maxTimerCatchup {
			t.next = now + t.interval
			count = 1
		} else {
			t.next += missed * t.interval
		}
		capacity := uint64(maxTimerCapacity)
		if r.lax {
			capacity = 1
		} else if t.capacity != 0 {
			if t.capacity < 0 {
				capacity = 0
			} else {
				capacity = uint64(t.capacity)
			}
		}
		available := capacity
		queued := r.events.Count(id, EventTimer)
		if queued >= int(available) {
			available = 0
		} else {
			available -= uint64(queued)
		}
		if count > available {
			count = available
		}
		for i := uint64(0); i < count; i++ {
			if err := r.events.Post(id, EventTimer, t.mode); err != nil {
				return err
			}
		}
	}
	r.nowMS = timeMS
	return nil
}

// AdvanceTimers is an explicit alias for hosts that distinguish timer samples
// from other clock advancement.
func (r *TimerRegistry) AdvanceTimers(timeMS uint64) error { return r.Advance(timeMS) }
