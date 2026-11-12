package util

import (
	"sync"
	"time"
)

// Clock is a minimal time source, so code that waits or measures time can be tested
// deterministically with a FakeClock instead of sleeping.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// After waits for the duration to elapse and then sends the current time on the returned channel.
	After(d time.Duration) <-chan time.Time
	// NewTicker returns a new Ticker that ticks with the given period.
	NewTicker(d time.Duration) Ticker
}

// Ticker is the subset of time.Ticker used by fan2go.
type Ticker interface {
	// C returns the channel on which the ticks are delivered.
	C() <-chan time.Time
	// Stop turns off the ticker.
	Stop()
}

// RealClock is the Clock backed by the time package.
var RealClock Clock = realClock{}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

func (realClock) NewTicker(d time.Duration) Ticker { return &realTicker{t: time.NewTicker(d)} }

type realTicker struct {
	t *time.Ticker
}

func (r *realTicker) C() <-chan time.Time { return r.t.C }

func (r *realTicker) Stop() { r.t.Stop() }

// FakeClock is a Clock for tests. Time only moves when Advance is called.
// Pending After channels and tickers fire when the time reaches their deadline.
type FakeClock struct {
	mu      sync.Mutex
	cond    *sync.Cond
	now     time.Time
	waiters []*fakeWaiter
	tickers []*fakeTicker
	// autoAdvance makes After advance the time by its duration and return immediately
	autoAdvance bool
}

type fakeWaiter struct {
	deadline time.Time
	ch       chan time.Time
}

type fakeTicker struct {
	clock  *FakeClock
	period time.Duration
	next   time.Time
	ch     chan time.Time
}

// NewFakeClock returns a FakeClock that starts at the given time.
func NewFakeClock(start time.Time) *FakeClock {
	c := &FakeClock{now: start}
	c.cond = sync.NewCond(&c.mu)
	return c
}

// NewAutoAdvancingFakeClock returns a FakeClock whose After advances the time by the requested
// duration and fires immediately. Use it for code that waits synchronously in the test goroutine
// (e.g. the fan analysis), where nobody else could call Advance.
func NewAutoAdvancingFakeClock(start time.Time) *FakeClock {
	c := NewFakeClock(start)
	c.autoAdvance = true
	return c
}

func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *FakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- c.now
		return ch
	}
	if c.autoAdvance {
		c.advanceLocked(d)
		ch <- c.now
		return ch
	}
	c.waiters = append(c.waiters, &fakeWaiter{deadline: c.now.Add(d), ch: ch})
	c.cond.Broadcast()
	return ch
}

// NewTicker returns a ticker driven by Advance. Like time.Ticker, it drops ticks
// if the receiver doesn't keep up. It panics if d <= 0, like time.NewTicker.
func (c *FakeClock) NewTicker(d time.Duration) Ticker {
	if d <= 0 {
		panic("non-positive interval for FakeClock.NewTicker")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTicker{clock: c, period: d, next: c.now.Add(d), ch: make(chan time.Time, 1)}
	c.tickers = append(c.tickers, t)
	c.cond.Broadcast()
	return t
}

func (t *fakeTicker) C() <-chan time.Time { return t.ch }

func (t *fakeTicker) Stop() {
	c := t.clock
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, other := range c.tickers {
		if other == t {
			c.tickers = append(c.tickers[:i], c.tickers[i+1:]...)
			break
		}
	}
	c.cond.Broadcast()
}

// Advance moves the time forward by d and fires all After channels and tickers that are due.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.advanceLocked(d)
}

func (c *FakeClock) advanceLocked(d time.Duration) {
	c.now = c.now.Add(d)

	remaining := c.waiters[:0]
	for _, w := range c.waiters {
		if !w.deadline.After(c.now) {
			w.ch <- c.now
		} else {
			remaining = append(remaining, w)
		}
	}
	// clear the tail so fired waiters can be garbage collected
	for i := len(remaining); i < len(c.waiters); i++ {
		c.waiters[i] = nil
	}
	c.waiters = remaining

	for _, t := range c.tickers {
		if t.next.After(c.now) {
			continue
		}
		select {
		case t.ch <- c.now:
		default:
			// drop the tick, like time.Ticker does for slow receivers
		}
		// skip all ticks up to now (the channel holds at most one tick anyway)
		missed := c.now.Sub(t.next)/t.period + 1
		t.next = t.next.Add(missed * t.period)
	}
	c.cond.Broadcast()
}

// Waiters returns the number of pending After channels plus the number of active tickers.
func (c *FakeClock) Waiters() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.waiters) + len(c.tickers)
}

// BlockUntil blocks until at least n After channels and tickers are pending (see Waiters).
// Tests use it to wait until the code under test is waiting on the clock before calling Advance.
// Note that an After channel stays pending until its deadline, even if the caller stopped
// waiting on it (e.g. because its context was cancelled).
func (c *FakeClock) BlockUntil(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.waiters)+len(c.tickers) < n {
		c.cond.Wait()
	}
}
