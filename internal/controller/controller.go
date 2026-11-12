package controller

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/markusressel/fan2go/internal/control_loop"
	"github.com/markusressel/fan2go/internal/controller/status"

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
	// ErrCurveEvaluation is returned (wrapped) by UpdateFanSpeed when the curve of the fan can't be evaluated.
	// The onSensorFailure action of the fan has been applied then, and the controller keeps running.
	ErrCurveEvaluation = errors.New("curve evaluation failed")
	// ErrPwmWriteFailed is returned (wrapped) by UpdateFanSpeed when setting the PWM value of the fan
	// kept failing for pwmWriteFailureTimeout. The controller stops then.
	ErrPwmWriteFailed = errors.New("setting the PWM value kept failing")
)

// initializationSequenceLock serializes the parts of the fan initialization that move the fans
// (the fan curve analysis and the setPwm -> getPwm detection) across all fan controllers,
// if runFanInitializationInParallel is false. A channel instead of a sync.Mutex, so waiting for it
// can be cancelled.
var initializationSequenceLock = make(chan struct{}, 1)

// initializationLockWaitHook is called (if set) when a controller has to wait for another fan
// to finish its initialization. Only used by tests.
var initializationLockWaitHook func(fanId string)

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

	// GetLastSetPwm returns the raw pwm value (after applying the pwmMap) that the controller
	// last set to the fan, and false if it hasn't set one yet. Safe to call while the controller is running.
	GetLastSetPwm() (int, bool)

	// GetRpmAvg returns the RPM moving average of the fan, and false until the controller's RPM monitor
	// has seeded it. Safe to call while the controller is running.
	GetRpmAvg() (float64, bool)

	// GetStatus returns the current state of the controller. Safe to call while the controller is running.
	GetStatus() status.Status

	UpdateFanSpeed() error

	// UpdateCurve dynamically updates the curve reference
	UpdateCurve(curve curves.SpeedCurve)

	// SetCurveCycles sets the notifications of the curve evaluator (see curves.CurveEvaluator.Subscribe)
	// that drive the update loop. Must be called before Run.
	SetCurveCycles(cycles <-chan struct{})

	// RunInitialization runs the fan initialization sequence.
	RunInitialization(ctx context.Context) (map[int]float64, error)

	// IsInitializing returns true while Run analyzes the fan (or waits to do so) because it has no usable saved
	// fan curve data or setPwmToGetPwmMap. Safe to call while the controller is running.
	IsInitializing() bool
}

type FanStateSnapshot struct {
	// the ControlMode the fan was in before the controller started,
	// nil if it is unknown (not readable, or reading it failed)
	ControlMode *fans.ControlMode
	// the raw pwm value read from the fan before the controller started,
	// nil if it is unknown (not readable, or reading it failed)
	// Note: this is the raw value, no pwmMap is applied to it
	PwmValue *int
	// the driver specific value behind ControlModeAutomatic (e.g. pwm_enable=5 of hwmon fans),
	// nil if unknown or not applicable (see fans.AutomaticControlModeKeeper)
	AutomaticControlModeValue *int
}

type DefaultFanController struct {
	// protects concurrent access to curve
	curveMutex sync.RWMutex
	// protects concurrent access to stats, which are written by the update loop
	// and read by the statistics collector (minPwmOffset is only used by the update loop)
	statsMutex sync.Mutex
	// controller statistics
	stats FanControllerStatistics
	// the raw pwm value (after applying the pwmMap) that setTargetPwm last applied successfully,
	// nil until then. Protected by statsMutex, read by the statistics collector.
	lastSetPwm *int
	// state of the controller ("" means StateStopped), the time it was entered and the error that caused it
	// (see setState and GetStatus). Protected by statsMutex, read by the statistics collector and the REST API.
	state      status.State
	stateSince time.Time
	stateError string
	// persistence where fan data is stored
	persistence persistence.Persistence
	// the fan to control
	fan fans.Fan
	// the curve used to control the fan. The controller only reads its result (see calculateTargetSpeed),
	// the curve evaluator evaluates it.
	curve curves.SpeedCurve
	// curveCycles receives a notification after each cycle of the curve evaluator. The update loop updates
	// the fan speed once per notification. If nil, the update loop uses its own ticker at updateRate instead
	// (and something else has to evaluate the curve).
	curveCycles <-chan struct{}
	// rate to update the target fan speed, if there are no curveCycles
	updateRate time.Duration
	// the fan state as it was before the controller started (nil until captured)
	originalFanState *FanStateSnapshot
	// restoreOnce makes Run restore the fan settings only once, no matter whether it stops normally,
	// because of an error or because of a panic (see restoreControlModeOnce). Replaced on every Run.
	restoreOnce *sync.Once
	// errorNotifier logs an error and notifies the user about it (nil means ui.ErrorAndNotify),
	// replaceable in tests
	errorNotifier func(title, format string, a ...any)
	// the last pwm value that was set to the fan, **before** applying the pwmMap to it.
	// This is always a target value (pre-pwmMap), never a raw value: only setTargetPwm writes it.
	// Apply the pwmMap (applyPwmMapToTarget) to get the raw value that was written to the fan.
	lastTarget *int
	// a list of all pre-pwmMap pwm values where pwmMap[x] != pwmMap[y] for the controlled fan
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
	// pwmMapComputed is true once pwmMapping has been computed (see computeFanSpecificMappings).
	// Before that, pwmMapping only contains zeros and must not be applied on restore.
	pwmMapComputed bool

	// holdsInitializationLock is true while this controller holds initializationSequenceLock,
	// so nested initialization steps don't try to acquire it again.
	// Only accessed by the goroutine that runs the initialization.
	holdsInitializationLock bool
	// initializing is true while Run runs the initialization sequence or the setPwm -> getPwm sweep
	// (see IsInitializing)
	initializing atomic.Bool
	// mayHaveWrittenToFan is set once the current Run (or initialization) may have written to the fan:
	// the initialization or the setPwm -> getPwm detection started, or Run switched the fan to manual mode.
	// Errors before that don't restore the fan settings, so a fan fan2go never wrote to isn't changed
	// (e.g. by an explicit controlMode.onExit speed). Only accessed by the goroutine that runs Run.
	mayHaveWrittenToFan bool
	// handbackPending is true if the fan may not be in its original state although the current Run didn't
	// write to it yet: the original state was loaded from the database (an earlier run didn't hand the fan
	// back), or the last restore was incomplete. Run then restores the fan also on early errors.
	handbackPending bool
	// originalFanStateSaved is true while the database (presumably) contains the original fan state of this
	// fan, saved by markMayWriteToFan or loaded by storeInitialFanState. Deleted after a complete restore.
	originalFanStateSaved bool
	// originalFanStateUnusable is true if the saved original fan state couldn't be loaded (e.g. the database
	// is locked): it may exist, so this controller neither overwrites nor deletes it. markMayWriteToFan
	// tries to load it again (once per Run).
	originalFanStateUnusable bool
	// originalFanStateSaveFailed is true if saving the original fan state failed in the current Run,
	// so it isn't tried (and logged) again until the next Run.
	originalFanStateSaveFailed bool

	// don't get and set PWMs in computeSetPwmToGetPwmMapAutomatically(), assume 1:1 mapping instead
	// (enabled by `fan2go fan --id <id> init --assume-pwm-map-identity`)
	assumePwmMapIdentity bool

	// control loop that specifies how the target value of the curve is approached
	controlLoop control_loop.ControlLoop

	// offset applied to the actual minPwm of the fan to ensure "neverStops" constraint
	minPwmOffset int
	// pwmFloorSetAt is the time of the first update of a fan that should never stop (which may have
	// to spin up from standstill), or of the last minPwmOffset increase because the fan stalled.
	// Stall detection gives the fan some time after that to react.
	// Like minPwmOffset, it is only used by the update loop.
	pwmFloorSetAt time.Time
	// rpmAvgSeeded is set by the RPM monitor once the RPM average of the fan has been seeded
	// with a successful reading. Until then the average is meaningless and stall detection is skipped.
	rpmAvgSeeded atomic.Bool

	// lastFanModeCheckTime is the last time we checked if some third party changed the fan control mode
	lastFanModeCheckTime time.Time

	// sensorFailureAction is the onSensorFailure action that is active because the curve can't be evaluated,
	// or "" while the fan is controlled normally. Only used by the update loop.
	sensorFailureAction configuration.OnSensorFailureAction
	// sensorFailureMaxWriteFailed is true once setting the max PWM failed in the current failure state
	// (only used by the update loop)
	sensorFailureMaxWriteFailed bool
	// pwmWriteFailures tracks consecutive failures to set the PWM value of the curve (only used by the update loop)
	pwmWriteFailures consecutiveFailures
	// thirdPartyPwmChanges tracks consecutive updates where the PWM value of the fan didn't match the
	// value fan2go set last (only used by the update loop)
	thirdPartyPwmChanges consecutiveFailures
	// rpmReadFailures tracks consecutive failures to read the RPM value of the fan (only used by the RPM monitor)
	rpmReadFailures consecutiveFailures
	// set once a missing setPwmToGetPwmMap (entry) has been logged, so it is logged only once per controller
	missingSetPwmToGetPwmMapLogged      atomic.Bool
	missingSetPwmToGetPwmMapEntryLogged atomic.Bool

	// clock is the time source for all waits, tickers and timestamps of the controller
	// (nil means util.RealClock), replaceable in tests via SetClock
	clock util.Clock
	// startupSensorWarmup is the time Run waits (in addition to one tempSensorPollingRate)
	// to gather monitoring data before it starts to control the fan
	startupSensorWarmup time.Duration
	// startupUpdateDelay is the time the update loop waits before the first fan speed update
	startupUpdateDelay time.Duration
}

const (
	defaultStartupSensorWarmup = 2 * time.Second
	defaultStartupUpdateDelay  = 1 * time.Second
	// minPwmIncreaseRpmPollingPeriods is the number of RPM polling periods a fan that should never stop
	// gets to react to an increased PWM value, before the stall detection increases it again
	// (or stops controlling the fan at maxPwm).
	minPwmIncreaseRpmPollingPeriods = 3
	// pwmWriteFailureTimeout is the time after which the update loop gives up, if setting the PWM value
	// of the fan fails on every update. The fan settings are restored then, and the controller stops.
	pwmWriteFailureTimeout = 10 * time.Second
	// pwmWriteFailureMinAttempts is the minimum number of failed attempts in a row before the update loop gives up
	pwmWriteFailureMinAttempts = 3
	// failureLogInterval: of a series of consecutive failures that are retried on every update
	// (e.g. PWM writes), only the first and every failureLogInterval-th one are logged
	failureLogInterval = 25
)

// consecutiveFailures tracks a series of consecutive failures of an operation that is retried on every
// update, so it can be logged rate-limited and escalated once it persists. Not safe for concurrent use.
type consecutiveFailures struct {
	// number of failures in a row, 0 after a success
	count int
	// time of the first failure of the current series
	since time.Time
}

// fail records a failure at now. It returns the number of failures in a row, the time since the first of them,
// and whether this failure should be logged (the first one, and every failureLogInterval-th one).
func (c *consecutiveFailures) fail(now time.Time) (count int, duration time.Duration, log bool) {
	if c.count == 0 {
		c.since = now
	}
	c.count++
	return c.count, now.Sub(c.since), c.count == 1 || c.count%failureLogInterval == 0
}

// succeed ends the current series of failures, and returns how many failures it had (0 if none),
// so a recovery can be logged.
func (c *consecutiveFailures) succeed() int {
	count := c.count
	c.count = 0
	c.since = time.Time{}
	return count
}

func (f *DefaultFanController) UpdateCurve(curve curves.SpeedCurve) {
	f.curveMutex.Lock()
	defer f.curveMutex.Unlock()
	f.curve = curve
}

func (f *DefaultFanController) getCurve() curves.SpeedCurve {
	f.curveMutex.RLock()
	defer f.curveMutex.RUnlock()
	return f.curve
}

func (f *DefaultFanController) SetCurveCycles(cycles <-chan struct{}) {
	f.curveCycles = cycles
}

func NewFanController(
	persistence persistence.Persistence,
	fan fans.Fan,
	curve curves.SpeedCurve,
	controlLoop control_loop.ControlLoop,
	updateRate time.Duration,
	assumePwmMapIdentity bool,
) *DefaultFanController {
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
		clock:                            util.RealClock,
		startupSensorWarmup:              defaultStartupSensorWarmup,
		startupUpdateDelay:               defaultStartupUpdateDelay,
	}
}

// SetClock replaces the time source used for all waits, tickers and timestamps of the controller.
// Intended for tests, which can then advance time deterministically instead of sleeping.
func (f *DefaultFanController) SetClock(clock util.Clock) {
	f.clock = clock
}

// getClock returns the clock of the controller, falling back to util.RealClock.
func (f *DefaultFanController) getClock() util.Clock {
	if f.clock == nil {
		return util.RealClock
	}
	return f.clock
}

func (f *DefaultFanController) GetFanId() string {
	return f.fan.GetId()
}

// GetStatistics returns a copy of the controller statistics.
// Safe to call while the controller is running.
func (f *DefaultFanController) GetStatistics() FanControllerStatistics {
	f.statsMutex.Lock()
	defer f.statsMutex.Unlock()
	return f.stats
}

// GetLastSetPwm returns the raw pwm value (after applying the pwmMap) that the controller
// last set to the fan, and false if it hasn't set one yet.
// Safe to call while the controller is running.
func (f *DefaultFanController) GetLastSetPwm() (int, bool) {
	f.statsMutex.Lock()
	defer f.statsMutex.Unlock()
	if f.lastSetPwm == nil {
		return 0, false
	}
	return *f.lastSetPwm, true
}

// GetRpmAvg returns the RPM moving average of the fan, and false until the RPM monitor has seeded it
// (it starts after the fan initialization). Safe to call while the controller is running.
func (f *DefaultFanController) GetRpmAvg() (float64, bool) {
	if !f.rpmAvgSeeded.Load() {
		return 0, false
	}
	return f.fan.GetRpmAvg(), true
}

func (f *DefaultFanController) recordLastSetPwm(pwm int) {
	f.statsMutex.Lock()
	defer f.statsMutex.Unlock()
	f.lastSetPwm = &pwm
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

// storeCurrentFanState captures the current fan control mode and PWM value,
// overwriting any previously saved snapshot in originalFanState.
func (f *DefaultFanController) storeCurrentFanState() error {
	fan := f.fan
	snapshot := FanStateSnapshot{}

	// store original pwm value, only if it is actually known
	pwm, err := f.getPwm()
	if err != nil {
		ui.Warning("Cannot read pwm value of %s, it will not be restored on exit: %v", fan.GetId(), err)
	} else {
		snapshot.PwmValue = &pwm
	}

	// store original pwm_enable value, only if it is actually known
	if f.fan.Supports(fans.FeatureControlModeRead) {
		controlMode, err := fan.GetControlMode()
		if err != nil {
			ui.Warning("Cannot read pwm_enable value of %s, it will not be restored on exit: %v", fan.GetId(), err)
		} else if controlMode < 0 {
			ui.Warning("Control mode of %s is unknown, it will not be restored on exit", fan.GetId())
		} else {
			snapshot.ControlMode = &controlMode
			if keeper, ok := fan.(fans.AutomaticControlModeKeeper); ok && controlMode == fans.ControlModeAutomatic {
				snapshot.AutomaticControlModeValue = keeper.GetLastKnownAutomaticControlMode()
			}
		}
	}
	f.originalFanState = &snapshot
	return nil
}

// storeInitialFanState stores the initial fan state snapshot if it hasn't been captured yet.
// Subsequent calls are no-ops to preserve the state before any controller actions.
// If the database contains a saved original state of the fan, that one wins (see useSavedOriginalFanState).
func (f *DefaultFanController) storeInitialFanState() error {
	if f.originalFanState != nil {
		return nil
	}
	if err := f.storeCurrentFanState(); err != nil {
		return err
	}
	f.useSavedOriginalFanState()
	return nil
}

// clearInitialFanState resets the captured initial fan state snapshot.
func (f *DefaultFanController) clearInitialFanState() {
	f.originalFanState = nil
}

// Run controls the fan until ctx is done or an error occurs, and restores the fan settings when it stops.
// A panic in Run or one of its goroutines is returned as an error, and also restores the fan settings
// if fan2go may have written to the fan already.
func (f *DefaultFanController) Run(ctx context.Context) (err error) {
	f.resetRuntimeState()
	// outside of resetRuntimeState: both lock statsMutex
	f.setState(status.StateInitializing, nil)
	// set once fan2go may write to the fan (initialization, set -> get PWM detection, control loop)
	fanMayBeTouched := false
	defer func() {
		panicked := false
		if r := recover(); r != nil {
			err = f.panicToError("fan controller", r)
			panicked = true
		}
		// after a panic, it's unclear what has been written to the fan already
		if (panicked && fanMayBeTouched) || (err != nil && f.mayHaveWrittenToFan) {
			// e.g. the initialization succeeded, but its result can't be used: it left the fan in manual mode.
			// A no-op if the fan settings have been restored already.
			f.restoreControlModeOnce()
		}
		f.setStoppedState(err)
	}()

	// prepare the controller by initializing persistence and checking the fan
	err = f.prepareController()
	if err != nil {
		return err
	}

	// store the current fan state to restore it when stopping the controller
	err = f.storeInitialFanState()
	if err != nil {
		return err
	}
	// the fan is not in its original state (e.g. an earlier run was killed): restore it also on early errors
	f.mayHaveWrittenToFan = f.mayHaveWrittenToFan || f.handbackPending

	fan := f.fan

	ui.Info("Gathering sensor data for %s...", fan.GetId())
	// wait a bit to gather monitoring data
	err = sleepWithContext(ctx, f.getClock(), f.startupSensorWarmup+configuration.CurrentConfig.TempSensorPollingRate)
	if err != nil {
		// cancelled before the fan was touched
		return err
	}

	fanMayBeTouched = true
	fanPwmData, err := f.runInitializationIfNeeded(ctx)
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

	err = f.computeFanSpecificMappings(ctx)
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

	f.markMayWriteToFan()
	err = takeManualControl(fan)
	if err != nil {
		// FanController can't do anything if manual fan control doesn't work
		return err
	}

	var g run.Group
	controllerCtx, cancelController := context.WithCancel(ctx)
	defer cancelController()

	if fan.Supports(fans.FeatureRpmSensor) {
		// === rpm monitoring
		pollingRate := configuration.CurrentConfig.RpmPollingRate

		g.Add(func() (err error) {
			// doesn't restore itself: the update loop may still be writing to the fan, it restores
			// the fan settings once it has stopped (the returned error stops it)
			defer func() {
				if r := recover(); r != nil {
					err = f.panicToError("RPM monitor", r)
				}
			}()
			tick := f.getClock().NewTicker(pollingRate)
			defer tick.Stop()
			f.rpmReadFailures.succeed()
			for {
				select {
				case <-controllerCtx.Done():
					ui.Info("Fan %s: Stopping RPM monitor of fan controller...", fan.GetId())
					return nil
				case <-tick.C():
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
		g.Add(func() (err error) {
			// the update loop is the only goroutine that writes to the fan, so it can restore right away
			defer func() {
				if r := recover(); r != nil {
					err = f.panicToError("update loop", r)
					f.restoreControlModeOnce()
				}
			}()
			if err := sleepWithContext(controllerCtx, f.getClock(), f.startupUpdateDelay); err != nil {
				ui.Info("Fan %s: Stopping fan controller...", fan.GetId())
				f.restoreControlModeOnce()
				return nil
			}
			// the update loop acts once per cycle of the curve evaluator, right after it evaluated the curves
			// (without an evaluator, on its own ticker)
			// (exactly one of cycles and tickC is set, receiving from the nil one blocks forever)
			cycles := f.curveCycles
			var tickC <-chan time.Time
			if cycles == nil {
				tick := f.getClock().NewTicker(f.updateRate)
				defer tick.Stop()
				tickC = tick.C()
			} else {
				// the first update below uses the latest result anyway
				select {
				case <-cycles:
				default:
				}
				// unless the evaluator hasn't evaluated the curve yet: wait for its first cycle then,
				// instead of treating that as a curve failure
				if errors.Is(f.getCurve().CurrentResult().Err, curves.ErrCurveNotEvaluated) {
					select {
					case <-controllerCtx.Done():
						ui.Info("Fan %s: Stopping fan controller...", fan.GetId())
						f.restoreControlModeOnce()
						return nil
					case <-cycles:
					}
				}
			}

			if err := f.UpdateFanSpeed(); err != nil && !errors.Is(err, ErrCurveEvaluation) {
				f.stopAfterUpdateError(err)
				return nil
			}

			for {
				select {
				case <-controllerCtx.Done():
					// doesn't depend on the curve evaluator: it may have stopped already
					ui.Info("Fan %s: Stopping fan controller...", fan.GetId())
					f.restoreControlModeOnce()
					return nil
				case <-cycles:
				case <-tickC:
				}
				// a curve error doesn't stop the controller: UpdateFanSpeed applied the onSensorFailure
				// action, and the next cycle tries again
				if err := f.UpdateFanSpeed(); err != nil && !errors.Is(err, ErrCurveEvaluation) {
					f.stopAfterUpdateError(err)
					return nil
				}
			}
		}, func(err error) {
			cancelController()
			if err != nil {
				ui.Error("Error in fan controller fan %s: %v", fan.GetId(), err)
			}
		})
	}

	err = g.Run()
	// the update loop restores the fan settings when it stops; this is a no-op then, but makes sure
	// they are restored on every exit path, now that no goroutine can write to the fan anymore
	f.restoreControlModeOnce()
	return err
}

// resetRuntimeState resets everything a previous Run learned or tracked about the fan to the state of a new
// controller, so Run starts like the first time when it is called again (e.g. after a failed config reload).
// It keeps what describes the fan and the database rather than a Run: originalFanState (the fan state before
// fan2go first took over the fan), handbackPending (the fan still has to be handed back), and
// originalFanStateSaved / originalFanStateUnusable (the saved original state in the database).
// Must not be called while Run is running.
func (f *DefaultFanController) resetRuntimeState() {
	f.restoreOnce = &sync.Once{}
	// an earlier run that didn't hand the fan back completely counts as a write: Run restores the fan also
	// on early errors
	f.mayHaveWrittenToFan = f.handbackPending
	f.originalFanStateSaveFailed = false
	f.lastTarget = nil
	f.pwmFloorSetAt = time.Time{}
	f.rpmAvgSeeded.Store(false)
	f.lastFanModeCheckTime = time.Unix(0, 0)
	f.sensorFailureAction = ""
	f.sensorFailureMaxWriteFailed = false
	f.pwmWriteFailures = consecutiveFailures{}
	f.thirdPartyPwmChanges = consecutiveFailures{}
	if f.controlLoop != nil {
		f.controlLoop.Reset()
	}

	f.statsMutex.Lock()
	defer f.statsMutex.Unlock()
	f.lastSetPwm = nil
	f.minPwmOffset = 0
	f.stats.MinPwmOffset = 0
}

// IsInitializing returns true while Run analyzes the fan or runs the setPwm -> getPwm sweep (or waits for
// the initialization lock to do so).
func (f *DefaultFanController) IsInitializing() bool {
	return f.initializing.Load()
}

func (f *DefaultFanController) runInitializationIfNeeded(ctx context.Context) (map[int]float64, error) {
	fan := f.fan
	// check if we have data for this fan in persistence,
	// if not we need to run the initialization sequence
	ui.Info("Fan %s: Loading fan curve data...", fan.GetId())
	fanRpmData, err := f.persistence.LoadFanRpmData(fan)
	if err == nil {
		return fanRpmData, nil
	}
	if !persistence.IsMissingOrCorrupt(err) {
		// the data may exist, but can't be read right now (e.g. the database is locked by another process):
		// don't re-run the whole initialization because of that
		return nil, fmt.Errorf("unable to load fan curve data of fan %s: %w", fan.GetId(), err)
	}

	config := fan.GetConfig()
	if config.HwMon != nil || config.Nvidia != nil {
		if errors.Is(err, persistence.ErrCorruptData) {
			ui.Warning("Saved data of fan '%s' is corrupt, starting initialization sequence...", fan.GetId())
		} else {
			ui.Warning("Fan '%s' has not yet been analyzed, starting initialization sequence...", fan.GetId())
		}
		// also cleared after a panic, so a config reload isn't deferred forever
		fanCurveData, err := func() (map[int]float64, error) {
			f.initializing.Store(true)
			defer f.initializing.Store(false)
			return f.RunInitialization(ctx)
		}()
		if err != nil {
			return nil, err
		}
		return fanCurveData, nil
	} else { // file/cmd fan
		if fan.GetFanRpmCurveData() != nil {
			err = f.persistence.SaveFanRpmData(fan)
		}
	}
	return fanRpmData, err
}

// RunInitialization runs the fan initialization sequence. If it fails, the fan settings are restored.
// If it succeeds, the fan is left in manual mode, at the last PWM value of the analysis.
// If runFanInitializationInParallel is false, it waits until no other fan is being initialized.
func (f *DefaultFanController) RunInitialization(ctx context.Context) (map[int]float64, error) {
	err := f.storeInitialFanState()
	if err != nil {
		return nil, err
	}
	f.mayHaveWrittenToFan = f.mayHaveWrittenToFan || f.handbackPending

	curveData, err := f.runInitializationSequentially(ctx)
	if err != nil {
		if f.mayHaveWrittenToFan {
			f.restoreControlModeOnce()
		}
		return nil, err
	}
	return curveData, nil
}

// RunInitializationAndRestore runs the fan initialization sequence (see RunInitialization) on its own,
// outside of Run (e.g. for `fan2go fan init`), and restores the fan settings afterwards, also if it panics
// (unless it failed before it wrote to the fan). A panic is returned as an error.
// Must not be called while Run is running on the same controller.
func (f *DefaultFanController) RunInitializationAndRestore(ctx context.Context) (curveData map[int]float64, err error) {
	f.restoreOnce = &sync.Once{}
	f.mayHaveWrittenToFan = f.handbackPending
	f.originalFanStateSaveFailed = false
	defer func() {
		if r := recover(); r != nil {
			curveData = nil
			err = f.panicToError("fan initialization", r)
		}
		if f.mayHaveWrittenToFan {
			f.restoreControlModeOnce()
		}
	}()
	return f.RunInitialization(ctx)
}

// runInitializationSequentially runs runInitialization, holding the initialization lock
// (see lockInitializationSequence).
func (f *DefaultFanController) runInitializationSequentially(ctx context.Context) (map[int]float64, error) {
	unlock, err := f.lockInitializationSequence(ctx)
	if err != nil {
		return nil, err
	}
	defer unlock()
	f.markMayWriteToFan()
	return f.runInitialization(ctx)
}

// lockInitializationSequence waits until no other fan controller runs a part of the initialization
// that moves the fan, if runFanInitializationInParallel is false, and returns the function to call
// when this controller is done. Returns ctx.Err() if ctx is done while waiting.
// Nested calls (e.g. the setPwm -> getPwm detection during the initialization) don't wait again.
func (f *DefaultFanController) lockInitializationSequence(ctx context.Context) (unlock func(), err error) {
	if configuration.CurrentConfig.RunFanInitializationInParallel || f.holdsInitializationLock {
		return func() {}, nil
	}

	select {
	case initializationSequenceLock <- struct{}{}:
	default:
		ui.Info("Fan %s: Waiting for the initialization of another fan to finish...", f.fan.GetId())
		if hook := initializationLockWaitHook; hook != nil {
			hook(f.fan.GetId())
		}
		select {
		case initializationSequenceLock <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	f.holdsInitializationLock = true
	return func() {
		f.holdsInitializationLock = false
		<-initializationSequenceLock
	}, nil
}

func (f *DefaultFanController) runInitialization(ctx context.Context) (map[int]float64, error) {
	fan := f.fan

	err := f.computeFanSpecificMappings(ctx)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			ui.Error("Fan %s: Error computing fan specific mappings: %v", fan.GetId(), err)
		}
		return nil, err
	}

	fanAnalyzer := NewFanCurveAnalyzer(f)
	curveData, err := fanAnalyzer.RunInitializationSequence(ctx)
	if err != nil {
		return nil, err
	}

	rawStartPwm, rawMaxPwm := fans.ComputePwmBoundariesFromCurveData(curveData, fans.MaxPwmValue)
	ui.Info("Fan %s: Analysis boundaries detected (before config overrides): Start %d, Max %d", fan.GetId(), rawStartPwm, rawMaxPwm)

	err = fan.AttachFanRpmCurveData(&curveData)
	if err != nil {
		ui.Error("Fan %s: Failed to attach fan curve data: %v", fan.GetId(), err)
		return nil, err
	}

	// save to database to restore it on restarts
	err = f.persistence.SaveFanRpmData(fan)
	if err != nil {
		ui.Error("Fan %s: Failed to save RPM data: %v", fan.GetId(), err)
	}

	fanRpmData, err := f.persistence.LoadFanRpmData(fan)
	if err != nil {
		return nil, err
	}

	return fanRpmData, nil
}

// UpdateFanSpeed updates the fan speed by:
// - calculating the target PWM value using the control loop and fan curve
// - applying clamping
// - mapping the resulting target value to the [minPwm, maxPwm] range of the fan
// - applying sanity checks to ensure the fan never stops (if specified)
//
// If the curve can't be evaluated, the onSensorFailure action of the fan is applied instead
// (see handleCurveFailure), and an error wrapping ErrCurveEvaluation is returned.
// Once the curve can be evaluated again, normal control resumes.
//
// returns ErrFanStalledAtMaxPwm if no rpm is detected even at fan.maxPwm
func (f *DefaultFanController) UpdateFanSpeed() error {
	fan := f.fan

	// calculate the direct optimal target speed
	target, err := f.calculateTargetSpeed()
	if err != nil {
		f.handleCurveFailure(err)
		return fmt.Errorf("%w: %w", ErrCurveEvaluation, err)
	}
	if !f.recoverFromCurveFailure() {
		return nil
	}
	// only now, after the first update that could evaluate the curve: a controller whose curve fails when
	// Run starts (e.g. again after a failed reload) goes from initializing straight to (or back to) its
	// sensor_failure_* state on its first update, and never reports running in between
	f.setState(status.StateRunning, nil)

	f.ensureNoThirdPartyIsMessingWithUs()

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
		if shouldNeverStop && f.pwmFloorSetAt.IsZero() {
			// the fan may be at standstill before the first update, give it time to spin up
			f.pwmFloorSetAt = f.getClock().Now()
		}
		if f.lastTarget != nil {
			lastTarget := *f.lastTarget
			lastSetTargetEqualsNewTarget := lastTarget == speedTarget
			// skip until the RPM monitor seeded the average, which is meaningless before that
			if shouldNeverStop && lastSetTargetEqualsNewTarget && f.rpmAvgSeeded.Load() {
				avgRpm := fan.GetRpmAvg()
				now := f.getClock().Now()
				// give the fan time to react to its first PWM value or the last increase before reacting (again)
				if !fans.IsRpmLikelySpinning(avgRpm) && f.hadTimeToReactToPwmFloor(now) {
					if speedTarget >= maxPwm {
						ui.Error("CRITICAL: Fan %s avg. RPM is %d, even at PWM value %d", fan.GetId(), int(avgRpm), lastTarget)
						ui.Warning("Fan %s: its RPM sensor reports (almost) 0 RPM. If the fan is actually spinning, "+
							"check its tach signal (wiring, sensor) or disable neverStop for this fan.", fan.GetId())
						return ErrFanStalledAtMaxPwm
					}
					ui.Warning("Increasing minPWM of %s from %d to %d, which is supposed to never stop, but RPM is %d at PWM %d",
						fan.GetId(), minPwm+f.minPwmOffset, minPwm+f.minPwmOffset+1, int(avgRpm), lastTarget)
					f.increaseMinPwmOffset()
					f.pwmFloorSetAt = now
					speedTarget++
				}
			}
		}
	}

	return f.handlePwmWriteResult(f.setTargetPwm(speedTarget))
}

// handlePwmWriteResult tracks the result (err) of setting the PWM value of the curve. Failures are logged
// rate-limited (see consecutiveFailures), and so is the recovery. If setting the PWM value keeps failing
// for pwmWriteFailureTimeout, an error wrapping ErrPwmWriteFailed is returned, which stops the controller.
func (f *DefaultFanController) handlePwmWriteResult(err error) error {
	fanId := f.fan.GetId()
	if err == nil {
		if failures := f.pwmWriteFailures.succeed(); failures > 0 {
			ui.Info("Fan %s: Setting the PWM value works again, after %d failed attempts", fanId, failures)
		}
		return nil
	}

	count, duration, log := f.pwmWriteFailures.fail(f.getClock().Now())
	// a minimum number of attempts, so a single write that blocks for a long time doesn't stop the controller
	if duration >= pwmWriteFailureTimeout && count >= pwmWriteFailureMinAttempts {
		return fmt.Errorf("%w for %s (%d attempts in a row, last error: %w). fan2go stopped controlling this fan "+
			"and restored its settings as configured in controlMode.onExit (if possible). Possible causes: the driver "+
			"was unloaded, missing permissions, or the device is gone. Restart fan2go or reload its config to control "+
			"the fan again",
			ErrPwmWriteFailed, duration.Round(time.Millisecond), count, err)
	}
	if log {
		ui.Error("Fan %s: Error setting PWM value (failed attempts in a row: %d; only the first and every %dth failure "+
			"are logged; fan2go stops controlling the fan if it keeps failing for %s): %v",
			fanId, count, failureLogInterval, pwmWriteFailureTimeout, err)
	}
	return nil
}

// hadTimeToReactToPwmFloor returns true if the fan had minPwmIncreaseRpmPollingPeriods RPM polling periods
// to react to the first PWM value set by the controller, or to the last minPwm increase.
func (f *DefaultFanController) hadTimeToReactToPwmFloor(now time.Time) bool {
	interval := minPwmIncreaseRpmPollingPeriods * configuration.CurrentConfig.RpmPollingRate
	return now.Sub(f.pwmFloorSetAt) >= interval
}

// read the current value of a fan RPM sensor and update its moving average with it.
// The first successful reading replaces the average, a failed reading leaves it unchanged.
// Of a series of failed readings, only the first one is logged, and the recovery.
func (f *DefaultFanController) measureRpm(fan fans.Fan) {
	rpm, err := fan.GetRpm()
	if err != nil {
		if count, _, _ := f.rpmReadFailures.fail(f.getClock().Now()); count == 1 {
			ui.Warning("Error reading RPM value of fan %s (further errors are not logged until it recovers): %v", fan.GetId(), err)
		}
		return
	}
	if failures := f.rpmReadFailures.succeed(); failures > 0 {
		ui.Info("Fan %s: reading the RPM value succeeded again after %d failed readings", fan.GetId(), failures)
	}

	if !f.rpmAvgSeeded.Load() {
		// the RPM monitor is the only writer of the average, so this can't race with another update
		fan.SetRpmAvg(float64(rpm))
		f.rpmAvgSeeded.Store(true)
		return
	}

	updatedRpmAvg := util.UpdateExponentialMovingAvg(fan.GetRpmAvg(), configuration.CurrentConfig.RpmRollingWindowSize, float64(rpm))
	fan.SetRpmAvg(updatedRpmAvg)
}

// getPwm returns the current raw PWM value of the fan.
// If the fan does not support PWM sensor reading,
// it returns the raw value of the last target set by fan2go.
// If no target has been set yet, the PWM value is unknown and an error is returned.
func (f *DefaultFanController) getPwm() (int, error) {
	if f.fan.Supports(fans.FeaturePwmSensor) {
		return f.fan.GetPwm()
	} else if f.lastTarget != nil {
		return f.applyPwmMapToTarget(*f.lastTarget), nil
	}

	return 0, fmt.Errorf("fan %s: PWM value is unknown, the fan can't report it and fan2go hasn't set one yet", f.fan.GetId())
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

// takeManualControl switches the fan to manual control (see trySetManualPwm).
// An error of trySetManualPwm is ignored if the fan is in PWM mode anyway.
func takeManualControl(fan fans.Fan) error {
	// TODO: check if fan.Supports(fans.FeatureControlModeWrite) - or is it ok if it doesn't and our
	//       default assumption is that it will always be in manual mode then?
	//       (trySetManualPwm() just returns nil in that case)
	//       Alternatively, should fans that don't support switching the mode but run in manual mode
	//       by default (cmd or file fans?) just always return ControlModePWM in fan.GetControlMode()
	//       so we can check that?
	err := trySetManualPwm(fan)
	if err != nil {
		cm, e := fan.GetControlMode()
		// if the control mode is PWM even though trySetManualPwm() failed, ignore that error
		if e != nil || cm != fans.ControlModePWM {
			return err
		}
	}
	return nil
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

// stopAfterUpdateError handles an error of UpdateFanSpeed, which stops the update loop:
// it restores the fan settings first, and only then notifies the user, so a slow or hanging
// notification can't delay the restore.
func (f *DefaultFanController) stopAfterUpdateError(err error) {
	f.restoreControlModeOnce()
	f.setState(stoppedStateForError(err), err)
	notify := f.errorNotifier
	if notify == nil {
		notify = ui.ErrorAndNotify
	}
	notify("Fan Control Error", "Fan %s: %v", f.fan.GetId(), err)
}

// handleCurveFailure applies the onSensorFailure action of the fan, because its curve can't be evaluated
// (err). It logs and notifies only when the fan enters the failure state, not on every tick.
//   - max (default): drive the fan to its maxPwm, on every tick while the failure lasts.
//   - restore: hand the fan back like on exit (restoreControlMode), once. If that doesn't hand the fan over
//     to a control mode other than manual PWM (e.g. controlMode.onExit: none, the original mode was manual,
//     the control mode can't be written or is unknown), fall back to max: a fixed PWM value is not fail-safe.
func (f *DefaultFanController) handleCurveFailure(err error) {
	fan := f.fan
	if f.sensorFailureAction == configuration.OnSensorFailureMax {
		// keep the fan at max while the failure lasts (e.g. if a third party changed it)
		f.setMaxPwmForSensorFailure()
		// keeps the state accurate on every update while the failure lasts
		f.setState(status.StateSensorFailureMax, err)
		return
	}
	if f.sensorFailureAction != "" {
		// already restored, leave the fan alone until the curve can be evaluated again
		f.setState(status.StateSensorFailureRestored, err)
		return
	}

	action := fan.GetConfig().GetOnSensorFailure()
	var description string
	switch action {
	case configuration.OnSensorFailureRestore:
		if f.restoreControlModeOnceForSensorFailure() {
			f.sensorFailureAction = configuration.OnSensorFailureRestore
			// fan2go didn't set the current PWM value, so don't compare it with the last target after the recovery
			f.lastTarget = nil
			f.thirdPartyPwmChanges.succeed()
			description = "handed the fan back (onSensorFailure: restore)"
		} else {
			// fan2go still controls the fan, so it must be restored when the controller stops
			f.restoreOnce = &sync.Once{}
			f.sensorFailureAction = configuration.OnSensorFailureMax
			f.setMaxPwmForSensorFailure()
			description = "restoring didn't hand the fan over to another control mode, set it to its max PWM instead (onSensorFailure: restore)"
		}
	default:
		f.sensorFailureAction = configuration.OnSensorFailureMax
		f.setMaxPwmForSensorFailure()
		description = "set it to its max PWM (onSensorFailure: max)"
	}

	if f.sensorFailureAction == configuration.OnSensorFailureRestore {
		f.setState(status.StateSensorFailureRestored, err)
	} else {
		f.setState(status.StateSensorFailureMax, err)
	}

	notify := f.errorNotifier
	if notify == nil {
		notify = ui.ErrorAndNotify
	}
	// notify only after the fan was set to a safe state
	notify("Sensor Failure", "Fan %s: curve can't be evaluated, %s until it can be evaluated again: %v", fan.GetId(), description, err)
}

// setMaxPwmForSensorFailure sets the fan to its maxPwm, the fail-safe speed while its curve can't be evaluated.
// A failed write is logged once per failure state, not on every tick.
func (f *DefaultFanController) setMaxPwmForSensorFailure() {
	err := f.setTargetPwm(f.fan.GetMaxPwm())
	if err != nil && !f.sensorFailureMaxWriteFailed {
		ui.Error("Fan %s: Error setting max PWM (further errors are not logged until the curve recovers): %v", f.fan.GetId(), err)
	}
	f.sensorFailureMaxWriteFailed = err != nil || f.sensorFailureMaxWriteFailed
}

// recoverFromCurveFailure leaves the failure state (see handleCurveFailure) after the curve
// could be evaluated again. If the fan was handed back, manual control is taken again first, like at startup.
// It returns false if the fan stays in the failure state, because manual control can't be taken again.
func (f *DefaultFanController) recoverFromCurveFailure() bool {
	switch f.sensorFailureAction {
	case "":
		return true
	case configuration.OnSensorFailureRestore:
		// the handback deleted the saved original state
		f.markMayWriteToFan()
		if err := takeManualControl(f.fan); err != nil {
			ui.Debug("Fan %s: Can't take manual control again: %v", f.fan.GetId(), err)
			f.setState(status.StateSensorFailureRestored, fmt.Errorf("the curve can be evaluated again, but manual control can't be taken again: %w", err))
			return false
		}
		// fan2go controls the fan again, so it must be restored when the controller stops
		f.restoreOnce = &sync.Once{}
	}
	f.sensorFailureAction = ""
	f.sensorFailureMaxWriteFailed = false
	// PWM write failures before the failure state don't count towards pwmWriteFailureTimeout anymore
	f.pwmWriteFailures.succeed()
	ui.Info("Fan %s: Curve can be evaluated again, resuming normal control", f.fan.GetId())
	return true
}

// restoreControlModeOnce restores the fan settings (see restoreControlMode), unless the current Run
// already did. Concurrent callers wait until the restore has finished.
// A panic during the restore is logged, so it can't skip the restore of other fans.
func (f *DefaultFanController) restoreControlModeOnce() {
	f.restoreControlModeOnceAndForget(false)
}

// restoreControlModeOnceForSensorFailure is restoreControlModeOnce, and returns true if this call restored
// the fan settings and handed the fan over to a control mode other than manual PWM (see restoreControlMode).
// After a successful restore, the update loop arms a new restoreOnce once it controls the fan again.
func (f *DefaultFanController) restoreControlModeOnceForSensorFailure() (restored bool) {
	return f.restoreControlModeOnceAndForget(true)
}

// restoreControlModeOnceAndForget restores the fan settings once (see restoreControlModeOnce), and deletes
// the saved original fan state if the restore was complete. For a sensor failure (forSensorFailure), only if
// the fan was handed over, too: otherwise fan2go keeps controlling it (at max speed).
// Returns whether the fan was handed over (see restoreControlMode).
func (f *DefaultFanController) restoreControlModeOnceAndForget(forSensorFailure bool) (restored bool) {
	once := f.restoreOnce
	if once == nil {
		// not called from Run
		once = &sync.Once{}
	}
	once.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				ui.Error("Fan %s: Panic while restoring the fan settings: %v\n%s", f.fan.GetId(), r, debug.Stack())
			}
		}()
		// stays pending if the restore panics
		f.handbackPending = true
		handedOver, complete := f.restoreControlMode()
		restored = handedOver
		f.handbackPending = !complete
		if complete && (handedOver || !forSensorFailure) {
			f.forgetOriginalFanState()
		}
	})
	return restored
}

// panicToError logs a panic r recovered in Run or one of its goroutines (named by where)
// together with the stack trace, and returns it as an error.
func (f *DefaultFanController) panicToError(where string, r any) error {
	ui.Error("Fan %s: Panic in %s: %v\n%s", f.fan.GetId(), where, r, debug.Stack())
	return fmt.Errorf("panic in %s of fan %s: %v", where, f.fan.GetId(), r)
}

// restoreControlMode is the single place that resets the fan when the controller stops
// (exit, config reload, or an error). What it does depends on controlMode.onExit:
//   - none: nothing.
//   - restore (default): restore the original control mode and PWM value.
//   - mode and/or speed: set the given control mode and/or speed (pre-pwmMap).
//
// Only values that are actually known are written: if the original control mode or PWM value
// could not be read when the controller started, it is not written, and the fan stays at
// the last speed fan2go set.
// Exception: if the original control mode is unknown on restore (default), but the fan supports
// writing it, the fan is handed back to automatic control (see restoreUnknownControlMode).
//
// handedOver is true if the fan was handed over to a control mode other than manual PWM (e.g. automatic),
// or set to max speed because its control mode couldn't be restored. Otherwise, the fan is left at a fixed
// PWM value (or wasn't changed at all).
// complete is true if the restore did what controlMode.onExit asks for, as far as the original state is
// known: no write failed, and it didn't fall back to max speed.
func (f *DefaultFanController) restoreControlMode() (handedOver bool, complete bool) {
	if f.originalFanState == nil {
		ui.Warning("Skipping fan settings restore for %s, original state was never captured", f.fan.GetId())
		return false, false
	}
	originalControlMode := f.originalFanState.ControlMode
	originalPwmValue := f.originalFanState.PwmValue

	ui.Info("Trying to restore fan settings for %s...", f.fan.GetId())

	var onExit *configuration.OnExitConfig
	if cfg := f.fan.GetConfig().ControlMode; cfg != nil {
		onExit = cfg.OnExit
	}

	// none: skip all restore actions
	if onExit != nil && onExit.None != nil {
		ui.Info("Skipping fan restore for %s (controlMode.onExit: none)", f.fan.GetId())
		return false, true
	}

	// restore (default), but the original control mode is unknown
	restoreOriginal := onExit == nil || onExit.Restore != nil
	if restoreOriginal && originalControlMode == nil && f.fan.Supports(fans.FeatureControlModeWrite) {
		return f.restoreUnknownControlMode(originalPwmValue)
	}

	var controlModeToSet *fans.ControlMode = nil
	var pwmToSet *int = nil
	complete = true

	// controlMode and/or speed: set explicit values on exit
	if onExit != nil {
		// determine control mode to set on exit, if any
		if onExit.Restore != nil {
			controlModeToSet = originalControlMode
		} else if onExit.ControlMode != nil {
			parsedControlMode, err := parseControlModeValue(*onExit.ControlMode)
			if err != nil {
				ui.Warning("Fan %s: Error parsing controlMode.onExit.controlMode: %v", f.fan.GetId(), err)
				complete = false
			} else {
				controlModeToSet = &parsedControlMode
			}
		} else {
			// if no explicit control mode to set is provided, but the original mode is known and was not automatic, restore the original mode
			if originalControlMode != nil && *originalControlMode != fans.ControlModeAutomatic {
				controlModeToSet = originalControlMode
			}
		}

		// determine PWM value to set on exit, if any
		if onExit.Speed != nil {
			// speed is a target value (pre-pwmMap), like the values fan2go sets while running
			raw := f.applyPwmMapForRestore(*onExit.Speed)
			pwmToSet = &raw
		}
	} else {
		// default restore behavior
		controlModeToSet = originalControlMode
	}

	if pwmToSet == nil {
		// unless the fan is handed back to automatic control, restore the original PWM value (if known)
		if controlModeToSet == nil || *controlModeToSet != fans.ControlModeAutomatic {
			if originalPwmValue != nil {
				pwmToSet = originalPwmValue
			} else {
				ui.Info("Original PWM value of fan %s is unknown, leaving it at the last speed set by fan2go", f.fan.GetId())
			}
		}
	}

	if controlModeToSet != nil {
		if f.fan.Supports(fans.FeatureControlModeWrite) {
			if err := f.fan.SetControlMode(*controlModeToSet); err != nil {
				// if this fails, try to set it to max speed instead
				ui.Warning("Fan %s: Error restoring control mode %d, setting max speed instead: %v", f.fan.GetId(), *controlModeToSet, err)
				if err := f.fan.SetPwm(f.applyPwmMapForRestore(fans.MaxPwmValue)); err != nil {
					ui.Warning("Unable to restore fan %s, make sure it is running!", f.fan.GetId())
					return false, false
				}
				return true, false
			}
			handedOver = *controlModeToSet != fans.ControlModePWM
		} else {
			ui.Warning("Cannot restore control mode of fan %s, writing control mode is not supported", f.fan.GetId())
		}
	}
	if pwmToSet != nil {
		// restore (default: onExit == nil or onExit.Restore != nil)
		if err := f.fan.SetPwm(*pwmToSet); err != nil {
			ui.Warning("Fan %s: Error restoring original PWM value: %v", f.fan.GetId(), err)
			complete = false
		}
	}
	return handedOver, complete
}

// restoreUnknownControlMode restores a fan that supports writing its control mode, but whose
// original control mode is unknown: it hands the fan back to automatic control. Only if that fails,
// it falls back to manual control at the original PWM value, if that is known.
// Returns true (handedOver and complete) if the fan was handed back to automatic control.
// The fallback to manual control doesn't count as complete.
func (f *DefaultFanController) restoreUnknownControlMode(originalPwmValue *int) (handedOver bool, complete bool) {
	err := f.fan.SetControlMode(fans.ControlModeAutomatic)
	if err == nil {
		// don't write a PWM value: on some drivers that switches the fan back to manual control
		ui.Info("Original control mode of fan %s is unknown, handed it back to automatic control", f.fan.GetId())
		return true, true
	}
	ui.Warning("Fan %s: Original control mode is unknown and automatic control could not be set: %v", f.fan.GetId(), err)

	if originalPwmValue == nil {
		ui.Warning("Original PWM value of fan %s is unknown, leaving it at the last speed set by fan2go", f.fan.GetId())
		return false, false
	}
	if err := f.fan.SetControlMode(fans.ControlModePWM); err != nil {
		ui.Warning("Fan %s: Error setting manual control mode: %v", f.fan.GetId(), err)
	}
	if err := f.fan.SetPwm(*originalPwmValue); err != nil {
		ui.Warning("Fan %s: Error restoring original PWM value: %v", f.fan.GetId(), err)
	}
	return false, false
}

// applyPwmMapForRestore maps a target value (pre-pwmMap) to the raw value to write on restore.
// If the pwmMap hasn't been computed yet (e.g. when the controller stops during initialization),
// the target is written as is.
func (f *DefaultFanController) applyPwmMapForRestore(target int) int {
	if !f.pwmMapComputed {
		ui.Warning("Fan %s: pwmMap is not known yet, writing %d without mapping it", f.fan.GetId(), target)
		return util.Coerce(target, fans.MinPwmValue, fans.MaxPwmValue)
	}
	return f.applyPwmMapToTarget(target)
}

// Calculates the next speed for the fan of this controller by
//   - reading the result of the associated curve from the last cycle of the curve evaluator
//     (the controller never evaluates the curve itself, since other fans and curves may use it too)
//   - cycling the control loop
//
// If the curve can be evaluated again after a failure (see handleCurveFailure), the control loop
// continues from the speed the fan has now, instead of from its output before the failure.
func (f *DefaultFanController) calculateTargetSpeed() (float64, error) {
	result := f.getCurve().CurrentResult()
	if result.Err != nil {
		return 0, result.Err
	}

	// this runs before recoverFromCurveFailure, which leaves the failure state
	switch f.sensorFailureAction {
	case configuration.OnSensorFailureMax:
		// the fan was set to maxPwm without the control loop, so ramp down from there
		f.controlLoop.Preset(f.maxPwmCurveValue())
	case configuration.OnSensorFailureRestore:
		// the speed the fan was handed back at is unknown, start at the target like on startup
		f.controlLoop.Reset()
	}

	// the new target speed to set, which approaches the actual target based on the control loop
	newTarget := f.controlLoop.Cycle(f.getClock().Now(), result.Value)

	return newTarget, nil
}

// maxPwmCurveValue returns the curve value that UpdateFanSpeed maps to the maxPwm of the fan:
// 255, or maxPwm itself if the fan uses unscaled curve values.
func (f *DefaultFanController) maxPwmCurveValue() float64 {
	if f.fan.GetConfig().UseUnscaledCurveValues {
		return float64(f.fan.GetMaxPwm())
	}
	return fans.MaxPwmValue
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
	pwmValueChangedByThirdPartyCheckConfig := sanityCheckConfig.PwmValueChangedByThirdParty
	if !pwmValueChangedByThirdPartyCheckConfig.Enabled.Get() {
		// sanity checks are disabled, so we don't check for third party changes
		return
	}

	if !f.fan.Supports(fans.FeaturePwmSensor) {
		// we cannot read the PWM value, so we also cannot check if third party changed the PWM value
		ui.Warning("Fan %s does not support PWM sensor reading, disabling 'PwmValueChangedByThirdParty' sanity check", f.fan.GetId())
		f.fan.DisableSanityCheck(fans.SanityCheckPwmValueChangedByThirdParty)
		return
	}

	if f.lastTarget != nil {
		pwmMappedValue := f.applyPwmMapToTarget(*f.lastTarget)
		expectedReportedPwm := f.getReportedPwmAfterApplyingPwm(pwmMappedValue)
		if currentPwm, err := f.fan.GetPwm(); err == nil {
			if currentPwm != expectedReportedPwm {
				// the statistics count every change, the warning is rate-limited
				f.statsMutex.Lock()
				f.stats.UnexpectedPwmValueCount += 1
				f.statsMutex.Unlock()
				count, _, log := f.thirdPartyPwmChanges.fail(f.getClock().Now())
				if log {
					ui.Warning("PWM of %s was changed by third party! Last set PWM value was '%d', expected reported pwm '%d' but was '%d' "+
						"(changes in a row: %d; only the first and every %dth change are logged)",
						f.fan.GetId(), pwmMappedValue, expectedReportedPwm, currentPwm, count, failureLogInterval)
				}
			} else if count := f.thirdPartyPwmChanges.succeed(); count > 0 {
				ui.Info("PWM of %s matches the value set by fan2go again, after %d unexpected values in a row", f.fan.GetId(), count)
			}
		}
	}
}

// setTargetPwm applies the given target speed in [0..255] to the fan which is controlled
// in this FanController. Since the fan might not support the range of [0..255]
// the target value is mapped to a pwm value in the supported range using the pwmMap.
// Always pass a target (pre-pwmMap) value, never a value that was already mapped
// with applyPwmMapToTarget, otherwise the pwmMap is applied twice.
func (f *DefaultFanController) setTargetPwm(target int) (err error) {
	pwmMappedValue := f.applyPwmMapToTarget(target)
	expectedReportedPwmValue := f.getReportedPwmAfterApplyingPwm(pwmMappedValue)
	// setting pwmMappedValue will yield expectedReportedPwmValue when reading back the pwm value

	f.ensureFanModeIsSetToExpectedMode()

	ui.Debug("Setting target PWM of %s to %d, applying PWM Map yields %d, expected reported pwm is %d", f.fan.GetId(), target, pwmMappedValue, expectedReportedPwmValue)
	// if we can read the PWM value, we can check if the fan is already at the target value
	// and avoid unnecessary setPwm calls
	if f.fan.Supports(fans.FeaturePwmSensor) {
		current, err := f.getPwm()
		if err == nil && expectedReportedPwmValue == current {
			// nothing to do
			f.lastTarget = &target
			f.recordLastSetPwm(pwmMappedValue)
			return nil
		}
	}
	err = f.fan.SetPwm(pwmMappedValue)
	if err == nil {
		// only after a successful write: lastTarget is compared with the PWM value of the fan
		f.lastTarget = &target
		f.recordLastSetPwm(pwmMappedValue)
	}
	return err
}

// ensureFanModeIsSetToExpectedMode makes sure that the fan control mode is set to the expected mode (manual PWM control),
// by checking periodically.
func (f *DefaultFanController) ensureFanModeIsSetToExpectedMode() {
	sanityCheckConfig := f.fan.GetConfig().SanityCheck.FanModeChangedByThirdParty

	if !sanityCheckConfig.Enabled.Get() {
		// sanity check is disabled
		return
	}

	if !f.fan.Supports(fans.FeatureControlModeRead) {
		ui.Warning("Fan %s does not support control mode reading, disabling 'FanModeChangedByThirdParty' sanity check", f.fan.GetId())
		f.fan.DisableSanityCheck(fans.SanityCheckFanModeChangedByThirdParty)
		return
	}

	// make sure we don't check this too often
	now := f.getClock().Now()
	if f.lastFanModeCheckTime.Add(sanityCheckConfig.ThrottleDuration).After(now) {
		return
	}
	f.lastFanModeCheckTime = now

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

// computeSetPwmToGetPwmMap computes a mapping between "set pwm value" -> "actual pwm value"
func (f *DefaultFanController) computeSetPwmToGetPwmMap(ctx context.Context) (err error) {
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
	if err != nil && !persistence.IsMissingOrCorrupt(err) {
		// the data may exist, but can't be read right now: don't sweep the fan because of that
		return fmt.Errorf("unable to load setPwmToGetPwmMap of fan %s: %w", f.fan.GetId(), err)
	}

	err = f.computeSetPwmToGetPwmMapAutomatically(ctx)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			ui.Error("Fan %s: Error computing setPwmToGetPwmMap: %v", f.fan.GetId(), err)
		}
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
	if err != nil && !persistence.IsMissingOrCorrupt(err) {
		return fmt.Errorf("unable to load pwm map of fan %s: %w", f.fan.GetId(), err)
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
	f.statsMutex.Lock()
	defer f.statsMutex.Unlock()
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
		if f.missingSetPwmToGetPwmMapLogged.CompareAndSwap(false, true) {
			ui.Warning("Fan '%s' does not have a setPwmToGetPwmMap, assuming 1:1 relation (logged only once).", f.fan.GetId())
		}
		return pwmMappedValue
	}
	if value, ok := f.setPwmToGetPwmMap[pwmMappedValue]; !ok {
		closestKey := util.FindClosest(pwmMappedValue, util.SortedKeys(f.setPwmToGetPwmMap))
		if f.missingSetPwmToGetPwmMapEntryLogged.CompareAndSwap(false, true) {
			ui.Warning("Fan '%s' does not have a setPwmToGetPwmMap entry for %d, using the entry of the closest value %d instead "+
				"(logged only once, for the first missing entry).", f.fan.GetId(), pwmMappedValue, closestKey)
		} else {
			ui.Debug("Fan '%s' does not have a setPwmToGetPwmMap entry for %d, using the entry of the closest value %d instead.",
				f.fan.GetId(), pwmMappedValue, closestKey)
		}
		return f.setPwmToGetPwmMap[closestKey]
	} else {
		return value
	}
}

func (f *DefaultFanController) computeSetPwmToGetPwmMapAutomatically(ctx context.Context) error {
	if !f.fan.Supports(fans.FeaturePwmSensor) || f.assumePwmMapIdentity {
		if f.assumePwmMapIdentity {
			ui.Info("Fan %s: Automatic calculation of setPwmToGetPwmMap disabled. Assuming 1:1 relation.", f.fan.GetId())
		} else {
			ui.Warning("Fan '%s' does not support PWM sensor, cannot compute setPwmToGetPwmMap. Assuming 1:1 relation.", f.fan.GetId())
		}
		f.setPwmToGetPwmMap, _ = util.InterpolateLinearlyInt(&map[int]int{0: 0, 255: 255}, 0, 255)
		return nil
	}

	// this sweeps the fan through all PWM values, so it is a part of the initialization sequence
	// (and counts as initializing, unless it runs within the whole initialization already)
	if f.initializing.CompareAndSwap(false, true) {
		defer f.initializing.Store(false)
	}
	unlock, err := f.lockInitializationSequence(ctx)
	if err != nil {
		return err
	}
	defer unlock()

	f.markMayWriteToFan()
	_ = trySetManualPwm(f.fan)

	setPwmToGetPwmMap := map[int]int{}
	for i := fans.MinPwmValue; i <= fans.MaxPwmValue; i++ {
		err := f.fan.SetPwm(i)
		if err != nil {
			ui.Warning("Error setting PWM value %d on fan %s: %v", i, f.fan.GetId(), err)
			continue
		}
		if err := sleepWithContext(ctx, f.getClock(), f.getPwmSetDelay()); err != nil {
			return err
		}
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

func (f *DefaultFanController) computeFanSpecificMappings(ctx context.Context) (err error) {
	err = f.computeSetPwmToGetPwmMap(ctx)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			ui.Error("Error computing setPwm(x) -> getPwm() map: %v", err)
		}
		return err
	}

	err = f.computePwmMap()
	if err != nil {
		ui.Warning("Error computing PWM map: %v", err)
		return err
	}
	f.pwmMapComputed = true

	f.updateDistinctPwmValues()

	return nil
}
