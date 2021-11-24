package internal

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

const (
	dbTestingPath = "./test.db"
)

type mockPersistence struct{}

func (p mockPersistence) SaveFanPwmData(fan Fan) (err error) { return nil }
func (p mockPersistence) LoadFanPwmData(fan Fan) (map[int][]float64, error) {
	fanCurveDataMap := map[int][]float64{}
	return fanCurveDataMap, nil
}

func TestWriteFan(t *testing.T) {
	// GIVEN
	persistence := NewPersistence(dbTestingPath)

	fan, _ := createFan(false, linearFan)

	// WHEN
	err := persistence.SaveFanPwmData(fan)

	// THEN
	assert.Nil(t, err)
}

func TestReadFan(t *testing.T) {
	// GIVEN
	persistence := NewPersistence(dbTestingPath)

	fan, _ := createFan(false, neverStoppingFan)
	err := persistence.SaveFanPwmData(fan)

	fan, _ = createFan(false, linearFan)

	// WHEN
	fanData, err := persistence.LoadFanPwmData(fan)

	// THEN
	assert.Nil(t, err)
	assert.NotNil(t, fanData)
}
