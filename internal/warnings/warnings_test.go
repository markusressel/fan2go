package warnings

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStore_SetAndFor(t *testing.T) {
	s := NewStore()
	assert.Equal(t, []Warning{}, s.For(KindFan, "fan1"))
	assert.Equal(t, []Entry{}, s.All())

	s.Set(KindFan, "fan1", CodeFlatRpmCurve, "flat")
	s.Set(KindFan, "fan1", "other", "other warning")
	s.Set(KindSensor, "fan1", CodeFlatRpmCurve, "same id, other kind")

	assert.Equal(t, []Warning{
		{Code: CodeFlatRpmCurve, Message: "flat"},
		{Code: "other", Message: "other warning"},
	}, s.For(KindFan, "fan1"))
	assert.Equal(t, []Warning{{Code: CodeFlatRpmCurve, Message: "same id, other kind"}}, s.For(KindSensor, "fan1"))
	assert.Equal(t, []Warning{}, s.For(KindFan, "fan2"))
	assert.True(t, s.Has(KindFan, "fan1", CodeFlatRpmCurve))
	assert.False(t, s.Has(KindCurve, "fan1", CodeFlatRpmCurve))
}

func TestStore_SetTwiceKeepsOneEntry(t *testing.T) {
	s := NewStore()
	s.Set(KindFan, "fan1", CodeFlatRpmCurve, "first")
	s.Set(KindFan, "fan1", "other", "other warning")
	s.Set(KindFan, "fan1", CodeFlatRpmCurve, "second")

	// the message is replaced, the position is kept
	assert.Equal(t, []Entry{
		{Kind: KindFan, ID: "fan1", Code: CodeFlatRpmCurve, Message: "second"},
		{Kind: KindFan, ID: "fan1", Code: "other", Message: "other warning"},
	}, s.All())
}

func TestStore_Clear(t *testing.T) {
	s := NewStore()
	s.Set(KindFan, "fan1", CodeFlatRpmCurve, "flat")
	s.Set(KindFan, "fan2", CodeFlatRpmCurve, "flat")

	s.Clear(KindFan, "fan1", CodeFlatRpmCurve)
	// not set: no-op
	s.Clear(KindFan, "fan1", CodeFlatRpmCurve)
	s.Clear(KindCurve, "fan2", CodeFlatRpmCurve)

	assert.False(t, s.Has(KindFan, "fan1", CodeFlatRpmCurve))
	assert.Equal(t, []Warning{}, s.For(KindFan, "fan1"))
	assert.Equal(t, []Entry{{Kind: KindFan, ID: "fan2", Code: CodeFlatRpmCurve, Message: "flat"}}, s.All())
}

func TestStore_ReturnsCopies(t *testing.T) {
	s := NewStore()
	s.Set(KindFan, "fan1", CodeFlatRpmCurve, "flat")

	all := s.All()
	all[0].Message = "changed"
	forFan := s.For(KindFan, "fan1")
	forFan[0].Message = "changed"

	assert.Equal(t, "flat", s.All()[0].Message)
}

func TestStore_Nil(t *testing.T) {
	var s *Store
	s.Set(KindFan, "fan1", CodeFlatRpmCurve, "flat")
	s.Clear(KindFan, "fan1", CodeFlatRpmCurve)
	assert.False(t, s.Has(KindFan, "fan1", CodeFlatRpmCurve))
	assert.Equal(t, []Warning{}, s.For(KindFan, "fan1"))
	assert.Equal(t, []Entry{}, s.All())
}

// TestStore_Concurrency is meant to be run with -race.
func TestStore_Concurrency(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("fan%d", i%2)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				s.Set(KindFan, id, CodeFlatRpmCurve, fmt.Sprintf("message %d", j))
				_ = s.For(KindFan, id)
				_ = s.All()
				_ = s.Has(KindFan, id, CodeFlatRpmCurve)
				if j%3 == 0 {
					s.Clear(KindFan, id, CodeFlatRpmCurve)
				}
			}
			s.Set(KindFan, id, CodeFlatRpmCurve, "final")
		}()
	}
	wg.Wait()

	// still at most one entry per item and code
	assert.Len(t, s.All(), 2)
	assert.Equal(t, []Warning{{Code: CodeFlatRpmCurve, Message: "final"}}, s.For(KindFan, "fan0"))
}
