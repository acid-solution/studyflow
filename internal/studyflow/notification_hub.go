package studyflow

import (
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"
)

type NotificationOptions struct {
	PollInterval, Heartbeat, Retention, CleanupInterval time.Duration
	QueueSize                                           int
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
	cursor  uint64
	clients map[*subscription]bool
}

type NotificationHub struct {
	repo    Repository
	cache   Cache
	logger  *slog.Logger
	options NotificationOptions
	mu      sync.Mutex
	groups  map[string]*subscriberGroup
	closed  bool
}

func NewNotificationHub(repo Repository, cache Cache, logger *slog.Logger, options NotificationOptions) *NotificationHub {
	if cache == nil {
		cache = noopCache{}
	}
	if options.PollInterval <= 0 {
		options.PollInterval = time.Second
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
	return &NotificationHub{repo: repo, cache: cache, logger: logger, options: options, groups: map[string]*subscriberGroup{}}
}

func (h *NotificationHub) Start(ctx context.Context) {
	go func() {
		poll := time.NewTicker(h.options.PollInterval)
		cleanup := time.NewTicker(h.options.CleanupInterval)
		defer poll.Stop()
		defer cleanup.Stop()
		defer h.closeAll()
		for {
			select {
			case <-ctx.Done():
				return
			case <-poll.C:
				h.PollOnce(ctx)
			case <-cleanup.C:
				job, cancel := context.WithTimeout(ctx, 5*time.Second)
				err := h.repo.PruneNotifications(job, time.Now().UTC().Add(-h.options.Retention), 1000)
				cancel()
				if err != nil && h.logger != nil {
					h.logger.Error("notification cleanup failed", "error", err)
				}
			}
		}
	}()
}

func (h *NotificationHub) subscribe(ctx context.Context, userID string) (*subscription, func(), error) {
	head, err := h.repo.NotificationHead(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	client := &subscription{events: make(chan streamMessage, h.options.QueueSize), done: make(chan struct{})}
	if h.closed {
		close(client.done)
		return client, func() {}, nil
	}
	group := h.groups[userID]
	if group == nil {
		group = &subscriberGroup{cursor: head.LastSequence, clients: map[*subscription]bool{}}
		h.groups[userID] = group
	}
	group.clients[client] = true
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

func (h *NotificationHub) PollOnce(ctx context.Context) {
	h.mu.Lock()
	groups := make(map[string]*subscriberGroup, len(h.groups))
	cursors := make(map[string]uint64, len(h.groups))
	for user, group := range h.groups {
		groups[user] = group
		cursors[user] = group.cursor
	}
	h.mu.Unlock()
	for user, group := range groups {
		job, cancel := context.WithTimeout(ctx, 3*time.Second)
		head, err := h.repo.NotificationHead(job, user)
		var messages []streamMessage
		if err == nil && cursors[user] < head.PrunedThrough {
			messages = []streamMessage{resetMessage(head.LastSequence, "expired")}
		} else if err == nil && head.LastSequence > cursors[user] {
			var events []Notification
			events, err = h.repo.NotificationsAfter(job, user, cursors[user], head.LastSequence, 100)
			if err == nil {
				expected := cursors[user] + 1
				for _, event := range events {
					if event.Sequence != expected {
						messages = nil
						break
					}
					messages = append(messages, changedMessage(event))
					expected++
				}
				if len(messages) == 0 {
					messages = []streamMessage{resetMessage(head.LastSequence, "expired")}
				}
			}
		}
		if err == nil && len(messages) > 0 {
			h.cache.Bump(job, user)
		}
		cancel()
		if err != nil {
			if h.logger != nil {
				h.logger.Warn("notification poll failed", "error", err)
			}
			continue
		}
		h.mu.Lock()
		if h.groups[user] == group {
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
		}
		h.mu.Unlock()
	}
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
