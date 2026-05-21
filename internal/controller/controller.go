package controller

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/markusressel/fan2go/internal/control_loop"

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
	// pwmMap is missing keys they're filled in using step interpolation: each missing key maps to
	// the value of the nearest preceding key - but the values are not guaranteed to be in the
	// range of [0..255] as well.
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
	//   [0:0, 1: 0, ..., 127: 0, 128: 3, 129: 3, ..., 254: 3, 255: 6]
	//
	// It's actually implemented as an array, where the "key" is the index
	pwmMapping [256]int

	// don't get and set PWMs in computeSetPwmToGetPwmMapAutomatically(), assume 1:1 mapping instead
	// (enabled by `fan2go fan --id <id> init --assume-pwm-map-identity`)
	assumePwmMapIdentity bool

	// control loop that specifies how the target value of the curve is approached
	controlLoop control_loop.ControlLoop

	// offset applied to the actual minPwm of the fan to ensure "neverStops" constraint
	minPwmOffset int

	// lastFanModeCheckTime is the last time we checked if some third party changed the fan control mode
	lastFanModeCheckTime time.Time
}

func NewFanController(
	persistence persistence.Persistence,
	fan fans.Fan,
	controlLoop control_loop.ControlLoop,
	updateRate time.Duration,
	assumePwmMapIdentity bool,
) FanController {
	curve, ok := curves.GetSpeedCurve(fan.GetCurveId())
	if !ok {
		ui.Fatal("Fan %s: Failed to create fan controller: Curve with ID '%s' not found", fan.GetId(), fan.GetCurveId())
	}
	return &DefaultFanController{
		persistence:                      persistence,
		fan:                              fan,
		curve:                            curve,
		updateRate:                       updateRate,
		targetValuesWithDistinctPWMValue: []int{},
		controlLoop:                      controlLoop,
		minPwmOffset:                     0,
		assumePwmMapIdentity:             assumePwmMapIdentity,
		lastFanModeCheckTime:             time.Unix(0, 0), // ensure first check happens immediately
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
	if f.fan.Supports(fans.FeatureControlModeRead) {
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
	if err != nil {
		return err
	}

	ui.Debug("setPwmToGetPwmMap of fan '%s': %v", fan.GetId(), f.setPwmToGetPwmMap)
	ui.Debug("pwmMap of fan '%s': %v", fan.GetId(), f.pwmMapping)
	ui.Info("PWM settings of fan '%s': Min %d, Start %d, Max %d", fan.GetId(), fan.GetMinPwm(), fan.GetStartPwm(), fan.GetMaxPwm())
	ui.Info("Fan %s: Starting controller loop", fan.GetId())

	if fan.GetMinPwm() > fan.GetStartPwm() {
		ui.Warning("Suspicious pwm config of fan '%s': MinPwm (%d) > StartPwm (%d)", fan.GetId(), fan.GetMinPwm(), fan.GetStartPwm())
	}

	// TODO: check if fan.Supports(fans.FeatureControlModeWrite) - or is it ok if it doesn't and our
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
	controllerCtx, cancelController := context.WithCancel(ctx)
	defer cancelController()

	if fan.Supports(fans.FeatureRpmSensor) {
		// === rpm monitoring
		pollingRate := configuration.CurrentConfig.RpmPollingRate

		g.Add(func() error {
			tick := time.NewTicker(pollingRate)
			defer tick.Stop()
			for {
				select {
				case <-controllerCtx.Done():
					ui.Info("Fan %s: Stopping RPM monitor of fan controller...", fan.GetId())
					return nil
				case <-tick.C:
					f.measureRpm(fan)
				}
			}
		}, func(err error) {
			cancelController()
			if err != nil {
				ui.Warning("Error monitoring fan rpm: %v", err)
			}
		})
	}

	{
		g.Add(func() error {
			time.Sleep(1 * time.Second)
			tick := time.NewTicker(f.updateRate)
			defer tick.Stop()
			for {
				select {
				case <-controllerCtx.Done():
					ui.Info("Fan %s: Stopping fan controller...", fan.GetId())
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
			cancelController()
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
	ui.Info("Fan %s: Loading fan curve data...", fan.GetId())
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

	var speedTarget int

	if fan.GetConfig().UseUnscaledCurveValues {
		speedTarget = int(math.Round(target))
		if speedTarget > 0 && speedTarget < minPwm {
			// the fan wouldn't spin with this PWM value anyway, so set 0 instead
			// (might be better for the hardware and preserve energy)
			speedTarget = 0
		}
	} else {
		if target < 1.0 && !shouldNeverStop {
			// target value 0 (or actually < 1) is mapped to PWM 0, if fan is allowed to stop
			speedTarget = 0
		} else {
			// target values [1..255] are mapped to [minPwm..maxPwm]
			// adjust the target value determined by the control algorithm to the operational needs
			// of the fan, which includes its supported pwm range (which might be different from [0..255])

			// target values [1..255] => [0..254]
			if target >= 1.0 {
				target -= 1.0
			} else {
				// values < 1 become 0 (which becomes speedTarget = minPwm), just like 1.
				// Only happens if NeverStop (where 0 should map to minPwm instead of 0)
				// is set, but shouldn't really matter and unifies the behavior
				// for NeverStop enabled/disabled (for >= 1)
				target = 0
			}
			// scale [0..254] to [minPwm..maxPwm]
			speedTarget = minPwm + int(math.Round((target/(fans.MaxPwmValue-1))*float64(maxPwm-minPwm)))
		}
	}

	// if this fan should never stop, make sure its target is always at least minPwm+f.minPwmOffset
	// (f.minPwmOffset is usually 0, but if the fan doesn't start at MinPwm it gets increased)
	if shouldNeverStop && speedTarget < minPwm+f.minPwmOffset {
		speedTarget = minPwm + f.minPwmOffset
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
			lastSetTargetEqualsNewTarget := lastTarget == speedTarget
			if shouldNeverStop && lastSetTargetEqualsNewTarget {
				avgRpm := fan.GetRpmAvg()
				if avgRpm <= 0 {
					if speedTarget >= maxPwm {
						ui.Error("CRITICAL: Fan %s avg. RPM is %d, even at PWM value %d", fan.GetId(), int(avgRpm), lastSetPwm)
						return ErrFanStalledAtMaxPwm
					}
					oldMinPwm := minPwm
					ui.Warning("Increasing minPWM of %s from %d to %d, which is supposed to never stop, but RPM is %d at PWM %d",
						fan.GetId(), oldMinPwm, oldMinPwm+1, int(avgRpm), lastSetPwm)
					f.increaseMinPwmOffset()
					speedTarget++

					// set the moving avg to a value > 0 to prevent
					// this increase from happening too fast
					fan.SetRpmAvg(1)
				}
			}
		}
	}

	err = f.setPwm(speedTarget)
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
		ui.Error("Fan %s: Error computing fan specific mappings: %v", fan.GetId(), err)
		return err
	}

	if !fan.Supports(fans.FeatureRpmSensor) {
		ui.Info("Fan '%s' doesn't support RPM sensor, skipping fan curve measurement", fan.GetId())
		return nil
	}
	ui.Info("Fan %s: Measuring RPM curve (two-phase adaptive sweep)...", fan.GetId())

	err = trySetManualPwm(fan)
	if err != nil {
		ui.Warning("Could not enable manual fan mode on %s, trying to continue anyway...", fan.GetId())
	}

	distinct := f.targetValuesWithDistinctPWMValue
	if len(distinct) == 0 {
		ui.Warning("Fan %s: no distinct PWM values found, skipping initialization", fan.GetId())
		return nil
	}

	curveData := map[int]float64{}

	// --- Phase 1: Coarse sweep ---
	curveData, err = f.rpmCurveMeasurementPhase1(fan, distinct, curveData)
	if err != nil {
		return err
	}

	// --- Phase 2: Targeted refinement ---
	// Build sorted list of coarse measurement PWM values.
	curveData, err = f.rpmCurveMeasurementPhase2(fan, distinct, curveData)
	if err != nil {
		return err
	}

	curveData, err = f.rpmCurveMeasurementCleanup(curveData)
	if err != nil {
		return err
	}

	err = fan.AttachFanRpmCurveData(&curveData)
	if err != nil {
		ui.Error("Fan %s: Failed to attach fan curve data: %v", fan.GetId(), err)
		return err
	}

	// save to database to restore it on restarts
	err = f.persistence.SaveFanRpmData(fan)
	if err != nil {
		ui.Error("Fan %s: Failed to save RWM data: %v", fan.GetId(), err)
	}
	return err
}

func (f *DefaultFanController) rpmCurveMeasurementPhase1(
	fan fans.Fan,
	distinct []int,
	curveData map[int]float64,
) (map[int]float64, error) {
	coarseStep := configuration.CurrentConfig.Analysis.CoarseStep
	if coarseStep <= 0 {
		coarseStep = 1
	}
	// Build indices into distinct[], spaced coarseStep apart, always including first and last.
	coarseIndices := []int{distinct[0]}
	for i := coarseStep; i < len(distinct)-1; i += coarseStep {
		coarseIndices = append(coarseIndices, i)
	}
	lastIdx := len(distinct) - 1
	if coarseIndices[len(coarseIndices)-1] != lastIdx {
		coarseIndices = append(coarseIndices, lastIdx)
	}

	ui.Info("Fan %s: Phase 1: Coarse sweep at %d points (every %dth out of %d total distinct PWM values)",
		fan.GetId(), len(coarseIndices), coarseStep, len(distinct))
	for _, idx := range coarseIndices {
		pwm := distinct[idx]
		measuredRpm, err := f.measureAtPwm(fan, pwm, configuration.CurrentConfig.Analysis.SettleTimeout)
		if err != nil {
			ui.Error("Fan %s: Error measuring at PWM %d: %v", fan.GetId(), pwm, err)
			return curveData, err
		}
		if measuredRpm < 0 {
			continue // PWM mismatch detected, skip
		}
		ui.Debug("Fan %s: Phase 1: at PWM %d: %.1f RPM", fan.GetId(), pwm, measuredRpm)
		curveData[pwm] = measuredRpm
		fan.SetRpmAvg(measuredRpm)
	}

	return curveData, nil
}

func (f *DefaultFanController) rpmCurveMeasurementPhase2(
	fan fans.Fan,
	distinct []int,
	curveData map[int]float64,
) (map[int]float64, error) {
	coarsePwms := make([]int, 0, len(curveData))
	for pwm := range curveData {
		coarsePwms = append(coarsePwms, pwm)
	}
	sort.Ints(coarsePwms)

	alreadyMeasured := make(map[int]bool, len(curveData))
	for k := range curveData {
		alreadyMeasured[k] = true
	}
	toMeasure := map[int]bool{}

	// startPwm refinement: densely measure between the last coarse PWM with RPM=0
	// and the first coarse PWM with RPM>0.
	lastZeroPwm := -1
	firstNonZeroPwm := -1
	for _, pwm := range coarsePwms {
		if curveData[pwm] <= 0 {
			lastZeroPwm = pwm
		} else if firstNonZeroPwm < 0 {
			firstNonZeroPwm = pwm
		}
	}
	if firstNonZeroPwm >= 0 {
		lowBound := distinct[0]
		if lastZeroPwm >= 0 {
			lowBound = lastZeroPwm
		}
		for _, pwm := range distinct {
			if pwm > lowBound && pwm < firstNonZeroPwm && !alreadyMeasured[pwm] {
				toMeasure[pwm] = true
			}
		}
	} else {
		ui.Warning("Fan %s: coarse sweep found no RPM > 0, skipping startPwm refinement", fan.GetId())
	}

	// maxPwm refinement: densely measure between the coarse point just before and just after the peak.
	peakRpm := 0.0
	peakPwm := -1
	for _, pwm := range coarsePwms {
		if curveData[pwm] > peakRpm {
			peakRpm = curveData[pwm]
			peakPwm = pwm
		}
	}
	lastIdx := len(distinct) - 1
	if peakPwm >= 0 {
		maxRegionLow := distinct[0]
		maxRegionHigh := distinct[lastIdx]
		for i, pwm := range coarsePwms {
			if pwm == peakPwm {
				if i > 0 {
					maxRegionLow = coarsePwms[i-1]
				}
				if i < len(coarsePwms)-1 {
					maxRegionHigh = coarsePwms[i+1]
				}
				break
			}
		}
		for _, pwm := range distinct {
			if pwm > maxRegionLow && pwm < maxRegionHigh && !alreadyMeasured[pwm] {
				toMeasure[pwm] = true
			}
		}
	}

	// Collect, sort, and measure Phase 2 PWM values.
	phase2Pwms := make([]int, 0, len(toMeasure))
	for pwm := range toMeasure {
		phase2Pwms = append(phase2Pwms, pwm)
	}
	sort.Ints(phase2Pwms)

	if len(phase2Pwms) > 0 {
		ui.Info("Fan %s: Phase 2: Refining %d boundary points", fan.GetId(), len(phase2Pwms))
		for _, pwm := range phase2Pwms {
			measuredRpm, err := f.measureAtPwm(fan, pwm, configuration.CurrentConfig.Analysis.SettleTimeout)
			if err != nil {
				ui.Error("Fan %s: Error measuring at PWM %d: %v", fan.GetId(), pwm, err)
				return curveData, err
			}
			if measuredRpm < 0 {
				continue
			}
			ui.Debug("Fan %s: Phase 2: at PWM %d: %.1f RPM", fan.GetId(), pwm, measuredRpm)
			curveData[pwm] = measuredRpm
			fan.SetRpmAvg(measuredRpm)
		}
	}

	return curveData, nil
}

func (f *DefaultFanController) rpmCurveMeasurementCleanup(curveData map[int]float64) (interpolatedCurveData map[int]float64, err error) {
	firstNonZeroPwm := 0
	sortedPwmValues := util.SortedKeys(curveData)
	lastPwm := sortedPwmValues[len(sortedPwmValues)-1]
	for _, pwm := range sortedPwmValues {
		if curveData[pwm] > 0 {
			firstNonZeroPwm = pwm
			break
		}
	}

	// Interpolation Phase:
	// First Step: Interpolate stepwise (using util.InterpolateStep()) until the first value > 0 is reached, to ensure the curve data contains the critical "startPwm" point where the fan starts spinning.
	// Second Step: Interpolate linearly (using util.InterpolateLinearly()) between all measured points to fill in any remaining gaps, ensuring the curve data contains the full range of PWM values as keys.
	interpolatedCurveData, err = util.InterpolateStep(&curveData, 0, firstNonZeroPwm)
	if err != nil {
		return nil, fmt.Errorf("error interpolating curve data: %v", err)
	}
	interpolatedCurveData, err = util.InterpolateLinearly(&interpolatedCurveData, firstNonZeroPwm, lastPwm)
	if err != nil {
		return nil, fmt.Errorf("error during linear interpolation of fan curve data: %w", err)
	}
	interpolatedCurveData = util.EnsureMonotonicallyIncreasing(interpolatedCurveData, firstNonZeroPwm, lastPwm)

	// Keep boundary detection (start/max PWM) based on robust raw statistics,
	// and only smooth the interior range to avoid boundary drift.
	startPwmRaw, maxPwmRaw := fans.ComputePwmBoundariesFromCurveData(interpolatedCurveData, fans.MaxPwmValue)
	if maxPwmRaw-startPwmRaw > 1 {
		interiorStart := startPwmRaw + 1
		interiorStop := maxPwmRaw - 1
		interpolatedCurveData = util.SmoothMapValuesKalman(interpolatedCurveData, interiorStart, interiorStop, util.DefaultKalmanConfig)
	}

	return interpolatedCurveData, nil
}

// measureAtPwm sets the fan to the given target PWM value, optionally waits for it to settle,
// takes SampleCount RPM samples spaced SampleDelay apart, and returns
// their median as a robust per-point estimate. Returns -1 (with nil error) if the reported PWM does not match the
// expected value after setting it (indicates the hardware ignored the request).
// If settleTimeout > 0, waitForFanToSettle is called with that timeout (used for large PWM steps).
// If settleTimeout == 0, a plain FanResponseDelay sleep is used instead (sufficient for small steps).
func (f *DefaultFanController) measureAtPwm(fan fans.Fan, pwm int, settleTimeout time.Duration) (float64, error) {
	actualPwm := f.applyPwmMapToTarget(pwm)
	err := f.setPwm(actualPwm)
	if err != nil {
		return 0, fmt.Errorf("unable to set PWM %d: %w", actualPwm, err)
	}
	expectedPwm := f.getReportedPwmAfterApplyingPwm(actualPwm)
	time.Sleep(f.getPwmSetDelay())

	currentPwm, err := f.getPwm()
	if err != nil {
		return 0, fmt.Errorf("fan %s: unable to read PWM: %w", fan.GetId(), err)
	}
	if currentPwm != expectedPwm {
		ui.Debug("Fan %s: PWM mismatch at target %d: expected %d, got %d, skipping",
			fan.GetId(), pwm, expectedPwm, currentPwm)
		return -1, nil
	}

	if settleTimeout > 0 {
		f.waitForFanToSettle(fan, settleTimeout)
	} else {
		// Small PWM step — a response-delay sleep is sufficient.
		time.Sleep(time.Duration(configuration.CurrentConfig.FanResponseDelay) * time.Second)
	}

	sampleCount := configuration.CurrentConfig.Analysis.SampleCount
	if sampleCount <= 0 {
		sampleCount = 1
	}
	sampleDelay := configuration.CurrentConfig.Analysis.SampleDelay
	samples := make([]float64, 0, sampleCount)
	for i := 0; i < sampleCount; i++ {
		if i > 0 {
			time.Sleep(sampleDelay)
		}
		r, err := fan.GetRpm()
		if err != nil {
			ui.Warning("Unable to read RPM of fan %s: %v", fan.GetId(), err)
			continue
		}
		samples = append(samples, float64(r))
	}
	if len(samples) == 0 {
		return 0, fmt.Errorf("no RPM samples collected at PWM %d", pwm)
	}
	return util.MedianFloat64(samples), nil
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
	}

	return f.fan.GetMinPwm(), nil
}

func parseControlModeValue(value configuration.ControlModeValue) (fans.ControlMode, error) {
	s := string(value)
	if i, err := strconv.Atoi(s); err == nil {
		return fans.ControlMode(i), nil
	}
	switch strings.ToLower(s) {
	case "auto", "automatic":
		return fans.ControlModeAutomatic, nil
	case "pwm", "manual":
		return fans.ControlModePWM, nil
	case "disabled":
		return fans.ControlModeDisabled, nil
	default:
		return fans.ControlModeUnknown, fmt.Errorf("unknown control mode %q (valid: auto, pwm, disabled, or integer)", s)
	}
}

func trySetManualPwm(fan fans.Fan) error {
	if !fan.Supports(fans.FeatureControlModeWrite) {
		return nil
	}

	// Use configured active mode, or default to ControlModePWM
	targetMode := fans.ControlModePWM
	if cfg := fan.GetConfig().ControlMode; cfg != nil && cfg.Active != nil {
		mode, err := parseControlModeValue(*cfg.Active)
		if err != nil {
			ui.Warning("Fan %s: Invalid controlMode.active: %v; falling back to pwm", fan.GetId(), err)
		} else {
			targetMode = mode
		}
	}

	err := fan.SetControlMode(targetMode)
	if err != nil {
		ui.Error("Unable to set Fan Mode of '%s' to \"%d\": %v", fan.GetId(), targetMode, err)
		// Fall back to disabled only when using default pwm mode (preserve explicit config)
		if targetMode == fans.ControlModePWM {
			err = fan.SetControlMode(fans.ControlModeDisabled)
			if err != nil {
				ui.Error("Unable to set Fan Mode of '%s' to \"%d\": %v", fan.GetId(), fans.ControlModeDisabled, err)
			}
		}
	}
	return err
}

func (f *DefaultFanController) restoreControlMode() {
	ui.Info("Trying to restore fan settings for %s...", f.fan.GetId())

	var onExit *configuration.OnExitConfig
	if cfg := f.fan.GetConfig().ControlMode; cfg != nil {
		onExit = cfg.OnExit
	}

	// none: skip all restore actions
	if onExit != nil && onExit.None != nil {
		ui.Info("Skipping fan restore for %s (controlMode.onExit: none)", f.fan.GetId())
		return
	}

	var controlModeToSet *fans.ControlMode = nil
	var pwmToSet *int = nil

	// controlMode and/or speed: set explicit values on exit
	if onExit != nil {
		// determine control mode to set on exit, if any
		if onExit.Restore != nil {
			controlModeToSet = &f.originalControlMode
		} else if onExit.ControlMode != nil {
			parsedControlMode, err := parseControlModeValue(*onExit.ControlMode)
			if err != nil {
				ui.Warning("Fan %s: Error parsing controlMode.onExit.controlMode: %v", f.fan.GetId(), err)
			} else {
				controlModeToSet = &parsedControlMode
			}
		} else {
			// if no explicit control mode to set is provided, but the fan supports writing the control mode and the original mode was not automatic, restore the original mode
			if f.originalControlMode != fans.ControlModeAutomatic {
				controlModeToSet = &f.originalControlMode
			}
		}

		// determine PWM value to set on exit, if any
		if onExit.Speed != nil {
			pwmToSet = onExit.Speed
		}
	} else {
		// default restore behavior
		controlModeToSet = &f.originalControlMode
	}

	if pwmToSet == nil {
		// if the original control mode was manual, restore it to manual and set the original PWM value
		if controlModeToSet != nil && *controlModeToSet != fans.ControlModeAutomatic {
			// if control mode is set to manual but no speed is provided, set the original value
			originalPwmValue := f.originalPwmValue
			pwmToSet = &originalPwmValue
		}
	}

	if controlModeToSet != nil {
		if f.fan.Supports(fans.FeatureControlModeWrite) {
			if err := f.fan.SetControlMode(*controlModeToSet); err != nil {
				// if this fails, try to set it to max speed instead
				if err := f.fan.SetPwm(fans.MaxPwmValue); err != nil {
					ui.Warning("Unable to restore fan %s, make sure it is running!", f.fan.GetId())
				}
				return
			}
		} else {
			ui.Warning("Cannot restore control mode of fan %s, writing control mode is not supported", f.fan.GetId())
		}
	}
	if pwmToSet != nil {
		// restore (default: onExit == nil or onExit.Restore != nil)
		if err := f.fan.SetPwm(*pwmToSet); err != nil {
			ui.Warning("Fan %s: Error restoring original PWM value: %v", f.fan.GetId(), err)
		}
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

// getPwmSetDelay returns the effective PWM set delay for this fan, using the per-fan override
// if configured or falling back to the global fanController.pwmSetDelay.
func (f *DefaultFanController) getPwmSetDelay() time.Duration {
	if d := f.fan.GetConfig().PwmSetDelay; d != nil {
		return *d
	}
	return configuration.CurrentConfig.FanController.PwmSetDelay
}

// ensureNoThirdPartyIsMessingWithUs checks if the PWM value of the fan does not match the last
// value PWM set by fan2go. If that is the case, it is assumed that a third party has changed the PWM value
// of the fan, which can lead to unexpected behavior.
func (f *DefaultFanController) ensureNoThirdPartyIsMessingWithUs() {
	fanConfig := f.fan.GetConfig()
	sanityCheckConfig := fanConfig.SanityCheck
	pwmValuwChngedByThirdPartyCheckConfig := sanityCheckConfig.PwmValueChangedByThirdParty
	if !pwmValuwChngedByThirdPartyCheckConfig.Enabled.Get() {
		// sanity checks are disabled, so we don't check for third party changes
		return
	}

	if !f.fan.Supports(fans.FeaturePwmSensor) {
		// we cannot read the PWM value, so we also cannot check if third party changed the PWM value
		ui.Warning("Fan %s does not support PWM sensor reading, disabling 'PwmValueChangedByThirdParty' sanity check", f.fan.GetId())
		fanConfig.SanityCheck.PwmValueChangedByThirdParty.Enabled.SetOverride(false)
		f.fan.SetConfig(fanConfig)
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

	f.ensureFanModeIsSetToExpectedMode()

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

// ensureFanModeIsSetToExpectedMode makes sure that the fan control mode is set to the expected mode (manual PWM control),
// by checking periodically.
func (f *DefaultFanController) ensureFanModeIsSetToExpectedMode() {
	fanConfig := f.fan.GetConfig()
	sanityCheckConfig := fanConfig.SanityCheck.FanModeChangedByThirdParty

	if !sanityCheckConfig.Enabled.Get() {
		// sanity check is disabled
		return
	}

	if !f.fan.Supports(fans.FeatureControlModeRead) {
		ui.Warning("Fan %s does not support control mode reading, disabling 'FanModeChangedByThirdParty' sanity check", f.fan.GetId())
		fanConfig.SanityCheck.FanModeChangedByThirdParty.Enabled.SetOverride(false)
		f.fan.SetConfig(fanConfig)
		return
	}

	// make sure we don't check this too often
	if f.lastFanModeCheckTime.Add(sanityCheckConfig.ThrottleDuration).After(time.Now()) {
		return
	}
	f.lastFanModeCheckTime = time.Now()

	cm, e := f.fan.GetControlMode()
	if e != nil {
		ui.Warning("Cannot read control mode of fan %s: %v", f.fan.GetId(), e)
		return
	}
	if cm != fans.ControlModePWM {
		restoreErr := trySetManualPwm(f.fan)
		if restoreErr != nil {
			ui.Warning("Fan mode of fan %s is %v (expected PWM); could not restore: %v",
				f.fan.GetId(), cm, restoreErr)
		} else {
			ui.Debug("Fan mode of fan %s was %v, silently restored to PWM mode", f.fan.GetId(), cm)
		}
	}
}

// waitForFanToSettle waits until the fan's RPM readings are stable (requiredConsecutive consecutive
// readings with diff <= MaxRpmDiffForSettledFan). If timeout > 0 and the deadline is exceeded, a
// warning is logged and the function returns early rather than blocking forever.
func (f *DefaultFanController) waitForFanToSettle(fan fans.Fan, timeout time.Duration) {
	const requiredConsecutive = 3
	diffThreshold := configuration.CurrentConfig.MaxRpmDiffForSettledFan

	oldRpm := 0
	// Prime oldRpm so the first diff is small rather than |rpm - 0|.
	if r, err := fan.GetRpm(); err == nil {
		oldRpm = r
	}

	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}

	consecutiveStable := 0
	for consecutiveStable < requiredConsecutive {
		if timeout > 0 && time.Now().After(deadline) {
			ui.Warning("Fan %s did not settle within %v, continuing anyway (%d/%d stable readings)", fan.GetId(), timeout, consecutiveStable, requiredConsecutive)
			return
		}
		ui.Debug("Fan %s: Waiting for fan to settle (%d/%d stable readings)...", fan.GetId(), consecutiveStable, requiredConsecutive)
		time.Sleep(1 * time.Second)

		currentRpm, err := fan.GetRpm()
		if err != nil {
			ui.Warning("Fan %s: Cannot read RPM value: %v", fan.GetId(), err)
			continue
		}
		diff := math.Abs(float64(currentRpm - oldRpm))
		if diff <= diffThreshold {
			consecutiveStable++
		} else {
			consecutiveStable = 0
		}
		oldRpm = currentRpm
	}
	ui.Debug("Fan %s has settled (%d consecutive stable readings)", fan.GetId(), requiredConsecutive)
}

// computeSetPwmToGetPwmMap computes a mapping between "set pwm value" -> "actual pwm value"
func (f *DefaultFanController) computeSetPwmToGetPwmMap() (err error) {
	cfg := f.fan.GetConfig().SetPwmToGetPwmMap

	if cfg != nil {
		if cfg.Identity != nil {
			ui.Info("Fan %s: Using identity set→get PWM map", f.fan.GetId())
			f.setPwmToGetPwmMap, _ = util.InterpolateLinearlyInt(&map[int]int{0: 0, 255: 255}, 0, 255)
			return nil
		}
		if cfg.Values != nil {
			ui.Info("Fan %s: Using user-defined step set→get PWM map", f.fan.GetId())
			pts := map[int]int(*cfg.Values)
			expanded, err := util.InterpolateStepInt(&pts, 0, 255)
			if err != nil {
				return fmt.Errorf("error expanding setPwmToGetPwmMap (values): %w", err)
			}
			f.setPwmToGetPwmMap = expanded
			return nil
		}
		if cfg.Linear != nil {
			ui.Info("Fan %s: Using user-defined linear set→get PWM map", f.fan.GetId())
			pts := map[int]int(*cfg.Linear)
			expanded, err := util.InterpolateLinearlyInt(&pts, 0, 255)
			if err != nil {
				return fmt.Errorf("error expanding setPwmToGetPwmMap (linear): %w", err)
			}
			f.setPwmToGetPwmMap = expanded
			return nil
		}
		// cfg.Autodetect != nil → fall through to persistence / auto-detect
	}

	// load the setPwmToGetPwmMap from persistence, if it exists
	f.setPwmToGetPwmMap, err = f.persistence.LoadFanSetPwmToGetPwmMap(f.fan.GetId())
	if err == nil && f.setPwmToGetPwmMap != nil {
		ui.Info("FanController: Using saved value for setPwmToGetPwmMap of Fan '%s'", f.fan.GetId())
		return nil
	}

	err = f.computeSetPwmToGetPwmMapAutomatically()
	if err != nil {
		ui.Error("Fan %s: Error computing setPwmToGetPwmMap: %v", f.fan.GetId(), err)
		return err
	}

	if len(f.setPwmToGetPwmMap) <= 0 {
		ui.Warning("Fan '%s' setPwmToGetPwmMap is empty, ignoring", f.fan.GetId())
		return nil
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

	cfg := f.fan.GetConfig().PwmMap

	if cfg != nil {
		if cfg.Identity != nil {
			ui.Info("Fan %s: Using identity pwm map", f.fan.GetId())
			for i := 0; i < 256; i++ {
				f.pwmMapping[i] = i
			}
			return nil
		}
		if cfg.Values != nil {
			ui.Info("Fan %s: Using user-defined step pwm map", f.fan.GetId())
			pts := map[int]int(*cfg.Values)
			expanded, err := util.InterpolateStepInt(&pts, 0, 255)
			if err != nil {
				return fmt.Errorf("error expanding pwmMap (values): %w", err)
			}
			for i := 0; i < 256; i++ {
				f.pwmMapping[i] = expanded[i]
			}
			return nil
		}
		if cfg.Linear != nil {
			ui.Info("Fan %s: Using user-defined linear pwm map", f.fan.GetId())
			pts := map[int]int(*cfg.Linear)
			expanded, err := util.InterpolateLinearlyInt(&pts, 0, 255)
			if err != nil {
				return fmt.Errorf("error expanding pwmMap (linear): %w", err)
			}
			for i := 0; i < 256; i++ {
				f.pwmMapping[i] = expanded[i]
			}
			return nil
		}
		// cfg.Autodetect != nil → fall through to autodetect logic below
	}

	savedPwmMap, err := f.persistence.LoadFanPwmMap(f.fan.GetId())
	if err == nil && savedPwmMap != nil {
		ui.Info("FanController: Using saved value for pwm map of Fan '%s'", f.fan.GetId())
		for i := 0; i < 256; i++ {
			f.pwmMapping[i] = savedPwmMap[i]
		}
		return nil
	}

	{
		ui.Info("Computing pwm map...")
		err = f.computePwmMapAutomatically()
		if err != nil {
			ui.Error("Fan %s: Error computing pwm map: %v", f.fan.GetId(), err)
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
	if len(f.setPwmToGetPwmMap) == 0 {
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
		ui.Debug("Fan %s: Using setPwmToGetPwmMap to compute pwmMap", fan.GetId())
		keySet := util.SortedKeys(f.setPwmToGetPwmMap)
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
	sanitizedTarget := util.Coerce(target, 0, 255)
	return f.pwmMapping[sanitizedTarget]
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
		closestKey := util.FindClosest(pwmMappedValue, util.SortedKeys(f.setPwmToGetPwmMap))
		return f.setPwmToGetPwmMap[closestKey]
	} else {
		return value
	}
}

func (f *DefaultFanController) computeSetPwmToGetPwmMapAutomatically() error {
	if !f.fan.Supports(fans.FeaturePwmSensor) || f.assumePwmMapIdentity {
		if f.assumePwmMapIdentity {
			ui.Info("Fan %s: Automatic calculation of setPwmToGetPwmMap disabled. Assuming 1:1 relation.", f.fan.GetId())
		} else {
			ui.Warning("Fan '%s' does not support PWM sensor, cannot compute setPwmToGetPwmMap. Assuming 1:1 relation.", f.fan.GetId())
		}
		f.setPwmToGetPwmMap, _ = util.InterpolateLinearlyInt(&map[int]int{0: 0, 255: 255}, 0, 255)
		return nil
	}

	_ = trySetManualPwm(f.fan)

	setPwmToGetPwmMap := map[int]int{}
	for i := fans.MinPwmValue; i <= fans.MaxPwmValue; i++ {
		err := f.fan.SetPwm(i)
		if err != nil {
			ui.Warning("Error setting PWM value %d on fan %s: %v", i, f.fan.GetId(), err)
			continue
		}
		time.Sleep(f.getPwmSetDelay())
		pwm, err := f.fan.GetPwm()
		if err != nil {
			ui.Warning("Error reading PWM value of fan %s: %v", f.fan.GetId(), err)
			continue
		}
		setPwmToGetPwmMap[i] = pwm
	}

	if len(setPwmToGetPwmMap) > 0 {
		// we can only use the map if we have at least one entry
		f.setPwmToGetPwmMap = setPwmToGetPwmMap
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
