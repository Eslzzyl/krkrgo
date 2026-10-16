package runtime

import (
	"container/heap"
	"errors"
	"fmt"
	"math"
	"sync"
)

// EventKind identifies the native callback represented by a posted event.
type EventKind uint8

const (
	EventTrigger EventKind = iota
	EventTimer
	EventSoundTrigger
	EventBitmapLoad
)

// Compatibility names used by hosts that spell the kind explicitly.
const (
	EventKindTrigger      = EventTrigger
	EventKindTimer        = EventTimer
	EventKindBitmapLoad   = EventBitmapLoad
	EventKindSoundTrigger = EventSoundTrigger
)

// Priority controls which deferred-event pass may consume an event. Values
// intentionally match Kirikiri's mode values: normal, exclusive, and idle.
// It is an alias rather than a distinct integer type so replay decoders can
// pass decoded mode fields without conversion.
type Priority = int

type EventPriority = Priority

const (
	PriorityNormal    Priority = 0
	PriorityExclusive Priority = 1
	PriorityIdle      Priority = 2

	EventPriorityNormal    = PriorityNormal
	EventPriorityExclusive = PriorityExclusive
	EventPriorityIdle      = PriorityIdle
)

const (
	// MaxPendingEvents bounds retained callbacks. This is also the bound used
	// by the reference runtime, and prevents an unbounded producer from
	// retaining target objects forever.
	MaxPendingEvents = 100000
)

// PostedEvent is an immutable queue entry. Sequence is globally increasing,
// making order deterministic even when callbacks have the same priority.
type PostedEvent struct {
	Target   uint64
	Kind     EventKind
	Sequence uint64
	Priority Priority
	Sound    bool
}

// EventQueue stores deferred callbacks in posting order. It is deliberately
// not a heap: dispatch passes select a priority and sequence cutoff while
// preserving the order in which matching events were posted.
type EventQueue struct {
	mu              sync.RWMutex
	pending         []PostedEvent
	sequence        uint64
	exclusivePosted bool
	maxPending      int
}

// NewEventQueue returns an empty queue with the standard pending-event bound.
func NewEventQueue() *EventQueue {
	return &EventQueue{maxPending: MaxPendingEvents}
}

func (q *EventQueue) ensureInit() {
	if q.maxPending == 0 {
		q.maxPending = MaxPendingEvents
	}
}

// SetMaxPending changes the queue bound. It cannot lower the bound below the
// number of events already retained.
func (q *EventQueue) SetMaxPending(limit int) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if limit < len(q.pending) || limit <= 0 {
		return fmt.Errorf("invalid pending event limit %d", limit)
	}
	q.maxPending = limit
	return nil
}

// Post adds one callback. Unknown mode values intentionally map to normal,
// as they do in the native implementation.
func (q *EventQueue) Post(target uint64, kind EventKind, mode int) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.ensureInit()
	if len(q.pending) >= q.maxPending {
		return errors.New("pending event limit exceeded")
	}
	if q.sequence == math.MaxUint64 {
		return errors.New("event sequence overflow")
	}
	q.sequence++
	priority := normalizePriority(mode)
	q.pending = append(q.pending, PostedEvent{Target: target, Kind: kind, Sequence: q.sequence, Priority: priority})
	if priority == PriorityExclusive {
		q.exclusivePosted = true
	}
	return nil
}

func normalizePriority(mode int) Priority {
	priority := Priority(mode)
	if priority != PriorityExclusive && priority != PriorityIdle {
		priority = PriorityNormal
	}
	return priority
}

// PostEvent is a descriptive alias for Post.
func (q *EventQueue) PostEvent(target uint64, kind EventKind, mode int) error {
	return q.Post(target, kind, mode)
}

// PostBatch atomically adds count callbacks with the same target, kind, and
// priority. Validation happens before the queue is mutated, allowing callers
// to retry without losing their source events when the queue is full.
func (q *EventQueue) PostBatch(target uint64, kind EventKind, mode int, count int) error {
	return q.postBatch(target, kind, mode, count, false)
}

// PostSoundBatch atomically adds sound-trigger callbacks while retaining the
// EventTrigger kind used by existing queue consumers.
func (q *EventQueue) PostSoundBatch(target uint64, kind EventKind, mode int, count int) error {
	return q.postBatch(target, kind, mode, count, true)
}

func (q *EventQueue) postBatch(target uint64, kind EventKind, mode int, count int, sound bool) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.ensureInit()
	if count < 0 {
		return errors.New("invalid event batch size")
	}
	if count == 0 {
		return nil
	}
	if count > q.maxPending-len(q.pending) {
		return errors.New("pending event limit exceeded")
	}
	if uint64(count) > math.MaxUint64-q.sequence {
		return errors.New("event sequence overflow")
	}
	priority := normalizePriority(mode)
	for range count {
		q.sequence++
		q.pending = append(q.pending, PostedEvent{Target: target, Kind: kind, Sequence: q.sequence, Priority: priority, Sound: sound})
	}
	if priority == PriorityExclusive {
		q.exclusivePosted = true
	}
	return nil
}

// Cancel removes every queued event for target and kind. It is safe to call
// repeatedly and is the operation used by timer disable/invalidation.
func (q *EventQueue) Cancel(target uint64, kind EventKind) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pending) == 0 {
		return
	}
	original := len(q.pending)
	out := q.pending[:0]
	for _, event := range q.pending {
		if event.Target != target || event.Kind != kind {
			out = append(out, event)
		}
	}
	if len(out) < original {
		q.pending[len(out)] = PostedEvent{}
	}
	q.pending = out
}

// Count reports the number of queued callbacks for target and kind.
func (q *EventQueue) Count(target uint64, kind EventKind) int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	count := 0
	for _, event := range q.pending {
		if event.Target == target && event.Kind == kind {
			count++
		}
	}
	return count
}

// Len reports the number of queued callbacks.
func (q *EventQueue) Len() int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return len(q.pending)
}

// BeginBatch resets the exclusive-posted marker and returns the sequence
// cutoff for the next dispatch pass.
func (q *EventQueue) BeginBatch() uint64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.exclusivePosted = false
	return q.sequence
}

func (q *EventQueue) ExclusivePosted() bool {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.exclusivePosted
}

// HasExclusive reports whether any exclusive event remains queued.
func (q *EventQueue) HasExclusive() bool {
	q.mu.RLock()
	defer q.mu.RUnlock()
	for _, event := range q.pending {
		if event.Priority == PriorityExclusive {
			return true
		}
	}
	return false
}

// Pop removes the first matching event at or before through. The bool is false
// when no event matches. Returning a value rather than a pointer avoids a
// lifetime dependency on the queue's backing storage.
func (q *EventQueue) Pop(through uint64, priority Priority) (PostedEvent, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, event := range q.pending {
		if event.Sequence <= through && event.Priority == priority {
			copy(q.pending[i:], q.pending[i+1:])
			q.pending = q.pending[:len(q.pending)-1]
			return event, true
		}
	}
	return PostedEvent{}, false
}

// Pending returns a copy in posting order, primarily for deterministic host
// inspection and replay diagnostics.
func (q *EventQueue) Pending() []PostedEvent {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return append([]PostedEvent(nil), q.pending...)
}

type eventQueueSnapshot struct {
	pending         []PostedEvent
	sequence        uint64
	exclusivePosted bool
	maxPending      int
}

func (q *EventQueue) snapshot() eventQueueSnapshot {
	if q == nil {
		return eventQueueSnapshot{}
	}
	q.mu.RLock()
	defer q.mu.RUnlock()
	return eventQueueSnapshot{pending: append([]PostedEvent(nil), q.pending...), sequence: q.sequence, exclusivePosted: q.exclusivePosted, maxPending: q.maxPending}
}

func (q *EventQueue) restore(snapshot eventQueueSnapshot) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pending = append(q.pending[:0], snapshot.pending...)
	q.sequence = snapshot.sequence
	q.exclusivePosted = snapshot.exclusivePosted
	q.maxPending = snapshot.maxPending
}

type schedulerDeadlineSnapshot struct {
	time uint64
	id   uint64
	name string
}

type schedulerSnapshot struct {
	now       uint64
	nextID    uint64
	deadlines []schedulerDeadlineSnapshot
}

func (s *Scheduler) snapshot() schedulerSnapshot {
	if s == nil {
		return schedulerSnapshot{}
	}
	snapshot := schedulerSnapshot{now: s.now, nextID: s.nextID, deadlines: make([]schedulerDeadlineSnapshot, 0, len(s.byID))}
	for _, deadline := range s.byID {
		snapshot.deadlines = append(snapshot.deadlines, schedulerDeadlineSnapshot{time: deadline.time, id: deadline.id, name: deadline.name})
	}
	return snapshot
}

func (s *Scheduler) restore(snapshot schedulerSnapshot) {
	if s == nil {
		return
	}
	s.now = snapshot.now
	s.nextID = snapshot.nextID
	s.byID = make(map[uint64]*deadline, len(snapshot.deadlines))
	s.queue = nil
	for _, saved := range snapshot.deadlines {
		deadline := &deadline{time: saved.time, id: saved.id, name: saved.name, idx: -1}
		s.byID[deadline.id] = deadline
		heap.Push(&s.queue, deadline)
	}
}

// Callback is a deadline callback emitted by Scheduler.Advance.
type Callback struct {
	ID     uint64
	TimeMS uint64
	Name   string
}

type deadline struct {
	time uint64
	id   uint64
	name string
	idx  int
}
type deadlineHeap []*deadline

func (h deadlineHeap) Len() int { return len(h) }
func (h deadlineHeap) Less(i, j int) bool {
	if h[i].time != h[j].time {
		return h[i].time < h[j].time
	}
	return h[i].id < h[j].id
}
func (h deadlineHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i]; h[i].idx, h[j].idx = i, j }
func (h *deadlineHeap) Push(x any) {
	d := x.(*deadline)
	d.idx = len(*h)
	*h = append(*h, d)
}
func (h *deadlineHeap) Pop() any {
	old := *h
	n := len(old)
	d := old[n-1]
	old[n-1] = nil
	d.idx = -1
	*h = old[:n-1]
	return d
}

// Scheduler is a deterministic, deadline-ordered one-shot scheduler. IDs are
// assigned in insertion order and break ties at equal deadlines.
type Scheduler struct {
	now    uint64
	nextID uint64
	byID   map[uint64]*deadline
	queue  deadlineHeap
}

func (s *Scheduler) ensureInit() {
	if s.byID == nil {
		s.byID = make(map[uint64]*deadline)
	}
}

// NewScheduler returns an empty scheduler. The zero value is equivalent.
func NewScheduler() *Scheduler { return &Scheduler{} }

// Now returns the current scheduler clock in milliseconds.
func (s *Scheduler) Now() uint64 { return s.now }

// Schedule registers a one-shot callback after delayMS and returns its stable
// ID. A zero delay is due at the current clock and is emitted by the next
// Advance call at or beyond that clock.
func (s *Scheduler) Schedule(delayMS uint64, name string) (uint64, error) {
	if delayMS > math.MaxUint64-s.now {
		return 0, errors.New("timer deadline overflow")
	}
	if s.nextID == math.MaxUint64 {
		return 0, errors.New("timer ID overflow")
	}
	s.ensureInit()
	d := &deadline{time: s.now + delayMS, id: s.nextID, name: name, idx: -1}
	s.nextID++
	s.byID[d.id] = d
	heap.Push(&s.queue, d)
	return d.id, nil
}

// ScheduleAt is the absolute-deadline form used by hosts restoring a replay.
func (s *Scheduler) ScheduleAt(timeMS uint64, name string) (uint64, error) {
	if timeMS < s.now {
		return 0, errors.New("deadline is earlier than scheduler clock")
	}
	return s.Schedule(timeMS-s.now, name)
}

// Cancel removes a callback if it is still pending. Unknown IDs are harmless.
func (s *Scheduler) Cancel(id uint64) {
	d := s.byID[id]
	if d == nil {
		return
	}
	delete(s.byID, id)
	if d.idx >= 0 {
		heap.Remove(&s.queue, d.idx)
	}
}

// Advance moves the clock forward and returns all callbacks due by timeMS in
// stable (deadline, ID) order. The clock cannot move backwards.
func (s *Scheduler) Advance(timeMS uint64) ([]Callback, error) {
	if timeMS < s.now {
		return nil, errors.New("clock cannot move backwards")
	}
	var out []Callback
	for len(s.queue) != 0 && s.queue[0].time <= timeMS {
		d := heap.Pop(&s.queue).(*deadline)
		delete(s.byID, d.id)
		out = append(out, Callback{ID: d.id, TimeMS: d.time, Name: d.name})
	}
	s.now = timeMS
	return out, nil
}

// Pending returns the number of one-shot callbacks still registered.
func (s *Scheduler) Pending() int { return len(s.byID) }
