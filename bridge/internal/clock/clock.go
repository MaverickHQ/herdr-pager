// Package clock is the time the bridge runs on: the real one in serve and
// stdio, a fake one in tests.
package clock

import "time"

// A Clock tells the time and waits.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// Real is the system's clock.
type Real struct{}

func (Real) Now() time.Time                         { return time.Now() }
func (Real) After(d time.Duration) <-chan time.Time { return time.After(d) }
