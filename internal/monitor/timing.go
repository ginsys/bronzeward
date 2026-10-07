package monitor

import "time"

// Timings are the monitor's fixed PoC values (dependency monitor §6.1, last paragraph), held here
// in one place so that a later version can make them configurable. Tests pass shorter ones.
type Timings struct {
	// Interval is the time from one pass's start to the next's (§6.1, choice §11.4).
	Interval time.Duration
	// Request bounds one metadata request (§6.1 step 3).
	Request time.Duration
	// IdleSession is a pass session's idle-session timeout, so the server ends the session of a
	// process that stopped and releases its advisory lock (§6.1 step 1).
	IdleSession time.Duration
	// Persistent is the time after which an unknown dependency alerts, and again after each
	// further such time (§6.2, design §7.8, choice §11.6).
	Persistent time.Duration
	// LockHolder is the statement and idle-in-transaction timeout of every transaction that locks
	// the DependencyMonitor row (§6.3).
	LockHolder time.Duration
}

// Defaults are the PoC values.
func Defaults() Timings {
	return Timings{
		Interval:    60 * time.Second,
		Request:     10 * time.Second,
		IdleSession: 30 * time.Second,
		Persistent:  15 * time.Minute,
		LockHolder:  10 * time.Second,
	}
}

// next is the wait before the pass after one that started at start and ended at end: one interval
// after start, or none if the pass took that long (§6.1).
func next(start, end time.Time, interval time.Duration) time.Duration {
	if d := start.Add(interval).Sub(end); d > 0 {
		return d
	}
	return 0
}
