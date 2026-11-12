package util

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func isReady(ch <-chan time.Time) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestFakeClock_After(t *testing.T) {
	// GIVEN
	start := time.Unix(1000, 0)
	clock := NewFakeClock(start)

	// WHEN
	ch := clock.After(time.Second)

	// THEN
	assert.Equal(t, 1, clock.Waiters())
	clock.Advance(999 * time.Millisecond)
	assert.False(t, isReady(ch))
	clock.Advance(time.Millisecond)
	assert.Equal(t, start.Add(time.Second), <-ch)
	assert.Equal(t, 0, clock.Waiters())
}

func TestFakeClock_AfterNonPositiveFiresImmediately(t *testing.T) {
	clock := NewFakeClock(time.Unix(1000, 0))
	assert.True(t, isReady(clock.After(0)))
	assert.True(t, isReady(clock.After(-time.Second)))
	assert.Equal(t, 0, clock.Waiters())
}

func TestFakeClock_Ticker(t *testing.T) {
	// GIVEN
	clock := NewFakeClock(time.Unix(1000, 0))
	ticker := clock.NewTicker(time.Second)
	assert.Equal(t, 1, clock.Waiters())

	// WHEN / THEN
	clock.Advance(500 * time.Millisecond)
	assert.False(t, isReady(ticker.C()))
	clock.Advance(500 * time.Millisecond)
	assert.True(t, isReady(ticker.C()))

	// ticks are dropped for slow receivers, like time.Ticker
	clock.Advance(5 * time.Second)
	assert.True(t, isReady(ticker.C()))
	assert.False(t, isReady(ticker.C()))

	// the next tick is aligned to the period (now at 6s, next tick at 7s)
	clock.Advance(999 * time.Millisecond)
	assert.False(t, isReady(ticker.C()))
	clock.Advance(time.Millisecond)
	assert.True(t, isReady(ticker.C()))

	// WHEN
	ticker.Stop()

	// THEN
	assert.Equal(t, 0, clock.Waiters())
	clock.Advance(time.Hour)
	assert.False(t, isReady(ticker.C()))
}

func TestFakeClock_TickerKeepsPeriodAfterUnalignedAdvance(t *testing.T) {
	// GIVEN
	clock := NewFakeClock(time.Unix(1000, 0))
	ticker := clock.NewTicker(time.Second)

	// WHEN: advance past the first tick, but not to the second one
	clock.Advance(1500 * time.Millisecond)
	assert.True(t, isReady(ticker.C()))

	// THEN: the second tick is due 2 s after the start, not 1 s after the last Advance
	clock.Advance(500 * time.Millisecond)
	assert.True(t, isReady(ticker.C()))
}

func TestFakeClock_NewTickerPanicsOnNonPositivePeriod(t *testing.T) {
	clock := NewFakeClock(time.Unix(1000, 0))
	assert.Panics(t, func() { clock.NewTicker(0) })
}

func TestFakeClock_BlockUntil(t *testing.T) {
	// GIVEN
	clock := NewFakeClock(time.Unix(1000, 0))
	done := make(chan struct{})
	go func() {
		<-clock.After(time.Second)
		close(done)
	}()

	// WHEN: wait until the goroutine waits on the clock, then advance
	clock.BlockUntil(1)
	clock.Advance(time.Second)

	// THEN
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("goroutine was not woken up by Advance")
	}
}

func TestFakeClock_AutoAdvance(t *testing.T) {
	// GIVEN
	start := time.Unix(1000, 0)
	clock := NewAutoAdvancingFakeClock(start)
	ticker := clock.NewTicker(time.Second)

	// WHEN
	ch := clock.After(1500 * time.Millisecond)

	// THEN: After returns immediately and the time moved on, firing due tickers
	assert.True(t, isReady(ch))
	assert.Equal(t, start.Add(1500*time.Millisecond), clock.Now())
	assert.True(t, isReady(ticker.C()))
}

func TestRealClock(t *testing.T) {
	before := time.Now()
	assert.False(t, RealClock.Now().Before(before))
	assert.True(t, isReadyWithin(RealClock.After(time.Millisecond), 5*time.Second))
	ticker := RealClock.NewTicker(time.Millisecond)
	defer ticker.Stop()
	assert.True(t, isReadyWithin(ticker.C(), 5*time.Second))
}

func isReadyWithin(ch <-chan time.Time, timeout time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(timeout):
		return false
	}
}
