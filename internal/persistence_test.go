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
	db := OpenPersistence(dbTestingPath)
	defer db.Close()

	fan, _ := createFan(false, linearFan)

	// WHEN
	err := SaveFanPwmData(db, fan)

	// THEN
	assert.Nil(t, err)
}

func TestReadFan(t *testing.T) {
	// GIVEN
	db := OpenPersistence(dbTestingPath)
	defer db.Close()

	fan, _ := createFan(false, neverStoppingFan)
	err := SaveFanPwmData(db, fan)

	fan, _ = createFan(false, linearFan)

	// WHEN
	fanData, err := LoadFanPwmData(db, fan)

	// THEN
	assert.Nil(t, err)
	assert.NotNil(t, fanData)
}
