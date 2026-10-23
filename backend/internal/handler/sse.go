package handler

import (
	"agent-platform/backend/internal/agent/contracts"
	"agent-platform/backend/internal/agent/manager"
	"agent-platform/backend/internal/middleware"
	"agent-platform/backend/internal/model"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"net/http"
	"strconv"
	"time"
)

func (a *API) RunEvents(c *gin.Context) {
	ctx := c.Request.Context()
	s := middleware.Scope(c)
	id := c.Param("id")
	after := int64(0)
	raw := c.GetHeader("Last-Event-ID")
	if raw == "" {
		raw = c.Query("after")
	}
	if raw != "" {
		n, e := strconv.ParseInt(raw, 10, 64)
		if e != nil || n < 0 {
			failure(c, model.ErrInvalidInput)
			return
		}
		after = n
	}
	ch, stop, e := a.Manager.Subscribe(ctx, s, id)
	if e != nil {
		failure(c, e)
		return
	}
	defer stop()
	initial, e := a.Manager.Events(ctx, s, id, after)
	if e != nil {
		failure(c, e)
		return
	}
	cursor := manager.NewEventCursor(after)
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache, no-transform")
	c.Header("X-Accel-Buffering", "no")
	c.Status(200)
	controller := http.NewResponseController(c.Writer)
	defer controller.SetWriteDeadline(time.Time{})
	send := func(event contracts.Event) error {
		if !cursor.Accept(event) {
			return nil
		}
		b, e := json.Marshal(event)
		if e != nil {
			return e
		}
		_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if event.Seq > 0 {
			if _, e = fmt.Fprintf(c.Writer, "id: %d\n", event.Seq); e != nil {
				return e
			}
		}
		if _, e = fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event.Type, b); e != nil {
			return e
		}
		c.Writer.Flush()
		return nil
	}
	for _, event := range initial {
		if send(event) != nil {
			return
		}
	}
	if len(initial) == 0 {
		_, _ = fmt.Fprint(c.Writer, ": connected\n\n")
		c.Writer.Flush()
	}
	replay := func() error {
		for {
			events, e := a.Manager.Events(ctx, s, id, cursor.Seq())
			if e != nil {
				return e
			}
			for _, event := range events {
				if e = send(event); e != nil {
					return e
				}
			}
			if len(events) < 1000 {
				return nil
			}
		}
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			if event.Type == "sync" {
				if replay() != nil {
					return
				}
			} else if send(event) != nil {
				return
			}
		case <-ticker.C:
			cookie, _ := c.Cookie("session")
			if _, e = a.Auth.Authenticate(ctx, cookie); e != nil {
				return
			}
			if replay() != nil {
				return
			}
			_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, e = fmt.Fprint(c.Writer, ": heartbeat\n\n"); e != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}
func (a *API) Metrics(c *gin.Context) { c.JSON(200, a.Manager.Metrics()) }
