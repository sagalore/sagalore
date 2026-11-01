package realtime

import (
	"math/rand"
	"time"
)

// Backoff is the wait before reconnect attempt n (1-based): 1s, 2s, 4s, …
// capped at 60s.
func Backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	shift := attempt - 1
	if shift > 6 {
		shift = 6
	}
	d := time.Second << shift
	if d > 60*time.Second {
		d = 60 * time.Second
	}
	return d
}

// Jitter spreads a backoff by ±20% so a fleet does not reconnect in lockstep.
// The result is never under 200ms.
func Jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return time.Second
	}
	span := int64(d) / 5
	if span < 1 {
		return d
	}
	delta := rand.Int63n(span*2+1) - span
	out := time.Duration(int64(d) + delta)
	if out < 200*time.Millisecond {
		return 200 * time.Millisecond
	}
	return out
}
