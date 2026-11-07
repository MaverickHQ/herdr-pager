package setup

import (
	"sync"
	"time"
)

// A fakeClock is the page's time in a test: it moves only when the test
// advances it, and then fires every timer that has come due, in order.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []fakeTimer
}

type fakeTimer struct {
	at time.Time
	ch chan time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- c.now
		return ch
	}
	c.timers = append(c.timers, fakeTimer{c.now.Add(d), ch})
	return ch
}

// waitTimer waits up to 5 s (real time) until a timer is due at at, as one
// set by After(d) at the moment at minus d.
func (c *fakeClock) waitTimer(at time.Time) bool {
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		c.mu.Lock()
		for _, t := range c.timers {
			if t.at.Equal(at) {
				c.mu.Unlock()
				return true
			}
		}
		c.mu.Unlock()
	}
	return false
}

// Advance moves the clock on by d and fires the timers due by then.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.timers[:0]
	for _, t := range c.timers {
		if t.at.After(c.now) {
			kept = append(kept, t)
		} else {
			t.ch <- t.at
		}
	}
	c.timers = kept
}
