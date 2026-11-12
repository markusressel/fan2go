package quirks

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
)

// AdviceLevel is the severity of an Advice.
type AdviceLevel int

const (
	// AdviceTip is a suggestion: the config works, but could work better with this device.
	AdviceTip AdviceLevel = iota
	// AdviceWarning means that a part of the config is likely not to work with this device.
	AdviceWarning
)

// Advice is a tip or warning about a fan config, for `fan2go config validate`.
type Advice struct {
	Level   AdviceLevel
	Message string
}

// AdviceContext is the input of an AdviceFunc.
type AdviceContext struct {
	// Fan is the config of the fan that matched the entry.
	Fan configuration.FanConfig
	// AdjustmentTickRate is fanController.adjustmentTickRate.
	AdjustmentTickRate time.Duration
	// ReadFile reads a file (e.g. a module parameter), os.ReadFile if nil.
	ReadFile func(name string) ([]byte, error)
}

func (c AdviceContext) readFile(name string) ([]byte, error) {
	if c.ReadFile != nil {
		return c.ReadFile(name)
	}
	return os.ReadFile(name)
}

// AdviceFunc returns the advice of an entry for one fan config.
type AdviceFunc func(e *Entry, ctx AdviceContext) []Advice

// Check returns the advice of the entry for the given fan config.
func (e *Entry) Check(ctx AdviceContext) []Advice {
	if e == nil {
		return nil
	}
	var result []Advice
	for _, f := range e.Advice {
		result = append(result, f(e, ctx)...)
	}
	return result
}

// adviseReadbackFormula: a configured setPwmToGetPwmMap (identity, linear or values) replaces the driver formula of
// the entry.
func adviseReadbackFormula(e *Entry, ctx AdviceContext) []Advice {
	if e.Readback == nil || !hasSetPwmToGetPwmMap(ctx.Fan) {
		return nil
	}
	return []Advice{{AdviceTip, fmt.Sprintf(
		"fan %s: setPwmToGetPwmMap is set, but fan2go knows the readback formula of the %s driver "+
			"(known hardware quirk '%s'). Remove setPwmToGetPwmMap (or set it to autodetect) to use the exact formula instead.",
		ctx.Fan.ID, e.Description, e.ID)}}
}

// adviseSmoothingResolution: a configured deadband or ramp of the direct control algorithm that is finer than one
// output step of the device.
func adviseSmoothingResolution(e *Entry, ctx AdviceContext) []Advice {
	levels := e.LevelCount()
	step := e.WriteStep()
	if levels < 2 || levels >= 256 || step <= 1 {
		return nil
	}
	algorithm := ctx.Fan.ControlAlgorithm
	if algorithm == nil || algorithm.Direct == nil {
		return nil
	}
	direct := algorithm.Direct
	// step is the smallest change of the written pwm value that changes the output. One step in curve units is at
	// least as large (curve values are scaled to minPwm..maxPwm), so this only reports settings that are certainly
	// finer than one step.
	const epsilon = 1e-9

	var result []Advice
	if deadband := direct.Deadband.Value(); deadband > 0 && deadband < step-epsilon {
		result = append(result, Advice{AdviceTip, fmt.Sprintf(
			"fan %s: controlAlgorithm.direct.deadband is %.4g, but the %s has only %d output levels "+
				"(one step is at least %.4g of 255, known hardware quirk '%s'). A deadband below one step doesn't keep "+
				"the fan from switching between two levels; use a deadband of at least %.4g to avoid that.",
			ctx.Fan.ID, deadband, e.Description, levels, step, e.ID, step)})
	}
	if tick := ctx.AdjustmentTickRate; tick > 0 {
		for _, ramp := range []struct {
			name  string
			value float64
		}{{"rampUp", direct.RampUp.Value()}, {"rampDown", direct.RampDown.Value()}} {
			if ramp.value <= 0 || ramp.value*tick.Seconds() >= step-epsilon {
				continue
			}
			result = append(result, Advice{AdviceTip, fmt.Sprintf(
				"fan %s: controlAlgorithm.direct.%s is %.4g per second, which is less than one output step "+
					"(at least %.4g of 255) per cycle of %s on the %s (%d output levels, known hardware quirk '%s'). "+
					"The fan still changes its speed in whole steps, at most one every %s.",
				ctx.Fan.ID, ramp.name, ramp.value, step, tick, e.Description, levels, e.ID,
				time.Duration(step/ramp.value*float64(time.Second)).Round(100*time.Millisecond))})
		}
	}
	return result
}

// adviseOnlyPwmControlMode: drivers that accept only pwm_enable=1 (manual) on a controllable channel.
func adviseOnlyPwmControlMode(e *Entry, ctx AdviceContext) []Advice {
	cfg := ctx.Fan.ControlMode
	if cfg == nil {
		return nil
	}
	var result []Advice
	check := func(key string, value *configuration.ControlModeValue) {
		if value == nil || isPwmControlMode(*value) {
			return
		}
		result = append(result, Advice{AdviceWarning, fmt.Sprintf(
			"fan %s: %s is '%s', but the %s driver only accepts pwm (1) on a channel with a fan "+
				"(known hardware quirk '%s'), so setting this mode will fail.",
			ctx.Fan.ID, key, *value, e.Description, e.ID)})
	}
	check("controlMode.active", cfg.Active)
	if cfg.OnExit != nil {
		check("controlMode.onExit.mode", cfg.OnExit.ControlMode)
	}
	return result
}

func isPwmControlMode(value configuration.ControlModeValue) bool {
	s := strings.ToLower(strings.TrimSpace(string(value)))
	if i, err := strconv.Atoi(s); err == nil {
		return i == 1
	}
	return s == "pwm" || s == "manual"
}

// thinkpadFanControlParameter is the module parameter that enables fan control in thinkpad_acpi.
const thinkpadFanControlParameter = "/sys/module/thinkpad_acpi/parameters/fan_control"

// adviseThinkpadFanControl: thinkpad_acpi rejects all fan control writes unless fan_control=1 is set.
func adviseThinkpadFanControl(e *Entry, ctx AdviceContext) []Advice {
	content, err := ctx.readFile(thinkpadFanControlParameter)
	if err != nil {
		return []Advice{{AdviceTip, fmt.Sprintf(
			"fan %s: the thinkpad_acpi driver only allows fan control when it is loaded with fan_control=1 "+
				"(known hardware quirk '%s'); fan2go can't check this setting here (%v).", ctx.Fan.ID, e.ID, err)}}
	}
	switch strings.TrimSpace(string(content)) {
	case "Y", "y", "1":
		return nil
	}
	return []Advice{{AdviceWarning, fmt.Sprintf(
		"fan %s: fan control is disabled in the thinkpad_acpi driver, so fan2go can't control this fan "+
			"(known hardware quirk '%s'). Load the driver with fan_control=1, e.g. with the line "+
			"'options thinkpad_acpi fan_control=1' in /etc/modprobe.d/thinkpad_acpi.conf.", ctx.Fan.ID, e.ID)}}
}
