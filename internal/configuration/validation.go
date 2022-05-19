package configuration

import (
	"errors"
	"fmt"
	"github.com/looplab/tarjan"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
	"golang.org/x/exp/slices"
	"strings"
)

func Validate(configPath string) error {
	return validateConfig(&CurrentConfig, configPath)
}

func validateConfig(config *Configuration, path string) error {
	err := validateSensors(config)
	if err != nil {
		return err
	}
	err = validateCurves(config)
	if err != nil {
		return err
	}
	err = validateFans(config)

	if containsCmdSensors() {
		if _, err := util.CheckFilePermissionsForExecution(path); err != nil {
			return errors.New(fmt.Sprintf("Config file '%s' has invalid permissions: %s", path, err))
		}
	}

	return err
}

func containsCmdSensors() bool {
	for _, sensorConfig := range CurrentConfig.Sensors {
		if sensorConfig.Cmd != nil {
			return true
		}
	}

	return false
}

func validateSensors(config *Configuration) error {
	for _, sensorConfig := range config.Sensors {

		subConfigs := 0
		if sensorConfig.HwMon != nil {
			subConfigs++
		}
		if sensorConfig.File != nil {
			subConfigs++
		}
		if sensorConfig.Cmd != nil {
			subConfigs++
		}
		if subConfigs > 1 {
			return errors.New(fmt.Sprintf("Sensor %s: only one sensor type can be used per sensor definition block", sensorConfig.ID))
		}
		if subConfigs <= 0 {
			return errors.New(fmt.Sprintf("Sensor %s: sub-configuration for sensor is missing, use one of: hwmon | file | cmd", sensorConfig.ID))
		}

		if !isSensorConfigInUse(sensorConfig, config.Curves) {
			ui.Warning("Unused sensor configuration: %s", sensorConfig.ID)
		}

		if sensorConfig.HwMon != nil {
			if sensorConfig.HwMon.Index <= 0 {
				return errors.New(fmt.Sprintf("Sensor %s: invalid index, must be >= 1", sensorConfig.ID))
			}
		}
	}

	return nil
}

func isSensorConfigInUse(config SensorConfig, curves []CurveConfig) bool {
	for _, curveConfig := range curves {
		if curveConfig.Function != nil {
			// function curves cannot reference sensors
			continue
		}
		if curveConfig.Linear != nil && curveConfig.Linear.Sensor == config.ID {
			return true
		}
	}

	return false
}

func validateCurves(config *Configuration) error {
	graph := make(map[interface{}][]interface{})

	for _, curveConfig := range config.Curves {
		subConfigs := 0
		if curveConfig.Linear != nil {
			subConfigs++
		}
		if curveConfig.PID != nil {
			subConfigs++
		}
		if curveConfig.Function != nil {
			subConfigs++
		}
		if subConfigs > 1 {
			return errors.New(fmt.Sprintf("Curve %s: only one curve type can be used per curve definition block", curveConfig.ID))
		}
		if subConfigs <= 0 {
			return errors.New(fmt.Sprintf("Curve %s: sub-configuration for curve is missing, use one of: linear | pid | function", curveConfig.ID))
		}

		if !isCurveConfigInUse(curveConfig, config.Curves, config.Fans) {
			ui.Warning("Unused curve configuration: %s", curveConfig.ID)
		}

		if curveConfig.Function != nil {
			supportedTypes := []string{FunctionMinimum, FunctionAverage, FunctionMaximum, FunctionDelta}
			if !slices.Contains(supportedTypes, curveConfig.Function.Type) {
				return errors.New(fmt.Sprintf("Curve %s: unsupported function type '%s', use one of: %s", curveConfig.ID, curveConfig.Function.Type, strings.Join(supportedTypes, " | ")))
			}

			var connections []interface{}
			for _, curve := range curveConfig.Function.Curves {
				if curve == curveConfig.ID {
					return errors.New(fmt.Sprintf("Curve %s: a curve cannot reference itself", curveConfig.ID))
				}
				if !curveIdExists(curve, config) {
					return errors.New(fmt.Sprintf("Curve %s: no curve definition with id '%s' found", curveConfig.ID, curve))
				}
				connections = append(connections, curve)
			}
			graph[curveConfig.ID] = connections
		}

		if curveConfig.Linear != nil {
			if len(curveConfig.Linear.Sensor) <= 0 {
				return errors.New(fmt.Sprintf("Curve %s: Missing sensorId", curveConfig.ID))
			}

			if !sensorIdExists(curveConfig.Linear.Sensor, config) {
				return errors.New(fmt.Sprintf("Curve %s: no sensor definition with id '%s' found", curveConfig.ID, curveConfig.Linear.Sensor))
			}
		}

		if curveConfig.PID != nil {
			if len(curveConfig.PID.Sensor) <= 0 {
				return errors.New(fmt.Sprintf("Curve %s: Missing sensorId", curveConfig.ID))
			}

			if !sensorIdExists(curveConfig.PID.Sensor, config) {
				return errors.New(fmt.Sprintf("Curve %s: no sensor definition with id '%s' found", curveConfig.ID, curveConfig.PID.Sensor))
			}

			pidConfig := curveConfig.PID
			if pidConfig.P == 0 && pidConfig.I == 0 && pidConfig.D == 0 {
				return errors.New(fmt.Sprintf("Curve %s: all PID constants are zero", curveConfig.ID))
			}
		}

	}

	err := validateNoLoops(graph)
	return err
}

func sensorIdExists(sensorId string, config *Configuration) bool {
	for _, sensor := range config.Sensors {
		if sensor.ID == sensorId {
			return true
		}
	}

	return false
}

func validateNoLoops(graph map[interface{}][]interface{}) error {
	output := tarjan.Connections(graph)
	for _, items := range output {
		if len(items) > 1 {
			return errors.New(fmt.Sprintf("You have created a curve dependency cycle: %v", items))
		}
	}
	return nil
}

func isCurveConfigInUse(config CurveConfig, curves []CurveConfig, fans []FanConfig) bool {
	for _, curveConfig := range curves {
		if curveConfig.Function != nil {
			if util.ContainsString(curveConfig.Function.Curves, config.ID) {
				return true
			}
		}
	}

	for _, fanConfig := range fans {
		if fanConfig.Curve == config.ID {
			return true
		}
	}

	return false
}

func validateFans(config *Configuration) error {
	for _, fanConfig := range config.Fans {
		subConfigs := 0
		if fanConfig.HwMon != nil {
			subConfigs++
		}
		if fanConfig.File != nil {
			subConfigs++
		}
		if fanConfig.Cmd != nil {
			subConfigs++
		}

		if subConfigs > 1 {
			return errors.New(fmt.Sprintf("Fan %s: only one fan type can be used per fan definition block", fanConfig.ID))
		}
		if subConfigs <= 0 {
			return errors.New(fmt.Sprintf("Fan %s: sub-configuration for fan is missing, use one of: lhwmon | file | cmd", fanConfig.ID))
		}

		if len(fanConfig.Curve) <= 0 {
			return errors.New(fmt.Sprintf("Fan %s: missing curve definition in configuration entry", fanConfig.ID))
		}

		if !curveIdExists(fanConfig.Curve, config) {
			return errors.New(fmt.Sprintf("Fan %s: no curve definition with id '%s' found", fanConfig.ID, fanConfig.Curve))
		}

		if fanConfig.HwMon != nil {
			if fanConfig.HwMon.Index <= 0 {
				return errors.New(fmt.Sprintf("Fan %s: invalid index, must be >= 1", fanConfig.ID))
			}
		}

		if fanConfig.File != nil {
			if len(fanConfig.File.Path) <= 0 {
				return errors.New(fmt.Sprintf("Fan %s: no file path provided", fanConfig.ID))
			}
		}

		if fanConfig.Cmd != nil {
			cmdConfig := fanConfig.Cmd
			if cmdConfig.PwmSet == nil {
				return errors.New(fmt.Sprintf("Fan %s: missing setPwm configuration", fanConfig.ID))
			}
			if len(cmdConfig.PwmSet.Exec) <= 0 {
				return errors.New(fmt.Sprintf("Fan %s: setPwm executable is missing", fanConfig.ID))
			}

			if cmdConfig.PwmGet == nil {
				return errors.New(fmt.Sprintf("Fan %s: missing getPwm configuration", fanConfig.ID))
			}
			if len(cmdConfig.PwmGet.Exec) <= 0 {
				return errors.New(fmt.Sprintf("Fan %s: getPwm executable is missing", fanConfig.ID))
			}
		}
	}

	return nil
}

func curveIdExists(curveId string, config *Configuration) bool {
	for _, curve := range config.Curves {
		if curve.ID == curveId {
			return true
		}
	}

	return false
}
