package util

import (
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestNewPidLoop(t *testing.T) {
	// GIVEN
	p, i, d := 1.0, 2.0, 3.0

	// WHEN
	pidLoop := NewPidLoop(p, i, d)

	// THEN
	assert.Equal(t, p, pidLoop.p)
	assert.Equal(t, i, pidLoop.i)
	assert.Equal(t, d, pidLoop.d)
}

func TestPidLoop_Advance(t *testing.T) {
	// GIVEN
	p, i, d := 1.0, 2.0, 3.0
	pidLoop := NewPidLoop(p, i, d)

	// WHEN
	output := pidLoop.Loop(10.0, 5.0)
	// THEN
	assert.Equal(t, 0.0, output)

	// WHEN
	output = pidLoop.Loop(10.0, 5.0)
	// THEN
	assert.Equal(t, 5, int(output))
}

func TestPidLoop_P(t *testing.T) {
	// GIVEN
	p, i, d := 0.01, 0.0, 0.0
	pidLoop := NewPidLoop(p, i, d)

	// WHEN
	output := pidLoop.Loop(10.0, 5.0)
	// THEN
	assert.Equal(t, 0.0, output)

	time.Sleep(1 * time.Second)

	// WHEN
	output = pidLoop.Loop(10.0, 5.0)
	// THEN
	assert.Equal(t, 0.05, output)
}

func TestPidLoop_I(t *testing.T) {
	// GIVEN
	p, i, d := 0.0, 0.01, 0.0
	pidLoop := NewPidLoop(p, i, d)

	// WHEN
	output := pidLoop.Loop(10.0, 5.0)
	// THEN
	assert.Equal(t, 0.0, output)

	time.Sleep(1 * time.Second)

	// WHEN
	output = pidLoop.Loop(10.0, 5.0)
	// THEN
	assert.InDelta(t, 0.05, output, 0.01)
}

func TestPidLoop_D(t *testing.T) {
	// GIVEN
	p, i, d := 0.0, 0.0, 0.01
	pidLoop := NewPidLoop(p, i, d)

	// WHEN
	output := pidLoop.Loop(10.0, 5.0)
	// THEN
	assert.Equal(t, 0.0, output)

	time.Sleep(1 * time.Second)

	// WHEN
	output = pidLoop.Loop(10.0, 8.0)
	// THEN
	assert.InDelta(t, -0.03, output, 0.01)
}
