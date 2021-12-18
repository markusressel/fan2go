package controller

import (
	"context"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/curves"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/persistence"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/oklog/run"
	"math"
	"sort"
	"sync"
	"time"
)

var InitializationSequenceMutex sync.Mutex

type FanController interface {
	Run(ctx context.Context) error
	UpdateFanSpeed() error
}

type fanController struct {
	persistence persistence.Persistence
	// the fan to control
	fan fans.Fan
	// the curve used to control the fan
	curve curves.SpeedCurve
	// rate to update the target fan speed
	updateRate time.Duration
	// the original pwm_enabled flag state of the fan before starting the controller
	originalPwmEnabled int
	// the last pwm value that was set to the fan
	lastSetPwm *int
	// a list of all pwm values where setPwm(x) != setPwm(y) for the controlled fan
	pwmValuesWithDistinctTarget []int
	// a map of x -> getPwm() where x is setPwm(x) for the controlled fan
	pwmMap map[int]int
}

func NewFanController(persistence persistence.Persistence, fan fans.Fan, updateRate time.Duration) FanController {
	return &fanController{
		persistence:                 persistence,
		fan:                         fan,
		curve:                       curves.SpeedCurveMap[fan.GetCurveId()],
		updateRate:                  updateRate,
		pwmValuesWithDistinctTarget: []int{},
		pwmMap:                      map[int]int{},
	}
}

func (f *fanController) Run(ctx context.Context) error {
	fan := f.fan

	// store original pwm_enable value
	pwmEnabled, err := fan.GetPwmEnabled()
	if err != nil {
		ui.Warning("Cannot read pwm_enable value of %s", fan.GetId())
	}
	f.originalPwmEnabled = pwmEnabled

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
			err = f.runInitializationSequence()
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

	err = fan.AttachFanCurveData(&fanPwmData)
	if err != nil {
		return err
	}

	f.updatePwmMap()
	f.updateDistinctPwmValues()

	ui.Info("Start PWM of %s: %d", fan.GetId(), fan.GetMinPwm())
	ui.Info("Max PWM of %s: %d", fan.GetId(), fan.GetMaxPwm())

	ui.Info("Starting controller loop for fan '%s'", fan.GetId())

	var g run.Group
	{
		// === rpm monitoring
		pollingRate := configuration.CurrentConfig.RpmPollingRate

		g.Add(func() error {
			tick := time.Tick(pollingRate)
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-tick:
					measureRpm(fan)
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
			tick := time.Tick(f.updateRate)
			for {
				select {
				case <-ctx.Done():
					ui.Info("Stopping fan controller for fan %s...", fan.GetId())
					f.restorePwmEnabled()
					return nil
				case <-tick:
					err = f.UpdateFanSpeed()
					if err != nil {
						ui.Error("Error in FanController for fan %s: %v", fan.GetId(), err)
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

func (f *fanController) UpdateFanSpeed() error {
	fan := f.fan
	target := f.calculateTargetPwm()
	if target >= 0 {
		_ = trySetManualPwm(f.fan)
		err := f.setPwm(target)
		if err != nil {
			ui.Error("Error setting %s: %v", fan.GetId(), err)
		}
	}

	return nil
}

// runs an initialization sequence for the given fan
// to determine an estimation of its fan curve
func (f *fanController) runInitializationSequence() (err error) {
	fan := f.fan

	curveData := map[int]float64{}

	if configuration.CurrentConfig.RunFanInitializationInParallel == false {
		InitializationSequenceMutex.Lock()
		defer InitializationSequenceMutex.Unlock()
	}

	err = trySetManualPwm(fan)
	if err != nil {
		ui.Warning("Could not enable manual fan mode on %s, trying to continue anyway...", f.fan.GetId())
	}

	initialMeasurement := true
	for pwm := fans.MinPwmValue; pwm <= fans.MaxPwmValue; pwm++ {
		// set a pwm
		err = fan.SetPwm(pwm)
		if err != nil {
			ui.Error("Unable to run initialization sequence on %s: %v", fan.GetId(), err)
			return err
		}

		// verify that we were successful in writing our desired PWM value
		// otherwise, skip this PWM value
		// TODO: this has to be done _always_, while the RPM measurements only make sense if the fan supports it
		actualPwm := fan.GetPwm()
		if actualPwm != pwm {
			ui.Debug("Actual PWM value differs from requested one, skipping. Requested: %d Actual: %d", pwm, actualPwm)
			continue
		}

		if initialMeasurement {
			initialMeasurement = false
			f.waitForFanToSettle(fan)
		} else {
			// wait a bit to allow the fan speed to settle.
			// since most sensors are update only each second,
			// we wait double that to make sure we get
			// the most recent measurement
			time.Sleep(2 * time.Second)
		}

		rpm := fan.GetRpm()
		ui.Debug("Measuring RPM of %s at PWM %d: %d", fan.GetId(), pwm, rpm)

		// update rpm curve
		fan.SetRpmAvg(float64(rpm))
		curveData[pwm] = float64(rpm)

		ui.Debug("Measured RPM of %d at PWM %d for fan %s", int(fan.GetRpmAvg()), pwm, fan.GetId())
	}

	err = fan.AttachFanCurveData(&curveData)
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
func measureRpm(fan fans.Fan) {
	pwm := fan.GetPwm()
	rpm := fan.GetRpm()

	updatedRpmAvg := util.UpdateSimpleMovingAvg(fan.GetRpmAvg(), configuration.CurrentConfig.RpmRollingWindowSize, float64(rpm))
	fan.SetRpmAvg(updatedRpmAvg)

	pwmRpmMap := fan.GetFanCurveData()
	(*pwmRpmMap)[pwm] = float64(rpm)
}

func trySetManualPwm(fan fans.Fan) error {
	err := fan.SetPwmEnabled(1)
	if err != nil {
		err = fan.SetPwmEnabled(0)
	}
	return err
}

func (f *fanController) restorePwmEnabled() {
	ui.Info("Trying to restore fan settings for %s...", f.fan.GetId())

	// try to reset the pwm_enable value
	if f.originalPwmEnabled != 1 {
		err := f.fan.SetPwmEnabled(f.originalPwmEnabled)
		if err == nil {
			return
		}
	}
	// if this fails, try to set it to max speed instead
	err := f.setPwm(fans.MaxPwmValue)
	if err != nil {
		ui.Warning("Unable to restore fan %s, make sure it is running!", f.fan.GetId())
	}
}

// calculates the optimal pwm for a fan with the given target level.
// returns -1 if no rpm is detected even at fan.maxPwm
func (f *fanController) calculateTargetPwm() int {
	fan := f.fan
	currentPwm := fan.GetPwm()
	target, err := f.curve.Evaluate()
	if err != nil {
		ui.Fatal("Unable to calculate optimal PWM value for %s: %v", fan.GetId(), err)
	}

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
	minPwm := fan.GetMinPwm()

	// TODO: this assumes a linear curve, but it might be something else
	target = minPwm + int((float64(target)/fans.MaxPwmValue)*(float64(maxPwm)-float64(minPwm)))

	// map the target value to the closest value supported by the fan
	target = f.mapToClosestDistinct(target)

	if f.lastSetPwm != nil && f.pwmMap != nil {
		lastSetPwm := *(f.lastSetPwm)
		expected := f.pwmMap[lastSetPwm]
		if currentPwm != expected {
			ui.Warning("PWM of %s was changed by third party! Last set PWM value was: %d but is now: %d",
				fan.GetId(), expected, currentPwm)
		}
	}

	if fan.Supports(fans.FeatureRpmSensor) {
		// make sure fans never stop by validating the current RPM
		// and adjusting the target PWM value upwards if necessary
		shouldNeverStop := fan.ShouldNeverStop()
		if shouldNeverStop && (f.lastSetPwm != nil || f.lastSetPwm == &target) {
			avgRpm := int(fan.GetRpmAvg())
			if avgRpm <= 0 {
				if target >= maxPwm {
					ui.Error("CRITICAL: Fan %s avg. RPM is %d, even at PWM value %d", fan.GetId(), avgRpm, target)
					return -1
				}
				ui.Warning("WARNING: Increasing startPWM of %s from %d to %d, which is supposed to never stop, but RPM is %d",
					fan.GetId(), fan.GetMinPwm(), fan.GetMinPwm()+1, avgRpm)
				fan.SetMinPwm(fan.GetMinPwm() + 1)
				target++

				// set the moving avg to a value > 0 to prevent
				// this increase from happening too fast
				fan.SetRpmAvg(1)
				err = f.setPwm(fan.GetMinPwm())
			}
		}
	}

	return target
}

// set the pwm speed of a fan to the specified value (0..255)
func (f *fanController) setPwm(target int) (err error) {
	current := f.fan.GetPwm()
	f.lastSetPwm = &target
	if target == current {
		return nil
	}
	err = f.fan.SetPwm(target)
	return err
}

func (f *fanController) waitForFanToSettle(fan fans.Fan) {
	// TODO: this "waiting" logic could also be applied to the other measurements
	diffThreshold := configuration.CurrentConfig.MaxRpmDiffForSettledFan

	measuredRpmDiffWindow := util.CreateRollingWindow(10)
	util.FillWindow(measuredRpmDiffWindow, 10, 2*diffThreshold)
	measuredRpmDiffMax := 2 * diffThreshold
	oldRpm := 0
	for !(measuredRpmDiffMax < diffThreshold) {
		ui.Debug("Waiting for fan %s to settle (current RPM max diff: %f)...", fan.GetId(), measuredRpmDiffMax)
		currentRpm := fan.GetRpm()
		measuredRpmDiffWindow.Append(math.Abs(float64(currentRpm - oldRpm)))
		oldRpm = currentRpm
		measuredRpmDiffMax = math.Ceil(util.GetWindowMax(measuredRpmDiffWindow))
		time.Sleep(1 * time.Second)
	}
	ui.Debug("Fan %s has settled (current RPM max diff: %f)", fan.GetId(), measuredRpmDiffMax)
}

func (f *fanController) mapToClosestDistinct(target int) int {
	closest := util.FindClosest(target, f.pwmValuesWithDistinctTarget)
	return f.pwmMap[closest]
}

func (f *fanController) updatePwmMap() {
	for i := 0; i <= 255; i++ {
		f.fan.SetPwm(i)
		time.Sleep(10 * time.Millisecond)
		f.pwmMap[i] = f.fan.GetPwm()
	}
}

func (f *fanController) updateDistinctPwmValues() {
	var keys []int

	lastDistinctOutput := -1
	for input, output := range f.pwmMap {
		if lastDistinctOutput == -1 || lastDistinctOutput != output {
			lastDistinctOutput = output
			keys = append(keys, input)
		}
	}
	sort.Ints(keys)

	f.pwmValuesWithDistinctTarget = keys
}
