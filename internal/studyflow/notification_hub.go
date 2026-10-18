package studyflow

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"
)

type NotificationOptions struct {
	Heartbeat, Retention, CleanupInterval time.Duration
	RetryInitial, RetryMax                time.Duration
	QueueSize                             int
}

type streamMessage struct {
	Event    string
	Sequence uint64
	Data     any
}
type subscription struct {
	events chan streamMessage
	done   chan struct{}
}
type subscriberGroup struct {
	cursor   uint64
	clients  map[*subscription]bool
	pending  bool
	retryAt  time.Time
	failures int
}

type NotificationHub struct {
	repo    Repository
	cache   Cache
	logger  *slog.Logger
	options NotificationOptions
	mu      sync.Mutex
	groups  map[string]*subscriberGroup
	closed  bool
	wake    chan struct{}
	start   sync.Once
}

func NewNotificationHub(repo Repository, cache Cache, logger *slog.Logger, options NotificationOptions) *NotificationHub {
	if cache == nil {
		cache = noopCache{}
	}
	if options.RetryInitial <= 0 {
		options.RetryInitial = time.Second
	}
	if options.RetryMax < options.RetryInitial {
		options.RetryMax = max(30*time.Second, options.RetryInitial)
	}
	if options.Heartbeat <= 0 {
		options.Heartbeat = 15 * time.Second
	}
	if options.Retention <= 0 {
		options.Retention = 7 * 24 * time.Hour
	}
	if options.CleanupInterval <= 0 {
		options.CleanupInterval = time.Minute
	}
	if options.QueueSize <= 0 {
		options.QueueSize = 64
	}
	return &NotificationHub{repo: repo, cache: cache, logger: logger, options: options, groups: map[string]*subscriberGroup{}, wake: make(chan struct{}, 1)}
}

func (h *NotificationHub) Start(ctx context.Context) {
	h.start.Do(func() { go h.run(ctx) })
}

func (h *NotificationHub) run(ctx context.Context) {
	cleanup := time.NewTicker(h.options.CleanupInterval)
	retry := time.NewTimer(time.Hour)
	retry.Stop()
	defer cleanup.Stop()
	defer retry.Stop()
	defer h.closeAll()
	for {
		if ctx.Err() != nil {
			return
		}
		h.deliverPending(ctx)
		var retryC <-chan time.Time
		if delay, pending := h.nextRetry(); pending {
			retry.Reset(delay)
			retryC = retry.C
		}
		select {
		case <-ctx.Done():
			return
		case <-h.wake:
		case <-retryC:
		case <-cleanup.C:
			job, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := h.repo.PruneNotifications(job, time.Now().UTC().Add(-h.options.Retention), 1000)
			cancel()
			if err != nil && h.logger != nil {
				h.logger.Error("notification cleanup failed", "error", err)
			}
		}
		retry.Stop()
	}
}

// Wake records work before signalling. A full signal channel only coalesces
// wake-ups: it cannot erase pending users or an in-flight delivery's next pass.
// Only this process's online subscribers are scheduled; offline clients replay.
func (h *NotificationHub) Wake(userID string) {
	h.mu.Lock()
	group := h.groups[userID]
	if h.closed || group == nil {
		h.mu.Unlock()
		return
	}
	group.pending = true
	h.mu.Unlock()
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

func (h *NotificationHub) subscribe(ctx context.Context, userID string) (*subscription, func(), error) {
	head, err := h.repo.NotificationHead(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	h.mu.Lock()
	client := &subscription{events: make(chan streamMessage, h.options.QueueSize), done: make(chan struct{})}
	if h.closed {
		close(client.done)
		h.mu.Unlock()
		return client, func() {}, nil
	}
	group := h.groups[userID]
	if group == nil {
		group = &subscriberGroup{cursor: head.LastSequence, clients: map[*subscription]bool{}}
		h.groups[userID] = group
	}
	group.clients[client] = true
	h.mu.Unlock()
	// Close the gap between reading the initial head and registering the group.
	// HTTP replay takes its cut-off only after this subscription is registered.
	h.Wake(userID)
	return client, func() { h.mu.Lock(); defer h.mu.Unlock(); h.remove(userID, group, client) }, nil
}

// remove is called with mu held. Channels carrying data are never closed while
// writers might use them; done is the one cancellation signal.
func (h *NotificationHub) remove(userID string, group *subscriberGroup, client *subscription) {
	if !group.clients[client] {
		return
	}
	delete(group.clients, client)
	close(client.done)
	if len(group.clients) == 0 && h.groups[userID] == group {
		delete(h.groups, userID)
	}
}

func (h *NotificationHub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for userID, group := range h.groups {
		for client := range group.clients {
			h.remove(userID, group, client)
		}
	}
}

func (h *NotificationHub) deliverPending(ctx context.Context) {
	h.mu.Lock()
	groups := make(map[string]*subscriberGroup, len(h.groups))
	cursors := make(map[string]uint64, len(h.groups))
	for user, group := range h.groups {
		if !group.pending || group.retryAt.After(time.Now()) {
			continue
		}
		// Clear before I/O. A concurrent commit can mark it pending again.
		group.pending = false
		group.retryAt = time.Time{}
		groups[user] = group
		cursors[user] = group.cursor
	}
	h.mu.Unlock()
	for user, group := range groups {
		if ctx.Err() != nil {
			return
		}
		job, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := h.deliver(job, user, group, cursors[user])
		cancel()
		if err != nil {
			if h.logger != nil {
				h.logger.Warn("notification delivery failed; retry scheduled", "error", err)
			}
		}
		h.mu.Lock()
		if h.groups[user] == group {
			if err != nil {
				group.failures++
				group.pending = true
				delay := h.options.RetryInitial
				for i := 1; i < group.failures && delay < h.options.RetryMax; i++ {
					delay = min(delay*2, h.options.RetryMax)
				}
				group.retryAt = time.Now().Add(delay)
			} else {
				group.failures = 0
			}
		}
		h.mu.Unlock()
	}
}

func (h *NotificationHub) nextRetry() (time.Duration, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var earliest time.Time
	pending := false
	for _, group := range h.groups {
		if group.pending && (!pending || group.retryAt.Before(earliest)) {
			earliest = group.retryAt
			pending = true
		}
	}
	return max(0, time.Until(earliest)), pending
}

func (h *NotificationHub) deliver(ctx context.Context, user string, group *subscriberGroup, cursor uint64) error {
	head, err := h.repo.NotificationHead(ctx, user)
	if err != nil {
		return err
	}
	if cursor < head.PrunedThrough {
		h.publish(ctx, user, group, []streamMessage{resetMessage(head.LastSequence, "expired")})
		return nil
	}
	// Drain the entire snapshot, not just the first page of a merged wake-up.
	for cursor < head.LastSequence {
		events, err := h.repo.NotificationsAfter(ctx, user, cursor, head.LastSequence, 100)
		if err != nil {
			return err
		}
		messages := make([]streamMessage, 0, len(events))
		expected := cursor + 1
		for _, event := range events {
			if event.Sequence != expected {
				break
			}
			messages = append(messages, changedMessage(event))
			expected++
		}
		if len(messages) == 0 || len(messages) != len(events) {
			h.publish(ctx, user, group, []streamMessage{resetMessage(head.LastSequence, "expired")})
			return nil
		}
		if !h.publish(ctx, user, group, messages) {
			return nil
		}
		cursor = messages[len(messages)-1].Sequence
	}
	return nil
}

func (h *NotificationHub) publish(ctx context.Context, user string, group *subscriberGroup, messages []streamMessage) bool {
	// Invalidate before any client can react by reading cached data.
	h.cache.Bump(ctx, user)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.groups[user] != group {
		return false
	}
	for _, message := range messages {
		if message.Sequence <= group.cursor {
			continue
		}
		for client := range group.clients {
			select {
			case client.events <- message:
			default:
				h.remove(user, group, client)
			}
		}
		group.cursor = message.Sequence
	}
	return h.groups[user] == group
}

func resetMessage(sequence uint64, reason string) streamMessage {
	return streamMessage{Event: "sync.reset", Sequence: sequence, Data: map[string]string{"cursor": strconv.FormatUint(sequence, 10), "reason": reason}}
}
func changedMessage(event Notification) streamMessage {
	return streamMessage{Event: "business.changed", Sequence: event.Sequence, Data: struct {
		ID string `json:"id"`
		Notification
	}{strconv.FormatUint(event.Sequence, 10), event}}
}
