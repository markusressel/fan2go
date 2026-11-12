package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/persistence"
	"github.com/markusressel/fan2go/internal/quirks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testQuirk is a known hardware quirks entry with a readback formula of 4 output levels and a hint.
var testQuirk = &quirks.Entry{
	ID:          "testdevice",
	Version:     2,
	Description: "Test device",
	Readback:    func(set int) int { return (set >> 6) * 85 },
	Hint:        "load the test driver with control=1",
}

// quirkInitTestFan is an initTestFan with a known hardware quirks entry.
type quirkInitTestFan struct {
	*initTestFan
	entry *quirks.Entry
}

func (fan *quirkInitTestFan) KnownQuirks() *quirks.Entry {
	return fan.entry
}

func newQuirkInitTestFan(log *writeLog, entry *quirks.Entry) *quirkInitTestFan {
	return &quirkInitTestFan{initTestFan: newInitTestFan("fan", log), entry: entry}
}

func TestComputeSetPwmToGetPwmMap_KnownReadbackFormula_DoesNotSweep(t *testing.T) {
	defer setUpInitTestConfig(true)()

	// GIVEN
	log := &writeLog{}
	fan := newQuirkInitTestFan(log, testQuirk)
	p := newFakePersistence()
	controller := newInitTestController(p, fan)

	// WHEN
	err := controller.computeSetPwmToGetPwmMap(context.Background())

	// THEN: the map is the formula, nothing is written to the fan or saved in the database
	require.NoError(t, err)
	assert.Empty(t, log.get())
	assert.Len(t, controller.setPwmToGetPwmMap, 256)
	assert.Equal(t, 0, controller.setPwmToGetPwmMap[63])
	assert.Equal(t, 85, controller.setPwmToGetPwmMap[64])
	assert.Equal(t, 255, controller.setPwmToGetPwmMap[255])
	assert.Empty(t, p.setPwmToGetPwmMaps)
}

func TestComputeSetPwmToGetPwmMap_KnownReadbackFormula_ExplicitConfigWins(t *testing.T) {
	tests := []struct {
		name              string
		setPwmToGetPwmMap *configuration.SetPwmToGetPwmMapConfig
		assumeIdentity    bool
		expectSweep       bool
		expectedAt64      int
	}{
		{"identity", &configuration.SetPwmToGetPwmMapConfig{Identity: &configuration.SetPwmToGetPwmMapIdentityConfig{}}, false, false, 64},
		{"linear", &configuration.SetPwmToGetPwmMapConfig{Linear: &configuration.SetPwmToGetPwmMapLinearConfig{0: 0, 255: 255}}, false, false, 64},
		// autodetect is the same as no setPwmToGetPwmMap: the formula is used
		{"explicit autodetect", &configuration.SetPwmToGetPwmMapConfig{Autodetect: &configuration.SetPwmToGetPwmMapAutodetectConfig{}}, false, false, 85},
		{"--assume-pwm-map-identity", nil, true, false, 64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer setUpInitTestConfig(true)()

			// GIVEN: the fan reports every written value (MockFan), unlike the formula
			log := &writeLog{}
			fan := newQuirkInitTestFan(log, testQuirk)
			fan.SetPwmToGetPwmMap = tt.setPwmToGetPwmMap
			p := newFakePersistence()
			controller := newInitTestController(p, fan)
			controller.assumePwmMapIdentity = tt.assumeIdentity

			// WHEN
			err := controller.computeSetPwmToGetPwmMap(context.Background())

			// THEN
			require.NoError(t, err)
			assert.Equal(t, tt.expectSweep, len(log.get()) > 0)
			assert.Equal(t, tt.expectedAt64, controller.setPwmToGetPwmMap[64])
		})
	}
}

func TestTakeManualControl_KnownQuirksHint_IsAddedToTheError(t *testing.T) {
	// GIVEN
	fan := &hintTestFan{restoreTestFan: newManualModeRejectingFan(fans.ControlModeAutomatic)}

	// WHEN
	err := takeManualControl(fan)

	// THEN
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pwm (1)")
	assert.Contains(t, err.Error(), "known hardware quirk 'testdevice': load the test driver with control=1")
}

func TestTakeManualControl_NoKnownQuirks_NoHint(t *testing.T) {
	err := takeManualControl(newManualModeRejectingFan(fans.ControlModeAutomatic))

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "known hardware quirk")
}

// hintTestFan is a restoreTestFan with the testQuirk entry.
type hintTestFan struct {
	*restoreTestFan
}

func (fan *hintTestFan) KnownQuirks() *quirks.Entry {
	return testQuirk
}

func TestRunInitializationIfNeeded_KnownQuirksChanged_AnalyzesAgain(t *testing.T) {
	tests := []struct {
		name          string
		entry         *quirks.Entry
		savedInfo     *persistence.FanAnalysisInfo
		expectAnalyze bool
	}{
		{"data from before the entry existed", testQuirk, nil, true},
		{"older version of the entry", testQuirk, &persistence.FanAnalysisInfo{Quirk: "testdevice", QuirkVersion: 1}, true},
		{"other entry", testQuirk, &persistence.FanAnalysisInfo{Quirk: "other", QuirkVersion: 2}, true},
		{"entry removed or disabled", nil, &persistence.FanAnalysisInfo{Quirk: "testdevice", QuirkVersion: 2}, true},
		{"entry without readback formula, data from before", &quirks.Entry{ID: "hintonly", Version: 1, Hint: "x"}, nil, false},
		{"same entry and version", testQuirk, &persistence.FanAnalysisInfo{Quirk: "testdevice", QuirkVersion: 2}, false},
		{"no entry, data from before", nil, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer setUpInitTestConfig(true)()

			// GIVEN: saved analysis data
			log := &writeLog{}
			fan := newQuirkInitTestFan(log, tt.entry)
			p := newFakePersistence()
			p.rpmData["fan"] = map[int]float64{0: 0, 255: 1000}
			p.setPwmToGetPwmMaps["fan"] = map[int]int{0: 0, 255: 255}
			p.pwmMaps["fan"] = make([]int, 256)
			if tt.savedInfo != nil {
				p.analysisInfos["fan"] = *tt.savedInfo
			}
			controller := newInitTestController(p, fan)

			// WHEN
			_, err := controller.runInitializationIfNeeded(context.Background())

			// THEN
			require.NoError(t, err)
			savedCurve := map[int]float64{0: 0, 255: 1000}
			if tt.expectAnalyze {
				// the fan has no RPM sensor, so the new analysis saves no curve
				assert.NotEqual(t, savedCurve, p.rpmData["fan"], "expected the fan to be analyzed again")
				id, version := tt.entry.Fingerprint()
				assert.Equal(t, id, p.analysisInfos["fan"].Quirk)
				assert.Equal(t, version, p.analysisInfos["fan"].QuirkVersion)
				// the old maps were deleted: the set->get map is the formula (not saved) or measured again,
				// and the pwm map is computed again (from the 256 keys of the set->get map)
				if tt.entry == nil {
					assert.Len(t, p.setPwmToGetPwmMaps["fan"], 256)
				} else {
					assert.NotContains(t, p.setPwmToGetPwmMaps, "fan")
				}
				assert.Equal(t, 255, p.pwmMaps["fan"][255])
			} else {
				assert.Equal(t, savedCurve, p.rpmData["fan"], "expected the saved data to be used")
				assert.Empty(t, log.get())
			}
		})
	}
}

func TestRunInitializationIfNeeded_AnalysisInfoPersistenceError_DoesNotInitialize(t *testing.T) {
	defer setUpInitTestConfig(true)()

	// GIVEN
	dbErr := errors.New("timeout")
	log := &writeLog{}
	fan := newQuirkInitTestFan(log, testQuirk)
	p := newFakePersistence()
	p.rpmData["fan"] = map[int]float64{0: 0, 255: 1000}
	p.loadAnalysisInfoErrors = []error{dbErr}
	controller := newInitTestController(p, fan)

	// WHEN
	_, err := controller.runInitializationIfNeeded(context.Background())

	// THEN: the data is kept, and the fan is not touched
	assert.ErrorIs(t, err, dbErr)
	assert.Empty(t, log.get())
	assert.True(t, p.hasRpmData("fan"))
}

func TestRunInitializationIfNeeded_CorruptAnalysisInfo_AnalyzesAgain(t *testing.T) {
	defer setUpInitTestConfig(true)()

	// GIVEN
	log := &writeLog{}
	fan := newQuirkInitTestFan(log, testQuirk)
	p := newFakePersistence()
	p.rpmData["fan"] = map[int]float64{0: 0, 255: 1000}
	p.loadAnalysisInfoErrors = []error{persistence.ErrCorruptData}
	controller := newInitTestController(p, fan)

	// WHEN
	_, err := controller.runInitializationIfNeeded(context.Background())

	// THEN
	require.NoError(t, err)
	assert.Equal(t, persistence.FanAnalysisInfo{Quirk: "testdevice", QuirkVersion: 2}, p.analysisInfos["fan"])
}

func TestRunInitializationIfNeeded_ReadbackFormulaOverriddenByConfig_NoNewAnalysis(t *testing.T) {
	defer setUpInitTestConfig(true)()

	// GIVEN: data from before the entry existed, but the config sets setPwmToGetPwmMap, so the formula isn't used
	log := &writeLog{}
	fan := newQuirkInitTestFan(log, testQuirk)
	fan.SetPwmToGetPwmMap = &configuration.SetPwmToGetPwmMapConfig{Identity: &configuration.SetPwmToGetPwmMapIdentityConfig{}}
	p := newFakePersistence()
	savedCurve := map[int]float64{0: 0, 255: 1000}
	p.rpmData["fan"] = savedCurve
	controller := newInitTestController(p, fan)

	// WHEN
	_, err := controller.runInitializationIfNeeded(context.Background())

	// THEN
	require.NoError(t, err)
	assert.Equal(t, savedCurve, p.rpmData["fan"])
	assert.Empty(t, log.get())
}

func TestComputeSetPwmToGetPwmMap_QuirksDisabled_ExplicitAutodetectSweeps(t *testing.T) {
	defer setUpInitTestConfig(true)()

	// GIVEN: no entry applies (e.g. quirks.enabled: false), setPwmToGetPwmMap: autodetect
	log := &writeLog{}
	fan := newQuirkInitTestFan(log, nil)
	fan.SetPwmToGetPwmMap = &configuration.SetPwmToGetPwmMapConfig{Autodetect: &configuration.SetPwmToGetPwmMapAutodetectConfig{}}
	controller := newInitTestController(newFakePersistence(), fan)

	// WHEN
	err := controller.computeSetPwmToGetPwmMap(context.Background())

	// THEN: the fan is swept
	require.NoError(t, err)
	assert.NotEmpty(t, log.get())
	assert.Equal(t, 64, controller.setPwmToGetPwmMap[64])
}

func TestRunInitialization_AssumePwmMapIdentity_SavesTheEntryFingerprint(t *testing.T) {
	defer setUpInitTestConfig(true)()

	// GIVEN: fan init --assume-pwm-map-identity on a fan with a readback formula
	log := &writeLog{}
	fan := newQuirkInitTestFan(log, testQuirk)
	p := newFakePersistence()
	initController := newInitTestController(p, fan)
	initController.assumePwmMapIdentity = true

	// WHEN
	_, err := initController.RunInitializationAndRestore(context.Background())
	require.NoError(t, err)

	// THEN: the identity was used for this run, but the record is the entry's,
	// so the daemon (which uses the formula) keeps the data
	assert.Equal(t, 64, initController.setPwmToGetPwmMap[64])
	assert.Equal(t, persistence.FanAnalysisInfo{Quirk: "testdevice", QuirkVersion: 2}, p.analysisInfos["fan"])
	savedCurve := p.rpmData["fan"]
	daemonController := newInitTestController(p, fan)
	_, err = daemonController.runInitializationIfNeeded(context.Background())
	require.NoError(t, err)
	assert.Contains(t, p.rpmData, "fan")
	assert.Equal(t, savedCurve, p.rpmData["fan"])
}

func TestRunInitialization_SaveRpmDataFails_NoAnalysisInfo(t *testing.T) {
	defer setUpInitTestConfig(true)()

	// GIVEN
	log := &writeLog{}
	fan := newQuirkInitTestFan(log, testQuirk)
	p := &failingRpmSavePersistence{fakePersistence: newFakePersistence()}
	controller := newInitTestController(p, fan)

	// WHEN
	_, _ = controller.RunInitializationAndRestore(context.Background())

	// THEN
	assert.NotContains(t, p.analysisInfos, "fan")
}

// failingRpmSavePersistence fails to save the RPM curve data.
type failingRpmSavePersistence struct {
	*fakePersistence
}

func (p *failingRpmSavePersistence) SaveFanRpmData(fans.Fan) error {
	return errors.New("disk full")
}

func (p *failingRpmSavePersistence) ReplaceFanAnalysisData(string, persistence.FanAnalysisData) error {
	return errors.New("disk full")
}
