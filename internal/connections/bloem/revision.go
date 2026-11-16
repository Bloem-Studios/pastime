package bloem

import "time"

// timeRevisionFloor separates the two revision domains Silo plugins send. Plugin
// 0.1.14 sent host sequence numbers (live maximum about 17,000); plugin 0.2.0 sends
// the occurrence time in Unix microseconds (about 1.8e15). Every 0.2.0 revision lies
// above the floor and every 0.1.14 revision below it.
const timeRevisionFloor = 1_000_000_000_000

// olderRevision reports whether an incoming source revision is older than the stored
// one, which makes the event stale. A sequence revision arriving after a time revision
// can only come from a return to plugin 0.1.14, whose changes all postdate what 0.2.0
// sent, so it counts as newer: otherwise every change of the returning plugin on an
// entity 0.2.0 touched would be stale and Silo would record it as delivered.
func olderRevision(incoming uint64, stored int64) bool {
	if stored >= timeRevisionFloor && incoming < timeRevisionFloor {
		return false
	}
	return stored > 0 && incoming < uint64(stored)
}

// completionClockMargin is how much later than a completion's occurrence a resume
// point may have been updated and still be cleared by it.
const completionClockMargin = 5 * time.Minute
