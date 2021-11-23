package internal

import (
	"fmt"
	"github.com/asecurityteam/rolling"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/stretchr/testify/assert"
	"testing"
)

var (
	linearFan = map[int][]float64{
		0:   {0.0},
		255: {255.0},
	}

	neverStoppingFan = map[int][]float64{
		0:   {50.0},
		50:  {50.0},
		255: {255.0},
	}

	cappedFan = map[int][]float64{
		0:   {0.0},
		1:   {0.0},
		2:   {0.0},
		3:   {0.0},
		4:   {0.0},
		5:   {0.0},
		6:   {20.0},
		200: {200.0},
	}

	cappedNeverStoppingFan = map[int][]float64{
		0:   {50.0},
		50:  {50.0},
		200: {200.0},
	}
)

func createFan(neverStop bool, curveData map[int][]float64) (fan Fan, err error) {
	configuration.CurrentConfig.RpmRollingWindowSize = 10

	fan = &fans.HwMonFan{
		Config: &configuration.FanConfig{
			ID: "fan1",
			HwMon: &configuration.HwMonFanConfig{
				Platform: "platform",
				Index:    1,
			},
			NeverStop: neverStop,
			Curve:     "curve",
		},
		FanCurveData: &map[int]*rolling.PointPolicy{},
		PwmOutput:    "fan1_output",
		RpmInput:     "fan1_rpm",
	}
	FanMap[fan.GetConfig().ID] = fan

	err = AttachFanCurveData(&curveData, fan)

	return fan, err
}

func createSensor(
	id string,
	hwMonConfig configuration.HwMonSensorConfig,
	avgTmp float64,
) (sensor Sensor) {
	sensor = &sensors.HwmonSensor{
		Config: &configuration.SensorConfig{
			ID:    id,
			HwMon: &hwMonConfig,
		},
		MovingAvg: avgTmp,
	}
	SensorMap[sensor.GetConfig().ID] = sensor
	return sensor
}

func TestLinearFan(t *testing.T) {
	// GIVEN
	fan, _ := createFan(false, linearFan)

	// WHEN
	startPwm, maxPwm := ComputePwmBoundaries(fan)

	// THEN
	assert.Equal(t, 1, startPwm)
	assert.Equal(t, 255, maxPwm)
}

func TestNeverStoppingFan(t *testing.T) {
	// GIVEN
	fan, _ := createFan(false, neverStoppingFan)

	// WHEN
	startPwm, maxPwm := ComputePwmBoundaries(fan)

	// THEN
	assert.Equal(t, 0, startPwm)
	assert.Equal(t, 255, maxPwm)
}

func TestCappedFan(t *testing.T) {
	// GIVEN
	fan, _ := createFan(false, cappedFan)

	// WHEN
	startPwm, maxPwm := ComputePwmBoundaries(fan)

	// THEN
	assert.Equal(t, 6, startPwm)
	assert.Equal(t, 200, maxPwm)
}

func TestCappedNeverStoppingFan(t *testing.T) {
	// GIVEN
	fan, _ := createFan(false, cappedNeverStoppingFan)

	// WHEN
	startPwm, maxPwm := ComputePwmBoundaries(fan)

	// THEN
	assert.Equal(t, 0, startPwm)
	assert.Equal(t, 200, maxPwm)
}

func TestCalculateTargetSpeedLinear(t *testing.T) {
	// GIVEN
	avgTmp := 50000.0
	s := createSensor(
		"sensor",
		configuration.HwMonSensorConfig{
			Platform: "platform",
			Index:    0,
		},
		avgTmp,
	)

	curveConfig := createLinearCurveConfig(
		"curve",
		s.GetConfig().ID,
		40,
		60,
	)
	NewSpeedCurve(curveConfig)

	fan, _ := createFan(false, linearFan)

	// WHEN
	optimal, err := calculateOptimalPwm(fan)
	if err != nil {
		assert.Fail(t, err.Error())
	}

	// THEN
	assert.Equal(t, 127, optimal)
}

func TestCalculateTargetSpeedNeverStop(t *testing.T) {
	// GIVEN
	avgTmp := 40000.0

	s := createSensor(
		"sensor",
		configuration.HwMonSensorConfig{
			Platform: "platform",
			Index:    0,
		},
		avgTmp,
	)

	curveConfig := createLinearCurveConfig(
		"curve",
		s.GetConfig().ID,
		40,
		60,
	)
	NewSpeedCurve(curveConfig)

	fan, _ := createFan(true, cappedFan)

	// WHEN
	optimal, err := calculateOptimalPwm(fan)
	if err != nil {
		assert.Fail(t, err.Error())
	}
	target := calculateTargetPwm(fan, 0, optimal)

	// THEN
	assert.Equal(t, 0, optimal)
	assert.Greater(t, fan.GetMinPwm(), 0)
	assert.Equal(t, fan.GetMinPwm(), target)
}

func TestFindDeviceName(t *testing.T) {
	// GIVEN
	deviceName := "some-device-name"
	devicePathToExpectedName := map[string]string{
		"/sys/devices/platform/nct6775.656/hwmon/hwmon4":                                                 deviceName,
		"/sys/devices/pci0000:00/0000:00:0e.0/pci10000:e0/10000:e0:06.0/10000:e1:00.0/nvme/nvme0/hwmon3": fmt.Sprintf("%s-pci-10000E100", deviceName),
		"/sys/devices/pci0000:00/0000:00:01.2/0000:02:00.0/0000:03:01.0/0000:04:00.0/nvme/nvme1/hwmon0":  fmt.Sprintf("%s-pci-0400", deviceName),
	}

	for key, value := range devicePathToExpectedName {
		// WHEN
		result := computeIdentifier(key, deviceName)

		// THEN
		assert.Equal(t, value, result)
	}
}

func TestFindPlatform(t *testing.T) {
	// GIVEN
	devicePath := "/sys/devices/pci0000:00/0000:00:0e.0/pci10000:e0/10000:e0:06.0/10000:e1:00.0/nvme/nvme0/hwmon3"

	// WHEN
	platform := findPlatform(devicePath)

	// THEN
	assert.Equal(t, "", platform)
}
