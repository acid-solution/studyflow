package studyflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"studyflow/internal/identity"
	"studyflow/internal/response"
)

func RegisterNotifications(router *gin.Engine, hub *NotificationHub, auth gin.HandlerFunc) {
	router.GET("/api/v1/events", auth, hub.serveEvents)
}

func (h *NotificationHub) serveEvents(c *gin.Context) {
	principal, ok := identity.RequestPrincipal(c)
	if !ok || !principal.ExpiresAt.After(time.Now()) {
		response.FailUnauthorized(c, "登录已过期")
		return
	}
	ctx, cancel := context.WithDeadline(c.Request.Context(), principal.ExpiresAt)
	defer cancel()
	raw := c.GetHeader("Last-Event-ID")
	cursor := uint64(0)
	if raw != "" {
		var err error
		cursor, err = strconv.ParseUint(raw, 10, 64)
		if err != nil {
			response.FailInvalidArgument(c, "同步游标无效")
			return
		}
	}
	client, unsubscribe, err := h.subscribe(ctx, principal.UserID)
	if err != nil {
		write(c, nil, err)
		return
	}
	defer unsubscribe()
	// Subscription comes before the replay cut-off: anything committed while
	// replaying is queued, and duplicate overlap is removed by the cursor below.
	head, err := h.repo.NotificationHead(ctx, principal.UserID)
	if err != nil {
		write(c, nil, err)
		return
	}
	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache, no-transform")
	c.Header("X-Accel-Buffering", "no")
	writeMessage := func(message streamMessage, hasID bool) bool {
		if ctx.Err() != nil {
			return false
		}
		raw, err := json.Marshal(message.Data)
		if err != nil {
			return false
		}
		controller := http.NewResponseController(c.Writer)
		if err := controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return false
		}
		if hasID {
			if _, err = fmt.Fprintf(c.Writer, "id: %d\n", message.Sequence); err != nil {
				return false
			}
		}
		if _, err = fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", message.Event, raw); err != nil {
			return false
		}
		return controller.Flush() == nil
	}
	if raw == "" || cursor < head.PrunedThrough || cursor > head.LastSequence {
		reason := "expired"
		if raw == "" {
			reason = "initial"
		} else if cursor > head.LastSequence {
			reason = "ahead"
		}
		h.cache.Bump(ctx, principal.UserID)
		if !writeMessage(resetMessage(head.LastSequence, reason), true) {
			return
		}
		cursor = head.LastSequence
	} else {
		for cursor < head.LastSequence {
			events, err := h.repo.NotificationsAfter(ctx, principal.UserID, cursor, head.LastSequence, 100)
			if err != nil {
				return
			}
			if len(events) == 0 || events[0].Sequence != cursor+1 {
				h.cache.Bump(ctx, principal.UserID)
				if !writeMessage(resetMessage(head.LastSequence, "expired"), true) {
					return
				}
				cursor = head.LastSequence
				break
			}
			h.cache.Bump(ctx, principal.UserID)
			for _, event := range events {
				if event.Sequence != cursor+1 {
					return
				}
				if !writeMessage(changedMessage(event), true) {
					return
				}
				cursor = event.Sequence
			}
		}
	}
	if !writeMessage(streamMessage{Event: "sync.ready", Data: map[string]string{"cursor": strconv.FormatUint(cursor, 10)}}, false) {
		return
	}
	heartbeat := time.NewTicker(h.options.Heartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-client.done:
			return
		case message := <-client.events:
			if message.Sequence <= cursor {
				continue
			}
			if message.Event == "business.changed" && message.Sequence != cursor+1 {
				return
			}
			if !writeMessage(message, true) {
				return
			}
			cursor = message.Sequence
		case <-heartbeat.C:
			controller := http.NewResponseController(c.Writer)
			_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err := fmt.Fprint(c.Writer, ": heartbeat\n\n"); err != nil {
				return
			}
			if controller.Flush() != nil {
				return
			}
		}
	}
}
