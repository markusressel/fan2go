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
)

func InitializeObjects() (fanMap map[configuration.FanConfig]fans.Fan, reg *registry.Registry, err error) {
	controllers := hwmon.GetChips()
	reg = registry.NewRegistry()

	statistics.UnregisterAll()

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

	var fanControllers = []controller.FanController{}
	for _, c := range result {
		fanControllers = append(fanControllers, c)
	}
	controllerCollector := statistics.NewControllerCollector(fanControllers)
	statistics.Register(controllerCollector)

	fanCollector := statistics.NewFanCollector(result)
	statistics.Register(fanCollector)

	return result, evaluator, nil
}

// newCurveEvaluator returns the curve evaluator for all curves of reg, which evaluates them
// every tickRate (fanController.adjustmentTickRate).
func newCurveEvaluator(reg *registry.Registry, tickRate time.Duration) (*curves.CurveEvaluator, error) {
	var curveList []curves.SpeedCurve
	for _, curve := range reg.SnapshotCurves() {
		curveList = append(curveList, curve)
	}
	evaluator, err := curves.NewCurveEvaluator(curveList, tickRate)
	if err != nil {
		return nil, fmt.Errorf("error initializing curve evaluator: %w", err)
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

	// 2. Check standard config
	if config.ControlAlgorithm != nil {
		if config.ControlAlgorithm.Pid != nil {
			return control_loop.NewPidControlLoop(
				config.ControlAlgorithm.Pid.P,
				config.ControlAlgorithm.Pid.I,
				config.ControlAlgorithm.Pid.D,
				tickRate,
			)
		}
		if config.ControlAlgorithm.Direct != nil {
			return control_loop.NewDirectControlLoop(
				config.ControlAlgorithm.Direct.MaxPwmChangePerCycle,
			)
		}
	}

	// 3. Fallback
	return control_loop.NewPidControlLoop(control_loop.DefaultPidConfig.P, control_loop.DefaultPidConfig.I, control_loop.DefaultPidConfig.D, tickRate)
}

func initializeSensors(
	controllers []*hwmon.HwMonController,
	reg *registry.Registry,
	configs []configuration.SensorConfig,
) error {
	var sensorList []sensors.Sensor
	for _, config := range configs {
		if config.HwMon != nil {
			err := hwmon.UpdateSensorConfigFromHwMonControllers(controllers, &config)
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
		sensorList = append(sensorList, sensor)

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

	sensorCollector := statistics.NewSensorCollector(sensorList)
	statistics.Register(sensorCollector)

	return nil
}

func initializeCurves(reg *registry.Registry, configs []configuration.CurveConfig) error {
	var curveList []curves.SpeedCurve
	for _, config := range configs {
		curve, err := curves.NewSpeedCurve(config)
		if err != nil {
			return fmt.Errorf("unable to process curve configuration: %s: %v", config.ID, err)
		}
		curveList = append(curveList, curve)
		reg.RegisterCurve(curve)
	}

	curveCollector := statistics.NewCurveCollector(curveList)
	statistics.Register(curveCollector)

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
		reg.RegisterFan(fan)
		result[config] = fan
	}

	return result, nil
}
