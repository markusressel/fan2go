package controller

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/curves"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
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
}

func (fan MockFan) GetStartPwm() int {
	return 0
}

func (fan *MockFan) SetStartPwm(pwm int) {
	panic("not supported")
}

func (fan MockFan) GetMinPwm() int {
	return fan.MinPWM
}

func (fan *MockFan) SetMinPwm(pwm int) {
	fan.MinPWM = pwm
}

func (fan MockFan) GetMaxPwm() int {
	return fans.MaxPwmValue
}

func (fan *MockFan) SetMaxPwm(pwm int) {
	panic("not supported")
}

func (fan MockFan) GetRpm() int {
	return fan.RPM
}

func (fan MockFan) GetRpmAvg() float64 {
	return float64(fan.RPM)
}

func (fan *MockFan) SetRpmAvg(rpm float64) {
	panic("not supported")
}

func (fan MockFan) GetPwm() (result int) {
	return fan.PWM
}

func (fan *MockFan) SetPwm(pwm int) (err error) {
	fan.PWM = pwm
	return nil
}

func (fan MockFan) GetFanCurveData() *map[int]float64 {
	panic("implement me")
}

func (fan *MockFan) AttachFanCurveData(curveData *map[int]float64) (err error) {
	panic("implement me")
}

func (fan MockFan) GetPwmEnabled() (int, error) {
	panic("implement me")
}

func (fan *MockFan) SetPwmEnabled(value int) (err error) {
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

func (fan MockFan) Supports(feature int) bool {
	return true
}

var (
	LinearFan = map[int]float64{
		0:   0.0,
		255: 255.0,
	}

	NeverStoppingFan = map[int]float64{
		0:   50.0,
		50:  50.0,
		255: 255.0,
	}

	CappedFan = map[int]float64{
		0:   0.0,
		1:   0.0,
		2:   0.0,
		3:   0.0,
		4:   0.0,
		5:   0.0,
		6:   20.0,
		200: 200.0,
	}

	CappedNeverStoppingFan = map[int]float64{
		0:   50.0,
		50:  50.0,
		200: 200.0,
	}
)

type mockPersistence struct{}

func (p mockPersistence) SaveFanPwmData(fan fans.Fan) (err error) { return nil }
func (p mockPersistence) LoadFanPwmData(fan fans.Fan) (map[int]float64, error) {
	fanCurveDataMap := map[int]float64{}
	return fanCurveDataMap, nil
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
		PwmOutput: "fan1_output",
		RpmInput:  "fan1_rpm",
		StartPwm:  startPwm,
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
	}
	fans.FanMap[fan.GetId()] = fan

	controller := fanController{
		persistence: mockPersistence{},
		fan:         fan,
		curve:       curve,
		updateRate:  time.Duration(100),
	}
	// WHEN
	optimal, err := controller.calculateOptimalPwm(fan)
	if err != nil {
		assert.Fail(t, err.Error())
	}

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
	}
	fans.FanMap[fan.GetId()] = fan

	controller := fanController{
		persistence: mockPersistence{}, fan: fan,
		curve:      curve,
		updateRate: time.Duration(100),
	}

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
