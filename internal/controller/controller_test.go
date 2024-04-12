package controller

import (
	"sort"
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

type MockFan struct {
	ID              string
	PWM             int
	MinPWM          int
	RPM             int
	curveId         string
	shouldNeverStop bool
	speedCurve      *map[int]float64
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

func (fan MockFan) GetFanCurveData() *map[int]float64 {
	return fan.speedCurve
}

func (fan *MockFan) AttachFanCurveData(curveData *map[int]float64) (err error) {
	fan.speedCurve = curveData
	return err
}

func (fan MockFan) GetPwmEnabled() (int, error) {
	panic("implement me")
}

func (fan *MockFan) SetPwmEnabled(value fans.ControlMode) (err error) {
	panic("implement me")
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

func (fan MockFan) Supports(feature fans.FeatureFlag) bool {
	return true
}

var (
	LinearFan = util.InterpolateLinearly(
		&map[int]float64{
			0:   0.0,
			255: 255.0,
		},
		0, 255,
	)

	NeverStoppingFan = util.InterpolateLinearly(
		&map[int]float64{
			0:   50.0,
			50:  50.0,
			255: 255.0,
		},
		0, 255,
	)

	CappedFan = util.InterpolateLinearly(
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

	CappedNeverStoppingFan = util.InterpolateLinearly(
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

type mockPersistence struct{}

func (p mockPersistence) Init() (err error) { return nil }

func (p mockPersistence) SaveFanPwmData(fan fans.Fan) (err error) { return nil }
func (p mockPersistence) LoadFanPwmData(fan fans.Fan) (map[int]float64, error) {
	fanCurveDataMap := map[int]float64{}
	return fanCurveDataMap, nil
}
func (p mockPersistence) DeleteFanPwmData(fan fans.Fan) (err error) { return nil }

func (p mockPersistence) LoadFanPwmMap(fanId string) (map[int]int, error) {
	pwmMap := map[int]int{}
	return pwmMap, nil
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
	fans.FanMap[fan.GetId()] = fan

	err = fan.AttachFanCurveData(&curveData)

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
	sensors.SensorMap[s.GetId()] = &s

	curveValue := 127
	curve := MockCurve{
		ID:    "curve",
		Value: curveValue,
	}
	curves.SpeedCurveMap[curve.GetId()] = &curve

	fan := &MockFan{
		ID:              "fan",
		PWM:             0,
		shouldNeverStop: false,
		curveId:         curve.GetId(),
		speedCurve:      &LinearFan,
	}
	fans.FanMap[fan.GetId()] = fan

	controller := PidFanController{
		persistence: mockPersistence{},
		fan:         fan,
		curve:       curve,
		updateRate:  time.Duration(100),
		pwmMap:      createOneToOnePwmMap(),
	}
	controller.updateDistinctPwmValues()

	// WHEN
	optimal := controller.calculateTargetPwm()

	// THEN
	assert.Equal(t, 127, optimal)
}

func TestCalculateTargetSpeedNeverStop(t *testing.T) {
	// GIVEN
	avgTmp := 40000.0

	s := MockSensor{
		ID:        "sensor",
		Name:      "sensor",
		MovingAvg: avgTmp,
	}
	sensors.SensorMap[s.GetId()] = &s

	curveValue := 0
	curve := &MockCurve{
		ID:    "curve",
		Value: curveValue,
	}
	curves.SpeedCurveMap[curve.GetId()] = curve

	fan := &MockFan{
		ID:              "fan",
		PWM:             0,
		RPM:             100,
		MinPWM:          10,
		curveId:         curve.GetId(),
		shouldNeverStop: true,
		speedCurve:      &NeverStoppingFan,
	}
	fans.FanMap[fan.GetId()] = fan

	controller := PidFanController{
		persistence: mockPersistence{}, fan: fan,
		curve:      curve,
		updateRate: time.Duration(100),
		pwmMap:     createOneToOnePwmMap(),
	}
	controller.updateDistinctPwmValues()

	// WHEN
	target := controller.calculateTargetPwm()

	// THEN
	assert.Greater(t, fan.GetMinPwm(), 0)
	assert.Equal(t, fan.GetMinPwm(), target)
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

	s := MockSensor{
		ID:        "sensor",
		Name:      "sensor",
		MovingAvg: avgTmp,
	}
	sensors.SensorMap[s.GetId()] = &s

	curveValue := 5
	curve := &MockCurve{
		ID:    "curve",
		Value: curveValue,
	}
	curves.SpeedCurveMap[curve.GetId()] = curve

	fan := &MockFan{
		ID:              "fan",
		PWM:             0,
		RPM:             100,
		MinPWM:          50,
		curveId:         curve.GetId(),
		shouldNeverStop: true,
		speedCurve:      &DutyCycleFan,
	}
	fans.FanMap[fan.GetId()] = fan

	var keys []int
	for pwm := range DutyCycleFan {
		keys = append(keys, pwm)
	}
	sort.Ints(keys)

	pwmMap := map[int]int{
		0:   0,
		1:   1,
		40:  40,
		58:  50,
		100: 120,
		222: 200,
		255: 255,
	}

	controller := PidFanController{
		persistence: mockPersistence{}, fan: fan,
		curve:      curve,
		updateRate: time.Duration(100),
		pwmMap:     pwmMap,
	}
	controller.updateDistinctPwmValues()

	// WHEN
	targetPwm := controller.calculateTargetPwm()

	// THEN
	assert.Equal(t, 54, targetPwm)

	closestTarget := controller.findClosestDistinctTarget(targetPwm)
	assert.Equal(t, 58, closestTarget)
}
