package curves

import (
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

// helper function to create a pid curve configuration
func createPidCurveConfig(
	id string,
	sensorId string,
	setPoint float64,
	p float64,
	i float64,
	d float64,
) (curve configuration.CurveConfig) {
	curve = configuration.CurveConfig{
		ID: id,
		PID: &configuration.PidCurveConfig{
			Sensor:   sensorId,
			SetPoint: setPoint,
			P:        p,
			I:        i,
			D:        d,
		},
	}
	return curve
}

// proportional

func TestPidCurveProportionalBelowTarget(t *testing.T) {
	// GIVEN
	avgTmp := 50000.0

	s := MockSensor{
		Name:      "sensor",
		MovingAvg: avgTmp,
	}
	sensors.SensorMap[s.GetId()] = &s

	curveConfig := createPidCurveConfig(
		"curve",
		s.GetId(),
		60,
		-0.05,
		0,
		0,
	)
	curve, err := NewSpeedCurve(curveConfig)
	if err != nil {
		assert.Fail(t, err.Error())
	}

	for loopIdx, expected := range []int{0, 0, 0} {
		// WHEN
		result, err := curve.Evaluate()
		if err != nil {
			assert.Fail(t, err.Error())
		}

		// THEN
		assert.Equal(t, expected, result, "loop: %d", loopIdx)

		time.Sleep(200 * time.Millisecond)
	}
}

func TestPidCurveProportionalAboveTarget(t *testing.T) {
	// GIVEN
	avgTmp := 70000.0

	s := MockSensor{
		Name:      "sensor",
		MovingAvg: avgTmp,
	}
	sensors.SensorMap[s.GetId()] = &s

	curveConfig := createPidCurveConfig(
		"curve",
		s.GetId(),
		60,
		-0.05,
		0,
		0,
	)
	curve, err := NewSpeedCurve(curveConfig)
	if err != nil {
		assert.Fail(t, err.Error())
	}

	for loopIdx, expected := range []int{0, 127, 127} {
		// WHEN
		result, err := curve.Evaluate()
		if err != nil {
			assert.Fail(t, err.Error())
		}

		// THEN
		assert.Equal(t, expected, result, "loop: %d", loopIdx)

		time.Sleep(200 * time.Millisecond)
	}
}

func TestPidCurveProportionalWayAboveTarget(t *testing.T) {
	// GIVEN
	avgTmp := 80000.0

	s := MockSensor{
		Name:      "sensor",
		MovingAvg: avgTmp,
	}
	sensors.SensorMap[s.GetId()] = &s

	curveConfig := createPidCurveConfig(
		"curve",
		s.GetId(),
		60,
		-0.05,
		0,
		0,
	)
	curve, err := NewSpeedCurve(curveConfig)
	if err != nil {
		assert.Fail(t, err.Error())
	}

	for loopIdx, expected := range []int{0, 255, 255} {
		// WHEN
		result, err := curve.Evaluate()
		if err != nil {
			assert.Fail(t, err.Error())
		}

		// THEN
		assert.Equal(t, expected, result, "loop: %d", loopIdx)

		time.Sleep(200 * time.Millisecond)
	}
}

// integral

func TestPidCurveIntegralBelowTarget(t *testing.T) {
	// GIVEN
	avgTmp := 50000.0

	s := MockSensor{
		Name:      "sensor",
		MovingAvg: avgTmp,
	}
	sensors.SensorMap[s.GetId()] = &s

	curveConfig := createPidCurveConfig(
		"curve",
		s.GetId(),
		60,
		0,
		-0.005,
		0,
	)
	curve, err := NewSpeedCurve(curveConfig)
	if err != nil {
		assert.Fail(t, err.Error())
	}

	for loopIdx, expected := range []int{0, 0, 0} {
		// WHEN
		result, err := curve.Evaluate()
		if err != nil {
			assert.Fail(t, err.Error())
		}

		// THEN
		assert.Equal(t, expected, result, "loop: %d", loopIdx)

		time.Sleep(200 * time.Millisecond)
	}
}

func TestPidCurveIntegralAboveTarget(t *testing.T) {
	// GIVEN
	avgTmp := 70000.0

	s := MockSensor{
		Name:      "sensor",
		MovingAvg: avgTmp,
	}
	sensors.SensorMap[s.GetId()] = &s

	curveConfig := createPidCurveConfig(
		"curve",
		s.GetId(),
		60,
		0,
		-0.005,
		0,
	)
	curve, err := NewSpeedCurve(curveConfig)
	if err != nil {
		assert.Fail(t, err.Error())
	}

	for loopIdx, expected := range []int{0, 2, 5} {
		// WHEN
		result, err := curve.Evaluate()
		if err != nil {
			assert.Fail(t, err.Error())
		}

		// THEN
		assert.Equal(t, expected, result, "loop: %d", loopIdx)

		time.Sleep(200 * time.Millisecond)
	}
}

func TestPidCurveIntegralWayAboveTarget(t *testing.T) {
	// GIVEN
	avgTmp := 80000.0

	s := MockSensor{
		Name:      "sensor",
		MovingAvg: avgTmp,
	}
	sensors.SensorMap[s.GetId()] = &s

	curveConfig := createPidCurveConfig(
		"curve",
		s.GetId(),
		60,
		0,
		-0.005,
		0,
	)
	curve, err := NewSpeedCurve(curveConfig)
	if err != nil {
		assert.Fail(t, err.Error())
	}

	for loopIdx, expected := range []int{0, 5, 10} {
		// WHEN
		result, err := curve.Evaluate()
		if err != nil {
			assert.Fail(t, err.Error())
		}

		// THEN
		assert.Equal(t, expected, result, "loop: %d", loopIdx)

		time.Sleep(200 * time.Millisecond)
	}
}

// derivative

func TestPidCurveDerivativeNoDiff(t *testing.T) {
	// GIVEN
	avgTmp := 60000.0

	s := MockSensor{
		Name:      "sensor",
		MovingAvg: avgTmp,
	}
	sensors.SensorMap[s.GetId()] = &s

	curveConfig := createPidCurveConfig(
		"curve",
		s.GetId(),
		60,
		0,
		0,
		-0.006,
	)
	curve, err := NewSpeedCurve(curveConfig)
	if err != nil {
		assert.Fail(t, err.Error())
	}

	for loopIdx, expected := range []int{0, 0, 0} {
		// WHEN
		result, err := curve.Evaluate()
		if err != nil {
			assert.Fail(t, err.Error())
		}

		// THEN
		assert.Equal(t, expected, result, "loop: %d", loopIdx)

		time.Sleep(200 * time.Millisecond)
	}
}

func TestPidCurveDerivativePositiveStaticDiff(t *testing.T) {
	// GIVEN
	avgTmp := 60000.0

	s := MockSensor{
		Name:      "sensor",
		MovingAvg: avgTmp,
	}
	sensors.SensorMap[s.GetId()] = &s

	curveConfig := createPidCurveConfig(
		"curve",
		s.GetId(),
		60,
		0,
		0,
		-0.006,
	)
	curve, err := NewSpeedCurve(curveConfig)
	if err != nil {
		assert.Fail(t, err.Error())
	}

	for loopIdx, expected := range []int{0, 7, 7} {
		// WHEN
		result, err := curve.Evaluate()
		if err != nil {
			assert.Fail(t, err.Error())
		}

		// temperature is increasing slowly
		s.MovingAvg = s.MovingAvg + 1000

		// THEN
		assert.Equal(t, expected, result, "loop: %d", loopIdx)

		time.Sleep(200 * time.Millisecond)
	}
}

func TestPidCurveDerivativeIncreasingDiff(t *testing.T) {
	// GIVEN
	avgTmp := 60000.0

	s := MockSensor{
		Name:      "sensor",
		MovingAvg: avgTmp,
	}
	sensors.SensorMap[s.GetId()] = &s

	curveConfig := createPidCurveConfig(
		"curve",
		s.GetId(),
		60,
		0,
		0,
		-0.006,
	)
	curve, err := NewSpeedCurve(curveConfig)
	if err != nil {
		assert.Fail(t, err.Error())
	}

	for loopIdx, expected := range []int{0, 7, 15, 22, 30} {
		// WHEN
		result, err := curve.Evaluate()
		if err != nil {
			assert.Fail(t, err.Error())
		}

		// temperature is increasing fast
		s.MovingAvg = s.MovingAvg + float64((loopIdx+1)*1000)

		// THEN
		assert.Equal(t, expected, result, "loop: %d", loopIdx)

		time.Sleep(200 * time.Millisecond)
	}
}

// combined tests

func TestPidCurveOnTarget(t *testing.T) {
	// GIVEN
	avgTmp := 60000.0

	s := MockSensor{
		Name:      "sensor",
		MovingAvg: avgTmp,
	}
	sensors.SensorMap[s.GetId()] = &s

	curveConfig := createPidCurveConfig(
		"curve",
		s.GetId(),
		60,
		-0.005,
		-0.005,
		-0.006,
	)
	curve, _ := NewSpeedCurve(curveConfig)

	// WHEN
	result, err := curve.Evaluate()
	if err != nil {
		assert.Fail(t, err.Error())
	}

	time.Sleep(1 * time.Second)

	result, err = curve.Evaluate()
	if err != nil {
		assert.Fail(t, err.Error())
	}

	// THEN
	assert.Equal(t, 0, result)
}

func TestPidCurveAboveTarget(t *testing.T) {
	// GIVEN
	avgTmp := 70000.0

	s := MockSensor{
		Name:      "sensor",
		MovingAvg: avgTmp,
	}
	sensors.SensorMap[s.GetId()] = &s

	curveConfig := createPidCurveConfig(
		"curve",
		s.GetId(),
		60,
		-0.005,
		-0.005,
		-0.006,
	)
	curve, err := NewSpeedCurve(curveConfig)
	if err != nil {
		assert.Fail(t, err.Error())
	}

	for loopIdx, expected := range []int{0, 15, 17, 20, 22, 25} {
		// WHEN
		result, err := curve.Evaluate()
		if err != nil {
			assert.Fail(t, err.Error())
		}

		// THEN
		assert.Equal(t, expected, result, "loop: %d", loopIdx)

		time.Sleep(200 * time.Millisecond)
	}
}

func TestPidCurveWayAboveTarget(t *testing.T) {
	// GIVEN
	avgTmp := 80000.0

	s := MockSensor{
		Name:      "sensor",
		MovingAvg: avgTmp,
	}
	sensors.SensorMap[s.GetId()] = &s

	curveConfig := createPidCurveConfig(
		"curve",
		s.GetId(),
		60,
		-0.005,
		-0.005,
		-0.006,
	)
	curve, err := NewSpeedCurve(curveConfig)
	if err != nil {
		assert.Fail(t, err.Error())
	}

	for loopIdx, expected := range []int{0, 30, 35, 40, 45, 51} {
		// WHEN
		result, err := curve.Evaluate()
		if err != nil {
			assert.Fail(t, err.Error())
		}

		// THEN
		assert.Equal(t, expected, result, "loop: %d", loopIdx)

		time.Sleep(200 * time.Millisecond)
	}
}
