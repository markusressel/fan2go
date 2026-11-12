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

func TestPersistence_FanAnalysisInfo_ReadbackDelay(t *testing.T) {
	// GIVEN
	p, _ := newTempPersistence(t)
	delay := int64(300)
	info := FanAnalysisInfo{Quirk: "", QuirkVersion: 0, ReadbackDelayMillis: &delay}

	// WHEN
	require.NoError(t, p.SaveFanAnalysisInfo("cpu", info))
	loaded, err := p.LoadFanAnalysisInfo("cpu")

	// THEN
	require.NoError(t, err)
	require.NotNil(t, loaded.ReadbackDelayMillis)
	assert.Equal(t, int64(300), *loaded.ReadbackDelayMillis)
}

func TestPersistence_LoadFanAnalysisInfo_RecordWithoutReadbackDelay(t *testing.T) {
	// GIVEN: a record written before the readback delay was measured
	p, dbPath := newTempPersistence(t)
	putRaw(t, dbPath, BucketFanAnalysisInfo, "cpu", []byte(`{"quirk":"corsaircpro","quirkVersion":1}`))

	// WHEN
	loaded, err := p.LoadFanAnalysisInfo("cpu")

	// THEN: valid, the delay is not measured
	require.NoError(t, err)
	assert.Equal(t, FanAnalysisInfo{Quirk: "corsaircpro", QuirkVersion: 1}, loaded)
	assert.Nil(t, loaded.ReadbackDelayMillis)
}

func TestPersistence_SaveFanAnalysisInfo_WithoutReadbackDelay_OmitsTheField(t *testing.T) {
	p, dbPath := newTempPersistence(t)
	require.NoError(t, p.SaveFanAnalysisInfo("cpu", FanAnalysisInfo{Quirk: "corsaircpro", QuirkVersion: 1}))

	assert.Equal(t, `{"quirk":"corsaircpro","quirkVersion":1}`, string(getRaw(t, dbPath, BucketFanAnalysisInfo, "cpu")))
}
