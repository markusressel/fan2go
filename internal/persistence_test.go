package internal

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

const (
	dbTestingPath = "./test.db"
)

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
