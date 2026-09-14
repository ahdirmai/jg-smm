package adapter

import "time"

// SystemClock implements port.Clock using the real system time.
type SystemClock struct{}

// Now returns the current time.
func (SystemClock) Now() time.Time { return time.Now() }
