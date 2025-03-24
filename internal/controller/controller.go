package controller

import (
	"context"
	"errors"
	"fmt"
	"github.com/markusressel/fan2go/internal/control_loop"
	"math"
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

// Amount of time to wait between a set-pwm and get-pwm. Used during fan initial calibration.
const pwmSetGetDelay = 5 * time.Millisecond

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
	// the original pwm_enabled flag state of the fan before starting the controller
	originalPwmEnabled fans.ControlMode
	// the original pwm value of the fan before starting the controller
	// Note: this is the raw value read from the fan, no pwmMap is applied to it
	originalPwmValue int
	// the last pwm value that was set to the fan, **before** applying the pwmMap to it
	lastSetPwm *int
	// a list of all pre-pwmMap pwm values where setPwm(x) != setPwm(y) for the controlled fan
	pwmValuesWithDistinctTarget []int
	// a map of x -> getPwm() where x is setPwm(x) for the controlled fan
	pwmMap map[int]int

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
) FanController {
	curve, ok := curves.GetSpeedCurve(fan.GetCurveId())
	if !ok {
		ui.Fatal("Failed to create fan controller for fan '%s': Curve with ID '%s' not found", fan.GetId(), fan.GetCurveId())
	}
	return &DefaultFanController{
		persistence:                 persistence,
		fan:                         fan,
		curve:                       curve,
		updateRate:                  updateRate,
		pwmValuesWithDistinctTarget: []int{},
		pwmMap:                      nil,
		controlLoop:                 controlLoop,
		minPwmOffset:                0,
	}
}

func (f *DefaultFanController) GetFanId() string {
	return f.fan.GetId()
}

func (f *DefaultFanController) GetStatistics() FanControllerStatistics {
	return f.stats
}

func (f *DefaultFanController) Run(ctx context.Context) error {
	err := f.persistence.Init()
	if err != nil {
		return err
	}

	fan := f.fan

	if fan.ShouldNeverStop() && !fan.Supports(fans.FeatureRpmSensor) {
		ui.Warning("WARN: cannot guarantee neverStop option on fan %s, since it has no RPM input.", fan.GetId())
	}

	// store original pwm value
	pwm, err := f.getPwm()
	if err != nil {
		ui.Warning("Cannot read pwm value of %s", fan.GetId())
	}
	f.originalPwmValue = pwm

	// store original pwm_enable value
	if f.fan.Supports(fans.FeatureControlMode) {
		pwmEnabled, err := fan.GetPwmEnabled()
		if err != nil {
			ui.Warning("Cannot read pwm_enable value of %s", fan.GetId())
		}
		f.originalPwmEnabled = fans.ControlMode(pwmEnabled)
	}

	ui.Info("Gathering sensor data for %s...", fan.GetId())
	// wait a bit to gather monitoring data
	time.Sleep(2*time.Second + configuration.CurrentConfig.TempSensorPollingRate*2)

	// check if we have data for this fan in persistence,
	// if not we need to run the initialization sequence
	ui.Info("Loading fan curve data for fan '%s'...", fan.GetId())
	fanPwmData, err := f.persistence.LoadFanPwmData(fan)
	if err != nil {
		_, ok := fan.(*fans.HwMonFan)
		if ok {
			ui.Warning("Fan '%s' has not yet been analyzed, starting initialization sequence...", fan.GetId())
			err = f.RunInitializationSequence()
			if err != nil {
				return err
			}
		} else {
			err = f.persistence.SaveFanPwmData(fan)
			if err != nil {
				return err
			}
		}
	}

	fanPwmData, err = f.persistence.LoadFanPwmData(fan)
	if err != nil {
		return err
	}

	err = fan.AttachFanRpmCurveData(&fanPwmData)
	if err != nil {
		return err
	}

	err1 := f.computePwmMap()
	if err1 != nil {
		ui.Warning("Error computing PWM map: %v", err1)
	}

	f.updateDistinctPwmValues()

	ui.Debug("PWM map of fan '%s': %v", fan.GetId(), f.pwmMap)
	ui.Info("PWM settings of fan '%s': Min %d, Start %d, Max %d", fan.GetId(), fan.GetMinPwm(), fan.GetStartPwm(), fan.GetMaxPwm())
	ui.Info("Starting controller loop for fan '%s'", fan.GetId())

	if fan.GetMinPwm() > fan.GetStartPwm() {
		ui.Warning("Suspicious pwm config of fan '%s': MinPwm (%d) > StartPwm (%d)", fan.GetId(), fan.GetMinPwm(), fan.GetStartPwm())
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
					f.restorePwmEnabled()
					return nil
				case <-tick.C:
					err = f.UpdateFanSpeed()
					if err != nil {
						ui.ErrorAndNotify("Fan Control Error", "Fan %s: %v", fan.GetId(), err)
						f.restorePwmEnabled()
						return nil
					}
				}
			}
		}, func(err error) {
			if err != nil {
				ui.Fatal("Error monitoring fan rpm: %v", err)
			}
		})
	}

	err = g.Run()
	return err
}

func (f *DefaultFanController) UpdateFanSpeed() error {
	fan := f.fan

	// calculate the direct optimal target speed
	target, err := f.calculateTargetPwm()
	if err != nil {
		return err
	}

	_ = trySetManualPwm(f.fan)
	err = f.setPwm(target)
	if err != nil {
		// TODO: maybe we should add some kind of critical failure mode here
		//  in case these errors don't resolve after a while
		ui.Error("Error setting %s: %v", fan.GetId(), err)
	}

	return nil
}

func (f *DefaultFanController) RunInitializationSequence() (err error) {
	fan := f.fan

	err1 := f.computePwmMap()
	if err1 != nil {
		ui.Warning("Error computing PWM map: %v", err1)
	}

	err = f.persistence.SaveFanPwmMap(fan.GetId(), f.pwmMap)
	if err != nil {
		ui.Error("Unable to persist pwmMap for fan %s", fan.GetId())
	}
	f.updateDistinctPwmValues()

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
	for _, pwm := range f.pwmValuesWithDistinctTarget {
		// set a pwm
		err = f.setPwm(pwm)
		if err != nil {
			ui.Error("Unable to run initialization sequence on %s: %v", fan.GetId(), err)
			return err
		}
		expectedPwm := f.applyPwmMapping(pwm)
		time.Sleep(pwmSetGetDelay)
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
	err = f.persistence.SaveFanPwmData(fan)
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

func (f *DefaultFanController) getPwm() (int, error) {
	if f.fan.Supports(fans.FeaturePwmSensor) {
		return f.fan.GetPwm()
	} else if f.lastSetPwm != nil {
		return *f.lastSetPwm, nil
	} else {
		return f.fan.GetMinPwm(), nil
	}
}

func trySetManualPwm(fan fans.Fan) error {
	if !fan.Supports(fans.FeatureControlMode) {
		return nil
	}

	err := fan.SetPwmEnabled(fans.ControlModePWM)
	if err != nil {
		ui.Error("Unable to set Fan Mode of '%s' to \"%d\": %v", fan.GetId(), fans.ControlModePWM, err)
		err = fan.SetPwmEnabled(fans.ControlModeDisabled)
		if err != nil {
			ui.Error("Unable to set Fan Mode of '%s' to \"%d\": %v", fan.GetId(), fans.ControlModeDisabled, err)
		}
	}
	return err
}

func (f *DefaultFanController) restorePwmEnabled() {
	ui.Info("Trying to restore fan settings for %s...", f.fan.GetId())

	err := f.fan.SetPwm(f.originalPwmValue)
	if err != nil {
		ui.Warning("Error restoring original PWM value for fan %s: %v", f.fan.GetId(), err)
	}

	// try to reset the pwm_enable value
	if f.fan.Supports(fans.FeatureControlMode) && f.originalPwmEnabled != fans.ControlModePWM {
		err := f.fan.SetPwmEnabled(f.originalPwmEnabled)
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

// Calculates the optimal pwm for the fan of this contoller by
// - evaluating the associated curve
// - cycling the control loop
// - applying clamping
// - mapping the resulting target value to the [minPwm, maxPwm] range of the fan
// - applying sanity checks to ensure the fan never stops (if specified)
//
// returns ErrFanStalledAtMaxPwm if no rpm is detected even at fan.maxPwm
func (f *DefaultFanController) calculateTargetPwm() (int, error) {
	lastSetPwm := 0
	if f.lastSetPwm != nil {
		lastSetPwm = *(f.lastSetPwm)
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

	fan := f.fan
	target, err := f.curve.Evaluate()
	if err != nil {
		ui.Fatal("Unable to calculate optimal PWM value for %s: %v", fan.GetId(), err)
	}

	// the target pwm, approaching the actual target smoothly
	target = f.controlLoop.Cycle(target, lastSetPwm)

	// ensure target value is within bounds of possible values
	if target > fans.MaxPwmValue {
		ui.Warning("Tried to set out-of-bounds PWM value %d on fan %s", target, fan.GetId())
		target = fans.MaxPwmValue
	} else if target < fans.MinPwmValue {
		ui.Warning("Tried to set out-of-bounds PWM value %d on fan %s", target, fan.GetId())
		target = fans.MinPwmValue
	}

	// map the target value to the possible range of this fan
	maxPwm := fan.GetMaxPwm()
	minPwm := fan.GetMinPwm() + f.minPwmOffset

	// determine the target value based on the pwm range as well as RPM curve of the fan
	// TODO: this assumes a linear curve, but it might be something else
	// TODO: remove
	target = minPwm + int((float64(target)/fans.MaxPwmValue)*(float64(maxPwm)-float64(minPwm)))

	f.ensureNoThirdPartyIsMessingWithUs()

	if fan.Supports(fans.FeatureRpmSensor) {
		// make sure fans never stop by validating the current RPM
		// and adjusting the target PWM value upwards if necessary
		shouldNeverStop := fan.ShouldNeverStop()
		if shouldNeverStop && (f.lastSetPwm != nil || f.lastSetPwm == &target) {
			avgRpm := fan.GetRpmAvg()
			if avgRpm <= 0 {
				if target >= maxPwm {
					ui.Error("CRITICAL: Fan %s avg. RPM is %d, even at PWM value %d", fan.GetId(), int(avgRpm), target)
					return -1, ErrFanStalledAtMaxPwm
				}
				oldMinPwm := minPwm
				ui.Warning("Increasing minPWM of %s from %d to %d, which is supposed to never stop, but RPM is %d at PWM %d",
					fan.GetId(), oldMinPwm, oldMinPwm+1, int(avgRpm), lastSetPwm)
				f.increaseMinPwmOffset()
				fan.SetMinPwm(f.minPwmOffset, true)
				target++

				// set the moving avg to a value > 0 to prevent
				// this increase from happening too fast
				fan.SetRpmAvg(1)
			}
		}
	}

	return target, nil
}

// ensureNoThirdPartyIsMessingWithUs checks if the PWM value of the fan does not match the last
// value PWM set by fan2go. If that is the case, it is assumed that a third party has changed the PWM value
// of the fan, which can lead to unexpected behavior.
func (f *DefaultFanController) ensureNoThirdPartyIsMessingWithUs() {
	if !f.fan.Supports(fans.FeaturePwmSensor) {
		// TODO: check if "disablePwmSanityChecks" is set, show warning if not
		// if we cannot read the PWM value, so we also cannot check if third party changed the PWM value
		return
	}
	if f.lastSetPwm != nil && f.pwmMap != nil {
		lastSetPwm := *(f.lastSetPwm)
		expected := f.applyPwmMapping(f.findClosestDistinctTarget(lastSetPwm))
		if currentPwm, err := f.fan.GetPwm(); err == nil {
			if currentPwm != expected {
				f.stats.UnexpectedPwmValueCount += 1
				ui.Warning("PWM of %s was changed by third party! Last set PWM value was: %d but is now: %d",
					f.fan.GetId(), expected, currentPwm)
			}
		}
	}
}

// set the pwm speed of a fan to the specified value (0..255)
func (f *DefaultFanController) setPwm(target int) (err error) {
	closestTarget := f.findClosestDistinctTarget(target)
	closestExpected := f.applyPwmMapping(closestTarget)

	ui.Debug("Setting PWM of %s to %d, found closest distinct PWM value at %d, applying PWM Map yields %d", f.fan.GetId(), target, closestTarget, closestExpected)
	f.lastSetPwm = &target
	// if we can read the PWM value, we can check if the fan is already at the target value
	// and avoid unnecessary setPwm calls
	if f.fan.Supports(fans.FeaturePwmSensor) {
		current, err := f.getPwm()
		if err == nil && closestExpected == current {
			// nothing to do
			return nil
		}
	}
	return f.fan.SetPwm(closestExpected)
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

// findClosestDistinctTarget traverses the entries of the pwmMap and returns
// the internal pwm value (key) of the entry whose value is closest (and distinct) value
// to the requested [target] value.
//
// Note: The value returned by this method must be used as the key
// to the pwmMap to get the actual target pwm value for the fan of this controller.
func (f *DefaultFanController) findClosestDistinctTarget(target int) int {
	return util.FindClosest(target, f.pwmValuesWithDistinctTarget)
}

// computePwmMap computes a mapping between "requested pwm value" -> "actual set pwm value"
func (f *DefaultFanController) computePwmMap() (err error) {
	if !configuration.CurrentConfig.RunFanInitializationInParallel {
		InitializationSequenceMutex.Lock()
		defer InitializationSequenceMutex.Unlock()
	}

	var configOverride *map[int]int

	switch f := f.fan.(type) {
	case *fans.HwMonFan:
		c := f.Config.PwmMap
		if c != nil {
			configOverride = c
		}
	case *fans.CmdFan:
		c := f.Config.PwmMap
		if c != nil {
			configOverride = c
		}
	case *fans.FileFan:
		c := f.Config.PwmMap
		if c != nil {
			configOverride = c
		}
	default:
		// if type is other than above
		fmt.Println("Type is unknown!")
	}

	if configOverride != nil {
		ui.Info("Using pwm map override from config...")
		f.pwmMap = *configOverride
		return nil
	}

	savedPwmMap, err := f.persistence.LoadFanPwmMap(f.fan.GetId())
	if err == nil && f.pwmMap != nil {
		ui.Info("FanController: Using saved value for pwm map of Fan '%s'", f.fan.GetId())
		f.pwmMap = savedPwmMap
		return nil
	}

	if f.pwmMap == nil {
		ui.Info("Computing pwm map...")
		f.computePwmMapAutomatically()
	}

	ui.Debug("Saving pwm map to fan...")
	return f.persistence.SaveFanPwmMap(f.fan.GetId(), f.pwmMap)
}

func (f *DefaultFanController) computePwmMapAutomatically() {
	fan := f.fan

	if !fan.Supports(fans.FeaturePwmSensor) {
		// we cannot read the PWM value, so we have to assume a default PWM map
		ui.Warning("Fan '%s' does not support PWM sensor, using default PWM map", fan.GetId())
		f.pwmMap = util.InterpolateLinearlyInt(&map[int]int{0: 0, 255: 255}, 0, 255)
		return
	}
	_ = trySetManualPwm(fan)

	// check every pwm value
	pwmMap := map[int]int{}
	for i := fans.MaxPwmValue; i >= fans.MinPwmValue; i-- {
		_ = fan.SetPwm(i)
		time.Sleep(pwmSetGetDelay)
		pwm, err := fan.GetPwm()
		if err != nil {
			ui.Warning("Error reading PWM value of fan %s: %v", fan.GetId(), err)
		}
		pwmMap[i] = pwm
	}
	f.pwmMap = pwmMap

	_ = fan.SetPwm(f.applyPwmMapping(fan.GetStartPwm()))
}

func (f *DefaultFanController) updateDistinctPwmValues() {
	var keys = util.ExtractKeysWithDistinctValues(f.pwmMap)
	sort.Ints(keys)
	f.pwmValuesWithDistinctTarget = keys

	ui.Debug("Distinct PWM value targets of fan %s: %v", f.fan.GetId(), keys)
}

func (f *DefaultFanController) increaseMinPwmOffset() {
	f.minPwmOffset += 1
	f.stats.MinPwmOffset = f.minPwmOffset
	f.stats.IncreasedMinPwmCount += 1
}

func (f *DefaultFanController) applyPwmMapping(target int) int {
	return f.pwmMap[target]
}
