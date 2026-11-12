package persistence

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersistence_FanAnalysisInfo_SaveLoadDelete(t *testing.T) {
	// GIVEN
	p, _ := newTempPersistence(t)
	info := FanAnalysisInfo{Quirk: "corsaircpro", QuirkVersion: 1}

	// WHEN / THEN
	_, err := p.LoadFanAnalysisInfo("cpu")
	assert.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, p.DeleteFanAnalysisInfo("cpu"))

	require.NoError(t, p.SaveFanAnalysisInfo("cpu", info))
	loaded, err := p.LoadFanAnalysisInfo("cpu")
	require.NoError(t, err)
	assert.Equal(t, info, loaded)

	require.NoError(t, p.DeleteFanAnalysisInfo("cpu"))
	_, err = p.LoadFanAnalysisInfo("cpu")
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestPersistence_LoadFanAnalysisInfo_CorruptData(t *testing.T) {
	// GIVEN
	p, dbPath := newTempPersistence(t)
	putRaw(t, dbPath, BucketFanAnalysisInfo, "cpu", []byte("garbage"))

	// WHEN
	_, err := p.LoadFanAnalysisInfo("cpu")

	// THEN: reported as corrupt and deleted
	assert.ErrorIs(t, err, ErrCorruptData)
	_, err = p.LoadFanAnalysisInfo("cpu")
	assert.ErrorIs(t, err, os.ErrNotExist)
}
