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

func TestPersistence_FanAnalysisInfo_BreakawayPwm(t *testing.T) {
	// GIVEN
	p, dbPath := newTempPersistence(t)
	breakaway := 116
	info := FanAnalysisInfo{BreakawayPwm: &breakaway}

	// WHEN
	require.NoError(t, p.SaveFanAnalysisInfo("cpu", info))
	loaded, err := p.LoadFanAnalysisInfo("cpu")

	// THEN
	require.NoError(t, err)
	require.NotNil(t, loaded.BreakawayPwm)
	assert.Equal(t, 116, *loaded.BreakawayPwm)
	assert.Equal(t, `{"quirk":"","quirkVersion":0,"breakawayPwm":116}`, string(getRaw(t, dbPath, BucketFanAnalysisInfo, "cpu")))
}

func TestPersistence_LoadFanAnalysisInfo_RecordWithoutBreakawayPwm(t *testing.T) {
	// GIVEN: a record written before the breakaway PWM was measured
	p, dbPath := newTempPersistence(t)
	putRaw(t, dbPath, BucketFanAnalysisInfo, "cpu", []byte(`{"quirk":"","quirkVersion":0,"readbackDelayMillis":20}`))

	// WHEN
	loaded, err := p.LoadFanAnalysisInfo("cpu")

	// THEN: valid, the breakaway PWM and the kick start timing are not measured
	require.NoError(t, err)
	assert.Nil(t, loaded.BreakawayPwm)
	assert.Nil(t, loaded.TachIntervalMillis)
	assert.Nil(t, loaded.SpinUpMillis)
	require.NotNil(t, loaded.ReadbackDelayMillis)
}

func TestPersistence_FanAnalysisInfo_KickStartTiming(t *testing.T) {
	// GIVEN
	p, dbPath := newTempPersistence(t)
	breakaway := 116
	tach := int64(1000)
	spinUp := int64(1800)
	info := FanAnalysisInfo{BreakawayPwm: &breakaway, TachIntervalMillis: &tach, SpinUpMillis: &spinUp}

	// WHEN
	require.NoError(t, p.SaveFanAnalysisInfo("cpu", info))
	loaded, err := p.LoadFanAnalysisInfo("cpu")

	// THEN
	require.NoError(t, err)
	assert.Equal(t, info, loaded)
	assert.Equal(t, `{"quirk":"","quirkVersion":0,"breakawayPwm":116,"tachIntervalMillis":1000,"spinUpMillis":1800}`,
		string(getRaw(t, dbPath, BucketFanAnalysisInfo, "cpu")))
}

func TestPersistence_ReplaceFanAnalysisData(t *testing.T) {
	// GIVEN: saved data
	p, _ := newTempPersistence(t)
	require.NoError(t, p.SaveFanSetPwmToGetPwmMap("cpu", map[int]int{0: 0, 255: 255}))
	require.NoError(t, p.SaveFanPwmMap("cpu", make([]int, 256)))
	require.NoError(t, p.SaveFanAnalysisInfo("cpu", FanAnalysisInfo{Quirk: "old", QuirkVersion: 1}))
	delay := int64(12)
	pwmMap := make([]int, 256)
	pwmMap[255] = 255

	// WHEN: replaced with data without a setPwmToGetPwmMap
	err := p.ReplaceFanAnalysisData("cpu", FanAnalysisData{
		RpmCurve: map[int]float64{0: 0, 255: 1000},
		PwmMap:   pwmMap,
		Info:     &FanAnalysisInfo{ReadbackDelayMillis: &delay},
	})

	// THEN
	require.NoError(t, err)
	_, err = p.LoadFanSetPwmToGetPwmMap("cpu")
	assert.ErrorIs(t, err, os.ErrNotExist)
	loadedPwmMap, err := p.LoadFanPwmMap("cpu")
	require.NoError(t, err)
	assert.Equal(t, pwmMap, loadedPwmMap)
	info, err := p.LoadFanAnalysisInfo("cpu")
	require.NoError(t, err)
	assert.Equal(t, "", info.Quirk)
	assert.Equal(t, int64(12), *info.ReadbackDelayMillis)
}

func TestPersistence_ReplaceFanAnalysisData_InvalidPwmMap_ChangesNothing(t *testing.T) {
	p, _ := newTempPersistence(t)
	require.NoError(t, p.SaveFanAnalysisInfo("cpu", FanAnalysisInfo{Quirk: "old", QuirkVersion: 1}))

	err := p.ReplaceFanAnalysisData("cpu", FanAnalysisData{PwmMap: []int{1, 2}})

	assert.Error(t, err)
	info, err := p.LoadFanAnalysisInfo("cpu")
	require.NoError(t, err)
	assert.Equal(t, "old", info.Quirk)
}
