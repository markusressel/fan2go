package controller

import (
	"errors"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/control_loop"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/curves"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
)

type MockSensor struct {
	ID        string
	Name      string
	MovingAvg float64
}

func (sensor MockSensor) GetId() string {
	return sensor.ID
}

func (sensor MockSensor) GetLabel() string {
	return sensor.Name
}

func (sensor MockSensor) GetConfig() configuration.SensorConfig {
	panic("not implemented")
}

func (sensor MockSensor) GetValue() (result float64, err error) {
	return sensor.MovingAvg, nil
}

func (sensor MockSensor) GetMovingAvg() (avg float64) {
	return sensor.MovingAvg
}

func (sensor *MockSensor) SetMovingAvg(avg float64) {
	sensor.MovingAvg = avg
}

type MockCurve struct {
	ID    string
	Value *float64
}

func (c MockCurve) GetId() string {
	return c.ID
}

func (c MockCurve) Evaluate() (value float64, err error) {
	return *c.Value, nil
}

func (c MockCurve) CurrentValue() float64 {
	return *c.Value
}

type MockFan struct {
	ID                                           string
	ControlMode                                  fans.ControlMode
	PWM                                          int
	MinPWM                                       int
	MaxPWM                                       int
	RPM                                          int
	curveId                                      string
	shouldNeverStop                              bool
	sanityCheckFanModeChangedByThirdPartyEnabled bool
	useUnscaledCurveValues                       bool
	speedCurve                                   *map[int]float64
	PwmMap                                       *map[int]int
}

func (fan MockFan) GetStartPwm() int {
	return 0
}

func (fan *MockFan) GetLabel() string {
	return "Mock Fan " + fan.ID
}

func (fan *MockFan) GetIndex() int {
	return 1
}

func (fan *MockFan) SetStartPwm(pwm int, force bool) {
	panic("not supported")
}

func (fan MockFan) GetMinPwm() int {
	return fan.MinPWM
}

func (fan *MockFan) SetMinPwm(pwm int, force bool) {
	fan.MinPWM = pwm
}

func (fan MockFan) GetMaxPwm() int {
	if fan.MaxPWM == 0 { // test didn't set an explicit value, use default
		return fans.MaxPwmValue
	} else {
		return fan.MaxPWM
	}
}

func (fan *MockFan) SetMaxPwm(pwm int, force bool) {
	panic("not supported")
}

func (fan MockFan) GetRpm() (int, error) {
	return fan.RPM, nil
}

func (fan MockFan) GetRpmAvg() float64 {
	return float64(fan.RPM)
}

func (fan *MockFan) SetRpmAvg(rpm float64) {
	panic("not supported")
}

func (fan MockFan) GetPwm() (result int, err error) {
	return fan.PWM, nil
}

func (fan *MockFan) SetPwm(pwm int) (err error) {
	fan.PWM = pwm
	return nil
}

func (fan MockFan) GetFanRpmCurveData() *map[int]float64 {
	return fan.speedCurve
}

func (fan *MockFan) AttachFanRpmCurveData(curveData *map[int]float64) (err error) {
	fan.speedCurve = curveData
	return err
}

func (fan *MockFan) UpdateFanRpmCurveValue(pwm int, rpm float64) {
	if (fan.speedCurve) == nil {
		fan.speedCurve = &map[int]float64{}
	}
	(*fan.speedCurve)[pwm] = rpm
}

func (fan MockFan) GetControlMode() (fans.ControlMode, error) {
	return fan.ControlMode, nil
}

func (fan *MockFan) SetControlMode(value fans.ControlMode) (err error) {
	fan.ControlMode = value
	return nil
}

func (fan MockFan) GetId() string {
	return fan.ID
}

func (fan MockFan) GetName() string {
	return fan.ID
}

func (fan MockFan) GetCurveId() string {
	return fan.curveId
}

func (fan MockFan) ShouldNeverStop() bool {
	return fan.shouldNeverStop
}

func (fan MockFan) GetConfig() configuration.FanConfig {
	startPwm := 0
	maxPwm := fan.GetMaxPwm()
	return configuration.FanConfig{
		ID:                     fan.ID,
		Curve:                  fan.curveId,
		NeverStop:              fan.shouldNeverStop,
		StartPwm:               &startPwm,
		MinPwm:                 &fan.MinPWM,
		MaxPwm:                 &maxPwm,
		PwmMap:                 fan.PwmMap,
		UseUnscaledCurveValues: fan.useUnscaledCurveValues,
		HwMon:                  nil, // Not used in this mock
		File:                   nil, // Not used in this mock
		Cmd:                    nil, // Not used in this mock
		SanityCheck: configuration.SanityCheckConfig{
			FanModeChangedByThirdParty: configuration.FanModeChangedByThirdPartyConfig{
				Enabled: configuration.DefaultTrueBool{
					Optional: configuration.Optional[bool]{
						Value:   fan.sanityCheckFanModeChangedByThirdPartyEnabled,
						Present: true,
					},
				},
			},
			PwmValueChangedByThirdParty: configuration.PwmValueChangedByThirdPartyConfig{
				Enabled: configuration.DefaultTrueBool{
					Optional: configuration.Optional[bool]{
						Value:   true,
						Present: true,
					},
				},
			},
		},
	}
}

func (fan MockFan) SetConfig(config configuration.FanConfig) {
	// Not needed for this mock
}

func (fan MockFan) Supports(feature fans.FeatureFlag) bool {
	return true
}

var (
	PwmMapForFanWithLimitedRange = map[int]int{
		0:   0,
		3:   1,
		5:   2,
		8:   3,
		10:  4,
		13:  5,
		15:  6,
		18:  7,
		20:  8,
		23:  9,
		25:  10,
		28:  11,
		31:  12,
		33:  13,
		36:  14,
		38:  15,
		41:  16,
		43:  17,
		46:  18,
		48:  19,
		51:  20,
		54:  21,
		56:  22,
		59:  23,
		61:  24,
		64:  25,
		66:  26,
		69:  27,
		71:  28,
		74:  29,
		77:  30,
		79:  31,
		82:  32,
		85:  33,
		87:  34,
		90:  35,
		92:  36,
		95:  37,
		97:  38,
		100: 39,
		103: 40,
		105: 41,
		108: 42,
		110: 43,
		113: 44,
		116: 45,
		118: 46,
		121: 47,
		123: 48,
		126: 49,
		128: 50,
		131: 51,
		134: 52,
		136: 53,
		139: 54,
		141: 55,
		144: 56,
		147: 57,
		149: 58,
		152: 59,
		154: 60,
		157: 61,
		160: 62,
		162: 63,
		165: 64,
		167: 65,
		170: 66,
		172: 67,
		175: 68,
		178: 69,
		180: 70,
		183: 71,
		185: 72,
		188: 73,
		190: 74,
		193: 75,
		196: 76,
		198: 77,
		201: 78,
		203: 79,
		206: 80,
		208: 81,
		211: 82,
		214: 83,
		216: 84,
		219: 85,
		221: 86,
		224: 87,
		226: 88,
		229: 89,
		232: 90,
		234: 91,
		237: 92,
		239: 93,
		242: 94,
		244: 95,
		247: 96,
		250: 97,
		252: 98,
		255: 100,
	}

	LinearFan, _ = util.InterpolateLinearly(
		&map[int]float64{
			0:   0.0,
			255: 255.0,
		},
		0, 255,
	)

	NeverStoppingFan, _ = util.InterpolateLinearly(
		&map[int]float64{
			0:   50.0,
			50:  50.0,
			255: 255.0,
		},
		0, 255,
	)

	CappedFan, _ = util.InterpolateLinearly(
		&map[int]float64{
			0:   0.0,
			1:   0.0,
			2:   0.0,
			3:   0.0,
			4:   0.0,
			5:   0.0,
			6:   20.0,
			200: 200.0,
		},
		0, 255,
	)

	CappedNeverStoppingFan, _ = util.InterpolateLinearly(
		&map[int]float64{
			0:   50.0,
			50:  50.0,
			200: 200.0,
		},
		0, 255,
	)

	DutyCycleFan = map[int]float64{
		0:   0.0,
		50:  50.0,
		100: 50.0,
		200: 200.0,
	}
)

type mockPersistence struct {
	hasPwmMap       bool
	hasSavedPwmData bool
}

func (p mockPersistence) Init() (err error) { return nil }

func (p mockPersistence) SaveFanRpmData(fan fans.Fan) (err error) { return nil }
func (p mockPersistence) LoadFanRpmData(fan fans.Fan) (map[int]float64, error) {
	if p.hasSavedPwmData {
		fanCurveDataMap := map[int]float64{}
		return fanCurveDataMap, nil
	} else {
		return nil, errors.New("no pwm data found")
	}
}
func (p mockPersistence) DeleteFanRpmData(fan fans.Fan) (err error) { return nil }

func (p mockPersistence) LoadFanSetPwmToGetPwmMap(fanId string) (map[int]int, error) {
	if p.hasPwmMap {
		pwmMap := map[int]int{}
		return pwmMap, nil
	} else {
		return nil, errors.New("no pwm map found")
	}
}
func (p mockPersistence) SaveFanSetPwmToGetPwmMap(fanId string, pwmMap map[int]int) (err error) {
	return nil
}
func (p mockPersistence) DeleteFanSetPwmToGetPwmMap(fanId string) (err error) { return nil }

func (p mockPersistence) LoadFanPwmMap(fanId string) ([]int, error) {
	if p.hasPwmMap {
		pwmMap := make([]int, 256)
		return pwmMap, nil
	} else {
		return nil, errors.New("no pwm map found")
	}
}
func (p mockPersistence) SaveFanPwmMap(fanId string, pwmMap []int) (err error) { return nil }
func (p mockPersistence) DeleteFanPwmMap(fanId string) (err error)             { return nil }

func createOneToOnePwmMap() [256]int {
	var pwmMap = [256]int{}
	for i := fans.MinPwmValue; i <= fans.MaxPwmValue; i++ {
		pwmMap[i] = i
	}
	return pwmMap
}

func CreateFan(neverStop bool, curveData map[int]float64, startPwm *int) (fan fans.Fan, err error) {
	configuration.CurrentConfig.RpmRollingWindowSize = 10

	fan = &fans.HwMonFan{
		Config: configuration.FanConfig{
			ID: "fan1",
			HwMon: &configuration.HwMonFanConfig{
				Platform: "platform",
				Index:    1,
			},
			NeverStop: neverStop,
			Curve:     "curve",
			StartPwm:  startPwm,
		},
		StartPwm: startPwm,
	}
	fans.RegisterFan(fan)

	err = fan.AttachFanRpmCurveData(&curveData)

	return fan, err
}

func TestLinearFan(t *testing.T) {
	// GIVEN
	fan, _ := CreateFan(false, LinearFan, nil)

	// WHEN
	startPwm, maxPwm := fans.ComputePwmBoundaries(fan)

	// THEN
	assert.Equal(t, 1, startPwm)
	assert.Equal(t, 255, maxPwm)
}

func TestNeverStoppingFan(t *testing.T) {
	// GIVEN
	fan, _ := CreateFan(false, NeverStoppingFan, nil)

	// WHEN
	startPwm, maxPwm := fans.ComputePwmBoundaries(fan)

	// THEN
	assert.Equal(t, 0, startPwm)
	assert.Equal(t, 255, maxPwm)
}

func TestCappedFan(t *testing.T) {
	// GIVEN
	fan, _ := CreateFan(false, CappedFan, nil)

	// WHEN
	startPwm, maxPwm := fans.ComputePwmBoundaries(fan)

	// THEN
	assert.Equal(t, 6, startPwm)
	assert.Equal(t, 200, maxPwm)
}

func TestCappedNeverStoppingFan(t *testing.T) {
	// GIVEN
	fan, _ := CreateFan(false, CappedNeverStoppingFan, nil)

	// WHEN
	startPwm, maxPwm := fans.ComputePwmBoundaries(fan)

	// THEN
	assert.Equal(t, 0, startPwm)
	assert.Equal(t, 200, maxPwm)
}

func TestCalculateTargetSpeedLinear(t *testing.T) {
	// GIVEN
	avgTmp := 50000.0
	s := MockSensor{
		ID:        "sensor",
		Name:      "sensor",
		MovingAvg: avgTmp,
	}
	sensors.RegisterSensor(&s)

	curveValue := 127.0
	curve := &MockCurve{
		ID:    "curve",
		Value: &curveValue,
	}
	curves.RegisterSpeedCurve(curve)

	fan := &MockFan{
		ID:              "fan",
		PWM:             0,
		shouldNeverStop: false,
		curveId:         curve.GetId(),
		speedCurve:      &LinearFan,
	}
	fans.RegisterFan(fan)

	controlLoop := control_loop.NewDirectControlLoop(nil)

	controller := DefaultFanController{
		persistence: mockPersistence{},
		fan:         fan,
		curve:       curve,
		updateRate:  time.Duration(100),
		controlLoop: controlLoop,
		pwmMapping:  createOneToOnePwmMap(),
	}
	controller.updateDistinctPwmValues()

	// WHEN
	optimal, err := controller.calculateTargetSpeed()

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, 127.0, optimal)
}

func TestCalculateTargetSpeedNeverStop(t *testing.T) {
	// GIVEN
	avgTmp := 40000.0

	s := &MockSensor{
		ID:        "sensor",
		Name:      "sensor",
		MovingAvg: avgTmp,
	}
	sensors.RegisterSensor(s)

	curveValue := 0.0
	curve := &MockCurve{
		ID:    "curve",
		Value: &curveValue,
	}
	curves.RegisterSpeedCurve(curve)

	fan := &MockFan{
		ID:              "fan",
		PWM:             0,
		RPM:             100,
		MinPWM:          10,
		curveId:         curve.GetId(),
		shouldNeverStop: true,
		speedCurve:      &NeverStoppingFan,
	}
	fans.RegisterFan(fan)

	controlLoop := control_loop.NewDirectControlLoop(nil)

	controller := DefaultFanController{
		persistence: mockPersistence{},
		fan:         fan,
		curve:       curve,
		updateRate:  time.Duration(100),
		controlLoop: controlLoop,
		pwmMapping:  createOneToOnePwmMap(),
	}
	controller.updateDistinctPwmValues()

	// WHEN
	target, err := controller.calculateTargetSpeed()

	// THEN
	assert.NoError(t, err)
	assert.Greater(t, fan.GetMinPwm(), 0)
	assert.Equal(t, 0.0, target)
}

func TestFanWithStartPwmConfig(t *testing.T) {
	// GIVEN
	startPwm := 50
	fan, _ := CreateFan(false, LinearFan, &startPwm)

	// WHEN
	newStartPwm, maxPwm := fans.ComputePwmBoundaries(fan)

	// THEN
	assert.Equal(t, startPwm, newStartPwm)
	assert.Equal(t, 255, maxPwm)
}

func TestFanController_ComputePwmBoundaries_FanCurveGaps(t *testing.T) {
	// GIVEN
	fan, _ := CreateFan(false, DutyCycleFan, nil)

	// WHEN
	startPwm, maxPwm := fans.ComputePwmBoundaries(fan)

	// THEN
	assert.Equal(t, 50, startPwm)
	assert.Equal(t, 200, maxPwm)
}

func TestFanController_UpdateFanSpeed_FanCurveGaps(t *testing.T) {
	// GIVEN
	avgTmp := 40000.0

	s := &MockSensor{
		ID:        "sensor",
		Name:      "sensor",
		MovingAvg: avgTmp,
	}
	sensors.RegisterSensor(s)

	curveValue := 5.0
	curve := &MockCurve{
		ID:    "curve",
		Value: &curveValue,
	}
	curves.RegisterSpeedCurve(curve)

	fan := &MockFan{
		ID:              "fan",
		PWM:             0,
		RPM:             100,
		MinPWM:          50,
		curveId:         curve.GetId(),
		shouldNeverStop: true,
		speedCurve:      &DutyCycleFan,
	}
	fans.RegisterFan(fan)

	fan.PwmMap = &map[int]int{
		0:   0,
		1:   1,
		40:  40,
		58:  50,
		100: 120,
		222: 200,
		255: 255,
	}

	controlLoop := control_loop.NewDirectControlLoop(nil)

	controller := DefaultFanController{
		persistence: mockPersistence{},
		fan:         fan,
		curve:       curve,
		updateRate:  time.Duration(100),
		controlLoop: controlLoop,
	}
	comperr := controller.computePwmMap() // uses fan.PwmMap
	controller.updateDistinctPwmValues()

	// WHEN
	targetPwm, err := controller.calculateTargetSpeed()

	// THEN
	assert.NoError(t, comperr)
	assert.NoError(t, err)
	assert.Equal(t, 5.0, targetPwm)

	rawFanSpeed := controller.applyPwmMapToTarget(int(targetPwm))
	assert.Equal(t, 1, rawFanSpeed)
}

func TestFanController_ComputePwmMap_FullRange(t *testing.T) {
	// GIVEN
	fan := &MockFan{
		ID:              "fan",
		PWM:             0,
		RPM:             100,
		MinPWM:          50,
		shouldNeverStop: true,
		speedCurve:      &DutyCycleFan,
	}
	fans.RegisterFan(fan)

	expectedPwmMapping := [256]int{}
	for i := 0; i <= 255; i++ {
		expectedPwmMapping[i] = i
	}

	controller := DefaultFanController{
		persistence: mockPersistence{
			hasPwmMap: false,
		},
		fan:        fan,
		updateRate: time.Duration(100),
	}

	// WHEN
	err := controller.computePwmMap()

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, expectedPwmMapping, controller.pwmMapping)
}

func TestFanController_ComputePwmMap_UserOverride(t *testing.T) {
	// GIVEN
	userDefinedPwmMap := PwmMapForFanWithLimitedRange
	fan := &MockFan{
		ID:              "fan",
		PWM:             0,
		RPM:             100,
		MinPWM:          50,
		shouldNeverStop: true,
		speedCurve:      &LinearFan,
		PwmMap:          &userDefinedPwmMap,
	}
	fans.RegisterFan(fan)

	controller := DefaultFanController{
		persistence: mockPersistence{
			hasPwmMap: false,
		},
		fan:        fan,
		updateRate: time.Duration(100),
	}
	controller.updateDistinctPwmValues()

	// WHEN
	err := controller.computePwmMap()

	// THEN
	assert.NoError(t, err)

	// expected: step interpolation of user's pwmMap over [0..255]
	expectedExpanded, err := util.InterpolateStepInt(&userDefinedPwmMap, 0, 255)
	assert.NoError(t, err)
	for i := 0; i < 256; i++ {
		assert.Equal(t, expectedExpanded[i], controller.pwmMapping[i], "at index %d", i)
	}
}

func TestFanController_SetPwm(t *testing.T) {
	// GIVEN
	fan := &MockFan{
		ID:              "fan",
		PWM:             0,
		RPM:             100,
		MinPWM:          50,
		shouldNeverStop: true,
		speedCurve:      &LinearFan,
	}
	fans.RegisterFan(fan)

	controller := DefaultFanController{
		persistence: mockPersistence{
			hasPwmMap: false,
		},
		fan:        fan,
		updateRate: time.Duration(100),
	}
	err := controller.computeFanSpecificMappings()
	assert.NoError(t, err)

	// WHEN
	err = controller.setPwm(100)

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, 100, fan.PWM)
}

func TestFanController_SetPwm_UserOverridePwmMap(t *testing.T) {
	// GIVEN
	fan := &MockFan{
		ID:              "fan",
		PWM:             0,
		RPM:             100,
		MinPWM:          50,
		shouldNeverStop: true,
		speedCurve:      &LinearFan,
		PwmMap:          &PwmMapForFanWithLimitedRange,
	}
	fans.RegisterFan(fan)

	controller := DefaultFanController{
		persistence: mockPersistence{
			hasPwmMap: false,
		},
		fan:        fan,
		updateRate: time.Duration(100),
	}
	err := controller.computeFanSpecificMappings()
	assert.NoError(t, err)

	// WHEN
	err = controller.setPwm(100)

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, 39, fan.PWM)
}

func TestFanController_PwmMapping(t *testing.T) {
	// GIVEN
	fan := &MockFan{
		ID:              "fan",
		PWM:             0,
		RPM:             100,
		MinPWM:          50,
		shouldNeverStop: true,
		speedCurve:      &LinearFan,
		PwmMap: &map[int]int{
			0:   0,
			64:  1,
			128: 2,
		},
	}
	fans.RegisterFan(fan)

	controller := DefaultFanController{
		persistence: mockPersistence{
			hasPwmMap: false,
		},
		fan:        fan,
		updateRate: time.Duration(100),
	}

	// WHEN
	err := controller.computeFanSpecificMappings()

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, controller.pwmMapping[0], 0)
	assert.Equal(t, controller.pwmMapping[1], 0)
	assert.Equal(t, controller.pwmMapping[2], 0)
	assert.Equal(t, controller.pwmMapping[31], 0)
	assert.Equal(t, controller.pwmMapping[32], 0) // step: transition at key 64, not midpoint 32
	assert.Equal(t, controller.pwmMapping[33], 0) // step: transition at key 64, not midpoint 32
	assert.Equal(t, controller.pwmMapping[64], 1)
	assert.Equal(t, controller.pwmMapping[65], 1)
	assert.Equal(t, controller.pwmMapping[95], 1)
	assert.Equal(t, controller.pwmMapping[96], 1) // step: transition at key 128, not midpoint 96
	assert.Equal(t, controller.pwmMapping[97], 1) // step: transition at key 128, not midpoint 96
	assert.Equal(t, controller.pwmMapping[128], 2)
	assert.Equal(t, controller.pwmMapping[129], 2)
	assert.Equal(t, controller.pwmMapping[130], 2)
	assert.Equal(t, controller.pwmMapping[180], 2)
	assert.Equal(t, controller.pwmMapping[254], 2)
	assert.Equal(t, controller.pwmMapping[255], 2)
}

func tryUpdateFanSpeed(t *testing.T, controller *DefaultFanController) {
	err := controller.UpdateFanSpeed()
	assert.NoError(t, err)
}

func assertPwm(t *testing.T, expectedPWM int, fan fans.Fan) {
	pwm, err := fan.GetPwm()
	assert.NoError(t, err)
	assert.Equal(t, expectedPWM, pwm)
}

func TestFanController_PwmMapping2(t *testing.T) {
	// GIVEN
	fan := &MockFan{
		ID:              "fan",
		PWM:             0,
		RPM:             100,
		MinPWM:          1,
		MaxPWM:          3,
		shouldNeverStop: true,
		PwmMap: &map[int]int{
			0: 0,
			1: 1,
			2: 2,
			3: 3,
		},
	}
	fans.RegisterFan(fan)

	curveValue := 0.0

	controller := DefaultFanController{
		persistence: mockPersistence{
			hasPwmMap: false,
		},
		fan:        fan,
		updateRate: time.Duration(100),
		curve: MockCurve{
			ID:    "MC",
			Value: &curveValue,
		},
		controlLoop: control_loop.NewDirectControlLoop(nil),
	}

	// WHEN
	err := controller.computeFanSpecificMappings()

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, 0, controller.pwmMapping[0])
	assert.Equal(t, 1, controller.pwmMapping[1])
	assert.Equal(t, 2, controller.pwmMapping[2])
	assert.Equal(t, 3, controller.pwmMapping[3])
	assert.Equal(t, 3, controller.pwmMapping[4])
	assert.Equal(t, 3, controller.pwmMapping[255])

	// several WHEN/THENs
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 1, fan) // even if the curve returns 0, PWM should be 1 because shouldNeverStop is true

	curveValue = 20
	assert.Equal(t, 20.0, controller.curve.CurrentValue())
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 1, fan) // 20 is about at the beginning of the range from 0-255, so it should use speed 1

	curveValue = 120
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 2, fan) // 120 is about at the mid of the range from 0-255, so it should use speed 2

	curveValue = 210
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 3, fan) // 210 is at the end of the range from 0-255, so it should use speed 3
}

func TestFanController_PwmMapping3(t *testing.T) {
	// GIVEN
	fan := &MockFan{
		ID:              "fan",
		PWM:             0,
		RPM:             100,
		MinPWM:          1,
		MaxPWM:          3,
		shouldNeverStop: true,
	}
	fans.RegisterFan(fan)

	curveValue := 0.0

	controller := DefaultFanController{
		persistence: mockPersistence{
			hasPwmMap: false,
		},
		fan:        fan,
		updateRate: time.Duration(100),
		curve: MockCurve{
			ID:    "MC",
			Value: &curveValue,
		},
		controlLoop: control_loop.NewDirectControlLoop(nil),
	}

	// WHEN
	err := controller.computeFanSpecificMappings()

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, 0, controller.pwmMapping[0])
	assert.Equal(t, 1, controller.pwmMapping[1])
	assert.Equal(t, 2, controller.pwmMapping[2])
	assert.Equal(t, 3, controller.pwmMapping[3])
	// basically the same as before, except here pwmMapping[i] = i for all cases
	// because it's initialized to standard 1-to-1 mapping (as PwmMap wasn't set)
	// however all the actual PWM checks below behave just like in TestFanController_PwmMapping2
	assert.Equal(t, 4, controller.pwmMapping[4])
	assert.Equal(t, 255, controller.pwmMapping[255])

	// several WHEN/THENs
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 1, fan) // even if the curve returns 0, PWM should be 1 because shouldNeverStop is true

	curveValue = 20
	assert.Equal(t, 20.0, controller.curve.CurrentValue())
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 1, fan) // 20 is about at the beginning of the range from 0-255, so it should use speed 1

	curveValue = 120
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 2, fan) // 120 is about at the mid of the range from 0-255, so it should use speed 2

	curveValue = 210
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 3, fan) // 210 is at the end of the range from 0-255, so it should use speed 3
}

func TestFanController_PwmMapping4(t *testing.T) {
	// GIVEN
	fan := &MockFan{
		ID:              "fan",
		PWM:             0,
		RPM:             100,
		MinPWM:          1,
		MaxPWM:          3,
		shouldNeverStop: false, // this is different in this test
	}
	fans.RegisterFan(fan)

	curveValue := 0.0

	controller := DefaultFanController{
		persistence: mockPersistence{
			hasPwmMap: false,
		},
		fan:        fan,
		updateRate: time.Duration(100),
		curve: MockCurve{
			ID:    "MC",
			Value: &curveValue,
		},
		controlLoop: control_loop.NewDirectControlLoop(nil),
	}

	// WHEN
	err := controller.computeFanSpecificMappings()

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, 0, controller.pwmMapping[0])
	assert.Equal(t, 1, controller.pwmMapping[1])
	assert.Equal(t, 2, controller.pwmMapping[2])
	assert.Equal(t, 3, controller.pwmMapping[3])
	assert.Equal(t, 4, controller.pwmMapping[4])
	assert.Equal(t, 255, controller.pwmMapping[255])

	// several WHEN/THENs

	// ... this one is different in this test
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 0, fan) // if the curve returns 0, PWM should be 0 because shouldNeverStop is false

	// ... the remaining ones are the same as in the previous tests
	curveValue = 20
	assert.Equal(t, 20.0, controller.curve.CurrentValue())
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 1, fan) // 20 is about at the beginning of the range from 0-255, so it should use speed 1

	curveValue = 120
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 2, fan) // 120 is about at the mid of the range from 0-255, so it should use speed 2

	curveValue = 210
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 3, fan) // 210 is at the end of the range from 0-255, so it should use speed 3
}

func TestFanController_UseUnscaledCurveValues(t *testing.T) {
	// GIVEN
	fan := &MockFan{
		ID:                     "fan",
		PWM:                    0,
		RPM:                    100,
		MinPWM:                 20,
		MaxPWM:                 100,
		shouldNeverStop:        false,
		useUnscaledCurveValues: true,
	}
	fans.RegisterFan(fan)

	curveValue := 0.0

	controller := DefaultFanController{
		persistence: mockPersistence{
			hasPwmMap: false,
		},
		fan:        fan,
		updateRate: time.Duration(100),
		curve: MockCurve{
			ID:    "MC",
			Value: &curveValue,
		},
		controlLoop: control_loop.NewDirectControlLoop(nil),
	}

	// WHEN
	err := controller.computeFanSpecificMappings()

	// THEN
	assert.NoError(t, err)

	// several WHEN/THENs

	// curve value 0 means PWM 0
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 0, fan)

	curveValue = 20
	assert.Equal(t, 20.0, controller.curve.CurrentValue())
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 20, fan) // curve value is applied unmodified (if >= MinPWM)

	curveValue = 10
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 0, fan) // 10 < MinPWM (20) so it's set to 0 by UpdateFanSpeed()

	curveValue = 12
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 0, fan) // ... same for 12

	curveValue = 30
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 30, fan) // 30 > MinPwm (20) so it's applied unmodified

	curveValue = 42.6
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 43, fan) // non-integers value from curve should be rounded to nearest int

	curveValue = 98
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 98, fan)

	curveValue = 100
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 100, fan)

	// MaxPWM is 100 - bigger curve values are still passed on to the fan as they are,
	// but real fan implementations might clamp to MaxPWM (MockFan doesn't)
	curveValue = 150
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 150, fan)
}

func TestFanController_UseScaledCurveValues(t *testing.T) {
	// same as before but with useUnscaledCurveValues = false (and thus different expected PWM values)
	// GIVEN
	fan := &MockFan{
		ID:                     "fan",
		PWM:                    0,
		RPM:                    100,
		MinPWM:                 20,
		MaxPWM:                 100,
		shouldNeverStop:        false,
		useUnscaledCurveValues: false,
	}
	fans.RegisterFan(fan)

	curveValue := 0.0

	controller := DefaultFanController{
		persistence: mockPersistence{
			hasPwmMap: false,
		},
		fan:        fan,
		updateRate: time.Duration(100),
		curve: MockCurve{
			ID:    "MC",
			Value: &curveValue,
		},
		controlLoop: control_loop.NewDirectControlLoop(nil),
	}

	// WHEN
	err := controller.computeFanSpecificMappings()

	// THEN
	assert.NoError(t, err)

	// several WHEN/THENs

	// curve value 0 means PWM 0 (except if shouldNeverStop = true, then it's MinPWM)
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 0, fan)

	// curve speed values between 1 and 255 are translated
	// to PWM values between MinPwm (20) and MaxPwm (100)

	// curve value 1 always translates to MinPWM
	curveValue = 1
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 20, fan)

	// curve value 255 always translates to MaxPWM
	curveValue = 255
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 100, fan)

	curveValue = 25
	assert.Equal(t, 25.0, controller.curve.CurrentValue())
	tryUpdateFanSpeed(t, &controller)
	// Example calculation for curve value 25, MinPWM 20 and MaxPWM 100:
	//   (25 - 1) / (255 - 1) scaled to [20..100] # -1 because this starts at speed 1, not 0
	//   (24 / 254) * (100 - 20) + 20 = 27.559
	//   => rounded to integer it's 28
	assertPwm(t, 28, fan)

	curveValue = 10
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 23, fan) // 22.83 rounded up

	curveValue = 12
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 23, fan) // 23.46 rounded down

	curveValue = 30
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 29, fan) // 29.13 rounded down

	curveValue = 42.6
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 33, fan) // 33.1 rounded down

	curveValue = 98
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 51, fan) // 50.55 rounded up

	curveValue = 100
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 51, fan) // 51.18 rounded down

	curveValue = 150
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 67, fan) // 66.93 rounded up

	curveValue = 200
	tryUpdateFanSpeed(t, &controller)
	assertPwm(t, 83, fan) // 82.99 rounded up
}

func assertControlMode(t *testing.T, expectedMode fans.ControlMode, fan fans.Fan) {
	cm, err := fan.GetControlMode()
	assert.NoError(t, err)
	assert.Equal(t, expectedMode, cm)
}

func TestFanController_AlwaysSetPwmMode(t *testing.T) {
	fan := &MockFan{
		ID:              "fan",
		PWM:             0,
		RPM:             100,
		MinPWM:          1,
		MaxPWM:          3,
		shouldNeverStop: true,
		sanityCheckFanModeChangedByThirdPartyEnabled: true,
		// usually Run() in controller.go sets the control mode to PWM at startup,
		// for testing set it here in initialization
		ControlMode: fans.ControlModePWM,
	}
	fans.RegisterFan(fan)

	curveValue := 0.0

	controller := DefaultFanController{
		persistence: mockPersistence{
			hasPwmMap: false,
		},
		fan:        fan,
		updateRate: time.Duration(100),
		curve: MockCurve{
			ID:    "MC",
			Value: &curveValue,
		},
		controlLoop: control_loop.NewDirectControlLoop(nil),
	}

	err := controller.computeFanSpecificMappings()
	assert.NoError(t, err)

	assertControlMode(t, fans.ControlModePWM, controller.fan)

	tryUpdateFanSpeed(t, &controller)
	assertControlMode(t, fans.ControlModePWM, controller.fan) // should still be ControlModePWM after UpdateFanSpeed()

	_ = controller.fan.SetControlMode(fans.ControlModeAutomatic)
	assertControlMode(t, fans.ControlModeAutomatic, controller.fan)

	// UpdateFanSpeed() should reset the control mode to manual/ControlModePWM
	// because sanityCheckFanModeChangedByThirdPartyEnabled is true
	tryUpdateFanSpeed(t, &controller)
	assertControlMode(t, fans.ControlModePWM, controller.fan)
}

func TestFanController_AlwaysSetPwmModeDisabled(t *testing.T) {
	fan := &MockFan{
		ID:              "fan",
		PWM:             0,
		RPM:             100,
		MinPWM:          1,
		MaxPWM:          3,
		shouldNeverStop: true,
		sanityCheckFanModeChangedByThirdPartyEnabled: false,
		// usually Run() in controller.go sets the control mode to PWM at startup,
		// for testing set it here in initialization
		ControlMode: fans.ControlModePWM,
	}
	fans.RegisterFan(fan)

	curveValue := 0.0

	controller := DefaultFanController{
		persistence: mockPersistence{
			hasPwmMap: false,
		},
		fan:        fan,
		updateRate: time.Duration(100),
		curve: MockCurve{
			ID:    "MC",
			Value: &curveValue,
		},
		controlLoop: control_loop.NewDirectControlLoop(nil),
	}

	err := controller.computeFanSpecificMappings()
	assert.NoError(t, err)

	assertControlMode(t, fans.ControlModePWM, controller.fan)

	tryUpdateFanSpeed(t, &controller)
	assertControlMode(t, fans.ControlModePWM, controller.fan) // should still be ControlModePWM after UpdateFanSpeed()

	_ = controller.fan.SetControlMode(fans.ControlModeAutomatic)
	assertControlMode(t, fans.ControlModeAutomatic, controller.fan)

	// UpdateFanSpeed() should NOT reset the control mode to manual/ControlModePWM
	// because in this test sanityCheckFanModeChangedByThirdPartyEnabled is false
	tryUpdateFanSpeed(t, &controller)
	assertControlMode(t, fans.ControlModeAutomatic, controller.fan)
}

type MockFanWithOffsetPwm struct {
	ID              string
	ControlMode     fans.ControlMode
	PWM             int
	RPM             int
	MinPWM          int
	curveId         string
	shouldNeverStop bool
	speedCurve      *map[int]float64
	PwmMap          *map[int]int
}

func (fan MockFanWithOffsetPwm) GetStartPwm() int {
	return 0
}

func (fan *MockFanWithOffsetPwm) SetStartPwm(pwm int, force bool) {
	panic("not supported")
}

func (fan MockFanWithOffsetPwm) GetMinPwm() int {
	return fan.MinPWM
}

func (fan *MockFanWithOffsetPwm) SetMinPwm(pwm int, force bool) {
	fan.MinPWM = pwm
}

func (fan MockFanWithOffsetPwm) GetMaxPwm() int {
	return fans.MaxPwmValue
}

func (fan *MockFanWithOffsetPwm) SetMaxPwm(pwm int, force bool) {
	panic("not supported")
}

func (fan MockFanWithOffsetPwm) GetRpm() (int, error) {
	return fan.RPM, nil
}

func (fan MockFanWithOffsetPwm) GetRpmAvg() float64 {
	return float64(fan.RPM)
}

func (fan *MockFanWithOffsetPwm) SetRpmAvg(rpm float64) {
	panic("not supported")
}

func (fan MockFanWithOffsetPwm) GetPwm() (result int, err error) {
	// intentional offset of 1 to simulate a fan that reports a different PWM value than the one that was set by us
	return fan.PWM + 1, nil
}

func (fan *MockFanWithOffsetPwm) SetPwm(pwm int) (err error) {
	fan.PWM = pwm
	return nil
}

func (fan MockFanWithOffsetPwm) GetFanRpmCurveData() *map[int]float64 {
	return fan.speedCurve
}

func (fan *MockFanWithOffsetPwm) AttachFanRpmCurveData(curveData *map[int]float64) (err error) {
	fan.speedCurve = curveData
	return err
}

func (fan *MockFanWithOffsetPwm) UpdateFanRpmCurveValue(pwm int, rpm float64) {
	if (fan.speedCurve) == nil {
		fan.speedCurve = &map[int]float64{}
	}
	(*fan.speedCurve)[pwm] = rpm
}

func (fan MockFanWithOffsetPwm) GetControlMode() (fans.ControlMode, error) {
	return fan.ControlMode, nil
}

func (fan *MockFanWithOffsetPwm) SetControlMode(value fans.ControlMode) (err error) {
	fan.ControlMode = value
	return nil
}

func (fan MockFanWithOffsetPwm) GetId() string {
	return fan.ID
}

func (fan *MockFanWithOffsetPwm) GetLabel() string {
	return "Mock Fan " + fan.ID
}

func (fan *MockFanWithOffsetPwm) GetIndex() int {
	return 1
}

func (fan MockFanWithOffsetPwm) GetName() string {
	return fan.ID
}

func (fan MockFanWithOffsetPwm) GetCurveId() string {
	return fan.curveId
}

func (fan MockFanWithOffsetPwm) ShouldNeverStop() bool {
	return fan.shouldNeverStop
}

func (fan MockFanWithOffsetPwm) GetConfig() configuration.FanConfig {
	startPwm := 0
	maxPwm := fans.MaxPwmValue
	return configuration.FanConfig{
		ID:        fan.ID,
		Curve:     fan.curveId,
		NeverStop: fan.shouldNeverStop,
		StartPwm:  &startPwm,
		MinPwm:    &fan.MinPWM,
		MaxPwm:    &maxPwm,
		PwmMap:    fan.PwmMap,
		HwMon:     nil, // Not used in this mock
		File:      nil, // Not used in this mock
		Cmd:       nil, // Not used in this mock
	}
}

func (fan MockFanWithOffsetPwm) SetConfig(config configuration.FanConfig) {
	// Not needed for this mock
}

func (fan MockFanWithOffsetPwm) Supports(feature fans.FeatureFlag) bool {
	return true
}

func TestFanController_SetPwm_FanReportsDifferentPwmFromSetValue(t *testing.T) {
	// GIVEN
	fan := &MockFanWithOffsetPwm{
		ID:              "fan",
		PWM:             0,
		RPM:             100,
		MinPWM:          50,
		shouldNeverStop: true,
		speedCurve:      &LinearFan,
	}
	fans.RegisterFan(fan)

	controller := DefaultFanController{
		persistence: mockPersistence{
			hasPwmMap: false,
		},
		fan:        fan,
		updateRate: time.Duration(100),
	}
	err := controller.computeFanSpecificMappings()
	assert.NoError(t, err)

	// WHEN
	err = controller.setPwm(100)

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, 100, fan.PWM)

	reportedPwm, _ := fan.GetPwm()
	assert.Equal(t, 100+1, reportedPwm)

	/**
		Automatic detection
	1. Tests all PWM values X in [0, 255]
	  a. Setting the value X might fail, in that case this value is skipped
	  b. If setting the value X succeeds, it checks what PWM value Y is reported by the fan after setting the value X
	    i. an entry (X -> Y) is added to the pwmMap

	When applying a curve value to the fan:
	1. The controller calculates the target PWM value T based on the fan's speed curve
	2. The controller needs to find the closest PWM value X in the pwmMap
	3. The controller sets the PWM value X to the fan, which results in the fan reporting a PWM value Y
	4. When the controller lateron checks if the fan pwm value was changed by a third party it has to compare
	   the current PWM value reported by the fan (Y) with the Y value specified in the pwmMap for the X value that it has last set.
	*/

}
