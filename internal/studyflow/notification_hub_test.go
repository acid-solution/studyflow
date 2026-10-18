package studyflow

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"studyflow/internal/model"
)

// A controllable durable log for delivery scheduling tests. Transaction behavior
// is covered separately, including against MySQL in the integration tests.
type deliveryLog struct {
	Repository
	mu           sync.Mutex
	events       map[string][]Notification
	headReads    int
	pageReads    int
	readFailures int
	headHook     func(string)
}

func newDeliveryLog() *deliveryLog { return &deliveryLog{events: map[string][]Notification{}} }
func (r *deliveryLog) append(user string, count int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for range count {
		r.events[user] = append(r.events[user], Notification{Sequence: uint64(len(r.events[user]) + 1), Kind: "task.updated", Topics: []string{"tasks"}})
	}
}
func (r *deliveryLog) NotificationHead(_ context.Context, user string) (model.NotificationStream, error) {
	r.mu.Lock()
	r.headReads++
	head := model.NotificationStream{UserID: user, LastSequence: uint64(len(r.events[user]))}
	hook := r.headHook
	r.mu.Unlock()
	if hook != nil {
		hook(user)
	}
	return head, nil
}
func (r *deliveryLog) NotificationsAfter(_ context.Context, user string, after, through uint64, limit int) ([]Notification, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pageReads++
	if r.readFailures > 0 {
		r.readFailures--
		return nil, errors.New("temporary database failure")
	}
	var events []Notification
	for _, event := range r.events[user] {
		if event.Sequence > after && event.Sequence <= through {
			events = append(events, event)
			if len(events) == limit {
				break
			}
		}
	}
	return events, nil
}
func (r *deliveryLog) counts() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.headReads, r.pageReads
}

func testDeliveryHub(repo Repository, cache Cache) *NotificationHub {
	return NewNotificationHub(repo, cache, nil, NotificationOptions{QueueSize: 512, CleanupInterval: time.Hour, RetryInitial: 10 * time.Millisecond, RetryMax: 40 * time.Millisecond})
}
func subscribeDelivery(t *testing.T, hub *NotificationHub, user string) *subscription {
	t.Helper()
	client, stop, err := hub.subscribe(t.Context(), user)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	return client
}
func receiveDelivery(t *testing.T, client *subscription, sequence uint64) {
	t.Helper()
	select {
	case message := <-client.events:
		if message.Event != "business.changed" || message.Sequence != sequence {
			t.Fatalf("expected sequence %d, got %+v", sequence, message)
		}
	case <-client.done:
		t.Fatal("unexpected subscriber close")
	case <-time.After(2 * time.Second):
		t.Fatalf("delivery %d timed out", sequence)
	}
}

func TestNotificationWakeDrainsMergedBatchesAndUsers(t *testing.T) {
	repo := newDeliveryLog()
	hub := testDeliveryHub(repo, nil)
	one := subscribeDelivery(t, hub, "one")
	two := subscribeDelivery(t, hub, "two")
	// A capacity-one signal is already full; work must live in per-user state.
	repo.append("one", 250)
	repo.append("two", 2)
	for range 100 {
		hub.Wake("one")
		hub.Wake("two")
	}
	hub.Start(t.Context())
	for sequence := uint64(1); sequence <= 250; sequence++ {
		receiveDelivery(t, one, sequence)
	}
	receiveDelivery(t, two, 1)
	receiveDelivery(t, two, 2)
	_, pages := repo.counts()
	if pages != 4 {
		t.Fatalf("expected three full-log pages plus other user, got %d", pages)
	}
}

func TestNotificationWakeDuringDeliveryIsNotLost(t *testing.T) {
	repo := newDeliveryLog()
	hub := testDeliveryHub(repo, nil)
	client := subscribeDelivery(t, hub, "user")
	repo.append("user", 1)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	repo.headHook = func(string) { once.Do(func() { close(entered); <-release }) }
	hub.Start(t.Context())
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("delivery not started")
	}
	repo.append("user", 1)
	hub.Wake("user")
	close(release)
	receiveDelivery(t, client, 1)
	receiveDelivery(t, client, 2)
}

func TestNotificationRegistrationGapIsCovered(t *testing.T) {
	repo := newDeliveryLog()
	hub := testDeliveryHub(repo, nil)
	var once sync.Once
	repo.headHook = func(user string) {
		once.Do(func() { repo.append(user, 1); hub.Wake(user) })
	}
	client := subscribeDelivery(t, hub, "user")
	hub.Start(t.Context())
	receiveDelivery(t, client, 1)
}

type deliveryCache struct {
	Cache
	bumps atomic.Int32
}

func (c *deliveryCache) Bump(context.Context, string) { c.bumps.Add(1) }

func TestNotificationFailureRetriesWithoutNewWriteAndInvalidatesBeforeDelivery(t *testing.T) {
	repo := newDeliveryLog()
	cache := &deliveryCache{}
	hub := testDeliveryHub(repo, cache)
	client := subscribeDelivery(t, hub, "user")
	repo.append("user", 1)
	repo.readFailures = 2
	hub.Start(t.Context())
	receiveDelivery(t, client, 1)
	if cache.bumps.Load() == 0 {
		t.Fatal("client was notified before cache invalidation")
	}
	_, pages := repo.counts()
	if pages != 3 {
		t.Fatalf("expected two failed attempts and recovery, got %d", pages)
	}
}

func TestNotificationIdleDoesNotPollDatabase(t *testing.T) {
	repo := newDeliveryLog()
	hub := testDeliveryHub(repo, nil)
	client := subscribeDelivery(t, hub, "user")
	repo.append("user", 1)
	hub.Start(t.Context())
	receiveDelivery(t, client, 1)
	before, _ := repo.counts()
	// Longer than the removed one-second delivery poll. Cleanup is independent.
	time.Sleep(1100 * time.Millisecond)
	after, _ := repo.counts()
	if before != after {
		t.Fatalf("idle subscriber caused database reads: %d -> %d", before, after)
	}
	hub.Wake("offline-user")
	hub.mu.Lock()
	_, queued := hub.groups["offline-user"]
	hub.mu.Unlock()
	if queued {
		t.Fatal("offline users must rely on replay, not background work")
	}
}

type changeTransaction struct {
	Repository
	committed, appended      bool
	commitError, appendError error
	afterCommit              func()
}

func (r *changeTransaction) WithinTransaction(_ context.Context, fn func(Repository) error) error {
	if err := fn(r); err != nil {
		return err
	}
	if r.commitError != nil {
		return r.commitError
	}
	r.committed = true
	if r.afterCommit != nil {
		r.afterCommit()
	}
	return nil
}
func (r *changeTransaction) CreateGoal(context.Context, *model.Goal) error { return nil }
func (r *changeTransaction) AppendNotification(context.Context, string, string, []NotificationResource, []string, time.Time) error {
	r.appended = true
	return r.appendError
}

type wakeFunc func(string)

func (f wakeFunc) Wake(user string) { f(user) }

func TestNotificationWakeOnlyAfterSuccessfulEventCommit(t *testing.T) {
	failure := errors.New("injected failure")
	for _, test := range []struct {
		name                                    string
		commitError, appendError, businessError error
		noop, cancelAfterCommit                 bool
		wantWake                                int
	}{
		{name: "committed", wantWake: 1},
		{name: "canceled request after commit", cancelAfterCommit: true, wantWake: 1},
		{name: "commit failed", commitError: failure},
		{name: "event insert failed", appendError: failure},
		{name: "business failed", businessError: failure},
		{name: "idempotent replay", noop: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			repo := &changeTransaction{commitError: test.commitError, appendError: test.appendError}
			if test.cancelAfterCommit {
				repo.afterCommit = cancel
			}
			wakes := 0
			service := NewServiceWithRepository(repo, nil, wakeFunc(func(user string) {
				if user != "user" || !repo.committed || !repo.appended {
					t.Fatal("wake before committed event")
				}
				wakes++
			}))
			err := service.withChange(ctx, "user", "goal.created", func(tx Repository) error {
				if test.businessError != nil {
					return test.businessError
				}
				if test.noop {
					return nil
				}
				return tx.CreateGoal(ctx, &model.Goal{ID: "goal"})
			})
			wantError := test.commitError != nil || test.appendError != nil || test.businessError != nil
			if (err != nil) != wantError || wakes != test.wantWake {
				t.Fatalf("error=%v wakes=%d", err, wakes)
			}
		})
	}
}
