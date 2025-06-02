package controller

import (
	"errors"
	"github.com/markusressel/fan2go/internal/control_loop"
	"testing"
	"time"

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
	Value int
}

func (c MockCurve) GetId() string {
	return c.ID
}

func (c MockCurve) Evaluate() (value int, err error) {
	return c.Value, nil
}

func (c MockCurve) CurrentValue() int {
	return c.Value
}

type MockFan struct {
	ID              string
	ControlMode     fans.ControlMode
	PWM             int
	MinPWM          int
	RPM             int
	curveId         string
	shouldNeverStop bool
	speedCurve      *map[int]float64
	PwmMap          *map[int]int
}

func (fan MockFan) GetStartPwm() int {
	return 0
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
	return fans.MaxPwmValue
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

func (fan MockFan) IsPwmAuto() (bool, error) {
	panic("implement me")
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

func (p mockPersistence) LoadFanPwmMap(fanId string) (map[int]int, error) {
	if p.hasPwmMap {
		pwmMap := map[int]int{}
		return pwmMap, nil
	} else {
		return nil, errors.New("no pwm map found")
	}
}
func (p mockPersistence) SaveFanPwmMap(fanId string, pwmMap map[int]int) (err error) { return nil }
func (p mockPersistence) DeleteFanPwmMap(fanId string) (err error)                   { return nil }

func createOneToOnePwmMap() map[int]int {
	var pwmMap = map[int]int{}
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

	curveValue := 127
	curve := &MockCurve{
		ID:    "curve",
		Value: curveValue,
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
		pwmMap:      createOneToOnePwmMap(),
	}
	controller.updateDistinctPwmValues()

	// WHEN
	optimal, err := controller.calculateTargetPwm()

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, 127, optimal)
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

	curveValue := 0
	curve := &MockCurve{
		ID:    "curve",
		Value: curveValue,
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
		pwmMap:      createOneToOnePwmMap(),
	}
	controller.updateDistinctPwmValues()

	// WHEN
	target, err := controller.calculateTargetPwm()

	// THEN
	assert.NoError(t, err)
	assert.Greater(t, fan.GetMinPwm(), 0)
	assert.Equal(t, 0, target)
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

	curveValue := 5
	curve := &MockCurve{
		ID:    "curve",
		Value: curveValue,
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

	pwmMap := map[int]int{
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
		pwmMap:      pwmMap,
	}
	controller.updateDistinctPwmValues()

	// WHEN
	targetPwm, err := controller.calculateTargetPwm()

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, 5, targetPwm)

	closestTarget := controller.findClosestDistinctTarget(targetPwm)
	assert.Equal(t, 1, closestTarget)
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

	expectedPwmMap := map[int]int{}
	for i := 0; i <= 255; i++ {
		expectedPwmMap[i] = i
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
	assert.Equal(t, expectedPwmMap, controller.pwmMap)
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

	expectedPwmMap := map[int]int{}
	for i := 0; i <= 255; i++ {
		expectedPwmMap[i] = i
	}

	controller := DefaultFanController{
		persistence: mockPersistence{
			hasPwmMap: false,
		},
		fan:        fan,
		updateRate: time.Duration(100),
		pwmMap:     userDefinedPwmMap,
	}
	controller.updateDistinctPwmValues()

	// WHEN
	err := controller.computePwmMap()

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, userDefinedPwmMap, controller.pwmMap)
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

	expectedPwmMap := map[int]int{}
	for i := 0; i <= 255; i++ {
		expectedPwmMap[i] = i
	}

	controller := DefaultFanController{
		persistence: mockPersistence{
			hasPwmMap: false,
		},
		fan:        fan,
		updateRate: time.Duration(100),
		pwmMap:     expectedPwmMap,
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
	}
	fans.RegisterFan(fan)

	controller := DefaultFanController{
		persistence: mockPersistence{
			hasPwmMap: false,
		},
		fan:        fan,
		updateRate: time.Duration(100),
		pwmMap:     PwmMapForFanWithLimitedRange,
	}
	err := controller.computeFanSpecificMappings()
	assert.NoError(t, err)

	// WHEN
	err = controller.setPwm(100)

	// THEN
	assert.NoError(t, err)
	assert.Equal(t, 39, fan.PWM)
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

func (fan MockFanWithOffsetPwm) IsPwmAuto() (bool, error) {
	panic("implement me")
}

func (fan MockFanWithOffsetPwm) GetId() string {
	return fan.ID
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
		pwmMap:     nil,
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
