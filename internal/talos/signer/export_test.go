package signer

import "time"

// SetTryLifetime sets the validity of the certificate Try signs, for a test of the connection
// bound, and returns the function that restores it.
func SetTryLifetime(d time.Duration) (restore func()) {
	old := tryLifetime
	tryLifetime = d
	return func() { tryLifetime = old }
}
