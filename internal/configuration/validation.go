package configuration

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"slices"

	"github.com/looplab/tarjan"
	"github.com/markusressel/fan2go/internal/control_loop"
	"github.com/markusressel/fan2go/internal/nvidia_base"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
)

// ValidateConfig checks the given config, read from the file at path. The returned error is a ConfigError.
func ValidateConfig(config *Configuration, path string) error {
	return NewConfigError(validateConfig(config, path))
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
	if err != nil {
		return err
	}
	// runs on the loaded config, so the deprecated controllerAdjustmentTickRate
	// (applied by applyDeprecations in LoadConfig) is covered too
	err = validateGlobals(config)
	if err != nil {
		return err
	}
	// needs a valid adjustmentTickRate
	err = validatePidControlAlgorithms(config)
	if err != nil {
		return err
	}
	// needs a valid sensorStaleTimeout
	warnAboutCmdSensorTimeouts(config)

	if containsCmdSensors(config) || containsCmdFan(config) {
		if _, err := util.CheckFilePermissionsForExecution(path); err != nil {
			return fmt.Errorf("config file '%s' has invalid permissions: %s", path, err)
		}
	}

	return nil
}

// validateGlobals checks top-level timing and sampling settings. Zero or negative
// rates make time.NewTicker panic, a window size of 0 turns the moving averages into NaN/Inf.
func validateGlobals(config *Configuration) error {
	positiveDurations := []struct {
		key   string
		value time.Duration
	}{
		{"tempSensorPollingRate", config.TempSensorPollingRate},
		{"rpmPollingRate", config.RpmPollingRate},
		{"fanController.adjustmentTickRate", config.FanController.AdjustmentTickRate},
	}
	for _, d := range positiveDurations {
		if d.value <= 0 {
			return fmt.Errorf("%s must be > 0, got %s", d.key, d.value)
		}
	}

	nonNegativeDurations := []struct {
		key   string
		value time.Duration
	}{
		{"fanController.pwmSetDelay", config.FanController.PwmSetDelay},
		{"analysis.sampleDelay", config.Analysis.SampleDelay},
		{"analysis.settleTimeout", config.Analysis.SettleTimeout},
	}
	for _, d := range nonNegativeDurations {
		if d.value < 0 {
			return fmt.Errorf("%s must be >= 0, got %s", d.key, d.value)
		}
	}

	if config.SensorStaleTimeout != nil && *config.SensorStaleTimeout <= 0 {
		return fmt.Errorf("sensorStaleTimeout must be > 0, got %s", *config.SensorStaleTimeout)
	}

	if config.FanResponseDelay < 0 {
		return fmt.Errorf("fanResponseDelay must be >= 0, got %v", config.FanResponseDelay)
	}

	minOne := []struct {
		key   string
		value int
	}{
		{"tempRollingWindowSize", config.TempRollingWindowSize},
		{"rpmRollingWindowSize", config.RpmRollingWindowSize},
		{"analysis.sampleCount", config.Analysis.SampleCount},
	}
	for _, v := range minOne {
		if v.value < 1 {
			return fmt.Errorf("%s must be >= 1, got %d", v.key, v.value)
		}
	}

	// written as !(x >= 0) so NaN is rejected too
	if !(config.MaxRpmDiffForSettledFan >= 0) {
		return fmt.Errorf("maxRpmDiffForSettledFan must be >= 0, got %v", config.MaxRpmDiffForSettledFan)
	}

	return nil
}

// validateLinearCurveRange checks how a linear curve is defined: by steps, or by a min/max temperature.
func validateLinearCurveRange(curveID string, linear *LinearCurveConfig) error {
	if len(linear.Steps) > 0 {
		if linear.Min != 0 || linear.Max != 0 {
			ui.Warning("curve %s: both steps and min/max are set, min/max are ignored", curveID)
		}
		return nil
	}
	if linear.Min == 0 && linear.Max == 0 {
		return fmt.Errorf("curve %s: linear curve needs either steps or min and max", curveID)
	}
	if linear.Min >= linear.Max {
		return fmt.Errorf("curve %s: min (%d) must be < max (%d)", curveID, linear.Min, linear.Max)
	}
	return nil
}

// validatePidControlAlgorithmGains checks the gains of the pid control algorithm. The loop uses its own
// previous output as the measured value, so without an I term its output collapses to p/(1+p) of the
// curve value (e.g. about 5 % with p: 0.05).
func validatePidControlAlgorithmGains(fanID, key string, p, i, d float64) error {
	for _, gain := range []struct {
		name  string
		value float64
	}{{"p", p}, {"i", i}, {"d", d}} {
		if math.IsNaN(gain.value) || math.IsInf(gain.value, 0) {
			return fmt.Errorf("fan %s: %s.%s must be a finite number, got %v", fanID, key, gain.name, gain.value)
		}
	}
	if i <= 0 {
		return fmt.Errorf("fan %s: %s.i must be > 0, got %v (omitted values are 0, the defaults are p: %v, i: %v, d: %v)",
			fanID, key, i, control_loop.DefaultPidConfig.P, control_loop.DefaultPidConfig.I, control_loop.DefaultPidConfig.D)
	}
	return nil
}

// pidControlAlgorithmGains returns the gains of the pid control algorithm the fan uses, and the config key
// they come from. ok is false if the fan uses another control algorithm. Mirrors the selection in
// createControlLoop (internal/factory.go).
func pidControlAlgorithmGains(fanConfig FanConfig) (p, i, d float64, key string, ok bool) {
	if controlLoop := fanConfig.ControlLoop; controlLoop != nil { //nolint:all
		return controlLoop.P, controlLoop.I, controlLoop.D, "controlLoop", true
	}
	// without controlAlgorithm, the fan uses the default (direct), see GetControlAlgorithm
	if algorithm := fanConfig.GetControlAlgorithm(); algorithm.Pid != nil {
		return algorithm.Pid.P, algorithm.Pid.I, algorithm.Pid.D, "controlAlgorithm.pid", true
	}
	return 0, 0, 0, "", false
}

// validatePidControlAlgorithms checks that the pid control algorithm of each fan is stable with the
// configured fanController.adjustmentTickRate (error), and warns if it overshoots. It also warns if the
// algorithm is unstable at the time step a gap between two cycles counts as (control_loop.GapDt): a fan whose
// every update takes longer than the gap threshold (e.g. a slow cmd fan) runs the loop at that step.
// Each warning is logged once per distinct algorithm (e.g. once for all fans with the same gains), with the
// ids of its fans.
func validatePidControlAlgorithms(config *Configuration) error {
	type algorithm struct {
		key     string
		p, i, d float64
	}
	tickRate := config.FanController.AdjustmentTickRate
	gapDt := control_loop.GapDt(tickRate)
	// the algorithms in the order of their first fan, and the ids of their fans
	var algorithms []algorithm
	fanIds := map[algorithm][]string{}
	for _, fanConfig := range config.Fans {
		p, i, d, key, ok := pidControlAlgorithmGains(fanConfig)
		if !ok {
			continue
		}
		a := algorithm{key: key, p: p, i: i, d: d}
		if _, seen := fanIds[a]; !seen {
			if !control_loop.AnalyzePidStability(p, i, d, tickRate).Stable {
				return fmt.Errorf("fan %s: %s (p: %v, i: %v, d: %v) is unstable with fanController.adjustmentTickRate %s, "+
					"its output would oscillate between 0 and 255. Lower i (for a slow adjustmentTickRate) or d (for a fast one), "+
					"change the adjustmentTickRate, or use controlAlgorithm: direct",
					fanConfig.ID, key, p, i, d, tickRate)
			}
			algorithms = append(algorithms, a)
		}
		fanIds[a] = append(fanIds[a], fanConfig.ID)
	}

	for _, a := range algorithms {
		label := "fan"
		if len(fanIds[a]) > 1 {
			label = "fans"
		}
		fans := strings.Join(fanIds[a], ", ")
		if result := control_loop.AnalyzePidStability(a.p, a.i, a.d, tickRate); result.Overshoots() {
			ui.Warning("%s %s: %s (p: %v, i: %v, d: %v) overshoots a change of the curve value by about %.0f%% with fanController.adjustmentTickRate %s. "+
				"Lower i or d, or the adjustmentTickRate, to avoid this",
				label, fans, a.key, a.p, a.i, a.d, result.Overshoot*100, tickRate)
		}
		if gapDt != tickRate && !control_loop.AnalyzePidStability(a.p, a.i, a.d, gapDt).Stable {
			ui.Warning("%s %s: %s (p: %v, i: %v, d: %v) is unstable at a time step of %s. A fan whose updates take longer than "+
				"the gap threshold (e.g. a slow cmd fan) runs the loop at this step, so its output would oscillate between 0 and 255. "+
				"Lower i if this applies to your fan",
				label, fans, a.key, a.p, a.i, a.d, gapDt)
		}
	}
	return nil
}

// warnAboutCmdSensorTimeouts warns about cmd sensors whose command may run longer than the sensorStaleTimeout:
// their value would become stale while the command is still running, so the fans using it would toggle
// between their onSensorFailure action and the curve.
func warnAboutCmdSensorTimeouts(config *Configuration) {
	staleTimeout := config.GetSensorStaleTimeout()
	for _, sensorConfig := range config.Sensors {
		if sensorConfig.Cmd == nil {
			continue
		}
		timeout := sensorConfig.Cmd.GetTimeout()
		if timeout >= staleTimeout {
			ui.Warning("sensor %s: cmd.timeout (%s) is not shorter than sensorStaleTimeout (%s), "+
				"the sensor value may become stale before the command times out", sensorConfig.ID, timeout, staleTimeout)
		}
	}
}

func containsCmdFan(config *Configuration) bool {
	for _, fanConfig := range config.Fans {
		if fanConfig.Cmd != nil {
			return true
		}
	}

	return false
}

func containsCmdSensors(config *Configuration) bool {
	for _, sensorConfig := range config.Sensors {
		if sensorConfig.Cmd != nil {
			return true
		}
	}

	return false
}

// validateNvidiaDevice checks the nvidia.device of a fan or sensor config. It is a (case-insensitive) regex
// that is matched against the device identifiers shown by `fan2go detect`, so an empty value would match
// the first GPU.
func validateNvidiaDevice(device string) error {
	if len(strings.TrimSpace(device)) <= 0 {
		return fmt.Errorf("nvidia.device must not be empty, use the identifier shown by 'fan2go detect', e.g. nvidia-10DE2489-0800")
	}
	if _, err := regexp.Compile("(?i)" + device); err != nil {
		return fmt.Errorf("nvidia.device is not a valid regex: %v", err)
	}
	return nil
}

func validateSensors(config *Configuration) error {
	sensorIds := []string{}

	for _, sensorConfig := range config.Sensors {
		if slices.Contains(sensorIds, sensorConfig.ID) {
			return fmt.Errorf("duplicate sensor id detected: %s", sensorConfig.ID)
		}
		sensorIds = append(sensorIds, sensorConfig.ID)

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
		if sensorConfig.Nvidia != nil {
			if nvidia_base.IsNvmlSupported {
				subConfigs++
				if err := validateNvidiaDevice(sensorConfig.Nvidia.Device); err != nil {
					return fmt.Errorf("sensor %s: %v", sensorConfig.ID, err)
				}
			} else {
				return fmt.Errorf("sensor %s: This version of fan2go was built without NVIDIA (nvml) support", sensorConfig.ID)
			}
		}
		if sensorConfig.Disk != nil {
			subConfigs++
		}
		if subConfigs > 1 {
			return fmt.Errorf("sensor %s: only one sensor type can be used per sensor definition block", sensorConfig.ID)
		}
		if subConfigs <= 0 {
			return fmt.Errorf("sensor %s: sub-configuration for sensor is missing, use one of: hwmon | nvidia | file | cmd | disk", sensorConfig.ID)
		}
		if sensorConfig.Cmd != nil && sensorConfig.Cmd.Timeout < 0 {
			return fmt.Errorf("sensor %s: cmd.timeout must be >= 0, got %s", sensorConfig.ID, sensorConfig.Cmd.Timeout)
		}

		if !isSensorConfigInUse(sensorConfig, config.Curves) {
			ui.Warning("Unused sensor configuration: %s", sensorConfig.ID)
		}

		if sensorConfig.HwMon != nil {
			hasIndex := sensorConfig.HwMon.Index > 0
			hasChannel := sensorConfig.HwMon.Channel > 0
			if (hasIndex && hasChannel) || (!hasIndex && !hasChannel) {
				return fmt.Errorf("sensor %s: must have exactly one of index or channel, must be >= 1", sensorConfig.ID)
			}
		}

		if sensorConfig.Disk != nil {
			if len(sensorConfig.Disk.Device) == 0 {
				return fmt.Errorf("sensor %s: disk sensor requires a device path", sensorConfig.ID)
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
		if curveConfig.Staircase != nil && curveConfig.Staircase.Sensor == config.ID {
			return true
		}
		if curveConfig.PID != nil && curveConfig.PID.Sensor == config.ID {
			return true
		}
	}

	return false
}

func validateCurves(config *Configuration) error {
	graph := make(map[interface{}][]interface{})
	curveIds := []string{}

	for _, curveConfig := range config.Curves {
		if slices.Contains(curveIds, curveConfig.ID) {
			return fmt.Errorf("duplicate curve id detected: %s", curveConfig.ID)
		}
		curveIds = append(curveIds, curveConfig.ID)

		subConfigs := 0
		if curveConfig.Linear != nil {
			subConfigs++
		}
		if curveConfig.Staircase != nil {
			subConfigs++
		}
		if curveConfig.PID != nil {
			subConfigs++
		}
		if curveConfig.Function != nil {
			subConfigs++
		}
		if subConfigs > 1 {
			return fmt.Errorf("curve %s: only one curve type can be used per curve definition block", curveConfig.ID)
		}
		if subConfigs <= 0 {
			return fmt.Errorf("curve %s: sub-configuration for curve is missing, use one of: linear | staircase | pid | function", curveConfig.ID)
		}

		if !isCurveConfigInUse(curveConfig, config.Curves, config.Fans) {
			ui.Warning("Unused curve configuration: %s", curveConfig.ID)
		}

		if curveConfig.Function != nil {
			supportedTypes := []string{FunctionMinimum, FunctionAverage, FunctionMaximum, FunctionDelta, FunctionSum, FunctionDifference}
			if !slices.Contains(supportedTypes, curveConfig.Function.Type) {
				return fmt.Errorf("curve %s: unsupported function type '%s', use one of: %s", curveConfig.ID, curveConfig.Function.Type, strings.Join(supportedTypes, " | "))
			}

			if len(curveConfig.Function.Curves) < 2 {
				return fmt.Errorf("curve %s: function curves must reference at least 2 other curves", curveConfig.ID)
			}

			var connections []interface{}
			for _, curve := range curveConfig.Function.Curves {
				if curve == curveConfig.ID {
					return fmt.Errorf("curve %s: a curve cannot reference itself", curveConfig.ID)
				}
				if !curveIdExists(curve, config) {
					return fmt.Errorf("curve %s: no curve definition with id '%s' found", curveConfig.ID, curve)
				}
				connections = append(connections, curve)
			}
			graph[curveConfig.ID] = connections
		}

		if curveConfig.Linear != nil {
			if len(curveConfig.Linear.Sensor) <= 0 {
				return fmt.Errorf("curve %s: missing sensorId", curveConfig.ID)
			}

			if !sensorIdExists(curveConfig.Linear.Sensor, config) {
				return fmt.Errorf("curve %s: no sensor definition with id '%s' found", curveConfig.ID, curveConfig.Linear.Sensor)
			}

			if err := validateLinearCurveRange(curveConfig.ID, curveConfig.Linear); err != nil {
				return err
			}
			if down := curveConfig.Linear.GetHysteresisDown(); math.IsNaN(down) || math.IsInf(down, 0) || down < 0 {
				return fmt.Errorf("curve %s: hysteresis.down must be a finite number >= 0, got %v", curveConfig.ID, down)
			}
		}

		if curveConfig.Staircase != nil {
			if len(curveConfig.Staircase.Sensor) <= 0 {
				return fmt.Errorf("curve %s: missing sensorId", curveConfig.ID)
			}

			if !sensorIdExists(curveConfig.Staircase.Sensor, config) {
				return fmt.Errorf("curve %s: no sensor definition with id '%s' found", curveConfig.ID, curveConfig.Staircase.Sensor)
			}

			if len(curveConfig.Staircase.InSteps) <= 0 {
				return fmt.Errorf("curve %s: missing steps", curveConfig.ID)
			}
		}

		if curveConfig.PID != nil {
			if len(curveConfig.PID.Sensor) <= 0 {
				return fmt.Errorf("curve %s: missing sensorId", curveConfig.ID)
			}

			if !sensorIdExists(curveConfig.PID.Sensor, config) {
				return fmt.Errorf("curve %s: no sensor definition with id '%s' found", curveConfig.ID, curveConfig.PID.Sensor)
			}

			pidConfig := curveConfig.PID
			if math.IsNaN(pidConfig.SetPoint) || math.IsInf(pidConfig.SetPoint, 0) {
				return fmt.Errorf("curve %s: setPoint must be a finite number, got %v", curveConfig.ID, pidConfig.SetPoint)
			}
			if pidConfig.P == 0 && pidConfig.I == 0 && pidConfig.D == 0 {
				return fmt.Errorf("curve %s: all PID constants are zero", curveConfig.ID)
			}
			// error = setPoint - temperature, so a curve that cools needs negative gains
			if pidConfig.P >= 0 && pidConfig.I >= 0 && pidConfig.D >= 0 {
				ui.Warning("curve %s: all PID constants are positive, so the curve value decreases when the temperature rises above the setPoint. "+
					"Use negative values to cool (e.g. p: -0.05, i: -0.005, d: -0.005)", curveConfig.ID)
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
			return fmt.Errorf("you have created a curve dependency cycle: %v", items)
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
	fanIds := []string{}

	for _, fanConfig := range config.Fans {
		if slices.Contains(fanIds, fanConfig.ID) {
			return fmt.Errorf("duplicate fan id detected: %s", fanConfig.ID)
		}
		fanIds = append(fanIds, fanConfig.ID)

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
		if fanConfig.Nvidia != nil {
			if nvidia_base.IsNvmlSupported {
				subConfigs++
				if err := validateNvidiaDevice(fanConfig.Nvidia.Device); err != nil {
					return fmt.Errorf("fan %s: %v", fanConfig.ID, err)
				}
			} else {
				return fmt.Errorf("fan %s: This version of fan2go was built without NVIDIA (nvml) support", fanConfig.ID)
			}
		}

		if subConfigs > 1 {
			return fmt.Errorf("fan %s: only one fan type can be used per fan definition block", fanConfig.ID)
		}
		if subConfigs <= 0 {
			return fmt.Errorf("fan %s: sub-configuration for fan is missing, use one of: hwmon | nvidia | file | cmd", fanConfig.ID)
		}

		if len(fanConfig.Curve) <= 0 {
			return fmt.Errorf("fan %s: missing curve definition in configuration entry", fanConfig.ID)
		}

		if !curveIdExists(fanConfig.Curve, config) {
			return fmt.Errorf("fan %s: no curve definition with id '%s' found", fanConfig.ID, fanConfig.Curve)
		}

		if err := validateFanPwmLimits(fanConfig); err != nil {
			return err
		}

		if fanConfig.Quirks != nil && fanConfig.HwMon == nil {
			return fmt.Errorf("fan %s: quirks can only be configured for hwmon fans", fanConfig.ID)
		}

		if fanConfig.PwmSetDelay != nil && *fanConfig.PwmSetDelay < 0 {
			return fmt.Errorf("fan %s: pwmSetDelay must be >= 0, got %s", fanConfig.ID, *fanConfig.PwmSetDelay)
		}

		switch fanConfig.OnSensorFailure {
		case "", OnSensorFailureMax, OnSensorFailureRestore:
		default:
			return fmt.Errorf("fan %s: invalid onSensorFailure %q, must be one of: %s | %s",
				fanConfig.ID, fanConfig.OnSensorFailure, OnSensorFailureMax, OnSensorFailureRestore)
		}

		if err := validateKickStart(fanConfig); err != nil {
			return err
		}

		if fanConfig.ControlLoop != nil { //nolint:all
			ui.Warning("fan %s: controlLoop is deprecated, use controlAlgorithm.pid instead", fanConfig.ID)
			if fanConfig.ControlAlgorithm != nil {
				return fmt.Errorf("fan %s: controlLoop and controlAlgorithm can't be used together, remove the deprecated controlLoop", fanConfig.ID)
			}
			controlLoop := fanConfig.ControlLoop //nolint:all
			if err := validatePidControlAlgorithmGains(fanConfig.ID, "controlLoop", controlLoop.P, controlLoop.I, controlLoop.D); err != nil {
				return err
			}
		}

		if fanConfig.ControlAlgorithm != nil {
			if fanConfig.ControlAlgorithm.Direct == nil && fanConfig.ControlAlgorithm.Pid == nil {
				// e.g. "direct:" without a value: it would silently get the default (smoothing), not the pass-through
				return fmt.Errorf("fan %s: controlAlgorithm is empty, use one of: direct | pid "+
					"(controlAlgorithm: direct applies the curve value without smoothing; omit controlAlgorithm for the default)", fanConfig.ID)
			}
			if fanConfig.ControlAlgorithm.Direct != nil && fanConfig.ControlAlgorithm.Pid != nil {
				return fmt.Errorf("fan %s: controlAlgorithm can only be one of: direct | pid", fanConfig.ID)
			}

			if fanConfig.ControlAlgorithm.Direct != nil {
				err := validateDirectControlAlgorithm(fanConfig.ID, *fanConfig.ControlAlgorithm.Direct, config.FanController.AdjustmentTickRate)
				if err != nil {
					return err
				}
			}

			if fanConfig.ControlAlgorithm.Pid != nil {
				pidConfig := fanConfig.ControlAlgorithm.Pid
				if pidConfig.P == 0 && pidConfig.I == 0 && pidConfig.D == 0 {
					return fmt.Errorf("fan %s: all PID constants are zero", fanConfig.ID)
				}
				if err := validatePidControlAlgorithmGains(fanConfig.ID, "controlAlgorithm.pid", pidConfig.P, pidConfig.I, pidConfig.D); err != nil {
					return err
				}
			}
		}

		if fanConfig.HwMon != nil {
			if (fanConfig.HwMon.Index != 0 && fanConfig.HwMon.RpmChannel != 0) || (fanConfig.HwMon.Index == 0 && fanConfig.HwMon.RpmChannel == 0) {
				return fmt.Errorf("fan %s: must have one of index or rpmChannel, must be >= 1", fanConfig.ID)
			}
			if fanConfig.HwMon.Index < 0 {
				return fmt.Errorf("fan %s: invalid index, must be >= 1", fanConfig.ID)
			}
			if fanConfig.HwMon.RpmChannel < 0 {
				return fmt.Errorf("fan %s: invalid rpmChannel, must be >= 1", fanConfig.ID)
			}
			if fanConfig.HwMon.PwmChannel < 0 {
				return fmt.Errorf("fan %s: invalid pwmChannel, must be >= 1", fanConfig.ID)
			}
		}

		validatePwmMapPoints := func(label string, pts map[int]int, strict bool) error {
			if len(pts) == 0 {
				return fmt.Errorf("fan '%s': %s requires at least one control point", fanConfig.ID, label)
			}
			for k, v := range pts {
				if k < 0 || k > 255 {
					return fmt.Errorf("fan '%s': %s key %d is out of range [0..255]", fanConfig.ID, label, k)
				}
				if v < 0 || v > 255 {
					return fmt.Errorf("fan '%s': %s value %d (key %d) is out of range [0..255]", fanConfig.ID, label, v, k)
				}
			}

			var err error
			if strict {
				err = util.IsStrictlyMonotonicallyIncreasing(pts)
			} else {
				err = util.IsMonotonicallyIncreasing(pts)
			}
			if err != nil {
				return fmt.Errorf("fan '%s': %s %w", fanConfig.ID, label, err)
			}

			return nil
		}

		if fanConfig.PwmMap != nil {
			pwmMapSubConfigs := 0
			if fanConfig.PwmMap.Autodetect != nil {
				pwmMapSubConfigs++
			}
			if fanConfig.PwmMap.Identity != nil {
				pwmMapSubConfigs++
			}
			if fanConfig.PwmMap.Linear != nil {
				pwmMapSubConfigs++
			}
			if fanConfig.PwmMap.Values != nil {
				pwmMapSubConfigs++
			}

			if pwmMapSubConfigs == 0 {
				return fmt.Errorf("fan '%s': pwmMap is set but no mode is specified", fanConfig.ID)
			}
			if pwmMapSubConfigs > 1 {
				return fmt.Errorf("fan '%s': only one pwmMap mode can be configured at a time", fanConfig.ID)
			}

			if fanConfig.PwmMap.Linear != nil {
				if err := validatePwmMapPoints("pwmMap linear", *fanConfig.PwmMap.Linear, true); err != nil {
					return err
				}
			}
			if fanConfig.PwmMap.Values != nil {
				if err := validatePwmMapPoints("pwmMap values", *fanConfig.PwmMap.Values, true); err != nil {
					return err
				}
			}
		}

		if fanConfig.SetPwmToGetPwmMap != nil {
			setPwmSubConfigs := 0
			if fanConfig.SetPwmToGetPwmMap.Autodetect != nil {
				setPwmSubConfigs++
			}
			if fanConfig.SetPwmToGetPwmMap.Identity != nil {
				setPwmSubConfigs++
			}
			if fanConfig.SetPwmToGetPwmMap.Linear != nil {
				setPwmSubConfigs++
			}
			if fanConfig.SetPwmToGetPwmMap.Values != nil {
				setPwmSubConfigs++
			}

			if setPwmSubConfigs == 0 {
				return fmt.Errorf("fan '%s': setPwmToGetPwmMap is set but no mode is specified", fanConfig.ID)
			}
			if setPwmSubConfigs > 1 {
				return fmt.Errorf("fan '%s': only one setPwmToGetPwmMap mode can be configured at a time", fanConfig.ID)
			}

			if fanConfig.SetPwmToGetPwmMap.Linear != nil {
				if err := validatePwmMapPoints("setPwmToGetPwmMap linear", *fanConfig.SetPwmToGetPwmMap.Linear, false); err != nil {
					return err
				}
			}
			if fanConfig.SetPwmToGetPwmMap.Values != nil {
				if err := validatePwmMapPoints("setPwmToGetPwmMap values", *fanConfig.SetPwmToGetPwmMap.Values, false); err != nil {
					return err
				}
			}
		}

		if fanConfig.ControlMode != nil {
			cm := fanConfig.ControlMode

			if cm.Active != nil {
				if err := validateControlModeValue(fanConfig.ID, "controlMode.active", string(*cm.Active)); err != nil {
					return err
				}
			}

			if cm.OnExit != nil {
				hasRestore := cm.OnExit.Restore != nil
				hasNone := cm.OnExit.None != nil
				hasMode := cm.OnExit.ControlMode != nil
				hasSpeed := cm.OnExit.Speed != nil

				if !hasRestore && !hasNone && !hasMode && !hasSpeed {
					return fmt.Errorf("fan '%s': controlMode.onExit is set but no option is specified", fanConfig.ID)
				}
				if (hasRestore || hasNone) && (hasMode || hasSpeed) {
					return fmt.Errorf("fan '%s': controlMode.onExit restore/none cannot be combined with mode/speed", fanConfig.ID)
				}
				if hasRestore && hasNone {
					return fmt.Errorf("fan '%s': controlMode.onExit restore and none cannot be combined", fanConfig.ID)
				}

				if hasMode {
					if err := validateControlModeValue(fanConfig.ID, "controlMode.onExit.mode", string(*cm.OnExit.ControlMode)); err != nil {
						return err
					}
				}
				if hasSpeed {
					speed := *cm.OnExit.Speed
					if speed < 0 || speed > 255 {
						return fmt.Errorf("fan '%s': controlMode.onExit.speed must be in [0..255], got %d", fanConfig.ID, speed)
					}
				}
			}
		}

		if fanConfig.File != nil {
			if len(fanConfig.File.Path) <= 0 {
				return fmt.Errorf("fan %s: no file path provided", fanConfig.ID)
			}
		}

		if fanConfig.Cmd != nil {
			cmdConfig := fanConfig.Cmd
			if cmdConfig.SetPwm == nil {
				return fmt.Errorf("fan %s: missing setPwm configuration", fanConfig.ID)
			}
			if len(cmdConfig.SetPwm.Exec) <= 0 {
				return fmt.Errorf("fan %s: setPwm executable is missing", fanConfig.ID)
			}
			execConfigs := []struct {
				key    string
				config *ExecConfig
			}{
				{"setPwm", cmdConfig.SetPwm},
				{"getPwm", cmdConfig.GetPwm},
				{"getRpm", cmdConfig.GetRpm},
			}
			for _, execConfig := range execConfigs {
				if execConfig.config != nil && execConfig.config.Timeout < 0 {
					return fmt.Errorf("fan %s: cmd.%s.timeout must be >= 0, got %s", fanConfig.ID, execConfig.key, execConfig.config.Timeout)
				}
			}
		}
	}

	return nil
}

// validateFanPwmLimits checks the configured minPwm, startPwm and maxPwm of a fan.
// Unset values are detected during fan initialization and can't be checked here.
func validateFanPwmLimits(fanConfig FanConfig) error {
	limits := []struct {
		key   string
		value *int
	}{
		{"minPwm", fanConfig.MinPwm},
		{"startPwm", fanConfig.StartPwm},
		{"maxPwm", fanConfig.MaxPwm},
	}
	for _, l := range limits {
		if l.value != nil && (*l.value < 0 || *l.value > 255) {
			return fmt.Errorf("fan %s: %s must be in [0..255], got %d", fanConfig.ID, l.key, *l.value)
		}
	}

	if fanConfig.MaxPwm != nil {
		maxPwm := *fanConfig.MaxPwm
		if fanConfig.MinPwm != nil && *fanConfig.MinPwm > maxPwm {
			return fmt.Errorf("fan %s: minPwm (%d) must be <= maxPwm (%d)", fanConfig.ID, *fanConfig.MinPwm, maxPwm)
		}
		if fanConfig.StartPwm != nil && *fanConfig.StartPwm > maxPwm {
			return fmt.Errorf("fan %s: startPwm (%d) must be <= maxPwm (%d)", fanConfig.ID, *fanConfig.StartPwm, maxPwm)
		}
	}

	if fanConfig.MinPwm != nil && fanConfig.StartPwm != nil && *fanConfig.MinPwm > *fanConfig.StartPwm {
		ui.Warning("fan %s: suspicious pwm config: minPwm (%d) > startPwm (%d)", fanConfig.ID, *fanConfig.MinPwm, *fanConfig.StartPwm)
	}
	if fanConfig.MinPwm != nil && fanConfig.StartPwm != nil && *fanConfig.MinPwm < *fanConfig.StartPwm && !fanConfig.NeverStop &&
		fanConfig.GetKickStart().Mode == KickStartOff {
		// without a kick, a fan that stopped doesn't get a higher PWM value to start again
		ui.Warning("fan %s: minPwm (%d) < startPwm (%d) without neverStop and with kickStart.mode: %s: once the fan "+
			"stopped, a PWM value between minPwm and startPwm may not start it again, so it stays stopped. Set minPwm "+
			"to at least startPwm, enable kickStart or set neverStop: true",
			fanConfig.ID, *fanConfig.MinPwm, *fanConfig.StartPwm, KickStartOff)
	}

	return nil
}

// validateKickStart checks the kickStart config of a fan.
func validateKickStart(fanConfig FanConfig) error {
	kick := fanConfig.KickStart
	if kick == nil {
		return nil
	}
	switch kick.Mode {
	case "", KickStartAuto, KickStartFixed, KickStartOff:
	default:
		return fmt.Errorf("fan %s: invalid kickStart.mode %q, must be one of: %s | %s | %s",
			fanConfig.ID, kick.Mode, KickStartAuto, KickStartFixed, KickStartOff)
	}
	if kick.Pwm != nil {
		pwm := *kick.Pwm
		if pwm < 1 || pwm > 255 {
			return fmt.Errorf("fan %s: kickStart.pwm must be in [1..255], got %d", fanConfig.ID, pwm)
		}
		if fanConfig.MaxPwm != nil && pwm > *fanConfig.MaxPwm {
			return fmt.Errorf("fan %s: kickStart.pwm (%d) must be <= maxPwm (%d)", fanConfig.ID, pwm, *fanConfig.MaxPwm)
		}
		if kick.Mode != KickStartFixed {
			ui.Warning("fan %s: kickStart.pwm is only used with kickStart.mode: %s. With mode auto, set startPwm to "+
				"the PWM value the fan needs to start from standstill instead", fanConfig.ID, KickStartFixed)
		}
	} else if kick.Mode == KickStartFixed {
		return fmt.Errorf("fan %s: kickStart.mode: %s requires kickStart.pwm", fanConfig.ID, KickStartFixed)
	}
	if kick.Duration < 0 {
		return fmt.Errorf("fan %s: kickStart.duration must be >= 0, got %s", fanConfig.ID, kick.Duration)
	}
	if kick.Duration > MaxKickStartDuration {
		return fmt.Errorf("fan %s: kickStart.duration must be <= %s, got %s", fanConfig.ID, MaxKickStartDuration, kick.Duration)
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

func validateControlModeValue(fanID, field, s string) error {
	if _, err := strconv.Atoi(s); err == nil {
		return nil // valid integer
	}
	switch strings.ToLower(s) {
	case "auto", "automatic", "pwm", "manual", "disabled":
		return nil
	default:
		return fmt.Errorf("fan '%s': invalid %s %q (valid: auto, pwm, disabled, or integer)", fanID, field, s)
	}
}

const (
	// minReasonableTimeConstant is the timeConstant of the direct control algorithm below which validation warns
	// (e.g. "2ms" instead of "2s")
	minReasonableTimeConstant = 100 * time.Millisecond
	// maxReasonableTimeConstant is the timeConstant of the direct control algorithm above which validation warns
	maxReasonableTimeConstant = time.Minute
	// maxReasonableFullRampUp is the time a full increase (0 to 255) may take with rampUp before validation warns
	maxReasonableFullRampUp = time.Minute
	// maxReasonableDeadband is the deadband (10%) above which validation warns
	maxReasonableDeadband = 25.5
)

// validateDirectControlAlgorithm checks the options of the direct control algorithm of a fan.
// tickRate is fanController.adjustmentTickRate (validated later, it may be <= 0 here).
func validateDirectControlAlgorithm(fanID string, direct DirectControlAlgorithmConfig, tickRate time.Duration) error {
	const key = "controlAlgorithm.direct"

	timeConstant := direct.TimeConstant.Duration()
	if timeConstant < 0 {
		return fmt.Errorf("fan %s: %s.timeConstant must be >= 0, got %s", fanID, key, timeConstant)
	}
	if timeConstant > 0 && timeConstant < minReasonableTimeConstant {
		ui.Warning("fan %s: %s.timeConstant is %s, which smooths almost nothing (a plain number is seconds, "+
			"e.g. 2 or \"2s\")", fanID, key, timeConstant)
	}
	if timeConstant > maxReasonableTimeConstant {
		ui.Warning("fan %s: %s.timeConstant is %s, the fan will react very slowly to temperature changes",
			fanID, key, timeConstant)
	}

	for _, rate := range []struct {
		name  string
		value CurveUnits
	}{{"rampUp", direct.RampUp}, {"rampDown", direct.RampDown}} {
		if err := validateCurveUnits(fanID, key+"."+rate.name, rate.value); err != nil {
			return err
		}
	}
	if direct.RampUp > 0 {
		if fullRamp := time.Duration(255 / direct.RampUp.Value() * float64(time.Second)); fullRamp > maxReasonableFullRampUp {
			ui.Warning("fan %s: %s.rampUp is %v per second, so a full increase of the fan speed takes %s, "+
				"which delays cooling", fanID, key, direct.RampUp.Value(), fullRamp.Round(time.Second))
		}
	}

	if err := validateCurveUnits(fanID, key+".deadband", direct.Deadband); err != nil {
		return err
	}
	if direct.Deadband >= 255 {
		return fmt.Errorf("fan %s: %s.deadband must be < 255 (100%%), got %v", fanID, key, direct.Deadband.Value())
	}
	if direct.Deadband > maxReasonableDeadband {
		ui.Warning("fan %s: %s.deadband is %v (more than 10%%), the fan speed may stay noticeably off the curve",
			fanID, key, direct.Deadband.Value())
	}

	maxPwmChangePerCycle := direct.MaxPwmChangePerCycle //nolint:staticcheck // deprecated, but still supported
	if maxPwmChangePerCycle != nil {
		if *maxPwmChangePerCycle <= 0 {
			return fmt.Errorf("fan %s: invalid maxPwmChangePerCycle, must be > 0", fanID)
		}
		if direct.RampUp > 0 || direct.RampDown > 0 {
			return fmt.Errorf("fan %s: %s.maxPwmChangePerCycle can't be combined with rampUp or rampDown, "+
				"remove the deprecated maxPwmChangePerCycle", fanID, key)
		}
		if tickRate > 0 {
			perSecond := float64(*maxPwmChangePerCycle) / tickRate.Seconds()
			ui.Warning("fan %s: %s.maxPwmChangePerCycle is deprecated, because it depends on fanController.adjustmentTickRate. "+
				"With the current adjustmentTickRate of %s, %d per cycle is %v per second: "+
				"use rampUp: %v and rampDown: %v instead",
				fanID, key, tickRate, *maxPwmChangePerCycle, perSecond, perSecond, perSecond)
		}
	}
	return nil
}

// validateCurveUnits checks that a CurveUnits value (a rate or a deadband) is >= 0 and finite.
func validateCurveUnits(fanID string, key string, value CurveUnits) error {
	v := value.Value()
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return fmt.Errorf("fan %s: %s must be a finite number, got %v", fanID, key, v)
	}
	if v < 0 {
		return fmt.Errorf("fan %s: %s must be >= 0, got %v", fanID, key, v)
	}
	return nil
}
