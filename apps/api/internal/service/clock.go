package service

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// systemClock implements port.Clock with the real system time. Tests inject a
// fake instead; production wires adapter.SystemClock.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

var _ port.Clock = systemClock{}

// newWorkerName returns a container name unique with overwhelming probability.
// It carries no meaning beyond identity: containers are referenced by ID in the
// UI, and the name only has to be unique (UNIQUE(name) on worker).
func newWorkerName(clock port.Clock) string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read failing is a fatal environment problem; fall back to the
		// clock so the call still returns a usable, time-varying name.
		return fmt.Sprintf("smm-%d", clock.Now().UnixNano())
	}
	return "smm-" + hex.EncodeToString(b[:])
}
