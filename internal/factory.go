package internal

import (
	"fmt"

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

func initializeFanControllers(
	pers persistence.Persistence,
	fanMap map[configuration.FanConfig]fans.Fan,
	reg *registry.Registry,
) (result map[fans.Fan]controller.FanController, err error) {
	result = map[fans.Fan]controller.FanController{}
	for config, fan := range fanMap {
		updateRate := configuration.CurrentConfig.FanController.AdjustmentTickRate
		controlLoop := createControlLoop(config)
		curve, _ := reg.GetCurve(fan.GetCurveId())
		fanController := controller.NewFanController(pers, fan, curve, controlLoop, updateRate, false)
		result[fan] = fanController
	}

	var fanControllers = []controller.FanController{}
	for _, c := range result {
		fanControllers = append(fanControllers, c)
	}
	controllerCollector := statistics.NewControllerCollector(fanControllers)
	statistics.Register(controllerCollector)

	return result, nil
}

func createControlLoop(config configuration.FanConfig) control_loop.ControlLoop {
	// 1. Check deprecated config first
	if config.ControlLoop != nil { //nolint:all
		ui.Warning("Using deprecated control loop configuration for fan %s...", config.ID)
		return control_loop.NewPidControlLoop(
			config.ControlLoop.P,
			config.ControlLoop.I,
			config.ControlLoop.D,
		)
	}

	// 2. Check standard config
	if config.ControlAlgorithm != nil {
		if config.ControlAlgorithm.Pid != nil {
			return control_loop.NewPidControlLoop(
				config.ControlAlgorithm.Pid.P,
				config.ControlAlgorithm.Pid.I,
				config.ControlAlgorithm.Pid.D,
			)
		}
		if config.ControlAlgorithm.Direct != nil {
			return control_loop.NewDirectControlLoop(
				config.ControlAlgorithm.Direct.MaxPwmChangePerCycle,
			)
		}
	}

	// 3. Fallback
	return control_loop.NewPidControlLoop(control_loop.DefaultPidConfig.P, control_loop.DefaultPidConfig.I, control_loop.DefaultPidConfig.D)
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

		currentValue, err := sensor.GetValue()
		if err != nil {
			ui.Warning("Error reading sensor %s: %v", config.ID, err)
		}
		sensor.SetMovingAvg(currentValue)

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

	var fanList []fans.Fan

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

		fanList = append(fanList, fan)
	}

	fanCollector := statistics.NewFanCollector(fanList)
	statistics.Register(fanCollector)

	return result, nil
}
