package controller

import (
	"context"
	"github.com/asecurityteam/rolling"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/curves"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/persistence"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/oklog/run"
	"math"
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
	fan         fans.Fan
	curve       curves.SpeedCurve
	updateRate  time.Duration
}

func NewFanController(persistence persistence.Persistence, fan fans.Fan, updateRate time.Duration) FanController {
	return fanController{
		persistence: persistence,
		fan:         fan,
		updateRate:  updateRate,
	}
}

func (f fanController) Run(ctx context.Context) error {
	fan := f.fan

	// store original pwm_enable value
	pwmEnabled, err := fan.GetPwmEnabled()
	if err != nil {
		ui.Fatal("Cannot read pwm_enable value of %s", fan.GetId())
	}
	fan.SetOriginalPwmEnabled(pwmEnabled)

	ui.Info("Gathering sensor data for %s...", fan.GetId())
	// wait a bit to gather monitoring data
	time.Sleep(2*time.Second + configuration.CurrentConfig.TempSensorPollingRate*2)

	// check if we have data for this fan in persistence,
	// if not we need to run the initialization sequence
	ui.Info("Loading fan curve data for fan '%s'...", fan.GetId())
	fanPwmData, err := f.persistence.LoadFanPwmData(fan)
	if err != nil {
		ui.Warning("No fan curve data found for fan '%s', starting initialization sequence...", fan.GetId())
		err = f.runInitializationSequence()
		if err != nil {
			return err
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

	ui.Info("Start PWM of %s: %d", fan.GetId(), fan.GetMinPwm())
	ui.Info("Max PWM of %s: %d", fan.GetId(), fan.GetMaxPwm())

	err = trySetManualPwm(fan)
	if err != nil {
		ui.Error("Could not enable fan control on %s", fan.GetId())
		return err
	}

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
				ui.Fatal("Error monitoring fan rpm: %v", err)
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
					return nil
				case <-tick:
					err = f.UpdateFanSpeed()
					if err != nil {
						ui.Error("Error in FanController for fan %s: %v", fan.GetId(), err)
						ui.Info("Trying to restore fan settings for %s...", f.fan.GetId())

						// try to reset the pwm_enable value
						if fan.GetOriginalPwmEnabled() != 1 {
							err1 := fan.SetPwmEnabled(fan.GetOriginalPwmEnabled())
							if err1 == nil {
								return err
							}
						}
						// if this fails, try to set it to max speed instead
						err1 := setPwm(fan, fans.MaxPwmValue)
						if err1 != nil {
							ui.Warning("Unable to restore fan %s, make sure it is running!", fan.GetId())
						}

						return err
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

func (f fanController) UpdateFanSpeed() error {
	fan := f.fan
	current := fan.GetPwm()
	optimalPwm, err := f.calculateOptimalPwm(fan)
	if err != nil {
		ui.Error("Unable to calculate optimal PWM value for %s: %v", fan.GetId(), err)
		return err
	}
	target := calculateTargetPwm(fan, current, optimalPwm)
	if target >= 0 {
		err = setPwm(fan, target)
		if err != nil {
			ui.Error("Error setting %s: %v", fan.GetId(), err)
			err = trySetManualPwm(fan)
			if err != nil {
				ui.Error("Could not enable fan control on %s", fan.GetId())
				return err
			}
		}
	}

	return nil
}

// runs an initialization sequence for the given fan
// to determine an estimation of its fan curve
func (f fanController) runInitializationSequence() (err error) {
	fan := f.fan

	if configuration.CurrentConfig.RunFanInitializationInParallel == false {
		InitializationSequenceMutex.Lock()
		defer InitializationSequenceMutex.Unlock()
	}

	err = trySetManualPwm(fan)
	if err != nil {
		ui.Error("Could not enable fan control on %s", fan.GetId())
		return err
	}

	for pwm := 0; pwm <= fans.MaxPwmValue; pwm++ {
		// set a pwm
		err = fan.SetPwm(pwm)
		if err != nil {
			ui.Error("Unable to run initialization sequence on %s: %v", fan.GetId(), err)
			return err
		}

		if pwm == 0 {
			// TODO: this "waiting" logic could also be applied to the other measurements
			diffThreshold := configuration.CurrentConfig.MaxRpmDiffForSettledFan

			measuredRpmDiffWindow := util.CreateRollingWindow(10)
			fillWindow(measuredRpmDiffWindow, 10, 2*diffThreshold)
			measuredRpmDiffMax := 2 * diffThreshold
			oldRpm := 0
			for !(measuredRpmDiffMax < diffThreshold) {
				ui.Debug("Waiting for fan %s to settle (current RPM max diff: %f)...", fan.GetId(), measuredRpmDiffMax)
				currentRpm := fan.GetPwm()
				measuredRpmDiffWindow.Append(math.Abs(float64(currentRpm - oldRpm)))
				oldRpm = currentRpm
				measuredRpmDiffMax = math.Ceil(getWindowMax(measuredRpmDiffWindow))
				time.Sleep(1 * time.Second)
			}
			ui.Debug("Fan %s has settled (current RPM max diff: %f)", fan.GetId(), measuredRpmDiffMax)
		} else {
			// wait a bit to allow the fan speed to settle.
			// since most sensors are update only each second,
			// we wait double that to make sure we get
			// the most recent measurement
			time.Sleep(2 * time.Second)
		}

		// TODO:
		// on some fans it is not possible to use the full pwm of 0..255
		// so we try what values work and save them for later

		ui.Debug("Measuring RPM of %s at PWM: %d", fan.GetId(), pwm)
		for i := 0; i < configuration.CurrentConfig.RpmRollingWindowSize; i++ {
			// update rpm curve
			measureRpm(fan)
		}
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

	ui.Debug("Measured RPM of %d at PWM %d for fan %s", rpm, pwm, fan.GetId())

	updatedRpmAvg := util.UpdateSimpleMovingAvg(fan.GetRpmAvg(), configuration.CurrentConfig.RpmRollingWindowSize, float64(rpm))
	fan.SetRpmAvg(updatedRpmAvg)

	pwmRpmMap := fan.GetFanCurveData()
	pointWindow, exists := (*pwmRpmMap)[pwm]
	if !exists {
		// create rolling window for current pwm value
		pointWindow = util.CreateRollingWindow(configuration.CurrentConfig.RpmRollingWindowSize)
		(*pwmRpmMap)[pwm] = pointWindow
	}
	pointWindow.Append(float64(rpm))
}

func trySetManualPwm(fan fans.Fan) (err error) {
	err = fan.SetPwmEnabled(1)
	if err != nil {
		err = fan.SetPwmEnabled(0)
	}
	return err
}

// calculates the target speed for a given device output
func (f fanController) calculateOptimalPwm(fan fans.Fan) (int, error) {
	curveConfigId := fan.GetCurveId()
	speedCurve := curves.SpeedCurveMap[curveConfigId]
	return speedCurve.Evaluate()
}

// calculates the optimal pwm for a fan with the given target level.
// returns -1 if no rpm is detected even at fan.maxPwm
func calculateTargetPwm(fan fans.Fan, currentPwm int, pwm int) int {
	target := pwm

	// ensure target value is within bounds of possible values
	if target > fans.MaxPwmValue {
		ui.Warning("Tried to set out-of-bounds PWM value %d on fan %s", pwm, fan.GetId())
		target = fans.MaxPwmValue
	} else if target < fans.MinPwmValue {
		ui.Warning("Tried to set out-of-bounds PWM value %d on fan %s", pwm, fan.GetId())
		target = fans.MinPwmValue
	}

	// map the target value to the possible range of this fan
	maxPwm := fan.GetMaxPwm()
	minPwm := fan.GetMinPwm()

	// TODO: this assumes a linear curve, but it might be something else
	target = minPwm + int((float64(target)/fans.MaxPwmValue)*(float64(maxPwm)-float64(minPwm)))

	lastSetPwm := fan.GetLastSetPwm()
	if lastSetPwm != fans.InitialLastSetPwm && lastSetPwm != currentPwm {
		ui.Warning("PWM of %s was changed by third party! Last set PWM value was: %d but is now: %d",
			fan.GetId(), lastSetPwm, currentPwm)
	}

	if fan.Supports(fans.FeatureRpmSensor) {
		// make sure fans never stop by validating the current RPM
		// and adjusting the target PWM value upwards if necessary
		if fan.ShouldNeverStop() && lastSetPwm == target {
			// TODO: this only works if the fan supports RPM measurements at all
			avgRpm := fan.GetRpmAvg()
			if avgRpm <= 0 {
				if target >= maxPwm {
					ui.Error("CRITICAL: Fan %s avg. RPM is %f, even at PWM value %d", fan.GetId(), avgRpm, target)
					return -1
				}
				ui.Warning("WARNING: Increasing startPWM of %s from %d to %d, which is supposed to never stop, but RPM is %f",
					fan.GetId(), fan.GetMinPwm(), fan.GetMinPwm()+1, avgRpm)
				fan.SetMinPwm(fan.GetMinPwm() + 1)
				target++

				// set the moving avg to a value > 0 to prevent
				// this increase from happening too fast
				fan.SetRpmAvg(1)
			}
		}
	}

	return target
}

// set the pwm speed of a fan to the specified value (0..255)
func setPwm(fan fans.Fan, target int) (err error) {
	current := fan.GetPwm()
	if target == current {
		return nil
	}
	err = fan.SetPwm(target)
	return err
}

// completely fills the given window with the given value
func fillWindow(window *rolling.PointPolicy, size int, value float64) {
	for i := 0; i < size; i++ {
		window.Append(value)
	}
}

// returns the max value in the window
func getWindowMax(window *rolling.PointPolicy) float64 {
	return window.Reduce(rolling.Max)
}
