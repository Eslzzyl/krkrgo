package runtime

import "testing"

func TestEventQueueBatchPriorityAndOrder(t *testing.T) {
	q := NewEventQueue()
	if err := q.Post(1, EventTimer, PriorityNormal); err != nil {
		t.Fatal(err)
	}
	if err := q.Post(2, EventTrigger, PriorityExclusive); err != nil {
		t.Fatal(err)
	}
	if err := q.Post(3, EventTimer, PriorityNormal); err != nil {
		t.Fatal(err)
	}
	if !q.ExclusivePosted() {
		t.Fatal("exclusive post was not recorded")
	}
	through := q.BeginBatch()
	if q.ExclusivePosted() {
		t.Fatal("batch marker was not reset")
	}
	got, ok := q.Pop(through, PriorityNormal)
	if !ok || got.Target != 1 {
		t.Fatalf("first normal event = %#v, %v", got, ok)
	}
	got, ok = q.Pop(through, PriorityNormal)
	if !ok || got.Target != 3 {
		t.Fatalf("second normal event = %#v, %v", got, ok)
	}
	got, ok = q.Pop(through, PriorityExclusive)
	if !ok || got.Target != 2 {
		t.Fatalf("exclusive event = %#v, %v", got, ok)
	}
}

func TestSchedulerStableDeadlinesAndCancellation(t *testing.T) {
	var s Scheduler
	first, err := s.Schedule(10, "first")
	if err != nil {
		t.Fatal(err)
	}
	cancel, err := s.Schedule(5, "cancel")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Schedule(10, "second")
	if err != nil {
		t.Fatal(err)
	}
	s.Cancel(cancel)
	if callbacks, err := s.Advance(9); err != nil || len(callbacks) != 0 {
		t.Fatalf("early advance = %#v, %v", callbacks, err)
	}
	callbacks, err := s.Advance(10)
	if err != nil || len(callbacks) != 2 {
		t.Fatalf("due callbacks = %#v, %v", callbacks, err)
	}
	if callbacks[0].ID != first || callbacks[1].ID != second || callbacks[0].Name != "first" || callbacks[1].Name != "second" {
		t.Fatalf("callback order = %#v", callbacks)
	}
	if _, err := s.Advance(9); err == nil {
		t.Fatal("backward clock was accepted")
	}
}

func TestTimerFixedPointCapacityLateWakeupAndLax(t *testing.T) {
	q := NewEventQueue()
	r := NewTimerRegistry(q)
	timer, err := r.Register(7)
	if err != nil {
		t.Fatal(err)
	}
	if err := timer.SetInterval(0.5); err != nil {
		t.Fatal(err)
	}
	if got := timer.IntervalTicks(); got != TimerScale/2 {
		t.Fatalf("fixed interval = %d", got)
	}
	if err := timer.SetInterval(10); err != nil {
		t.Fatal(err)
	}
	if got := timer.IntervalTicks(); got != 10*TimerScale {
		t.Fatalf("millisecond interval = %d", got)
	}
	if err := timer.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if err := r.Advance(0); err != nil {
		t.Fatal(err)
	}
	if q.Count(7, EventTimer) != 0 {
		t.Fatal("timer fired before its deadline")
	}
	if err := r.Advance(10); err != nil {
		t.Fatal(err)
	}
	if q.Count(7, EventTimer) != 0 {
		t.Fatal("timer fired at its exact deadline")
	}
	if err := r.Advance(11); err != nil {
		t.Fatal(err)
	}
	if q.Count(7, EventTimer) != 1 {
		t.Fatalf("first timer count = %d", q.Count(7, EventTimer))
	}
	if err := r.Advance(100); err != nil {
		t.Fatal(err)
	}
	if q.Count(7, EventTimer) != 6 {
		t.Fatalf("capacity count = %d", q.Count(7, EventTimer))
	}
	q.Cancel(7, EventTimer)
	if err := r.Advance(1000); err != nil {
		t.Fatal(err)
	}
	if q.Count(7, EventTimer) != 1 {
		t.Fatalf("late collapse count = %d", q.Count(7, EventTimer))
	}
	q.Cancel(7, EventTimer)
	r.SetLax(true)
	if err := r.Advance(2000); err != nil {
		t.Fatal(err)
	}
	if q.Count(7, EventTimer) != 1 {
		t.Fatalf("lax count = %d", q.Count(7, EventTimer))
	}
	if err := timer.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if q.Len() != 0 {
		t.Fatalf("disable did not cancel events: %d", q.Len())
	}
}
