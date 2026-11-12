package controller

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/control_loop"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/persistence"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePersistence is an in-memory persistence.Persistence, safe for concurrent use.
// The load*Errors queues inject errors: each Load call pops the next entry (if any); a nil entry
// (or an empty queue) means "load the stored data".
type fakePersistence struct {
	mu                 sync.Mutex
	rpmData            map[string]map[int]float64
	setPwmToGetPwmMaps map[string]map[int]int
	pwmMaps            map[string][]int
	originalStates     map[string]persistence.FanOriginalState
	analysisInfos      map[string]persistence.FanAnalysisInfo
	// errors for the next LoadFanAnalysisInfo calls (nil: no error)
	loadAnalysisInfoErrors []error

	loadRpmDataErrors           []error
	loadSetPwmToGetPwmMapErrors []error
	loadPwmMapErrors            []error
	// original state: errors for the next Load, Save or Delete call (nil: no error)
	loadOriginalStateErrors   []error
	saveOriginalStateErrors   []error
	deleteOriginalStateErrors []error
	// number of SaveFanOriginalState and DeleteFanOriginalState calls
	originalStateSaves   int
	originalStateDeletes int
}

func newFakePersistence() *fakePersistence {
	return &fakePersistence{
		rpmData:            map[string]map[int]float64{},
		setPwmToGetPwmMaps: map[string]map[int]int{},
		pwmMaps:            map[string][]int{},
		originalStates:     map[string]persistence.FanOriginalState{},
		analysisInfos:      map[string]persistence.FanAnalysisInfo{},
	}
}

func popError(queue *[]error) error {
	if len(*queue) == 0 {
		return nil
	}
	err := (*queue)[0]
	*queue = (*queue)[1:]
	return err
}

func (p *fakePersistence) Init() error { return nil }

func (p *fakePersistence) LoadFanRpmData(fan fans.Fan) (map[int]float64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := popError(&p.loadRpmDataErrors); err != nil {
		return nil, err
	}
	data, ok := p.rpmData[fan.GetId()]
	if !ok {
		return nil, os.ErrNotExist
	}
	return data, nil
}

func (p *fakePersistence) SaveFanRpmData(fan fans.Fan) error {
	var data map[int]float64
	if curveData := fan.GetFanRpmCurveData(); curveData != nil {
		data = *curveData
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rpmData[fan.GetId()] = data
	return nil
}

func (p *fakePersistence) DeleteFanRpmData(fan fans.Fan) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.rpmData, fan.GetId())
	return nil
}

func (p *fakePersistence) LoadFanSetPwmToGetPwmMap(fanId string) (map[int]int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := popError(&p.loadSetPwmToGetPwmMapErrors); err != nil {
		return nil, err
	}
	data, ok := p.setPwmToGetPwmMaps[fanId]
	if !ok {
		return nil, os.ErrNotExist
	}
	return data, nil
}

func (p *fakePersistence) SaveFanSetPwmToGetPwmMap(fanId string, pwmMap map[int]int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.setPwmToGetPwmMaps[fanId] = pwmMap
	return nil
}

func (p *fakePersistence) DeleteFanSetPwmToGetPwmMap(fanId string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.setPwmToGetPwmMaps, fanId)
	return nil
}

func (p *fakePersistence) LoadFanPwmMap(fanId string) ([]int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := popError(&p.loadPwmMapErrors); err != nil {
		return nil, err
	}
	data, ok := p.pwmMaps[fanId]
	if !ok {
		return nil, os.ErrNotExist
	}
	return data, nil
}

func (p *fakePersistence) SaveFanPwmMap(fanId string, pwmMapping []int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pwmMaps[fanId] = append([]int{}, pwmMapping...)
	return nil
}

func (p *fakePersistence) DeleteFanPwmMap(fanId string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.pwmMaps, fanId)
	return nil
}

func (p *fakePersistence) LoadFanOriginalState(fanId string) (persistence.FanOriginalState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := popError(&p.loadOriginalStateErrors); err != nil {
		return persistence.FanOriginalState{}, err
	}
	state, ok := p.originalStates[fanId]
	if !ok {
		return persistence.FanOriginalState{}, os.ErrNotExist
	}
	return state, nil
}

func (p *fakePersistence) LoadFanOriginalStates() (map[string]persistence.FanOriginalState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := map[string]persistence.FanOriginalState{}
	for id, state := range p.originalStates {
		result[id] = state
	}
	return result, nil
}

func (p *fakePersistence) SaveFanOriginalState(fanId string, state persistence.FanOriginalState) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.originalStateSaves++
	if err := popError(&p.saveOriginalStateErrors); err != nil {
		return err
	}
	p.originalStates[fanId] = state
	return nil
}

func (p *fakePersistence) DeleteFanOriginalState(fanId string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.originalStateDeletes++
	if err := popError(&p.deleteOriginalStateErrors); err != nil {
		return err
	}
	delete(p.originalStates, fanId)
	return nil
}

func (p *fakePersistence) LoadFanAnalysisInfo(fanId string) (persistence.FanAnalysisInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := popError(&p.loadAnalysisInfoErrors); err != nil {
		return persistence.FanAnalysisInfo{}, err
	}
	info, ok := p.analysisInfos[fanId]
	if !ok {
		return persistence.FanAnalysisInfo{}, os.ErrNotExist
	}
	return info, nil
}

func (p *fakePersistence) SaveFanAnalysisInfo(fanId string, info persistence.FanAnalysisInfo) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.analysisInfos[fanId] = info
	return nil
}

func (p *fakePersistence) DeleteFanAnalysisInfo(fanId string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.analysisInfos, fanId)
	return nil
}

// originalState returns the saved original state of the fan, and false if there is none.
func (p *fakePersistence) originalState(fanId string) (persistence.FanOriginalState, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	state, ok := p.originalStates[fanId]
	return state, ok
}

func (p *fakePersistence) hasRpmData(fanId string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.rpmData[fanId]
	return ok
}

// writeLog records the hardware writes of several fans in order.
type writeLog struct {
	mu     sync.Mutex
	writes []string
}

func (l *writeLog) add(write string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.writes = append(l.writes, write)
}

func (l *writeLog) get() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string{}, l.writes...)
}

// initTestFan is a hwmon-like MockFan in automatic mode without RPM sensor, so its initialization
// only consists of the setPwm -> getPwm detection (a sweep through all PWM values), which doesn't
// wait on the clock with pwmSetDelay 0. Its writes are recorded in log as "<id>:pwm" or "<id>:mode".
type initTestFan struct {
	MockFan
	log *writeLog

	// closed on the first write
	firstWrite     chan struct{}
	firstWriteOnce sync.Once
	// if set, the first SetPwm call closes blockedInSetPwm and waits until releaseSetPwm is closed
	releaseSetPwm   chan struct{}
	blockedInSetPwm chan struct{}
	blockOnce       sync.Once
	// if set, SetPwm panics
	panicOnSetPwm bool
	// errors returned by the next AttachFanRpmCurveData calls, in order (nil: attach the data)
	attachErrorsMu sync.Mutex
	attachErrors   []error
}

func newInitTestFan(id string, log *writeLog) *initTestFan {
	return &initTestFan{
		MockFan: MockFan{
			ID:          id,
			ControlMode: fans.ControlModeAutomatic,
			PWM:         100,
			MaxPWM:      255,
		},
		log:        log,
		firstWrite: make(chan struct{}),
	}
}

func (fan *initTestFan) wrote(kind string) {
	fan.log.add(fan.GetId() + ":" + kind)
	fan.firstWriteOnce.Do(func() { close(fan.firstWrite) })
}

func (fan *initTestFan) SetPwm(pwm int) error {
	if fan.panicOnSetPwm {
		panic("SetPwm exploded")
	}
	if fan.releaseSetPwm != nil {
		fan.blockOnce.Do(func() {
			close(fan.blockedInSetPwm)
			<-fan.releaseSetPwm
		})
	}
	fan.wrote("pwm")
	return fan.MockFan.SetPwm(pwm)
}

func (fan *initTestFan) SetControlMode(mode fans.ControlMode) error {
	fan.wrote("mode")
	return fan.MockFan.SetControlMode(mode)
}

func (fan *initTestFan) AttachFanRpmCurveData(curveData *map[int]float64) error {
	fan.attachErrorsMu.Lock()
	err := popError(&fan.attachErrors)
	fan.attachErrorsMu.Unlock()
	if err != nil {
		return err
	}
	return fan.MockFan.AttachFanRpmCurveData(curveData)
}

func (fan *initTestFan) GetConfig() configuration.FanConfig {
	config := fan.MockFan.GetConfig()
	// a hwmon fan, so a missing fan curve triggers the initialization
	config.HwMon = &configuration.HwMonFanConfig{}
	return config
}

func (fan *initTestFan) Supports(feature fans.FeatureFlag) bool {
	return feature != fans.FeatureRpmSensor
}

// blockInFirstSetPwm makes the first SetPwm call of the fan block until the returned function is called
// (at the end of the test at the latest). blockedInSetPwm is closed once it blocks.
func (fan *initTestFan) blockInFirstSetPwm(t *testing.T) (release func()) {
	fan.releaseSetPwm = make(chan struct{})
	fan.blockedInSetPwm = make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(fan.releaseSetPwm) }) }
	t.Cleanup(release)
	return release
}

// holdInitializationLock acquires the initialization lock, as if another fan was being initialized,
// until the returned function is called (at the end of the test at the latest).
func holdInitializationLock(t *testing.T) (release func()) {
	initializationSequenceLock <- struct{}{}
	var once sync.Once
	release = func() { once.Do(func() { <-initializationSequenceLock }) }
	t.Cleanup(release)
	return release
}

func setUpInitTestConfig(runInParallel bool) func() {
	originalConfig := configuration.CurrentConfig
	configuration.CurrentConfig.RunFanInitializationInParallel = runInParallel
	configuration.CurrentConfig.TempSensorPollingRate = time.Millisecond
	configuration.CurrentConfig.RpmPollingRate = time.Hour
	configuration.CurrentConfig.FanController.PwmSetDelay = 0
	configuration.CurrentConfig.RpmRollingWindowSize = 10
	return func() {
		configuration.CurrentConfig = originalConfig
	}
}

func newInitTestController(p persistence.Persistence, fan fans.Fan) *DefaultFanController {
	curveValue := 100.0
	return NewFanController(p, fan, MockCurve{ID: "curve", Value: &curveValue}, control_loop.NewDirectControlLoop(nil), time.Millisecond, false)
}

func startInitialization(controller *DefaultFanController, ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := controller.RunInitialization(ctx)
		done <- err
	}()
	return done
}

func await(t *testing.T, c <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// waitHook installs initializationLockWaitHook for the duration of the test and returns
// the channel it reports the waiting fans to.
func waitHook(t *testing.T) <-chan string {
	waiting := make(chan string, 10)
	initializationLockWaitHook = func(fanId string) { waiting <- fanId }
	t.Cleanup(func() { initializationLockWaitHook = nil })
	return waiting
}

func indexOfFirst(writes []string, prefix string) int {
	for i, w := range writes {
		if len(w) >= len(prefix) && w[:len(prefix)] == prefix {
			return i
		}
	}
	return -1
}

func indexOfLast(writes []string, prefix string) int {
	for i := len(writes) - 1; i >= 0; i-- {
		if len(writes[i]) >= len(prefix) && writes[i][:len(prefix)] == prefix {
			return i
		}
	}
	return -1
}

func TestRunInitialization_NotInParallel_SecondFanWaitsForFirst(t *testing.T) {
	defer setUpInitTestConfig(false)()
	waiting := waitHook(t)

	// GIVEN: two fans that have to be initialized, the first one blocks in the middle of its initialization
	log := &writeLog{}
	fanA := newInitTestFan("a", log)
	releaseFanA := fanA.blockInFirstSetPwm(t)
	fanB := newInitTestFan("b", log)
	p := newFakePersistence()
	controllerA := newInitTestController(p, fanA)
	controllerB := newInitTestController(p, fanB)

	// WHEN
	doneA := startInitialization(controllerA, context.Background())
	await(t, fanA.blockedInSetPwm, "fan a to be initialized")
	doneB := startInitialization(controllerB, context.Background())

	// THEN: fan b waits, without touching the fan, until fan a is done
	select {
	case id := <-waiting:
		assert.Equal(t, "b", id)
	case <-fanB.firstWrite:
		t.Fatal("fan b was initialized while fan a was still being initialized")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for fan b")
	}
	releaseFanA()
	require.NoError(t, awaitError(t, doneA))
	require.NoError(t, awaitError(t, doneB))

	writes := log.get()
	assert.Less(t, indexOfLast(writes, "a:"), indexOfFirst(writes, "b:"),
		"all writes of fan a must happen before the first write of fan b")
	assert.True(t, p.hasRpmData("a"))
	assert.True(t, p.hasRpmData("b"))
}

func TestRunInitialization_InParallel_FansAreInitializedConcurrently(t *testing.T) {
	defer setUpInitTestConfig(true)()
	waiting := waitHook(t)

	// GIVEN
	log := &writeLog{}
	fanA := newInitTestFan("a", log)
	releaseFanA := fanA.blockInFirstSetPwm(t)
	fanB := newInitTestFan("b", log)
	p := newFakePersistence()
	controllerA := newInitTestController(p, fanA)
	controllerB := newInitTestController(p, fanB)

	// WHEN
	doneA := startInitialization(controllerA, context.Background())
	await(t, fanA.blockedInSetPwm, "fan a to be initialized")
	doneB := startInitialization(controllerB, context.Background())

	// THEN: fan b doesn't wait for fan a
	await(t, fanB.firstWrite, "fan b to be initialized")
	require.NoError(t, awaitError(t, doneB))
	releaseFanA()
	require.NoError(t, awaitError(t, doneA))
	assert.Empty(t, waiting)
}

func TestComputeFanSpecificMappings_NotInParallel_SweepWaitsForOtherFan(t *testing.T) {
	defer setUpInitTestConfig(false)()
	waiting := waitHook(t)

	// GIVEN: another fan is being initialized, and this one has no setPwm -> getPwm map yet,
	// so it would sweep through all PWM values
	releaseLock := holdInitializationLock(t)
	log := &writeLog{}
	fan := newInitTestFan("fan", log)
	controller := newInitTestController(newFakePersistence(), fan)

	// WHEN
	done := make(chan error, 1)
	go func() { done <- controller.computeFanSpecificMappings(context.Background()) }()

	// THEN: the sweep waits for the other fan
	select {
	case id := <-waiting:
		assert.Equal(t, "fan", id)
	case <-fan.firstWrite:
		t.Fatal("the fan was swept while another fan was being initialized")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the fan")
	}
	releaseLock()
	require.NoError(t, awaitError(t, done))
	assert.NotEmpty(t, log.get())
}

func TestComputeFanSpecificMappings_NotInParallel_SavedMappingsDontWait(t *testing.T) {
	defer setUpInitTestConfig(false)()
	waiting := waitHook(t)

	// GIVEN: another fan is being initialized, and this one has saved mappings
	holdInitializationLock(t)
	log := &writeLog{}
	fan := newInitTestFan("fan", log)
	p := newFakePersistence()
	identity := make([]int, 256)
	setToGet := map[int]int{}
	for i := range identity {
		identity[i] = i
		setToGet[i] = i
	}
	require.NoError(t, p.SaveFanPwmMap("fan", identity))
	require.NoError(t, p.SaveFanSetPwmToGetPwmMap("fan", setToGet))
	controller := newInitTestController(p, fan)

	// WHEN
	err := controller.computeFanSpecificMappings(context.Background())

	// THEN: nothing moves the fan, so there is nothing to wait for
	assert.NoError(t, err)
	assert.Empty(t, waiting)
	assert.Empty(t, log.get())
}

func TestRunInitialization_NotInParallel_CancelledWhileWaiting(t *testing.T) {
	defer setUpInitTestConfig(false)()
	waiting := waitHook(t)

	// GIVEN: another fan is being initialized
	holdInitializationLock(t)
	log := &writeLog{}
	fan := newInitTestFan("fan", log)
	controller := newInitTestController(newFakePersistence(), fan)
	ctx, cancel := context.WithCancel(context.Background())

	// WHEN: fan2go stops while this fan waits
	done := startInitialization(controller, ctx)
	select {
	case <-waiting:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the fan")
	}
	cancel()

	// THEN
	err := awaitError(t, done)
	assert.True(t, errors.Is(err, context.Canceled), "expected context.Canceled, got %v", err)
	assert.Empty(t, log.get(), "the fan was never touched, so nothing must be written to it")
}

func awaitError(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
		return nil
	}
}

func TestRunInitializationIfNeeded_PersistenceError_DoesNotInitialize(t *testing.T) {
	defer setUpInitTestConfig(true)()

	// GIVEN: the fan curve data can't be read (e.g. the database is locked by another process)
	dbErr := errors.New("timeout")
	log := &writeLog{}
	fan := newInitTestFan("fan", log)
	p := newFakePersistence()
	p.loadRpmDataErrors = []error{dbErr}
	controller := newInitTestController(p, fan)

	// WHEN
	_, err := controller.runInitializationIfNeeded(context.Background())

	// THEN: the error is returned, and the fan is not analyzed again
	assert.True(t, errors.Is(err, dbErr), "expected the persistence error, got %v", err)
	assert.Empty(t, log.get())
	assert.False(t, p.hasRpmData("fan"))
}

func TestRunInitializationIfNeeded_CorruptData_Initializes(t *testing.T) {
	defer setUpInitTestConfig(true)()

	// GIVEN: the stored fan curve data is corrupt
	log := &writeLog{}
	fan := newInitTestFan("fan", log)
	p := newFakePersistence()
	p.loadRpmDataErrors = []error{persistence.ErrCorruptData}
	controller := newInitTestController(p, fan)

	// WHEN
	_, err := controller.runInitializationIfNeeded(context.Background())

	// THEN: it is treated like missing data
	assert.NoError(t, err)
	assert.NotEmpty(t, log.get())
	assert.True(t, p.hasRpmData("fan"))
}

func TestComputeSetPwmToGetPwmMap_PersistenceError_DoesNotSweep(t *testing.T) {
	defer setUpInitTestConfig(true)()

	// GIVEN
	dbErr := errors.New("timeout")
	log := &writeLog{}
	fan := newInitTestFan("fan", log)
	p := newFakePersistence()
	p.loadSetPwmToGetPwmMapErrors = []error{dbErr}
	controller := newInitTestController(p, fan)

	// WHEN
	err := controller.computeSetPwmToGetPwmMap(context.Background())

	// THEN
	assert.True(t, errors.Is(err, dbErr), "expected the persistence error, got %v", err)
	assert.Empty(t, log.get())
}

func TestComputeSetPwmToGetPwmMap_CorruptData_Sweeps(t *testing.T) {
	defer setUpInitTestConfig(true)()

	// GIVEN
	log := &writeLog{}
	fan := newInitTestFan("fan", log)
	p := newFakePersistence()
	p.loadSetPwmToGetPwmMapErrors = []error{persistence.ErrCorruptData}
	controller := newInitTestController(p, fan)

	// WHEN
	err := controller.computeSetPwmToGetPwmMap(context.Background())

	// THEN
	assert.NoError(t, err)
	assert.NotEmpty(t, log.get())
	assert.Len(t, controller.setPwmToGetPwmMap, 256)
}

func TestComputePwmMap_PersistenceError(t *testing.T) {
	defer setUpInitTestConfig(true)()

	// GIVEN
	dbErr := errors.New("timeout")
	fan := newInitTestFan("fan", &writeLog{})
	p := newFakePersistence()
	p.loadPwmMapErrors = []error{dbErr}
	controller := newInitTestController(p, fan)

	// WHEN
	err := controller.computePwmMap()

	// THEN: the saved pwmMap is not silently replaced by a computed one
	assert.True(t, errors.Is(err, dbErr), "expected the persistence error, got %v", err)
	_, loadErr := p.LoadFanPwmMap("fan")
	assert.True(t, errors.Is(loadErr, os.ErrNotExist))
}

// runUntilError runs controller.Run, advances the fake clock through the startup warm-up and
// returns the error Run returns.
func runUntilError(t *testing.T, controller *DefaultFanController) error {
	t.Helper()
	clock := util.NewFakeClock(time.Unix(1000, 0))
	controller.SetClock(clock)
	done := startRun(controller, context.Background())
	clock.BlockUntil(1)
	clock.Advance(defaultStartupSensorWarmup + configuration.CurrentConfig.TempSensorPollingRate)
	return awaitRun(t, done)
}

func TestFanController_Run_ErrorAfterInitialization_RestoresFan(t *testing.T) {
	attachErr := errors.New("attach failed")
	pwmMapErr := errors.New("pwm map load failed")

	tests := map[string]struct {
		setUp       func(p *fakePersistence, fan *initTestFan)
		expectedErr error
	}{
		"attaching the fan curve data fails": {
			setUp: func(p *fakePersistence, fan *initTestFan) {
				// 1st attach: during the initialization, 2nd: in Run
				fan.attachErrors = []error{nil, attachErr}
			},
			expectedErr: attachErr,
		},
		"computing the fan specific mappings fails": {
			setUp: func(p *fakePersistence, fan *initTestFan) {
				// 1st load: during the initialization (no data), 2nd: in Run
				p.loadPwmMapErrors = []error{nil, pwmMapErr}
			},
			expectedErr: pwmMapErr,
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			defer setUpInitTestConfig(true)()

			// GIVEN: a fan in automatic mode that has to be initialized, which leaves it in manual mode
			fan := newInitTestFan("fan", &writeLog{})
			p := newFakePersistence()
			tt.setUp(p, fan)
			controller := newInitTestController(p, fan)

			// WHEN: the initialization succeeds, but a later step of the startup fails
			err := runUntilError(t, controller)

			// THEN: the fan is handed back to automatic control
			assert.True(t, errors.Is(err, tt.expectedErr), "expected %v, got %v", tt.expectedErr, err)
			assert.True(t, p.hasRpmData("fan"), "the initialization must have run")
			mode, _ := fan.GetControlMode()
			assert.Equal(t, fans.ControlModeAutomatic, mode)
		})
	}
}

func TestFanController_Run_PersistenceErrorBeforeWriting_LeavesFanAlone(t *testing.T) {
	defer setUpInitTestConfig(true)()

	// GIVEN: a fan with an explicit speed on exit, whose fan curve data can't be read
	dbErr := errors.New("timeout")
	log := &writeLog{}
	fan := newInitTestFan("fan", log)
	speed := 200
	fan.ControlModeConfig = &configuration.ControlModeConfig{OnExit: &configuration.OnExitConfig{Speed: &speed}}
	p := newFakePersistence()
	p.loadRpmDataErrors = []error{dbErr}
	controller := newInitTestController(p, fan)

	// WHEN
	err := runUntilError(t, controller)

	// THEN: fan2go never wrote to the fan, so it doesn't "restore" anything either
	assert.True(t, errors.Is(err, dbErr), "expected the persistence error, got %v", err)
	assert.Empty(t, log.get())
}

func TestRunInitializationAndRestore_Success_RestoresFan(t *testing.T) {
	defer setUpInitTestConfig(true)()

	// GIVEN
	fan := newInitTestFan("fan", &writeLog{})
	p := newFakePersistence()
	controller := newInitTestController(p, fan)

	// WHEN
	_, err := controller.RunInitializationAndRestore(context.Background())

	// THEN: the analysis left the fan in manual mode, it is handed back afterwards
	assert.NoError(t, err)
	assert.True(t, p.hasRpmData("fan"))
	mode, _ := fan.GetControlMode()
	assert.Equal(t, fans.ControlModeAutomatic, mode)
}

func TestRunInitializationAndRestore_Panic_RestoresFanAndReturnsError(t *testing.T) {
	defer setUpInitTestConfig(true)()

	// GIVEN: a fan that panics during the initialization, after it was switched to manual mode
	fan := newInitTestFan("fan", &writeLog{})
	fan.panicOnSetPwm = true
	controller := newInitTestController(newFakePersistence(), fan)

	// WHEN
	_, err := controller.RunInitializationAndRestore(context.Background())

	// THEN
	require.Error(t, err)
	assert.Contains(t, err.Error(), "panic in fan initialization")
	mode, _ := fan.GetControlMode()
	assert.Equal(t, fans.ControlModeAutomatic, mode)
}

func TestRunInitializationIfNeeded_IsInitializingWhileWaitingAndAnalyzing(t *testing.T) {
	defer setUpInitTestConfig(false)()
	waiting := waitHook(t)

	// GIVEN: a fan without saved data, and another fan that is being initialized
	releaseLock := holdInitializationLock(t)
	log := &writeLog{}
	fan := newInitTestFan("fan", log)
	releaseSetPwm := fan.blockInFirstSetPwm(t)
	controller := newInitTestController(newFakePersistence(), fan)
	assert.False(t, controller.IsInitializing())

	// WHEN
	done := make(chan error, 1)
	go func() {
		_, err := controller.runInitializationIfNeeded(context.Background())
		done <- err
	}()

	// THEN: it counts as initializing while it waits for the other fan
	select {
	case <-waiting:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the fan")
	}
	assert.True(t, controller.IsInitializing())

	// and while it analyzes the fan
	releaseLock()
	await(t, fan.blockedInSetPwm, "the analysis")
	assert.True(t, controller.IsInitializing())

	// but not anymore once the analysis finished
	releaseSetPwm()
	require.NoError(t, awaitError(t, done))
	assert.False(t, controller.IsInitializing())
}

// The setPwm -> getPwm sweep also runs on its own (saved RPM data, but no saved map), and counts as initializing.
func TestComputeFanSpecificMappings_IsInitializingDuringSweep(t *testing.T) {
	defer setUpInitTestConfig(true)()
	log := &writeLog{}
	fan := newInitTestFan("fan", log)
	releaseSetPwm := fan.blockInFirstSetPwm(t)
	controller := newInitTestController(newFakePersistence(), fan)

	done := make(chan error, 1)
	go func() { done <- controller.computeFanSpecificMappings(context.Background()) }()

	await(t, fan.blockedInSetPwm, "the sweep")
	assert.True(t, controller.IsInitializing())
	releaseSetPwm()
	require.NoError(t, awaitError(t, done))
	assert.False(t, controller.IsInitializing())
}
