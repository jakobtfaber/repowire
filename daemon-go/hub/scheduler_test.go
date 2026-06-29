package hub

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeSchedDelivery records the dispatch calls the firing loop makes.
type fakeSchedDelivery struct {
	mu       sync.Mutex
	notifies []string // texts
	asks     []string // texts
}

func (d *fakeSchedDelivery) Notify(_ context.Context, p NotifyParams) (NotifyResult, error) {
	d.mu.Lock()
	d.notifies = append(d.notifies, p.Text)
	d.mu.Unlock()
	return NotifyResult{Status: "sent", DeliveryState: "delivered"}, nil
}

func (d *fakeSchedDelivery) OpenScheduledAsk(_ context.Context, _, _, text string, _ *string, _ string) (string, error) {
	d.mu.Lock()
	d.asks = append(d.asks, text)
	d.mu.Unlock()
	return "ask-1", nil
}

func (d *fakeSchedDelivery) notifyCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.notifies)
}

func (d *fakeSchedDelivery) askCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.asks)
}

// waitFor polls cond until true or the deadline, failing the test otherwise.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

// TestSchedulerFiresPastDueOneShot is the primary firing path: a past-due
// one-shot notify fires immediately, is delivered via PeerDelivery, then dropped.
func TestSchedulerFiresPastDueOneShot(t *testing.T) {
	store := newFakeScheduleStore()
	del := &fakeSchedDelivery{}
	// Past-due fire_at so the loop fires it on the first iteration.
	_, _ = store.CreateSchedule(context.Background(), "alpha", "beta", "ping", time.Now().Add(-time.Minute), "notify", nil, nil)

	s := NewScheduler(store, del)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	defer s.Stop()

	waitFor(t, "one-shot notify delivered", func() bool { return del.notifyCount() == 1 })
	waitFor(t, "one-shot dropped after fire", func() bool { return store.count() == 0 })
}

// TestSchedulerRecurringCronAdvances: a past-due cron ask fires then is
// rescheduled (not deleted) to a future fire_at.
func TestSchedulerRecurringCronAdvances(t *testing.T) {
	store := newFakeScheduleStore()
	del := &fakeSchedDelivery{}
	cron := "0 * * * *"
	_, _ = store.CreateSchedule(context.Background(), "alpha", "beta", "hourly", time.Now().Add(-time.Minute), "ask", nil, &cron)

	s := NewScheduler(store, del)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	defer s.Stop()

	waitFor(t, "cron ask delivered", func() bool { return del.askCount() == 1 })
	// Recurring schedules are advanced, not dropped.
	waitFor(t, "cron schedule advanced (still present, fire_at in the future)", func() bool {
		got, _ := store.NextDueSchedule(context.Background())
		if got == nil {
			return false
		}
		fa, err := time.Parse(time.RFC3339Nano, got.FireAt)
		return err == nil && fa.After(time.Now())
	})
	if store.count() != 1 {
		t.Fatalf("recurring schedule should persist, got count=%d", store.count())
	}
}

// TestSchedulerWakeOnCreate: an empty scheduler is idle; creating + waking
// causes the new schedule to fire.
func TestSchedulerWakeOnCreate(t *testing.T) {
	store := newFakeScheduleStore()
	del := &fakeSchedDelivery{}
	s := NewScheduler(store, del)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	defer s.Stop()

	// Loop should be blocked (no schedules). Add a past-due one then Wake.
	_, _ = store.CreateSchedule(context.Background(), "alpha", "beta", "later", time.Now().Add(-time.Second), "notify", nil, nil)
	s.Wake()

	waitFor(t, "woken schedule fires", func() bool { return del.notifyCount() == 1 })
}
