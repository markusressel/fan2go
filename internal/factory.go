package internal

import (
	"fmt"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/control_loop"
	"github.com/markusressel/fan2go/internal/controller"
	"github.com/markusressel/fan2go/internal/curves"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/hwmon"
	"github.com/markusressel/fan2go/internal/persistence"
	"github.com/markusressel/fan2go/internal/registry"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/statistics"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/prometheus/client_golang/prometheus"
)

func InitializeObjects() (fanMap map[configuration.FanConfig]fans.Fan, reg *registry.Registry, err error) {
	controllers := hwmon.GetChips()
	reg = registry.NewRegistry()

	config := configuration.CurrentConfig
	err = initializeSensors(controllers, reg, config.Sensors)
	if err != nil {
		return nil, nil, fmt.Errorf("error initializing sensors: %v", err)
	}
	err = initializeCurves(reg, config.Curves)
	if err != nil {
		return nil, nil, fmt.Errorf("error initializing curves: %v", err)
	}
	fanMap, err = initializeFans(controllers, reg, config.Fans)
	if err != nil {
		return nil, nil, fmt.Errorf("error initializing fans: %v", err)
	}

	return fanMap, reg, nil
}

// initializeFanControllers creates the curve evaluator for all curves of reg, and a fan controller
// for each fan, which is driven by the cycles of the evaluator.
// Errors of the curve evaluator are configuration.ConfigErrors; an error registering the statistics
// collectors is not.
func initializeFanControllers(
	pers persistence.Persistence,
	fanMap map[configuration.FanConfig]fans.Fan,
	reg *registry.Registry,
) (result map[fans.Fan]controller.FanController, evaluator *curves.CurveEvaluator, err error) {
	updateRate := configuration.CurrentConfig.FanController.AdjustmentTickRate
	evaluator, err = newCurveEvaluator(reg, updateRate)
	if err != nil {
		return nil, nil, err
	}

	result = map[fans.Fan]controller.FanController{}
	for config, fan := range fanMap {
		controlLoop := createControlLoop(config, updateRate)
		curve, _ := reg.GetCurve(fan.GetCurveId())
		fanController := controller.NewFanController(pers, fan, curve, controlLoop, updateRate, false)
		fanController.SetCurveCycles(evaluator.Subscribe())
		result[fan] = fanController
	}

	for _, c := range result {
		reg.RegisterFanController(c)
	}

	// only now that all objects of the (new) config exist: a failed reload keeps the collectors
	// of the old objects, which keep running
	err = statistics.ReplaceAll(createCollectors(reg, result))
	if err != nil {
		return nil, nil, fmt.Errorf("error registering the statistics collectors: %w", err)
	}

	return result, evaluator, nil
}

// createCollectors returns the statistics collectors for the sensors and curves of reg and the given fan controllers.
func createCollectors(reg *registry.Registry, fanControllers map[fans.Fan]controller.FanController) []prometheus.Collector {
	var sensorList []sensors.Sensor
	for _, sensor := range reg.SnapshotSensors() {
		sensorList = append(sensorList, sensor)
	}
	var curveList []curves.SpeedCurve
	for _, curve := range reg.SnapshotCurves() {
		curveList = append(curveList, curve)
	}
	var controllerList []controller.FanController
	for _, c := range fanControllers {
		controllerList = append(controllerList, c)
	}
	return []prometheus.Collector{
		statistics.NewSensorCollector(sensorList),
		statistics.NewCurveCollector(curveList),
		statistics.NewControllerCollector(controllerList),
		statistics.NewFanCollector(fanControllers),
	}
}

// newCurveEvaluator returns the curve evaluator for all curves of reg, which evaluates them
// every tickRate (fanController.adjustmentTickRate). Its errors (invalid tick rate, duplicate curve ids,
// curve dependency cycle) are caused by the config, so they are configuration.ConfigErrors.
func newCurveEvaluator(reg *registry.Registry, tickRate time.Duration) (*curves.CurveEvaluator, error) {
	var curveList []curves.SpeedCurve
	for _, curve := range reg.SnapshotCurves() {
		curveList = append(curveList, curve)
	}
	evaluator, err := curves.NewCurveEvaluator(curveList, tickRate)
	if err != nil {
		return nil, configuration.NewConfigError(fmt.Errorf("error initializing curve evaluator: %w", err))
	}
	return evaluator, nil
}

// createControlLoop creates the control loop of a fan. tickRate is the time between two cycles of the loop
// (fanController.adjustmentTickRate).
func createControlLoop(config configuration.FanConfig, tickRate time.Duration) control_loop.ControlLoop {
	// 1. Check deprecated config first (validation warns about it and rejects it together with controlAlgorithm)
	if config.ControlLoop != nil { //nolint:all
		return control_loop.NewPidControlLoop(
			config.ControlLoop.P, //nolint:all
			config.ControlLoop.I, //nolint:all
			config.ControlLoop.D, //nolint:all
			tickRate,
		)
	}

	// 2. Check standard config, 3. fall back to the default
	algorithm := config.GetControlAlgorithm()
	if algorithm.Pid != nil {
		return control_loop.NewPidControlLoop(
			algorithm.Pid.P,
			algorithm.Pid.I,
			algorithm.Pid.D,
			tickRate,
		)
	}
	return newDirectControlLoop(*algorithm.Direct)
}

// newDirectControlLoop creates the direct control loop with the given config.
func newDirectControlLoop(config configuration.DirectControlAlgorithmConfig) control_loop.ControlLoop {
	return control_loop.NewDirectControlLoopWithOptions(control_loop.DirectControlLoopOptions{
		MaxPwmChangePerCycle: config.MaxPwmChangePerCycle, //nolint:staticcheck // deprecated, but still supported
		TimeConstant:         config.TimeConstant.Duration(),
		RampUp:               config.RampUp.Value(),
		RampDown:             config.RampDown.Value(),
		Deadband:             config.Deadband.Value(),
	})
}

func initializeSensors(
	controllers []*hwmon.HwMonController,
	reg *registry.Registry,
	configs []configuration.SensorConfig,
) error {
	for _, config := range configs {
		var hwmonSensor *sensors.HwmonSensor
		if config.HwMon != nil {
			var err error
			hwmonSensor, err = hwmon.FindSensorForConfig(controllers, &config)
			if err != nil {
				errMsg := fmt.Sprintf("couldn't find sensor for %s: %v. Skipping.", config.ID, err)
				ui.Warning("%s", errMsg)
				ui.NotifyError("Sensor Skipped", errMsg)
				continue
			}
		}

		sensor, err := sensors.NewSensor(config)
		if err != nil {
			errMsg := fmt.Sprintf("unable to process sensor configuration: %s: %v. Skipping.", config.ID, err)
			ui.Warning("%s", errMsg)
			ui.NotifyError("Sensor Skipped", errMsg)
			continue
		}
		if s, ok := sensor.(*sensors.HwmonSensor); ok && hwmonSensor != nil {
			// as detected by hwmon (not part of the config)
			s.Label = hwmonSensor.Label
			s.Min = hwmonSensor.Min
			s.Max = hwmonSensor.Max
		}

		// seed the moving average with a first reading. If that fails, the sensor has no reading,
		// so it is stale until the sensor monitor reads it successfully (instead of starting at 0).
		currentValue, err := sensor.GetValue()
		if err != nil {
			ui.Warning("Error reading sensor %s: %v", config.ID, err)
		} else {
			sensor.SetMovingAvg(currentValue)
			sensor.RecordReading(sensors.Reading{Value: currentValue, Time: util.RealClock.Now()})
		}

		reg.RegisterSensor(sensor)
	}

	return nil
}

func initializeCurves(reg *registry.Registry, configs []configuration.CurveConfig) error {
	for _, config := range configs {
		curve, err := curves.NewSpeedCurve(config)
		if err != nil {
			return fmt.Errorf("unable to process curve configuration: %s: %v", config.ID, err)
		}
		reg.RegisterCurve(curve)
	}

	return nil
}

func initializeFans(
	controllers []*hwmon.HwMonController,
	reg *registry.Registry,
	configs []configuration.FanConfig,
) (map[configuration.FanConfig]fans.Fan, error) {
	var result = map[configuration.FanConfig]fans.Fan{}

	for _, config := range configs {
		if config.HwMon != nil {
			err := hwmon.UpdateFanConfigFromHwMonControllers(controllers, &config)
			if err != nil {
				errMsg := fmt.Sprintf("couldn't update fan config from hwmon for %s: %v. Skipping.", config.ID, err)
				ui.Warning("%s", errMsg)
				ui.NotifyError("Fan Skipped", errMsg)
				continue
			}
		}

		fan, err := fans.NewFan(config)
		if err != nil {
			errMsg := fmt.Sprintf("unable to process fan configuration of '%s': %v. Skipping.", config.ID, err)
			ui.Warning("%s", errMsg)
			ui.NotifyError("Fan Skipped", errMsg)
			continue
		}
		if entry := fans.KnownQuirksOf(fan); entry != nil {
			ui.Info("%s", entry.AppliedMessage(config))
		}
		reg.RegisterFan(fan)
		result[config] = fan
	}

	return result, nil
}
