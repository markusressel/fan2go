package api

import (
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/controller/status"
	"github.com/markusressel/fan2go/internal/curves"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/registry"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var updateGolden = flag.Bool("update", false, "update the golden files in testdata/")

func intPtr(v int) *int { return &v }

// createTestRegistry creates a registry with one fan, sensor and curve of every type
// (except nvidia, which needs hardware), all in a fixed state.
func createTestRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.NewRegistry()

	enabled := configuration.DefaultTrueBool{Optional: configuration.Optional[bool]{Value: false, Present: true}}
	reg.RegisterFan(&fans.HwMonFan{
		Label:        "hwmon_fan",
		Index:        1,
		RpmMovingAvg: 1234.5,
		Config: configuration.FanConfig{
			ID:        "hwmon_fan",
			Curve:     "linear_curve",
			NeverStop: true,
			MinPwm:    intPtr(30),
			SanityCheck: configuration.SanityCheckConfig{
				PwmValueChangedByThirdParty: configuration.PwmValueChangedByThirdPartyConfig{Enabled: enabled},
			},
			HwMon: &configuration.HwMonFanConfig{
				Platform:      "nct6798-isa-0290",
				Index:         1,
				RpmChannel:    1,
				PwmChannel:    1,
				SysfsPath:     "/sys/class/hwmon/hwmon4",
				RpmInputPath:  "/sys/class/hwmon/hwmon4/fan1_input",
				PwmPath:       "/sys/class/hwmon/hwmon4/pwm1",
				PwmEnablePath: "/sys/class/hwmon/hwmon4/pwm1_enable",
			},
		},
		MinPwm:       intPtr(30),
		StartPwm:     intPtr(40),
		MaxPwm:       intPtr(250),
		FanCurveData: &map[int]float64{0: 0, 128: 900.5, 255: 1800},
		Rpm:          1200,
		Pwm:          128,
	})
	reg.RegisterFan(&fans.FileFan{
		Config: configuration.FanConfig{
			ID:    "file_fan",
			Curve: "pid_curve",
			File:  &configuration.FileFanConfig{Path: "/tmp/file_fan_pwm", RpmPath: "/tmp/file_fan_rpm"},
		},
		MovingAvg: 12.5,
		Pwm:       100,
		Rpm:       1000,
	})
	reg.RegisterFan(&fans.CmdFan{
		Config: configuration.FanConfig{
			ID:    "cmd_fan",
			Curve: "function_curve",
			Cmd: &configuration.CmdFanConfig{
				SetPwm: &configuration.ExecConfig{Exec: "/usr/bin/setpwm", Args: []string{"%pwm%"}},
				GetPwm: &configuration.ExecConfig{Exec: "/usr/bin/getpwm"},
				GetRpm: &configuration.ExecConfig{Exec: "/usr/bin/getrpm", Args: []string{"-a"}},
			},
		},
		MovingAvg: 3,
		Pwm:       50,
		Rpm:       500,
	})

	reg.RegisterSensor(&sensors.HwmonSensor{
		Label:     "Package id 0",
		Index:     1,
		Channel:   1,
		Input:     "/sys/class/hwmon/hwmon2/temp1_input",
		Max:       100000,
		Min:       0,
		Config:    configuration.SensorConfig{ID: "hwmon_sensor", HwMon: &configuration.HwMonSensorConfig{Platform: "coretemp", Index: 1, Channel: 1, TempInput: "/sys/class/hwmon/hwmon2/temp1_input"}},
		MovingAvg: 45250.5,
	})
	fileSensor := &sensors.FileSensor{
		Config:    configuration.SensorConfig{ID: "file_sensor", File: &configuration.FileSensorConfig{Path: "/tmp/file_sensor"}},
		MovingAvg: 40000,
	}
	// a reading long ago (so it is stale at any time the test runs), followed by failed readings
	fileSensor.RecordReading(sensors.Reading{Value: 40000, Time: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)})
	fileSensor.RecordFailure()
	fileSensor.RecordFailure()
	reg.RegisterSensor(fileSensor)
	reg.RegisterSensor(&sensors.CmdSensor{
		Name:      "cmd",
		Config:    configuration.SensorConfig{ID: "cmd_sensor", Cmd: &configuration.CmdSensorConfig{Exec: "/usr/bin/temp", Args: []string{"--json"}}},
		MovingAvg: 38000,
	})
	reg.RegisterSensor(&sensors.DiskSensor{
		Config:    configuration.SensorConfig{ID: "disk_sensor", Disk: &configuration.DiskSensorConfig{Device: "/dev/sda"}},
		MovingAvg: 35000,
	})

	curveConfigs := []configuration.CurveConfig{
		{ID: "linear_curve", Linear: &configuration.LinearCurveConfig{Sensor: "hwmon_sensor", Min: 30, Max: 80, Steps: map[int]float64{30: 0, 80: 255}}},
		{ID: "staircase_curve", Staircase: &configuration.StaircaseCurveConfig{Sensor: "file_sensor", Hysteresis: configuration.HysteresisConfig{Down: 2}, Steps: map[int]float64{40: 100, 60: 200}}},
		{ID: "pid_curve", PID: &configuration.PidCurveConfig{Sensor: "cmd_sensor", SetPoint: 60, P: 0.03, I: 0.002, D: 0.0005}},
		{ID: "function_curve", Function: &configuration.FunctionCurveConfig{Type: configuration.FunctionMaximum, Curves: []string{"linear_curve", "staircase_curve"}}},
	}
	for i, config := range curveConfigs {
		curve, err := curves.NewSpeedCurve(config)
		require.NoError(t, err)
		curve.(interface{ SetValue(float64) }).SetValue(float64(10 * (i + 1)))
		reg.RegisterCurve(curve)
	}

	reg.RegisterFanController(&fakeFanController{status: status.Status{
		FanId: "hwmon_fan", State: status.StateRunning, Since: time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
	}})
	reg.RegisterFanController(&fakeFanController{status: status.Status{
		FanId: "file_fan", State: status.StateSensorFailureMax, Since: time.Date(2024, 1, 2, 3, 4, 6, 0, time.UTC),
		Error: "curve pid_curve: sensor cmd_sensor: sensor value is stale: no successful reading yet",
	}})
	reg.RegisterFanController(&fakeFanController{status: status.Status{FanId: "cmd_fan", State: status.StateStopped}})

	return reg
}

// fakeFanController returns a fixed status
type fakeFanController struct {
	status status.Status
}

func (c *fakeFanController) GetFanId() string         { return c.status.FanId }
func (c *fakeFanController) GetStatus() status.Status { return c.status }

func get(t *testing.T, handler http.Handler, path string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// The REST API is consumed by external tools, so its JSON output must not change shape unintentionally.
// Update the golden files with -update, and review the diff.
func TestRestApi_JsonOutputMatchesGoldenFiles(t *testing.T) {
	rest := CreateRestService(createTestRegistry(t))

	for _, path := range []string{
		"/fan/", "/fan/hwmon_fan/", "/fan/file_fan/", "/fan/cmd_fan/",
		"/sensor/", "/sensor/hwmon_sensor/", "/sensor/file_sensor/", "/sensor/cmd_sensor/", "/sensor/disk_sensor/",
		"/curve/", "/curve/linear_curve/", "/curve/staircase_curve/", "/curve/pid_curve/", "/curve/function_curve/",
		"/controller/", "/controller/hwmon_fan/", "/controller/file_fan/", "/controller/cmd_fan/",
		"/fan/unknown/", "/controller/unknown/",
	} {
		t.Run(path, func(t *testing.T) {
			code, body := get(t, rest, path)

			name := strings.ReplaceAll(strings.Trim(path, "/"), "/", "_") + ".json"
			golden := filepath.Join("testdata", name)
			if *updateGolden {
				require.NoError(t, os.MkdirAll("testdata", 0755))
				require.NoError(t, os.WriteFile(golden, []byte(body), 0644))
			}
			expected, err := os.ReadFile(golden)
			require.NoError(t, err)

			if strings.HasSuffix(path, "/unknown/") {
				assert.Equal(t, http.StatusNotFound, code)
			} else {
				assert.Equal(t, http.StatusOK, code)
			}
			assert.Equal(t, string(expected), body)
		})
	}
}

// The monitors and fan controllers update sensors, fans and curves while the API serializes them.
func TestRestApi_ConcurrentUpdatesDuringGet(t *testing.T) {
	reg := createTestRegistry(t)
	rest := CreateRestService(reg)

	sensor, _ := reg.GetSensor("file_sensor")
	fan, _ := reg.GetFan("hwmon_fan")
	staircase, _ := reg.GetCurve("staircase_curve")

	const iterations = 50
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			sensor.SetMovingAvg(float64(i))
			fan.SetRpmAvg(float64(i))
			fan.SetMinPwm(i%100, true)
			_ = fan.AttachFanRpmCurveData(&map[int]float64{0: 0, 255: float64(i)})
			_, _ = staircase.Evaluate()
		}
	}()

	for i := 0; i < iterations; i++ {
		for _, path := range []string{"/sensor/", "/sensor/file_sensor/", "/fan/", "/fan/hwmon_fan/", "/curve/", "/curve/staircase_curve/"} {
			code, _ := get(t, rest, path)
			assert.Equal(t, http.StatusOK, code)
		}
	}
	close(stop)
	wg.Wait()
	// THEN (no race reported)
}
