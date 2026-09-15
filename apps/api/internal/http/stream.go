package http

import (
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/adapter"
)

// keepaliveInterval bounds how often a comment frame is written so proxies do
// not consider an idle SSE connection dead (ADR 0010 "Confirmation": the client
// reconnects and refetches on drop).
const keepaliveInterval = 15 * time.Second

// StreamHandler serves GET /api/stream, the dashboard's real-time channel. It
// holds one open connection per browser tab; commands still go over REST.
type StreamHandler struct {
	hub *adapter.Hub
}

// NewStreamHandler wires the hub. hub may be nil, in which case the endpoint
// reports unavailable (the API boots before any dashboard connects).
func NewStreamHandler(hub *adapter.Hub) *StreamHandler {
	return &StreamHandler{hub: hub}
}

// Register mounts the SSE endpoint on the same auth+RBAC group as the other
// /api routes.
func (h *StreamHandler) Register(g *echo.Group) {
	g.GET("/stream", h.stream)
}

// stream writes SSE frames until the client disconnects or the server shuts.
func (h *StreamHandler) stream(c echo.Context) error {
	if h.hub == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "streaming unavailable")
	}

	resp := c.Response()
	w := resp.Writer
	r := c.Request()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	// X-Accel-Buffering tells nginx not to buffer the stream.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	events, unsub := h.hub.Subscribe()
	defer unsub()

	flusher, ok := w.(http.Flusher)
	if !ok {
		return echo.NewHTTPError(http.StatusInternalServerError, "streaming unsupported")
	}
	flusher.Flush()

	tick := time.NewTicker(keepaliveInterval)
	defer tick.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			// Client closed the tab or the server is draining.
			return nil
		case ev, open := <-events:
			if !open {
				// Hub dropped us (overflow). The browser EventSource reconnects.
				return nil
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Kind, ev.Body); err != nil {
				return nil // pipe broken; nothing worth reporting
			}
			flusher.Flush()
		case <-tick.C:
			// Comment frame: keeps the connection alive without emitting an event.
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return nil
			}
			flusher.Flush()
		}
	}
}
