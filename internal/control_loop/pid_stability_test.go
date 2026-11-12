package control_loop

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAnalyzePidStability(t *testing.T) {
	def := DefaultPidConfig
	tests := []struct {
		name           string
		p, i, d        float64
		dt             time.Duration
		wantStable     bool
		wantOvershoots bool
	}{
		{name: "defaults at the default tick rate", p: def.P, i: def.I, d: def.D, dt: 200 * time.Millisecond, wantStable: true},
		{name: "defaults at 50ms", p: def.P, i: def.I, d: def.D, dt: 50 * time.Millisecond, wantStable: true},
		{name: "defaults at 500ms", p: def.P, i: def.I, d: def.D, dt: 500 * time.Millisecond, wantStable: true},
		// the warning threshold of 1 % overshoot is crossed just above 800ms
		{name: "defaults at 800ms", p: def.P, i: def.I, d: def.D, dt: 800 * time.Millisecond, wantStable: true},
		{name: "defaults at 820ms overshoot", p: def.P, i: def.I, d: def.D, dt: 820 * time.Millisecond, wantStable: true, wantOvershoots: true},
		{name: "defaults at 1s overshoot", p: def.P, i: def.I, d: def.D, dt: time.Second, wantStable: true, wantOvershoots: true},
		{name: "defaults at 2.5s overshoot", p: def.P, i: def.I, d: def.D, dt: 2500 * time.Millisecond, wantStable: true, wantOvershoots: true},
		{name: "defaults at 2.7s are unstable", p: def.P, i: def.I, d: def.D, dt: 2700 * time.Millisecond},
		{name: "defaults at 3s are unstable", p: def.P, i: def.I, d: def.D, dt: 3 * time.Second},
		// d/dt too large: the D term on the own output amplifies itself (pole below -1)
		{name: "defaults at 10ms are unstable", p: def.P, i: def.I, d: def.D, dt: 10 * time.Millisecond},
		{name: "defaults at 20ms are unstable", p: def.P, i: def.I, d: def.D, dt: 20 * time.Millisecond},
		{name: "defaults at 21ms", p: def.P, i: def.I, d: def.D, dt: 21 * time.Millisecond, wantStable: true},
		{name: "i = 0 is unstable", p: 0.1, i: 0, d: 0, dt: 200 * time.Millisecond},
		{name: "negative i is unstable", p: 0.05, i: -0.4, d: 0, dt: 200 * time.Millisecond},
		{name: "large i overshoots", p: 0.05, i: 4, d: 0, dt: 200 * time.Millisecond, wantStable: true, wantOvershoots: true},
		{name: "zero tick rate", p: def.P, i: def.I, d: def.D, dt: 0},
		// each fails only one of the Jury conditions
		{name: "pole below -1", p: 0.086, i: 3.248, d: 0.128, dt: 200 * time.Millisecond},
		{name: "product of the poles above 1", p: -0.745, i: 4.938, d: 0.223, dt: 200 * time.Millisecond},
		{name: "complex poles outside the unit circle", p: -0.606, i: 2.602, d: 0.05, dt: 200 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := AnalyzePidStability(tt.p, tt.i, tt.d, tt.dt)

			assert.Equal(t, tt.wantStable, result.Stable)
			assert.Equal(t, tt.wantOvershoots, result.Overshoots(), "overshoot %v", result.Overshoot)
		})
	}
}

// TestAnalyzePidStability_MatchesPidControlLoop checks the model of the analysis against the real loop
func TestAnalyzePidStability_MatchesPidControlLoop(t *testing.T) {
	def := DefaultPidConfig
	tests := []struct {
		name    string
		p, i, d float64
		dt      time.Duration
	}{
		{name: "defaults at 200ms", p: def.P, i: def.I, d: def.D, dt: 200 * time.Millisecond},
		{name: "defaults at 1s", p: def.P, i: def.I, d: def.D, dt: time.Second},
		{name: "defaults at 2s", p: def.P, i: def.I, d: def.D, dt: 2 * time.Second},
		{name: "defaults at 3s", p: def.P, i: def.I, d: def.D, dt: 3 * time.Second},
		{name: "defaults at 5ms", p: def.P, i: def.I, d: def.D, dt: 5 * time.Millisecond},
		{name: "defaults at 20ms", p: def.P, i: def.I, d: def.D, dt: 20 * time.Millisecond},
		{name: "defaults at 25ms", p: def.P, i: def.I, d: def.D, dt: 25 * time.Millisecond},
		{name: "p only", p: 0.1, i: 0, d: 0, dt: 200 * time.Millisecond},
		{name: "large p and d", p: 1.5, i: 0.4, d: 0.1, dt: 200 * time.Millisecond},
		{name: "large d", p: def.P, i: def.I, d: 0.08, dt: 200 * time.Millisecond},
		// d raises the overshoot from about 8 % to about 12 %
		{name: "large d at 1s", p: def.P, i: def.I, d: 0.3, dt: time.Second},
		{name: "pole below -1", p: 0.086, i: 3.248, d: 0.128, dt: 200 * time.Millisecond},
		{name: "product of the poles above 1", p: -0.745, i: 4.938, d: 0.223, dt: 200 * time.Millisecond},
		{name: "complex poles outside the unit circle", p: -0.606, i: 2.602, d: 0.05, dt: 200 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := AnalyzePidStability(tt.p, tt.i, tt.d, tt.dt)

			clock := &fakeNow{now: time.Unix(1000, 0)}
			loop := NewPidControlLoop(tt.p, tt.i, tt.d, tt.dt)
			// settle at 100, then step the target to 120
			const start, target = 100.0, 120.0
			loop.Cycle(clock.Now(), start)
			peak := start
			hitLimits := false
			last := start
			for k := 0; k < 2000; k++ {
				clock.Advance(tt.dt)
				out := loop.Cycle(clock.Now(), target)
				peak = max(peak, out)
				hitLimits = hitLimits || out <= 0 || out >= 255
				last = out
			}

			if result.Stable {
				assert.False(t, hitLimits, "a stable loop stays inside the output range")
				assert.InDelta(t, target, last, 0.5, "a stable loop converges")
				assert.InDelta(t, result.Overshoot, (peak-target)/(target-start), 0.01)
			} else {
				// unstable: the output doesn't settle at the target, it oscillates between the limits
				// or (without an I term) collapses
				assert.False(t, last > target-0.5 && last < target+0.5 && !hitLimits, "last output %v", last)
			}
		})
	}
}
