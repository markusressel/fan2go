package controller

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
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
	"github.com/markusressel/fan2go/internal/warnings"
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
	// PwmReadGlitchCount is the number of PWM mismatches of the pwmValueChangedByThirdParty check that a second
	// read didn't confirm (see pendingConfirmation)
	PwmReadGlitchCount int
	// ControlModeReadGlitchCount is the number of control mode mismatches of the fanModeChangedByThirdParty check
	// that a second read didn't confirm
	ControlModeReadGlitchCount int
	IncreasedMinPwmCount       int
	MinPwmOffset               int
	// StallCount is the number of times the stall check of a fan without neverStop found the fan stopped
	// (once per stall, see checkStall)
	StallCount int
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
	// warnings receives the warnings the controller detects about the fan (shown in the REST API).
	// nil if there is no store (e.g. `fan2go fan init`): then they are only logged.
	warnings *warnings.Store
	// rate to update the target fan speed, if there are no curveCycles
	updateRate time.Duration
	// the fan state as it was before the controller started (nil until captured)
	originalFanState *FanStateSnapshot
	// restoreOnce makes Run restore the fan settings only once, no matter whether it stops normally,
	// because of an error or because of a panic (see restoreControlModeOnce). Replaced on every Run, and
	// whenever the update loop takes manual control again after onSensorFailure: restore (also if that fails).
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

	// freshAnalysis is true while RunInitializationAndRestore runs (`fan2go fan init`): the saved analysis data
	// is neither used nor changed until the analysis succeeded, then it is replaced (see saveFreshAnalysisData).
	// Only accessed by the goroutine that runs the initialization.
	freshAnalysis bool
	// pendingSetPwmToGetPwmMap is the measured setPwmToGetPwmMap of a fresh analysis, saved once it succeeded
	// (nil: nothing to save, e.g. it comes from the config or a known readback formula)
	pendingSetPwmToGetPwmMap map[int]int
	// pendingPwmMap is true if the pwmMap of a fresh analysis was computed automatically and has to be saved
	pendingPwmMap bool

	// readbackDelay is the measured time the fan needs to report a written PWM value when it is read back
	// (see FanAnalysisInfo.ReadbackDelayMillis), 0 if it isn't known or the readback is immediate.
	// Only used by the goroutine that runs Run.
	readbackDelay time.Duration
	// observedReadbackDelay is the longest readback delay observed during the current analysis, nil if none
	observedReadbackDelay *time.Duration
	// lastPwmWriteValue and lastPwmWriteAt are the raw value and the time of the last successful write of a PWM
	// value by the controller (zero time: none in this Run). See readbackMayBeStale.
	lastPwmWriteValue int
	lastPwmWriteAt    time.Time
	// pwmWriteCount counts the successful writes of PWM values by the controller (see writePwm)
	pwmWriteCount uint64

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

	// stall check of fans without neverStop (see checkStall), only used by the update loop:
	// stallCheckSince is the time since which the target qualifies for the check (zero if it doesn't),
	// stallCheckTarget the target of the last update, stallReported is true from the warning about a stall
	// until the fan spins again.
	stallCheckSince  time.Time
	stallCheckTarget int
	stallReported    bool
	// lastRpmReading is the last successful RPM reading of the RPM monitor, and rpmSpinSeen is set once a
	// reading showed the fan spinning in this Run (written by the RPM monitor, read by the update loop)
	lastRpmReading atomic.Int64
	rpmSpinSeen    atomic.Bool
	// rpmReadFailing is true while the last RPM reading of the RPM monitor failed
	rpmReadFailing atomic.Bool

	// lastFanModeCheckTime is the last time we checked if some third party changed the fan control mode
	lastFanModeCheckTime time.Time

	// sensorFailureAction is the onSensorFailure action that is active because the curve can't be evaluated,
	// or "" while the fan is controlled normally. Only used by the update loop.
	sensorFailureAction configuration.OnSensorFailureAction
	// sensorFailureMaxWriteFailed is true once setting the max PWM failed in the current failure state
	// (only used by the update loop)
	sensorFailureMaxWriteFailed bool
	// retakeManualControlFailed is true once taking manual control again after onSensorFailure: restore failed,
	// until it succeeds (only used by the update loop). The user is warned and notified only once per failure.
	retakeManualControlFailed bool
	// pwmWriteFailures tracks consecutive failures to set the PWM value of the curve (only used by the update loop)
	pwmWriteFailures consecutiveFailures
	// thirdPartyPwmChanges tracks consecutive updates where the PWM value of the fan didn't match the
	// value fan2go set last (only used by the update loop)
	thirdPartyPwmChanges consecutiveFailures
	// pendingPwmMismatch and pendingModeMismatch are the mismatches of the pwmValueChangedByThirdParty and
	// fanModeChangedByThirdParty checks that wait for their confirmation (only used by the update loop)
	pendingPwmMismatch  pendingConfirmation
	pendingModeMismatch pendingConfirmation
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
	// maxReadbackDelay is the longest time fan2go waits for a fan to report a written PWM value when it measures
	// the readback delay (see probeReadbackDelay), and the longest readback delay it stores
	maxReadbackDelay = 2 * time.Second
	// readbackPollInterval is the time between two reads of the PWM value while waiting for the readback
	readbackPollInterval       = 5 * time.Millisecond
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

// SetWarnings sets the store the controller reports the warnings it detects about the fan to.
// Must not be called while Run is running.
func (f *DefaultFanController) SetWarnings(store *warnings.Store) {
	f.warnings = store
}

// updateFlatRpmCurveWarning logs a warning if the RPM curve data attached to the fan is flat
// (see fans.WarnIfFlatRpmCurve), and sets or clears the matching warning in the warnings store.
func (f *DefaultFanController) updateFlatRpmCurveWarning() {
	if flat, message := fans.WarnIfFlatRpmCurve(f.fan); flat {
		f.warnings.Set(warnings.KindFan, f.fan.GetId(), warnings.CodeFlatRpmCurve, message)
	} else {
		f.warnings.Clear(warnings.KindFan, f.fan.GetId(), warnings.CodeFlatRpmCurve)
	}
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

	err = fan.AttachFanRpmCurveData(&fanPwmData)
	if err != nil {
		return err
	}

	err = f.computeFanSpecificMappings(ctx)
	if err != nil {
		return err
	}
	f.alignDerivedMaxPwmWithOutputLevels()
	f.loadReadbackDelay()
	// explains a derived maxPwm of 255 (the curve data gives no speed range)
	f.updateFlatRpmCurveWarning()

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
	f.lastPwmWriteAt = time.Time{}
	f.pwmFloorSetAt = time.Time{}
	f.rpmAvgSeeded.Store(false)
	f.stallCheckSince = time.Time{}
	f.stallCheckTarget = 0
	f.stallReported = false
	f.lastRpmReading.Store(0)
	f.rpmSpinSeen.Store(false)
	f.rpmReadFailing.Store(false)
	f.lastFanModeCheckTime = time.Unix(0, 0)
	f.sensorFailureAction = ""
	f.sensorFailureMaxWriteFailed = false
	f.retakeManualControlFailed = false
	f.pwmWriteFailures = consecutiveFailures{}
	f.thirdPartyPwmChanges = consecutiveFailures{}
	f.clearPendingThirdPartyChecks()
	if f.controlLoop != nil {
		f.controlLoop.Reset()
	}
	// re-evaluated once Run has the curve data of the fan
	f.warnings.Clear(warnings.KindFan, f.fan.GetId(), warnings.CodeFlatRpmCurve)

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
	if fan.GetConfig().HwMon != nil {
		if err := f.deleteAnalysisDataIfKnownQuirksChanged(); err != nil {
			return nil, err
		}
	}
	ui.Info("Fan %s: Loading fan curve data...", fan.GetId())
	fanRpmData, err := f.persistence.LoadFanRpmData(fan)
	if err == nil {
		if len(fanRpmData) > 0 || !f.needsRpmCurve() {
			return fanRpmData, nil
		}
		// saved while the RPM sensor was missing (e.g. a wrong rpmChannel, or the driver was loaded late):
		// now that it is there, the fan can be analyzed
		err = errAnalyzedWithoutRpmSensor
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
		} else if errors.Is(err, errAnalyzedWithoutRpmSensor) {
			ui.Warning("Fan '%s' was analyzed without an RPM sensor, but has one now, starting initialization sequence...", fan.GetId())
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
	}

	// file/cmd fan: save its fixed curve
	curveData := fan.GetFanRpmCurveData()
	if curveData == nil {
		return nil, nil
	}
	if err := f.persistence.SaveFanRpmData(fan); err != nil {
		return nil, fmt.Errorf("unable to save the fan curve data of fan %s: %w", fan.GetId(), err)
	}
	return *curveData, nil
}

// errAnalyzedWithoutRpmSensor means that the saved data of a fan has no RPM curve, although the fan has an RPM
// sensor now (see needsRpmCurve).
var errAnalyzedWithoutRpmSensor = fmt.Errorf("%w: analyzed without an RPM sensor", os.ErrNotExist)

// needsRpmCurve returns true if the fan is analyzed (hwmon and nvidia fans) and has an RPM sensor, so the
// analysis measures an RPM curve. Saved data without a curve is not usable for such a fan.
func (f *DefaultFanController) needsRpmCurve() bool {
	config := f.fan.GetConfig()
	return (config.HwMon != nil || config.Nvidia != nil) && f.fan.Supports(fans.FeatureRpmSensor)
}

// deleteAnalysisDataIfKnownQuirksChanged deletes the saved analysis data of the fan (RPM curve, setPwmToGetPwmMap,
// pwmMap) if it was measured with another known hardware quirks readback formula than the one that applies now
// (see analysisQuirkFingerprint), so it is measured again. Data without analysis info was measured without one.
// The RPM curve and pwmMap are deleted too: they were measured with the readback the old map predicted (a wrong
// map skips measurement points, a sweep with failed writes leaves keys out), and a formula may change the key set.
func (f *DefaultFanController) deleteAnalysisDataIfKnownQuirksChanged() error {
	fan := f.fan
	id, version := f.analysisQuirkFingerprint()
	saved, err := f.persistence.LoadFanAnalysisInfo(fan.GetId())
	if err != nil && !persistence.IsMissingOrCorrupt(err) {
		return fmt.Errorf("unable to load the analysis info of fan %s: %w", fan.GetId(), err)
	}
	if saved.Quirk == id && saved.QuirkVersion == version {
		return nil
	}

	if _, err := f.persistence.LoadFanRpmData(fan); err == nil {
		ui.Info("Fan %s: The known hardware quirks of the fan changed since it was analyzed (%s -> %s), analyzing it again",
			fan.GetId(), quirkFingerprintName(saved.Quirk, saved.QuirkVersion), quirkFingerprintName(id, version))
	}
	if err := f.persistence.DeleteFanRpmData(fan); err != nil {
		return fmt.Errorf("unable to delete the fan curve data of fan %s: %w", fan.GetId(), err)
	}
	if err := f.persistence.DeleteFanSetPwmToGetPwmMap(fan.GetId()); err != nil {
		return fmt.Errorf("unable to delete the setPwmToGetPwmMap of fan %s: %w", fan.GetId(), err)
	}
	if err := f.persistence.DeleteFanPwmMap(fan.GetId()); err != nil {
		return fmt.Errorf("unable to delete the pwm map of fan %s: %w", fan.GetId(), err)
	}
	if err := f.persistence.DeleteFanAnalysisInfo(fan.GetId()); err != nil {
		return fmt.Errorf("unable to delete the analysis info of fan %s: %w", fan.GetId(), err)
	}
	return nil
}

// analysisQuirkFingerprint returns the id and version of the known hardware quirks entry that the analysis data
// of the fan depends on: the entry, if its readback formula is used as the setPwmToGetPwmMap, else ("", 0).
func (f *DefaultFanController) analysisQuirkFingerprint() (id string, version int) {
	// assumePwmMapIdentity (fan init) doesn't count: it only applies to that run, the daemon uses the formula again,
	// and both have the same keys 0..255, so the RPM curve and pwmMap measured with it stay valid
	entry := fans.KnownQuirksOf(f.fan)
	if !entry.UsesReadback(f.fan.GetConfig(), false) {
		return "", 0
	}
	return entry.Fingerprint()
}

// quirkFingerprintName returns a known hardware quirks entry id and version for log messages.
func quirkFingerprintName(id string, version int) string {
	if id == "" {
		return "none"
	}
	return fmt.Sprintf("%s v%d", id, version)
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
// It measures everything again (the setPwmToGetPwmMap, the pwmMap and the RPM curve) without using the saved
// analysis data, and replaces the saved data only if it succeeds: a failed or interrupted analysis keeps it.
// Must not be called while Run is running on the same controller.
func (f *DefaultFanController) RunInitializationAndRestore(ctx context.Context) (curveData map[int]float64, err error) {
	f.restoreOnce = &sync.Once{}
	f.mayHaveWrittenToFan = f.handbackPending
	f.originalFanStateSaveFailed = false
	f.freshAnalysis = true
	f.pendingSetPwmToGetPwmMap = nil
	f.pendingPwmMap = false
	defer func() {
		f.freshAnalysis = false
		f.pendingSetPwmToGetPwmMap = nil
		f.pendingPwmMap = false
		if r := recover(); r != nil {
			curveData = nil
			err = f.panicToError("fan initialization", r)
		}
		if f.mayHaveWrittenToFan {
			f.restoreControlModeOnce()
		}
	}()
	curveData, err = f.RunInitialization(ctx)
	if err == nil {
		f.updateFlatRpmCurveWarning()
	}
	return curveData, err
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
	f.observedReadbackDelay = nil

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

	rawStartPwm, rawMaxPwm := fans.ComputeFanPwmBoundariesFromCurveData(fan, curveData, fans.MaxPwmValue)
	ui.Info("Fan %s: Analysis boundaries detected (before config overrides): Start %d, Max %d", fan.GetId(), rawStartPwm, rawMaxPwm)

	err = fan.AttachFanRpmCurveData(&curveData)
	if err != nil {
		ui.Error("Fan %s: Failed to attach fan curve data: %v", fan.GetId(), err)
		return nil, err
	}

	if f.observedReadbackDelay != nil {
		f.readbackDelay = *f.observedReadbackDelay
		ui.Info("Fan %s: Measured PWM readback delay: %s", fan.GetId(), f.readbackDelay)
	}

	var info *persistence.FanAnalysisInfo
	if config := fan.GetConfig(); config.HwMon != nil || config.Nvidia != nil {
		id, version := f.analysisQuirkFingerprint()
		info = &persistence.FanAnalysisInfo{Quirk: id, QuirkVersion: version}
		if f.observedReadbackDelay != nil {
			millis := f.observedReadbackDelay.Milliseconds()
			if time.Duration(millis)*time.Millisecond < *f.observedReadbackDelay {
				millis++ // round up
			}
			info.ReadbackDelayMillis = &millis
		}
	}

	// save to database to restore it on restarts
	if f.freshAnalysis {
		// replace everything at once, so a failed save keeps the old data
		if err := f.replaceSavedAnalysisData(curveData, info); err != nil {
			return nil, err
		}
		return curveData, nil
	}
	err = f.persistence.SaveFanRpmData(fan)
	if err != nil {
		return nil, fmt.Errorf("unable to save the fan curve data of fan %s: %w", fan.GetId(), err)
	}
	if info != nil {
		if err := f.persistence.SaveFanAnalysisInfo(fan.GetId(), *info); err != nil {
			return nil, fmt.Errorf("unable to save the analysis info of fan %s: %w", fan.GetId(), err)
		}
	}

	return curveData, nil
}

// replaceSavedAnalysisData replaces the saved analysis data of the fan with the result of a fresh analysis
// (see freshAnalysis) in one transaction. Mappings that don't come from the database (config, known readback
// formula) are deleted, like `fan reset` does.
func (f *DefaultFanController) replaceSavedAnalysisData(curveData map[int]float64, info *persistence.FanAnalysisInfo) error {
	data := persistence.FanAnalysisData{
		RpmCurve:          curveData,
		SetPwmToGetPwmMap: f.pendingSetPwmToGetPwmMap,
		Info:              info,
	}
	if f.pendingPwmMap {
		data.PwmMap = append([]int(nil), f.pwmMapping[:]...)
	}
	if err := f.persistence.ReplaceFanAnalysisData(f.fan.GetId(), data); err != nil {
		return fmt.Errorf("unable to save the analysis data of fan %s: %w", f.fan.GetId(), err)
	}
	return nil
}

// loadReadbackDelay loads the readback delay measured by the analysis of the fan (see FanAnalysisInfo).
// It is 0 if none was measured (e.g. data saved by an older version) or the info can't be read.
func (f *DefaultFanController) loadReadbackDelay() {
	fan := f.fan
	f.readbackDelay = 0
	if config := fan.GetConfig(); config.HwMon == nil && config.Nvidia == nil {
		return
	}
	info, err := f.persistence.LoadFanAnalysisInfo(fan.GetId())
	if err != nil {
		if !persistence.IsMissingOrCorrupt(err) {
			ui.Warning("Fan %s: unable to load the measured PWM readback delay, assuming none: %v", fan.GetId(), err)
		}
		return
	}
	if info.ReadbackDelayMillis == nil {
		ui.Debug("Fan %s: PWM readback delay not measured yet (run 'fan2go fan --id %s init' to measure it)", fan.GetId(), fan.GetId())
		return
	}
	delay := time.Duration(*info.ReadbackDelayMillis) * time.Millisecond
	if delay < 0 || delay > maxReadbackDelay {
		ui.Warning("Fan %s: the saved PWM readback delay (%dms) is out of range, using %s",
			fan.GetId(), *info.ReadbackDelayMillis, util.Coerce(delay, 0, maxReadbackDelay))
		delay = util.Coerce(delay, 0, maxReadbackDelay)
	}
	f.readbackDelay = delay
	if f.readbackDelay > f.getPwmSetDelay() {
		ui.Info("Fan %s: Using the measured PWM readback delay of %s", fan.GetId(), f.readbackDelay)
	}
}

// alignDerivedMaxPwmWithOutputLevels makes sure that a maxPwm derived from the curve data selects an output level
// of the fan that reaches fans.MaxPwmRpmRatio of the peak RPM. Target values whose raw values read back the same
// form one output level. The curve between two measured points is interpolated, so inside a level it can
// reach the ratio although the level itself doesn't (e.g. a fan with 8 speed levels). The curve value of the
// first target value of a level is used for the whole level; maxPwm is raised to the first level that reaches
// the ratio (255 if none). Configured maxPwm values and nvidia fans (whose values are percent) are not changed.
// Must be called after computeFanSpecificMappings.
func (f *DefaultFanController) alignDerivedMaxPwmWithOutputLevels() {
	fan := f.fan
	config := fan.GetConfig()
	if config.MaxPwm != nil || config.Nvidia != nil || !f.pwmMapComputed {
		return
	}
	curveData := fan.GetFanRpmCurveData()
	if curveData == nil || len(*curveData) == 0 {
		return
	}
	maxPwm := fan.GetMaxPwm()
	alignedMaxPwm := alignMaxPwmWithOutputLevels(*curveData, maxPwm, f.outputLevelsOfTargets())
	if alignedMaxPwm != maxPwm {
		ui.Info("Fan %s: raising the derived maxPwm from %d to %d, the first output level that reaches %.0f%% of the peak RPM",
			fan.GetId(), maxPwm, alignedMaxPwm, fans.MaxPwmRpmRatio*100)
		fan.SetMaxPwm(alignedMaxPwm, false)
	}
}

// outputLevelsOfTargets returns the PWM value the fan reports for each target value (pre-pwmMap): target values
// with the same value select the same output level.
func (f *DefaultFanController) outputLevelsOfTargets() (levels [fans.MaxPwmValue + 1]int) {
	for target := fans.MinPwmValue; target <= fans.MaxPwmValue; target++ {
		levels[target] = f.getReportedPwmAfterApplyingPwm(f.applyPwmMapToTarget(target))
	}
	return levels
}

// alignMaxPwmWithOutputLevels returns the lowest target value >= maxPwm whose output level reaches
// fans.MaxPwmRpmRatio of the peak RPM of the curve, judged by the curve value of the first target value of the
// level (see alignDerivedMaxPwmWithOutputLevels). Returns 255 if no level reaches it, and maxPwm if the curve
// has no spinning value.
func alignMaxPwmWithOutputLevels(curveData map[int]float64, maxPwm int, outputLevels [fans.MaxPwmValue + 1]int) int {
	peakRpm := 0.0
	for _, rpm := range curveData {
		peakRpm = math.Max(peakRpm, rpm)
	}
	if !fans.IsRpmLikelySpinning(peakRpm) {
		return maxPwm
	}
	threshold := peakRpm * fans.MaxPwmRpmRatio
	levelStart := func(target int) int {
		for target > fans.MinPwmValue && outputLevels[target-1] == outputLevels[target] {
			target--
		}
		return target
	}
	for target := util.Coerce(maxPwm, fans.MinPwmValue, fans.MaxPwmValue); target <= fans.MaxPwmValue; target++ {
		if rpm, ok := curveData[levelStart(target)]; ok && rpm >= threshold {
			return target
		}
	}
	return fans.MaxPwmValue
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

	hasRpmSensor := fan.Supports(fans.FeatureRpmSensor)
	if hasRpmSensor {
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
					// a fan that stalls at a derived maxPwm still gets the range up to full speed (see fans.FailSafeMaxPwm)
					if speedTarget >= fans.FailSafeMaxPwm(fan) {
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

	err = f.setTargetPwm(speedTarget)
	if hasRpmSensor && !shouldNeverStop {
		f.checkStall(speedTarget, minPwm, err == nil)
	}
	return f.handlePwmWriteResult(err)
}

// checkStall warns and notifies once when a fan without neverStop doesn't spin, although fan2go set a target of
// at least its minPwm (and at least 1) long enough ago, and logs when it spins again. It changes neither the PWM
// value nor the controller state. A fan with neverStop has its own stall handling in UpdateFanSpeed.
// target is the target of this update (before the pwmMap), written whether setting it succeeded.
func (f *DefaultFanController) checkStall(target int, minPwm int, written bool) {
	fan := f.fan
	if enabled := fan.GetConfig().SanityCheck.FanStalled.Enabled; !enabled.Get() {
		return
	}
	now := f.getClock().Now()

	// the fan should spin at this target: at least minPwm and 1, and the pwmMap doesn't map it to 0
	floor := max(minPwm, 1)
	qualifies := written && target >= floor && f.applyPwmMapToTarget(target) > 0
	switch {
	case !qualifies:
		f.stallCheckSince = time.Time{}
	case f.stallCheckSince.IsZero():
		// the fan may have to start from standstill
		f.stallCheckSince = now
	case target != f.stallCheckTarget && (target <= floor || f.stallCheckTarget <= floor):
		// at the floor, the fan may not have been spinning before: give it time to react to the new target.
		// A target that changes above the floor doesn't need that, the fan was spinning before.
		f.stallCheckSince = now
	}
	f.stallCheckTarget = target

	// skip until the RPM monitor seeded the average, which is meaningless before that
	if !f.rpmAvgSeeded.Load() {
		return
	}
	avgRpm := fan.GetRpmAvg()
	if fans.IsRpmLikelySpinning(avgRpm) {
		if f.stallReported {
			f.stallReported = false
			ui.Info("Fan %s is spinning again (avg. RPM %d at PWM %d)", fan.GetId(), int(avgRpm), target)
		}
		return
	}
	if f.stallReported || f.stallCheckSince.IsZero() || !f.hadTimeToReact(f.stallCheckSince, now) {
		return
	}
	// while reading the RPM fails, the average and the last reading are frozen (the failure is logged)
	if f.rpmReadFailing.Load() {
		return
	}
	// the average lags behind: a fan that spins up slowly is not stalled if its last reading shows it spinning
	if fans.IsRpmLikelySpinning(float64(f.lastRpmReading.Load())) {
		return
	}
	// a fan header with an RPM input but no tach signal always reads 0 RPM
	if !f.tachIsCredible() {
		return
	}

	f.stallReported = true
	f.statsMutex.Lock()
	f.stats.StallCount += 1
	f.statsMutex.Unlock()
	notify := f.errorNotifier
	if notify == nil {
		notify = ui.WarningAndNotify
	}
	notify("Fan Stalled", "Fan %s doesn't spin: its avg. RPM is %d at PWM %d (minPwm %d) since %s. "+
		"fan2go doesn't change the PWM value because of this. Check the fan and its tach signal. If the fan doesn't "+
		"start at this PWM value, raise its minPwm or set neverStop: true. If something else stops the fan on purpose "+
		"(e.g. the device firmware), set sanityCheck.fanStalled.enabled: false for this fan",
		fan.GetId(), int(avgRpm), target, minPwm, now.Sub(f.stallCheckSince).Round(time.Second))
}

// restartStallReactionPeriod makes the stall check give the fan the full reaction period again from its next
// update, e.g. after something else may have stopped it.
func (f *DefaultFanController) restartStallReactionPeriod() {
	f.stallCheckSince = time.Time{}
}

// tachIsCredible returns whether the RPM sensor of the fan has been seen working: its curve data contains a
// point where it was spinning, or the RPM monitor read a spinning fan in this Run.
func (f *DefaultFanController) tachIsCredible() bool {
	if f.rpmSpinSeen.Load() {
		return true
	}
	if curveData := f.fan.GetFanRpmCurveData(); curveData != nil {
		for _, rpm := range *curveData {
			if fans.IsRpmLikelySpinning(rpm) {
				return true
			}
		}
	}
	return false
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
	return f.hadTimeToReact(f.pwmFloorSetAt, now)
}

// hadTimeToReact returns true if the fan had minPwmIncreaseRpmPollingPeriods RPM polling periods since the given
// time to react to a new PWM value.
func (f *DefaultFanController) hadTimeToReact(since time.Time, now time.Time) bool {
	interval := minPwmIncreaseRpmPollingPeriods * configuration.CurrentConfig.RpmPollingRate
	return now.Sub(since) >= interval
}

// read the current value of a fan RPM sensor and update its moving average with it.
// The first successful reading replaces the average, a failed reading leaves it unchanged.
// Of a series of failed readings, only the first one is logged, and the recovery.
func (f *DefaultFanController) measureRpm(fan fans.Fan) {
	rpm, err := fan.GetRpm()
	f.rpmReadFailing.Store(err != nil)
	if err != nil {
		if count, _, _ := f.rpmReadFailures.fail(f.getClock().Now()); count == 1 {
			ui.Warning("Error reading RPM value of fan %s (further errors are not logged until it recovers): %v", fan.GetId(), err)
		}
		return
	}
	if failures := f.rpmReadFailures.succeed(); failures > 0 {
		ui.Info("Fan %s: reading the RPM value succeeded again after %d failed readings", fan.GetId(), failures)
	}
	f.lastRpmReading.Store(int64(rpm))
	if fans.IsRpmLikelySpinning(float64(rpm)) {
		f.rpmSpinSeen.Store(true)
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
// An error of trySetManualPwm is ignored if the fan is in PWM mode anyway (e.g. a read-only pwm_enable
// that is 1 already). Otherwise the returned error means that fan2go can't control the fan.
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
			return fmt.Errorf("fan2go can't control the fan: %w", withKnownQuirksHint(fan, err))
		}
		ui.Debug("Fan %s: is in manual PWM mode already, ignoring: %v", fan.GetId(), err)
	}
	return nil
}

// withKnownQuirksHint adds the hint of the known hardware quirks entry of the fan (if any) to err.
func withKnownQuirksHint(fan fans.Fan, err error) error {
	entry := fans.KnownQuirksOf(fan)
	if entry == nil || entry.Hint == "" {
		return err
	}
	return fmt.Errorf("%w (known hardware quirk '%s': %s)", err, entry.ID, entry.Hint)
}

// trySetManualPwm writes the control mode fan2go uses while it controls the fan: controlMode.active,
// default pwm (hwmon: pwm_enable=1). It doesn't fall back to another mode if that fails: disabled
// (pwm_enable=0) is rejected by some drivers, and means full speed without PWM control on most others.
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
		return fmt.Errorf("can't switch fan %s to control mode %s: %w", fan.GetId(), controlModeName(targetMode), err)
	}
	return nil
}

// controlModeName returns the name of mode for log and error messages, e.g. "pwm (1)".
func controlModeName(mode fans.ControlMode) string {
	switch mode {
	case fans.ControlModeDisabled:
		return "disabled (0)"
	case fans.ControlModePWM:
		return "pwm (1)"
	case fans.ControlModeAutomatic:
		return "auto"
	default:
		return strconv.Itoa(int(mode))
	}
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
	// the fan may be handed back or set to max: after the failure, it gets the full reaction period again
	f.restartStallReactionPeriod()
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
			f.clearPendingThirdPartyChecks()
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

// setMaxPwmForSensorFailure sets the fan to its fail-safe speed while its curve can't be evaluated: the configured
// maxPwm, or else full speed (see fans.FailSafeMaxPwm), never the maxPwm derived from the curve data.
// A failed write is logged once per failure state, not on every tick.
func (f *DefaultFanController) setMaxPwmForSensorFailure() {
	err := f.setTargetPwm(fans.FailSafeMaxPwm(f.fan))
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
		// the handback deleted the saved original state: save it again before writing to the fan
		f.markMayWriteToFan()
		// fan2go may have written to the fan, so it must be restored when the controller stops, also if taking
		// manual control fails (the write may have changed the fan anyway): the stop runs the usual restore,
		// which deletes the saved original state if it is complete
		f.restoreOnce = &sync.Once{}
		if err := takeManualControl(f.fan); err != nil {
			err = fmt.Errorf("the curve can be evaluated again, but manual control can't be taken again: %w", err)
			f.setState(status.StateSensorFailureRestored, err)
			if !f.retakeManualControlFailed {
				// once per failure, it is retried on every update
				f.retakeManualControlFailed = true
				notify := f.errorNotifier
				if notify == nil {
					notify = ui.WarningAndNotify
				}
				notify("Fan Control Error", "Fan %s: %v; the fan stays handed back, fan2go retries on every update", f.fan.GetId(), err)
			} else {
				ui.Debug("Fan %s: Can't take manual control again: %v", f.fan.GetId(), err)
			}
			return false
		}
		if f.retakeManualControlFailed {
			f.retakeManualControlFailed = false
			ui.Info("Fan %s: Manual control taken again", f.fan.GetId())
		}
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
// After a successful restore, the update loop arms a new restoreOnce before it takes manual control again.
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
				if err := f.writePwm(f.applyPwmMapForRestore(fans.MaxPwmValue)); err != nil {
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
		if err := f.writePwm(*pwmToSet); err != nil {
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
	if err := f.writePwm(*originalPwmValue); err != nil {
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

// maxPwmCurveValue returns the curve value that is closest to the fail-safe speed set by onSensorFailure: max
// (see setMaxPwmForSensorFailure): 255, which UpdateFanSpeed maps to the maxPwm of the fan, or the fail-safe
// value itself if the fan uses unscaled curve values.
func (f *DefaultFanController) maxPwmCurveValue() float64 {
	if f.fan.GetConfig().UseUnscaledCurveValues {
		return float64(fans.FailSafeMaxPwm(f.fan))
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
//
// A mismatch is only reported once a second read confirms it (see pendingConfirmation): the first one is
// remembered, and a later update reads the value again after thirdPartyConfirmationDelay. If that read shows the
// expected value, the first one was a read glitch (or, if fan2go wrote a PWM value in between, it can't tell).
// While a mismatch is pending, setTargetPwm doesn't write just because of what the fan reports.
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

	if f.readbackMayBeStale(f.getClock().Now()) {
		// the fan may still report the value before the last write
		return
	}

	now := f.getClock().Now()
	if f.lastTarget == nil || f.pendingPwmMismatch.waiting(now) {
		return
	}
	pwmMappedValue := f.applyPwmMapToTarget(*f.lastTarget)
	expectedReportedPwm := f.getReportedPwmAfterApplyingPwm(pwmMappedValue)
	currentPwm, err := f.fan.GetPwm()
	if err != nil {
		return
	}
	switch f.pendingPwmMismatch.observe(now, f.thirdPartyConfirmationDelay(), currentPwm == expectedReportedPwm) {
	case confirmationStarted:
		// a third party may have stopped the fan: don't report a stall before the mismatch is resolved
		f.restartStallReactionPeriod()
		ui.Debug("PWM of %s reads '%d' instead of the expected '%d', reading it again in %s before reacting",
			f.fan.GetId(), currentPwm, expectedReportedPwm, f.thirdPartyConfirmationDelay())
	case confirmationGlitch:
		f.statsMutex.Lock()
		f.stats.PwmReadGlitchCount += 1
		f.statsMutex.Unlock()
		ui.Debug("PWM of %s reads the expected '%d' again, the unexpected value before was a read glitch",
			f.fan.GetId(), expectedReportedPwm)
		f.logThirdPartyPwmRecovery()
	case confirmationOverwritten:
		ui.Debug("PWM of %s reads the expected '%d' again after fan2go wrote a new value, ignoring the unexpected value before",
			f.fan.GetId(), expectedReportedPwm)
		f.logThirdPartyPwmRecovery()
	case confirmationMatch:
		f.logThirdPartyPwmRecovery()
	case confirmationConfirmed:
		// the third party may have stopped the fan, give it time to start again after fan2go's write
		f.restartStallReactionPeriod()
		// the statistics count every change, the warning is rate-limited
		f.statsMutex.Lock()
		f.stats.UnexpectedPwmValueCount += 1
		f.statsMutex.Unlock()
		count, _, log := f.thirdPartyPwmChanges.fail(now)
		if log {
			ui.Warning("PWM of %s was changed by third party! Last set PWM value was '%d', expected reported pwm '%d' but was '%d' "+
				"(changes in a row: %d; only the first and every %dth change are logged)",
				f.fan.GetId(), pwmMappedValue, expectedReportedPwm, currentPwm, count, failureLogInterval)
		}
	}
}

// logThirdPartyPwmRecovery ends a series of PWM values changed by a third party (see ensureNoThirdPartyIsMessingWithUs),
// and logs the recovery if there was one.
func (f *DefaultFanController) logThirdPartyPwmRecovery() {
	if count := f.thirdPartyPwmChanges.succeed(); count > 0 {
		ui.Info("PWM of %s matches the value set by fan2go again, after %d unexpected values in a row", f.fan.GetId(), count)
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
		alreadySet := false
		if f.readbackMayBeStale(f.getClock().Now()) {
			// the readback may still show an older value (e.g. A, B, A within the delay would read A and skip the
			// second A, so B would stay): compare with the value written last instead
			alreadySet = f.lastPwmWriteValue == pwmMappedValue
		} else if f.pendingPwmMismatch.pending() && f.lastTarget != nil {
			// the fan reported an unexpected value that isn't confirmed yet (see ensureNoThirdPartyIsMessingWithUs):
			// don't write just because of that read, which may have been a glitch. A new target is written as usual.
			alreadySet = f.applyPwmMapToTarget(*f.lastTarget) == pwmMappedValue
		} else {
			current, err := f.getPwm()
			alreadySet = err == nil && expectedReportedPwmValue == current
		}
		if alreadySet {
			// nothing to do
			f.lastTarget = &target
			f.recordLastSetPwm(pwmMappedValue)
			return nil
		}
	}
	err = f.writePwm(pwmMappedValue)
	if err == nil {
		// only after a successful write: lastTarget is compared with the PWM value of the fan
		f.lastTarget = &target
		f.recordLastSetPwm(pwmMappedValue)
	}
	return err
}

// writePwm writes the raw PWM value to the fan and remembers it and the time of the write (see readbackMayBeStale).
func (f *DefaultFanController) writePwm(raw int) error {
	err := f.fan.SetPwm(raw)
	if err == nil {
		f.lastPwmWriteValue = raw
		f.lastPwmWriteAt = f.getClock().Now()
		f.pwmWriteCount++
		f.pendingPwmMismatch.noteWrite()
	}
	return err
}

// readbackMayBeStale returns true if the fan may still report a PWM value from before the last write at the given
// time: a readback delay was measured (see readbackDelay), and the last write is less than
// max(readbackDelay, pwmSetDelay) ago. Without a measured delay it is always false, like before it was measured.
func (f *DefaultFanController) readbackMayBeStale(now time.Time) bool {
	if f.readbackDelay <= 0 || f.lastPwmWriteAt.IsZero() {
		return false
	}
	return now.Sub(f.lastPwmWriteAt) < max(f.readbackDelay, f.getPwmSetDelay())
}

// observeReadbackDelay records a readback delay observed during the analysis (see observedReadbackDelay),
// capped at maxReadbackDelay.
func (f *DefaultFanController) observeReadbackDelay(delay time.Duration) {
	delay = min(delay, maxReadbackDelay)
	if f.observedReadbackDelay == nil || delay > *f.observedReadbackDelay {
		f.observedReadbackDelay = &delay
	}
}

// waitForReadback reads the PWM value of the fan (see getPwm) after a write until done returns true for it, or
// until window has passed since the call. It waits pwmSetDelay before the first read and readbackPollInterval between reads.
// It returns the last value read, whether done returned true, and the delay until then: 0 if the first read was
// done, else the time since the call. A read error is returned as is, and so is ctx.Err().
func (f *DefaultFanController) waitForReadback(
	ctx context.Context,
	done func(pwm int) bool,
	window time.Duration,
) (pwm int, ok bool, delay time.Duration, err error) {
	clock := f.getClock()
	start := clock.Now()
	if err := sleepWithContext(ctx, clock, f.getPwmSetDelay()); err != nil {
		return 0, false, 0, err
	}
	for first := true; ; first = false {
		pwm, err = f.getPwm()
		if err != nil {
			return 0, false, 0, err
		}
		elapsed := clock.Now().Sub(start)
		if done(pwm) {
			if first {
				return pwm, true, 0, nil
			}
			return pwm, true, elapsed, nil
		}
		if elapsed >= window {
			return pwm, false, elapsed, nil
		}
		if err := sleepWithContext(ctx, clock, readbackPollInterval); err != nil {
			return 0, false, 0, err
		}
	}
}

// probeReadbackDelay measures how long the fan takes to report a written PWM value before the setPwm -> getPwm
// sweep: it writes 0 and 255 alternately (four writes, starting with the value further away from the current
// one) and waits until the readback changes, up to maxReadbackDelay each. It returns the time the sweep waits
// for the readback of each write: twice the longest observed delay (the readback of a caching driver changes at
// its next refresh, so a single observation can be shorter than the cache period), at most maxReadbackDelay, but
// at least pwmSetDelay. If the readback never changes, the delay can't be measured, and it returns pwmSetDelay.
func (f *DefaultFanController) probeReadbackDelay(ctx context.Context) (time.Duration, error) {
	floor := f.getPwmSetDelay()
	previous, err := f.fan.GetPwm()
	if err != nil {
		return floor, nil
	}
	values := []int{fans.MaxPwmValue, fans.MinPwmValue, fans.MaxPwmValue, fans.MinPwmValue}
	if previous > fans.MaxPwmValue/2 {
		values = []int{fans.MinPwmValue, fans.MaxPwmValue, fans.MinPwmValue, fans.MaxPwmValue}
	}
	var longest *time.Duration
	for _, value := range values {
		if err := f.writePwm(value); err != nil {
			ui.Debug("Fan %s: unable to measure the PWM readback delay, writing %d failed: %v", f.fan.GetId(), value, err)
			return floor, nil
		}
		last := previous
		pwm, changed, delay, err := f.waitForReadback(ctx, func(pwm int) bool { return pwm != last }, maxReadbackDelay)
		if err != nil {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			ui.Debug("Fan %s: unable to measure the PWM readback delay: %v", f.fan.GetId(), err)
			return floor, nil
		}
		if changed {
			f.observeReadbackDelay(delay)
			if longest == nil || delay > *longest {
				longest = &delay
			}
		}
		previous = pwm
	}
	if longest == nil {
		ui.Warning("Fan %s: the reported PWM value didn't change within %s after writing 0 and 255, "+
			"unable to measure the readback delay", f.fan.GetId(), maxReadbackDelay)
		return floor, nil
	}
	ui.Info("Fan %s: PWM readback delay before the set/get sweep: %s", f.fan.GetId(), *longest)
	return max(floor, min(2**longest, maxReadbackDelay)), nil
}

// ensureFanModeIsSetToExpectedMode makes sure that the fan control mode is set to the expected mode (manual PWM control),
// by checking periodically. Like ensureNoThirdPartyIsMessingWithUs, it only reacts to a mode that a second read
// confirms after thirdPartyConfirmationDelay (see pendingConfirmation); that read ignores the throttle.
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

	now := f.getClock().Now()
	if f.pendingModeMismatch.pending() {
		// the confirmation of an unexpected mode is due after its delay, whatever the throttle says
		if f.pendingModeMismatch.waiting(now) {
			return
		}
	} else if f.lastFanModeCheckTime.Add(sanityCheckConfig.ThrottleDuration).After(now) {
		// make sure we don't check this too often
		return
	}
	f.lastFanModeCheckTime = now

	cm, e := f.fan.GetControlMode()
	if e != nil {
		ui.Warning("Cannot read control mode of fan %s: %v", f.fan.GetId(), e)
		return
	}
	switch f.pendingModeMismatch.observe(now, f.thirdPartyConfirmationDelay(), cm == fans.ControlModePWM) {
	case confirmationStarted:
		// the fan may run in another mode: don't report a stall before the mismatch is resolved
		f.restartStallReactionPeriod()
		ui.Debug("Fan mode of fan %s reads %v (expected PWM), reading it again in %s before reacting",
			f.fan.GetId(), cm, f.thirdPartyConfirmationDelay())
	case confirmationGlitch, confirmationOverwritten:
		// PWM writes don't change the mode: both mean that the unexpected mode was a read glitch
		f.statsMutex.Lock()
		f.stats.ControlModeReadGlitchCount += 1
		f.statsMutex.Unlock()
		ui.Debug("Fan mode of fan %s reads PWM again, the unexpected mode before was a read glitch", f.fan.GetId())
	case confirmationConfirmed:
		// the fan may have been stopped in the other mode, give it time to start again
		f.restartStallReactionPeriod()
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

	// the readback formula of a known driver, unless the identity is requested explicitly
	// (identity, linear and values were handled above; autodetect counts as not configured)
	if entry := fans.KnownQuirksOf(f.fan); entry.UsesReadback(f.fan.GetConfig(), f.assumePwmMapIdentity) {
		ui.Info("Fan %s: Using the set→get PWM map of the driver formula (known hardware quirk '%s')", f.fan.GetId(), entry.ID)
		f.setPwmToGetPwmMap = entry.SetPwmToGetPwmMap()
		return nil
	}

	// load the setPwmToGetPwmMap from persistence, if it exists (a fresh analysis measures it again)
	if !f.freshAnalysis {
		f.setPwmToGetPwmMap, err = f.persistence.LoadFanSetPwmToGetPwmMap(f.fan.GetId())
		if err == nil && f.setPwmToGetPwmMap != nil {
			ui.Info("FanController: Using saved value for setPwmToGetPwmMap of Fan '%s'", f.fan.GetId())
			return nil
		}
		if err != nil && !persistence.IsMissingOrCorrupt(err) {
			// the data may exist, but can't be read right now: don't sweep the fan because of that
			return fmt.Errorf("unable to load setPwmToGetPwmMap of fan %s: %w", f.fan.GetId(), err)
		}
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

	if f.freshAnalysis {
		// saved once the analysis succeeded
		f.pendingSetPwmToGetPwmMap = f.setPwmToGetPwmMap
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

	// a fresh analysis computes it again
	if !f.freshAnalysis {
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
	}

	{
		ui.Info("Computing pwm map...")
		err = f.computePwmMapAutomatically()
		if err != nil {
			ui.Error("Fan %s: Error computing pwm map: %v", f.fan.GetId(), err)
			return err
		}
	}

	if f.freshAnalysis {
		// saved once the analysis succeeded
		f.pendingPwmMap = true
		return nil
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
		// if we have a setPwmToGetPwmMap, we use the raw values the driver accepted (its keys) to compute the
		// pwmMap. Values that read back the same are one output level of the fan, so only one value per level is
		// used (see outputLevelValues). The levels are spread evenly over the internal range of [0..255] as steps,
		// so every target range selects another level and there are no target ranges without an effect.
		ui.Debug("Fan %s: Using setPwmToGetPwmMap to compute pwmMap", fan.GetId())
		levels := f.outputLevelValues()
		identityMappingOfLevels := make(map[int]int, len(levels))
		for _, value := range levels {
			identityMappingOfLevels[value] = value
		}
		pwmMap := util.ExpandMapToFullRange(identityMappingOfLevels, fans.MinPwmValue, fans.MaxPwmValue)

		for i := 0; i < 256; i++ {
			f.pwmMapping[i] = pwmMap[i]
		}
	}
	return err
}

// outputLevelValues returns the raw values the autodetected pwmMap uses: one per output level of the fan, sorted.
// The written values (keys of the setPwmToGetPwmMap) are grouped by the value they read back. Of each group, the
// value that reads back as itself is used if there is one, else the lowest one. For the readback formula of a
// known hardware quirks entry, the groups are the entry's output levels (quirks.Entry.OutputLevels).
//
// The grouping is only used if the readback looks like the output level of the fan: it doesn't decrease when the
// written value increases, and there are at least two levels. Otherwise, and for nvidia fans (whose PWM
// boundaries are percent values of the identity mapping), all accepted values are used.
func (f *DefaultFanController) outputLevelValues() []int {
	keys := util.SortedKeys(f.setPwmToGetPwmMap)
	if f.fan.GetConfig().Nvidia != nil {
		return keys
	}
	type level struct {
		readback int
		value    int
	}
	var levels []level
	for _, key := range keys {
		readback := f.setPwmToGetPwmMap[key]
		if len(levels) > 0 {
			current := &levels[len(levels)-1]
			if readback < current.readback {
				ui.Debug("Fan %s: the reported PWM decreases from %d to %d at %d, using every accepted value in the pwmMap",
					f.fan.GetId(), current.readback, readback, key)
				return keys
			}
			if readback == current.readback {
				if key == readback {
					current.value = key
				}
				continue
			}
		}
		levels = append(levels, level{readback: readback, value: key})
	}
	if len(levels) < 2 {
		ui.Debug("Fan %s: the reported PWM has fewer than two values, using every accepted value in the pwmMap", f.fan.GetId())
		return keys
	}
	result := make([]int, 0, len(levels))
	for _, l := range levels {
		result = append(result, l.value)
	}
	if len(result) < len(keys) {
		ui.Info("Fan %s: the driver reports %d distinct PWM values for %d accepted values, the pwmMap uses one value per level",
			f.fan.GetId(), len(result), len(keys))
	}
	return result
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
	// the sweep is meaningless if fan2go can't control the fan
	err = takeManualControl(f.fan)
	if err != nil {
		return err
	}

	// slow drivers report a written value only after a while: wait for it (see probeReadbackDelay)
	window, err := f.probeReadbackDelay(ctx)
	if err != nil {
		return err
	}
	previous, err := f.fan.GetPwm()
	if err != nil {
		previous = -1
	}

	setPwmToGetPwmMap := map[int]int{}
	for i := fans.MinPwmValue; i <= fans.MaxPwmValue; i++ {
		err := f.writePwm(i)
		if err != nil {
			ui.Warning("Error setting PWM value %d on fan %s: %v", i, f.fan.GetId(), err)
			continue
		}
		// wait until the readback changes, or for the window if it doesn't (e.g. several values have the same level)
		last := previous
		pwm, changed, delay, err := f.waitForReadback(ctx, func(pwm int) bool { return pwm != last }, window)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			ui.Warning("Error reading PWM value of fan %s: %v", f.fan.GetId(), err)
			continue
		}
		if changed {
			f.observeReadbackDelay(delay)
		}
		setPwmToGetPwmMap[i] = pwm
		previous = pwm
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
