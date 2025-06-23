package controller

import (
	"context"
	"errors"
	"github.com/markusressel/fan2go/internal/control_loop"
	"golang.org/x/exp/maps"
	"math"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/curves"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/persistence"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/oklog/run"
)

var (
	ErrFanStalledAtMaxPwm = errors.New("fan stalled at max pwm")
)

var InitializationSequenceMutex sync.Mutex

type FanControllerStatistics struct {
	UnexpectedPwmValueCount int
	IncreasedMinPwmCount    int
	MinPwmOffset            int
}

type FanController interface {
	// Run starts the control loop
	Run(ctx context.Context) error

	GetFanId() string

	GetStatistics() FanControllerStatistics

	// RunInitializationSequence for the given fan to determine its characteristics
	RunInitializationSequence() (err error)

	UpdateFanSpeed() error
}

type DefaultFanController struct {
	// controller statistics
	stats FanControllerStatistics
	// persistence where fan data is stored
	persistence persistence.Persistence
	// the fan to control
	fan fans.Fan
	// the curve used to control the fan
	curve curves.SpeedCurve
	// rate to update the target fan speed
	updateRate time.Duration
	// the original ControlMode state of the fan before starting the controller
	originalControlMode fans.ControlMode
	// the original pwm value of the fan before starting the controller
	// Note: this is the raw value read from the fan, no pwmMap is applied to it
	originalPwmValue int
	// the last pwm value that was set to the fan, **before** applying the pwmMap to it
	lastTarget *int
	// a list of all pre-pwmMap pwm values where setPwm(x) != setPwm(y) for the controlled fan
	targetValuesWithDistinctPWMValue []int

	// a map of setPwm(x) -> getPwm() = Y for x in [0..255] for the controlled fan
	// this map is used to know what pwm value Y will be reported by the fan
	// after applying a certain pwm value x to it. This is necessary
	// because some fans do not work in the internal value range of [0..255] expected by fan2go,
	// but rather in a different range, e.g. [0..100] or [0..255] with some values missing, yet still
	// require the value that is set to be in the range of [0..255].
	// Don't ask me why, hardware drivers are weird.
	//
	// Note that this map is guaranteed to contain all values in the range of [0..255] as keys,
	// since some fans do not support writing the full range.
	// Note that the values in this map also are not guaranteed to cover the entire range of [0..255],
	// be completely distinct, or be without gaps.
	//
	// Examples:
	//  [0: 0, 1: 1, 2: 2, 3: 3, ..., 100: 100, 101: 101, 102: 102, ..., 255: 255]
	//  [0: 0, 1: 1, 2: 2, 3: 3]
	//  [0: 0, 128: 128, 255: 255]
	//  [0: 0, 128: 1, 255: 2]
	//  [0: 0, 1: 128, 2: 255]
	setPwmToGetPwmMap map[int]int

	// The pwmMap is used to map a pwm value X to be applied to a fan to another value.
	// It can be provided by the user (in fan2go.yaml) or be generated automatically.
	// This is used to support fans that do not support the full range of [0..255] pwm values,
	// but rather a subset of it, e.g. [0..100] or [0..255] with some values missing.
	//
	// This mapping **always** contains the full range of [0..255] as keys - if the user-provided
	// pwmMap is missing keys they're added and those added keys then map to the value of the closest
	// user-provided key - but the values are not guaranteed to be in the range of [0..255] as well.
	//
	// Examples:
	//  [0: 0, 1: 1, 2: 2, 3: 3, ..., 100: 100, 101: 101, 102: 102, ..., 255: 255]
	//  [0: 0, 1: 0, 2: 0, 3: 0, ..., 84: 0, 85: 128, ..., 169: 128, 170: 255, ..., 255: 255]
	//  [0: 0, 1: 0, 2: 0, 3: 0, ..., 63: 0, 64: 1, ..., 127: 1, 128: 2, ..., 191: 2, 192: 3, ..., 255: 3]
	//
	//  If the user provided a map like [0: 0, 128: 3, 255: 6], meaning "the fan supports three speed values:
	//  PWM 0 (not rotating) happens with 0, PWM 128 (running at half speed or so) happens when setting
	//  the fan-specific speed value to 3, you get full speed (PWM 255) with fan-specific speed value 6",
	//  then that's expanded to:
	//   [0:0, 1: 0, ..., 63: 0, 64: 3, 65: 3, ..., 128: 3, 129: 3, ..., 190: 3, 191: 6, 192: 6, ... 255: 6]
	//
	// It's actually implemented as an array, where the "key" is the index
	pwmMapping [256]int

	// don't get and set PWMs in computeSetPwmToGetPwmMapAutomatically(), assume 1:1 mapping instead
	// (enabled by `fan2go fan -i bla init -s` or with the skipAutoPwmMap option in fan configs)
	skipAutoPwmMapping bool

	// control loop that specifies how the target value of the curve is approached
	controlLoop control_loop.ControlLoop

	// offset applied to the actual minPwm of the fan to ensure "neverStops" constraint
	minPwmOffset int
}

func NewFanController(
	persistence persistence.Persistence,
	fan fans.Fan,
	controlLoop control_loop.ControlLoop,
	updateRate time.Duration,
	skipAutoPwmMapping bool,
) FanController {
	curve, ok := curves.GetSpeedCurve(fan.GetCurveId())
	if !ok {
		ui.Fatal("Failed to create fan controller for fan '%s': Curve with ID '%s' not found", fan.GetId(), fan.GetCurveId())
	}
	return &DefaultFanController{
		persistence:                      persistence,
		fan:                              fan,
		curve:                            curve,
		updateRate:                       updateRate,
		targetValuesWithDistinctPWMValue: []int{},
		controlLoop:                      controlLoop,
		minPwmOffset:                     0,
		skipAutoPwmMapping:               skipAutoPwmMapping,
	}
}

func (f *DefaultFanController) GetFanId() string {
	return f.fan.GetId()
}

func (f *DefaultFanController) GetStatistics() FanControllerStatistics {
	return f.stats
}

func (f *DefaultFanController) prepareController() (err error) {
	err = f.persistence.Init()
	if err != nil {
		return err
	}

	fan := f.fan

	if fan.ShouldNeverStop() && !fan.Supports(fans.FeatureRpmSensor) {
		ui.Warning("WARN: cannot guarantee neverStop option on fan %s, since it has no RPM input.", fan.GetId())
	}

	return err
}

func (f *DefaultFanController) storeCurrentFanState() error {
	fan := f.fan
	// store original pwm value
	pwm, err := f.getPwm()
	if err != nil {
		ui.Warning("Cannot read pwm value of %s", fan.GetId())
	}
	f.originalPwmValue = pwm

	// store original pwm_enable value
	if f.fan.Supports(fans.FeatureControlMode) {
		controlMode, err := fan.GetControlMode()
		if err != nil {
			ui.Warning("Cannot read pwm_enable value of %s", fan.GetId())
		}
		f.originalControlMode = controlMode
	}
	return nil
}

func (f *DefaultFanController) Run(ctx context.Context) error {
	// prepare the controller by initializing persistence and checking the fan
	err := f.prepareController()
	if err != nil {
		return err
	}

	// store the current fan state to restore it when stopping the controller
	err = f.storeCurrentFanState()
	if err != nil {
		return err
	}

	fan := f.fan

	ui.Info("Gathering sensor data for %s...", fan.GetId())
	// wait a bit to gather monitoring data
	time.Sleep(2*time.Second + configuration.CurrentConfig.TempSensorPollingRate*2)

	fanPwmData, err := f.runInitializationIfNeeded()
	if err != nil {
		return err
	}

	fanPwmData, err = f.persistence.LoadFanRpmData(fan)
	if err != nil {
		return err
	}

	err = fan.AttachFanRpmCurveData(&fanPwmData)
	if err != nil {
		return err
	}

	err = f.computeFanSpecificMappings()

	ui.Debug("setPwmToGetPwmMap of fan '%s': %v", fan.GetId(), f.setPwmToGetPwmMap)
	ui.Debug("pwmMap of fan '%s': %v", fan.GetId(), f.pwmMapping)
	ui.Info("PWM settings of fan '%s': Min %d, Start %d, Max %d", fan.GetId(), fan.GetMinPwm(), fan.GetStartPwm(), fan.GetMaxPwm())
	alwaysSetPwmStr := ""
	if fan.GetConfig().AlwaysSetPwmMode {
		alwaysSetPwmStr = "with AlwaysSetPwmMode enabled: Will (re)set the PWM mode to manual each cycle"
	}
	ui.Info("Starting controller loop for fan '%s' %s", fan.GetId(), alwaysSetPwmStr)

	if fan.GetMinPwm() > fan.GetStartPwm() {
		ui.Warning("Suspicious pwm config of fan '%s': MinPwm (%d) > StartPwm (%d)", fan.GetId(), fan.GetMinPwm(), fan.GetStartPwm())
	}

	// TODO: check if fan.Supports(fans.FeatureControlMode) - or is it ok if it doesn't and our
	//       default assumption is that it will always be in manual mode then?
	//       (trySetManualPwm() just returns nil in that case)
	//       Alternatively, should fans that don't support switching the mode but run in manual mode
	//       by default (cmd or file fans?) just always return ControlModePWM in fan.GetControlMode()
	//       so we can check that?
	err = trySetManualPwm(fan)
	if err != nil {
		cm, e := fan.GetControlMode()
		// if the control mode is PWM even though trySetManualPwm() failed, ignore that error
		if e != nil || cm != fans.ControlModePWM {
			// ... otherwise cancel here, FanController can't do anything if manual fan control doesn't work
			return err
		}
	}

	var g run.Group

	if fan.Supports(fans.FeatureRpmSensor) {
		// === rpm monitoring
		pollingRate := configuration.CurrentConfig.RpmPollingRate

		g.Add(func() error {
			tick := time.NewTicker(pollingRate)
			for {
				select {
				case <-ctx.Done():
					ui.Info("Stopping RPM monitor of fan controller for fan %s...", fan.GetId())
					return nil
				case <-tick.C:
					f.measureRpm(fan)
				}
			}
		}, func(err error) {
			if err != nil {
				ui.Warning("Error monitoring fan rpm: %v", err)
			}
		})
	}

	{
		g.Add(func() error {
			time.Sleep(1 * time.Second)
			tick := time.NewTicker(f.updateRate)
			for {
				select {
				case <-ctx.Done():
					ui.Info("Stopping fan controller for fan %s...", fan.GetId())
					f.restoreControlMode()
					return nil
				case <-tick.C:
					err = f.UpdateFanSpeed()
					if err != nil {
						ui.ErrorAndNotify("Fan Control Error", "Fan %s: %v", fan.GetId(), err)
						f.restoreControlMode()
						return nil
					}
				}
			}
		}, func(err error) {
			if err != nil {
				ui.Fatal("Error in fan controller fan %s: %v", fan.GetId(), err)
			}
		})
	}

	err = g.Run()
	return err
}

func (f *DefaultFanController) runInitializationIfNeeded() (map[int]float64, error) {
	fan := f.fan
	// check if we have data for this fan in persistence,
	// if not we need to run the initialization sequence
	ui.Info("Loading fan curve data for fan '%s'...", fan.GetId())
	fanRpmData, err := f.persistence.LoadFanRpmData(fan)
	if err != nil {
		config := fan.GetConfig()
		if config.HwMon != nil || config.Nvidia != nil {
			ui.Warning("Fan '%s' has not yet been analyzed, starting initialization sequence...", fan.GetId())
			err = f.RunInitializationSequence()
			if err != nil {
				f.restoreControlMode()
				return nil, err
			}
			fanRpmData, err = f.persistence.LoadFanRpmData(fan)
			if err != nil {
				f.restoreControlMode()
				return nil, err
			}
		} else { // file/cmd fan
			if fan.GetFanRpmCurveData() != nil {
				err = f.persistence.SaveFanRpmData(fan)
			}
		}
	}
	return fanRpmData, err
}

// UpdateFanSpeed updates the fan speed by:
// - calculating the target PWM value using the control loop and fan curve
// - applying clamping
// - mapping the resulting target value to the [minPwm, maxPwm] range of the fan
// - applying sanity checks to ensure the fan never stops (if specified)
//
// returns ErrFanStalledAtMaxPwm if no rpm is detected even at fan.maxPwm
func (f *DefaultFanController) UpdateFanSpeed() error {
	fan := f.fan

	f.ensureNoThirdPartyIsMessingWithUs()

	// calculate the direct optimal target speed
	target, err := f.calculateTargetSpeed()
	if err != nil {
		return err
	}

	// ensure target value is within bounds of possible values
	if target > fans.MaxPwmValue {
		ui.Warning("Tried to set out-of-bounds PWM value %.2f on fan %s", target, fan.GetId())
		target = fans.MaxPwmValue
	} else if target < fans.MinPwmValue {
		ui.Warning("Tried to set out-of-bounds PWM value %.2f on fan %s", target, fan.GetId())
		target = fans.MinPwmValue
	}

	// map the target value to the possible range of this fan
	maxPwm := fan.GetMaxPwm()
	minPwm := fan.GetMinPwm()
	shouldNeverStop := fan.ShouldNeverStop()

	// TODO: in theory even pwmTarget could be a float, because f.setPwm() looks for the closest value
	//       in the pwm map and uses that
	var pwmTarget int

	if fan.GetConfig().UseUnscaledCurveValues {
		pwmTarget = int(math.Round(target))
		if pwmTarget > 0 && pwmTarget < minPwm {
			// the fan wouldn't spin with this PWM value anyway, so set 0 instead
			// (might be better for the hardware and preserve energy)
			pwmTarget = 0
		}
	} else {
		if target < 1.0 && !shouldNeverStop {
			// target value 0 (or actually < 1) is mapped to PWM 0, if fan is allowed to stop
			pwmTarget = 0
		} else {
			// target values [1..255] are mapped to [minPwm..maxPwm]
			// adjust the target value determined by the control algorithm to the operational needs
			// of the fan, which includes its supported pwm range (which might be different from [0..255])

			// target values [1..255] => [0..254]
			if target >= 1.0 {
				target -= 1.0
			} else {
				// values < 1 become 0 (which becomes pwmTarget = minPwm), just like 1.
				// Only happens if NeverStop (where 0 should map to minPwm instead of 0)
				// is set, but shouldn't really matter and unifies the behavior
				// for NeverStop enabled/disabled (for >= 1)
				target = 0
			}
			// scale [0..254] to [minPwm..maxPwm]
			pwmTarget = minPwm + int(math.Round((target/(fans.MaxPwmValue-1))*float64(maxPwm-minPwm)))
		}
	}

	// if this fan should never stop, make sure its target is always at least minPwm+f.minPwmOffset
	// (f.minPwmOffset is usually 0, but if the fan doesn't start at MinPwm it gets increased)
	if shouldNeverStop && pwmTarget < minPwm+f.minPwmOffset {
		pwmTarget = minPwm + f.minPwmOffset
	}

	if fan.Supports(fans.FeatureRpmSensor) {
		// make sure fans never stop by validating the current RPM
		// and adjusting the target PWM value upwards if necessary
		if f.lastTarget != nil {
			lastTarget := *f.lastTarget
			// TODO: check this logic
			lastSetPwm, err := f.getLastTarget()
			if err != nil {
				ui.Warning("Error reading last set PWM value of fan %s: %v", fan.GetId(), err)
			}
			lastSetTargetEqualsNewTarget := lastTarget == pwmTarget
			if shouldNeverStop && lastSetTargetEqualsNewTarget {
				avgRpm := fan.GetRpmAvg()
				if avgRpm <= 0 {
					if pwmTarget >= maxPwm {
						ui.Error("CRITICAL: Fan %s avg. RPM is %d, even at PWM value %d", fan.GetId(), int(avgRpm), lastSetPwm)
						return ErrFanStalledAtMaxPwm
					}
					oldMinPwm := minPwm
					ui.Warning("Increasing minPWM of %s from %d to %d, which is supposed to never stop, but RPM is %d at PWM %d",
						fan.GetId(), oldMinPwm, oldMinPwm+1, int(avgRpm), lastSetPwm)
					f.increaseMinPwmOffset()
					pwmTarget++

					// set the moving avg to a value > 0 to prevent
					// this increase from happening too fast
					fan.SetRpmAvg(1)
				}
			}
		}
	}

	err = f.setPwm(pwmTarget)
	if err != nil {
		// TODO: maybe we should add some kind of critical failure mode here
		//  in case these errors don't resolve after a while
		ui.Error("Error setting %s: %v", fan.GetId(), err)
	}

	return nil
}

func (f *DefaultFanController) RunInitializationSequence() (err error) {
	fan := f.fan

	err = f.computeFanSpecificMappings()
	if err != nil {
		ui.Error("Error computing fan specific mappings for %s: %v", fan.GetId(), err)
		return err
	}

	if !fan.Supports(fans.FeatureRpmSensor) {
		ui.Info("Fan '%s' doesn't support RPM sensor, skipping fan curve measurement", fan.GetId())
		return nil
	}
	ui.Info("Measuring RPM curve...")

	err = trySetManualPwm(fan)
	if err != nil {
		ui.Warning("Could not enable manual fan mode on %s, trying to continue anyway...", fan.GetId())
	}

	curveData := map[int]float64{}

	initialMeasurement := true
	for _, pwm := range f.targetValuesWithDistinctPWMValue {
		// set a pwm
		actualPwm := f.applyPwmMapToTarget(pwm)
		err = f.setPwm(actualPwm)
		if err != nil {
			ui.Error("Unable to run initialization sequence on %s: %v", fan.GetId(), err)
			return err
		}
		expectedPwm := f.getReportedPwmAfterApplyingPwm(actualPwm)
		time.Sleep(configuration.CurrentConfig.FanController.PwmSetDelay)
		actualPwm, err := f.getPwm()
		if err != nil {
			ui.Error("Fan %s: Unable to measure current PWM", fan.GetId())
			return err
		}
		if actualPwm != expectedPwm {
			ui.Debug("Fan %s: Actual PWM value differs from requested one, skipping: requested: %d, expected: %d, actual: %d", fan.GetId(), pwm, expectedPwm, actualPwm)
			continue
		}

		if initialMeasurement {
			initialMeasurement = false
			f.waitForFanToSettle(fan)
		} else {
			// wait a bit to allow the fan speed to settle
			time.Sleep(time.Duration(configuration.CurrentConfig.FanResponseDelay) * time.Second)
		}

		rpm, err := fan.GetRpm()
		if err != nil {
			ui.Error("Unable to measure RPM of fan %s", fan.GetId())
			return err
		}
		ui.Debug("Measuring RPM of %s at PWM %d: %d", fan.GetId(), pwm, rpm)

		// update rpm curve
		fan.SetRpmAvg(float64(rpm))
		curveData[pwm] = float64(rpm)

		ui.Debug("Measured RPM of %d at PWM %d for fan %s", int(fan.GetRpmAvg()), pwm, fan.GetId())
	}

	err = fan.AttachFanRpmCurveData(&curveData)
	if err != nil {
		ui.Error("Failed to attach fan curve data to fan %s: %v", fan.GetId(), err)
		return err
	}

	// save to database to restore it on restarts
	err = f.persistence.SaveFanRpmData(fan)
	if err != nil {
		ui.Error("Failed to save fan PWM data for %s: %v", fan.GetId(), err)
	}
	return err
}

// read the current value of a fan RPM sensor and append it to the moving window
func (f *DefaultFanController) measureRpm(fan fans.Fan) {
	pwm, err := f.getPwm()
	if err != nil {
		ui.Warning("Error reading PWM value of fan %s: %v", fan.GetId(), err)
	}
	rpm, err := fan.GetRpm()
	if err != nil {
		ui.Warning("Error reading RPM value of fan %s: %v", fan.GetId(), err)
	}

	updatedRpmAvg := util.UpdateSimpleMovingAvg(fan.GetRpmAvg(), configuration.CurrentConfig.RpmRollingWindowSize, float64(rpm))
	fan.SetRpmAvg(updatedRpmAvg)

	fan.UpdateFanRpmCurveValue(pwm, float64(rpm))
}

// getPwm returns the current raw PWM value of the fan.
// If the fan does not support PWM sensor reading,
// it returns the last set PWM value.
// If no last set PWM value is available, it returns the min PWM value.
func (f *DefaultFanController) getPwm() (int, error) {
	if f.fan.Supports(fans.FeaturePwmSensor) {
		return f.fan.GetPwm()
	} else if f.lastTarget != nil {
		return f.applyPwmMapToTarget(*f.lastTarget), nil
	} else {
		return f.fan.GetMinPwm(), nil
	}
}

func trySetManualPwm(fan fans.Fan) error {
	if !fan.Supports(fans.FeatureControlMode) {
		return nil
	}

	err := fan.SetControlMode(fans.ControlModePWM)
	if err != nil {
		ui.Error("Unable to set Fan Mode of '%s' to \"%d\": %v", fan.GetId(), fans.ControlModePWM, err)
		err = fan.SetControlMode(fans.ControlModeDisabled)
		if err != nil {
			ui.Error("Unable to set Fan Mode of '%s' to \"%d\": %v", fan.GetId(), fans.ControlModeDisabled, err)
		}
	}
	return err
}

func (f *DefaultFanController) restoreControlMode() {
	ui.Info("Trying to restore fan settings for %s...", f.fan.GetId())

	err := f.fan.SetPwm(f.originalPwmValue)
	if err != nil {
		ui.Warning("Error restoring original PWM value for fan %s: %v", f.fan.GetId(), err)
	}

	// try to reset the pwm_enable value
	if f.fan.Supports(fans.FeatureControlMode) && f.originalControlMode != fans.ControlModePWM {
		err := f.fan.SetControlMode(f.originalControlMode)
		if err == nil {
			return
		}
	}
	// if this fails, try to set it to max speed instead
	err = f.fan.SetPwm(fans.MaxPwmValue)
	if err != nil {
		ui.Warning("Unable to restore fan %s, make sure it is running!", f.fan.GetId())
	}
}

// Calculates the next speed for the fan of this controller by
// - evaluating the associated curve
// - cycling the control loop
func (f *DefaultFanController) calculateTargetSpeed() (float64, error) {
	fan := f.fan
	target, err := f.curve.Evaluate()
	if err != nil {
		ui.Fatal("Unable to calculate optimal speed value for %s: %v", fan.GetId(), err)
	}

	// the new target speed to set, which approaches the actual target based on the control loop
	newTarget := f.controlLoop.Cycle(target)

	return newTarget, nil
}

func (f *DefaultFanController) getLastTarget() (int, error) {
	lastSetPwm := 0
	if f.lastTarget != nil {
		lastTarget := *(f.lastTarget)
		lastSetPwm = lastTarget
	} else {
		if f.fan.Supports(fans.FeaturePwmSensor) {
			pwm, err := f.getPwm()
			if err != nil {
				return -1, err
			}
			lastSetPwm = pwm
		} else {
			// assume the fan was set to its MinPwm value after initialization
			lastSetPwm = f.fan.GetMinPwm()
		}
	}
	return lastSetPwm, nil
}

// ensureNoThirdPartyIsMessingWithUs checks if the PWM value of the fan does not match the last
// value PWM set by fan2go. If that is the case, it is assumed that a third party has changed the PWM value
// of the fan, which can lead to unexpected behavior.
func (f *DefaultFanController) ensureNoThirdPartyIsMessingWithUs() {
	fanConfig := f.fan.GetConfig()
	sanityCheckConfig := fanConfig.SanityCheck
	if sanityCheckConfig != nil {
		if sanityCheckConfig.PwmValueChangedByThirdParty != nil {
			pwmValuwChngedByThirdPartyCheckConfig := sanityCheckConfig.PwmValueChangedByThirdParty
			if pwmValuwChngedByThirdPartyCheckConfig.Enabled != nil {
				if !*pwmValuwChngedByThirdPartyCheckConfig.Enabled {
					// sanity checks are disabled, so we don't check for third party changes
					return
				}
			}
		}

	}

	if !f.fan.Supports(fans.FeaturePwmSensor) {
		// we cannot read the PWM value, so we also cannot check if third party changed the PWM value
		ui.Debug("Fan %s does not support PWM sensor reading, cannot check for third party changes to the PWM value", f.fan.GetId())
		return
	}

	if f.lastTarget != nil {
		lastSetPwm, err := f.getLastTarget()
		if err != nil {
			ui.Warning("Error reading last set PWM value of fan %s: %v", f.fan.GetId(), err)
		}
		pwmMappedValue := f.applyPwmMapToTarget(lastSetPwm)
		expectedReportedPwm := f.getReportedPwmAfterApplyingPwm(pwmMappedValue)
		if currentPwm, err := f.fan.GetPwm(); err == nil {
			if currentPwm != expectedReportedPwm {
				f.stats.UnexpectedPwmValueCount += 1
				ui.Warning("PWM of %s was changed by third party! Last set PWM value was '%d', expected reported pwm '%d' but was '%d'",
					f.fan.GetId(), pwmMappedValue, expectedReportedPwm, currentPwm)
			}
		}
	}
}

// setPwm applies the given target speed in [0..255] to the fan which is controlled
// in this FanController. Since the fan might not support the range of [0..255]
// the target value is mapped to a pwm value in the supported range using the pwmMap.
func (f *DefaultFanController) setPwm(target int) (err error) {
	pwmMappedValue := f.applyPwmMapToTarget(target)
	expectedReportedPwmValue := f.getReportedPwmAfterApplyingPwm(pwmMappedValue)
	// setting pwmMappedValue will yield expectedReportedPwmValue when reading back the pwm value

	if f.fan.GetConfig().AlwaysSetPwmMode {
		_ = trySetManualPwm(f.fan)
	}

	ui.Debug("Setting target PWM of %s to %d, applying PWM Map yields %d, expected reported pwm is %d", f.fan.GetId(), target, pwmMappedValue, expectedReportedPwmValue)
	f.lastTarget = &target
	// if we can read the PWM value, we can check if the fan is already at the target value
	// and avoid unnecessary setPwm calls
	if f.fan.Supports(fans.FeaturePwmSensor) {
		current, err := f.getPwm()
		if err == nil && expectedReportedPwmValue == current {
			// nothing to do
			return nil
		}
	}
	return f.fan.SetPwm(pwmMappedValue)
}

func (f *DefaultFanController) waitForFanToSettle(fan fans.Fan) {
	// TODO: this "waiting" logic could also be applied to the other measurements
	diffThreshold := configuration.CurrentConfig.MaxRpmDiffForSettledFan

	measuredRpmDiffWindow := util.CreateRollingWindow(10)
	util.FillWindow(measuredRpmDiffWindow, 10, 2*diffThreshold)
	measuredRpmDiffMax := 2 * diffThreshold
	oldRpm := 0
	for !(measuredRpmDiffMax < diffThreshold) {
		ui.Debug("Waiting for fan %s to settle (current RPM max diff: %f)...", fan.GetId(), measuredRpmDiffMax)
		time.Sleep(1 * time.Second)

		currentRpm, err := fan.GetRpm()
		if err != nil {
			ui.Warning("Cannot read RPM value of fan %s: %v", fan.GetId(), err)
			continue
		}
		measuredRpmDiffWindow.Append(math.Abs(float64(currentRpm - oldRpm)))
		oldRpm = currentRpm
		measuredRpmDiffMax = math.Ceil(util.GetWindowMax(measuredRpmDiffWindow))
	}
	ui.Debug("Fan %s has settled (current RPM max diff: %f)", fan.GetId(), measuredRpmDiffMax)
}

// computeSetPwmToGetPwmMap computes a mapping between "set pwm value" -> "actual pwm value"
func (f *DefaultFanController) computeSetPwmToGetPwmMap() (err error) {

	// load the setPwmToGetPwmMap from persistence, if it exists
	f.setPwmToGetPwmMap, err = f.persistence.LoadFanSetPwmToGetPwmMap(f.fan.GetId())
	if err == nil && f.setPwmToGetPwmMap != nil {
		ui.Info("FanController: Using saved value for setPwmToGetPwmMap of Fan '%s'", f.fan.GetId())
		return nil
	}

	err = f.computeSetPwmToGetPwmMapAutomatically()
	if err != nil {
		ui.Error("Error computing setPwmToGetPwmMap for fan %s: %v", f.fan.GetId(), err)
		return err
	}

	ui.Debug("Saving setPwmToGetPwmMap to fan...")
	return f.persistence.SaveFanSetPwmToGetPwmMap(f.fan.GetId(), f.setPwmToGetPwmMap)
}

// computePwmMap computes a mapping between "internal target pwm value" -> "actual set pwm value"
// Ensure that computeSetPwmToGetPwmMap has been called before this method,
// otherwise the pwmMap will always fall back to a linear 1:1 interpolation in the range of [0..255].
func (f *DefaultFanController) computePwmMap() (err error) {
	if !configuration.CurrentConfig.RunFanInitializationInParallel {
		InitializationSequenceMutex.Lock()
		defer InitializationSequenceMutex.Unlock()
	}

	configOverride := f.fan.GetConfig().PwmMap

	if configOverride != nil && len(*configOverride) > 0 {
		ui.Info("Using pwm map override from config...")

		keys := maps.Keys(*configOverride)
		slices.Sort(keys)

		lastKeyIdx := 0
		numKeys := len(keys)

		for i := 0; i < 256; i++ {
			// find the index within keys so keys[index] is closest to i
			diff := util.Abs(keys[lastKeyIdx] - i)
			for ki := lastKeyIdx + 1; ki < numKeys; ki++ {
				d := util.Abs(keys[ki] - i)
				if d <= diff { // <= to round up when at midpoint
					diff = d
					lastKeyIdx = ki
				} else {
					// if d was > diff, diff and lastKeyIdx are the optimum (as keys[] are sorted)
					break
				}
			}
			configOverrideKey := keys[lastKeyIdx]
			// map i to the value of the user's PWM map with the key closest to i
			f.pwmMapping[i] = (*configOverride)[configOverrideKey]
		}

		return nil
	}

	savedPwmMap, err := f.persistence.LoadFanPwmMap(f.fan.GetId())
	if err == nil && savedPwmMap != nil {
		ui.Info("FanController: Using saved value for pwm map of Fan '%s'", f.fan.GetId())
		for i := 0; i < 256; i++ {
			f.pwmMapping[i] = savedPwmMap[i]
		}
		return nil
	}

	//if f.pwmMap == nil - TODO: any check needed? where would it come from anyway?
	// and if it's calculated successfully, it gets saved anyway so next time this function will return after loading it
	{
		ui.Info("Computing pwm map...")
		err = f.computePwmMapAutomatically()
		if err != nil {
			ui.Error("Error computing pwm map for fan %s: %v", f.fan.GetId(), err)
			return err
		}
	}

	ui.Debug("Saving pwm map to fan...")
	return f.persistence.SaveFanPwmMap(f.fan.GetId(), f.pwmMapping[:])
}

func (f *DefaultFanController) computePwmMapAutomatically() (err error) {
	fan := f.fan

	// since the setPwmToGetPwmMap is an indicator of what values are supported by the fan driver,
	// we can use it to determine the pwmMap as well.
	if f.setPwmToGetPwmMap == nil {
		// if we don't have a setPwmToGetPwmMap, there was either an error computing it,
		// or it is impossible to compute it due to the fan not supporting PWM sensor reading.
		// In this case, we have to assume a default pwmMap.
		ui.Warning("Fan '%s' does not have a setPwmToGetPwmMap, using default pwmMap", fan.GetId())
		//f.pwmMap, err = util.InterpolateLinearlyInt(&map[int]int{0: 0, 255: 255}, 0, 255)

		for i := 0; i < 256; i++ {
			f.pwmMapping[i] = i
		}
	} else {
		// if we have a setPwmToGetPwmMap, we can use its keyset to compute the pwmMap.
		// Since this map will be used to map the internal target pwm value
		// to the fan range, we need to interpolate it in a way that the internal range of [0..255]
		// is mapped to the full supported range of the fan in [minPwm, maxPwm].
		// Since there might be gaps in the setPwmToGetPwmMap, the pwmMap will be populated
		// so that the supported values are represented as steps, with the steps being aligned to be
		// in the middle of two adjacent values in the supported range.
		ui.Debug("Using setPwmToGetPwmMap to compute pwmMap for fan %s", fan.GetId())
		keySet := maps.Keys(f.setPwmToGetPwmMap)
		sort.Ints(keySet)
		identityMappingOfKeyset := make(map[int]int, len(keySet))
		for i := 0; i < len(keySet); i++ {
			key := keySet[i]
			identityMappingOfKeyset[key] = key
		}
		// TODO: shouldn't this take the values into account or something?
		pwmMap := util.ExpandMapToFullRange(identityMappingOfKeyset, fans.MinPwmValue, fans.MaxPwmValue)

		// TODO: can all this be done more easily?
		for i := 0; i < 256; i++ {
			f.pwmMapping[i] = pwmMap[i]
		}
	}
	return err
}

func (f *DefaultFanController) updateDistinctPwmValues() {
	var targetValues = util.ExtractIndicesWithDistinctValues(f.pwmMapping[:])
	sort.Ints(targetValues)
	f.targetValuesWithDistinctPWMValue = targetValues

	ui.Debug("Target values with distinct PWM value of fan %s: %v", f.fan.GetId(), targetValues)
}

func (f *DefaultFanController) increaseMinPwmOffset() {
	f.minPwmOffset += 1
	f.stats.MinPwmOffset = f.minPwmOffset
	f.stats.IncreasedMinPwmCount += 1
}

// applyPwmMapToTarget maps a given target PWM value to the actual to-be-applied PWM value.
// This is necessary because some fans do not support the full range of [0..255] PWM values,
// but rather a subset of it, e.g. [0..100] or [0..255] with some values missing.
// Another reason for this is that some fans require a different PWM value to be set
// to achieve a certain target speed.
// See the pwmMap field for more details.
func (f *DefaultFanController) applyPwmMapToTarget(target int) int {
	if target < 0 || target > 255 {
		return 0 // TODO: panic or something? or just clamp target to [0..255]?
	}
	return f.pwmMapping[target]
}

// getReportedPwmAfterApplyingPwm returns the expected reported PWM value after applying the given pwmMappedValue.
// This is necessary because some fans do not report the exact value that was set,
// but rather a different value, e.g. due to hardware limitations or driver quirks.
// This method uses the setPwmToGetPwmMap to determine the expected reported PWM value.
// If the setPwmToGetPwmMap is not available, it assumes a 1:1 relation between set and reported PWM values.
// If the pwmMappedValue is not present in the setPwmToGetPwmMap, it will find the closest key
// and return the corresponding value from the map.
// Make sure to pass in a value that has been mapped to the fan's supported range using the pwmMap.
func (f *DefaultFanController) getReportedPwmAfterApplyingPwm(pwmMappedValue int) int {
	if f.setPwmToGetPwmMap == nil {
		ui.Warning("Fan '%s' does not have a setPwmToGetPwmMap, assuming 1:1 relation.", f.fan.GetId())
		return pwmMappedValue
	}
	if value, ok := f.setPwmToGetPwmMap[pwmMappedValue]; !ok {
		ui.Warning("Fan '%s' does not have a setPwmToGetPwmMap entry for %d, assuming 1:1 relation.", f.fan.GetId(), pwmMappedValue)
		// FIXME: the next line is broken. FindClosest() does a binary search (=> requires sorted slice),
		//        but maps.Keys() returns the keys "in an indeterminate order"
		closestKey := util.FindClosest(pwmMappedValue, maps.Keys(f.setPwmToGetPwmMap))
		return f.setPwmToGetPwmMap[closestKey]
	} else {
		return value
	}
}

func (f *DefaultFanController) computeSetPwmToGetPwmMapAutomatically() error {
	if !f.fan.Supports(fans.FeaturePwmSensor) || f.skipAutoPwmMapping {
		if f.skipAutoPwmMapping {
			ui.Info("Automatic calculation of setPwmToGetPwmMap disabled for Fan '%s'. Assuming 1:1 relation.", f.fan.GetId())
		} else {
			ui.Warning("Fan '%s' does not support PWM sensor, cannot compute setPwmToGetPwmMap. Assuming 1:1 relation.", f.fan.GetId())
		}
		f.setPwmToGetPwmMap, _ = util.InterpolateLinearlyInt(&map[int]int{0: 0, 255: 255}, 0, 255)
		return nil
	}

	_ = trySetManualPwm(f.fan)

	f.setPwmToGetPwmMap = map[int]int{}
	for i := fans.MinPwmValue; i <= fans.MaxPwmValue; i++ {
		err := f.fan.SetPwm(i)
		if err != nil {
			ui.Warning("Error setting PWM value %d on fan %s: %v", i, f.fan.GetId(), err)
			continue
		}
		time.Sleep(configuration.CurrentConfig.FanController.PwmSetDelay)
		pwm, err := f.fan.GetPwm()
		if err != nil {
			ui.Warning("Error reading PWM value of fan %s: %v", f.fan.GetId(), err)
			continue
		}
		f.setPwmToGetPwmMap[i] = pwm
	}

	return nil
}

func (f *DefaultFanController) computeFanSpecificMappings() (err error) {
	err = f.computeSetPwmToGetPwmMap()
	if err != nil {
		ui.Fatal("Error computing setPwm(x) -> getPwm() map: %v", err)
		return err
	}

	err = f.computePwmMap()
	if err != nil {
		ui.Warning("Error computing PWM map: %v", err)
		return err
	}

	f.updateDistinctPwmValues()

	return nil
}
