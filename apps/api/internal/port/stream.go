package port

import "context"

// StreamPublisher pushes real-time frames to connected dashboards over SSE
// (ADR 0010). The concrete hub lives in adapter/; services depend on this port
// so the arch layering stays intact (service never imports http).
//
// Frames carry the full entity (ADR 0010 "Consequences") so the browser can
// reconcile without a second fetch. A nil publisher means "no dashboard wired";
// services must treat it as optional and never fail the write because of it.
type StreamPublisher interface {
	// Publish fans a frame out to every subscriber. It never blocks the caller
	// beyond a bounded buffer; a slow consumer is dropped, not waited on.
	Publish(ctx context.Context, kind string, payload []byte)
}

// StreamEvent is the wire shape of one SSE frame: an SSE `event:` line plus the
// JSON body for the `data:` line.
type StreamEvent struct {
	Kind string
	Body []byte
}

// Event kinds used across the app. The FE subscribes to these names.
const (
	EventAccountUpdated   = "account-updated"
	EventWorkerHealth     = "worker-health"
	EventProvisionUpdated = "provision-updated"
	EventActionUpdated    = "action-updated"
)
