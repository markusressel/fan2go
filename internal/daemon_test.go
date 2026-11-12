package internal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/controller"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validTestConfig is a minimal valid config file (dbPath: valid.db)
const validTestConfig = `
dbPath: valid.db
sensors:
  - id: sensor
    file:
      path: /dev/null
curves:
  - id: curve
    linear:
      sensor: sensor
      steps:
        - 40: 0
        - 80: 255
fans:
  - id: fan
    curve: curve
    file:
      path: /dev/null
`

// invalidTestConfig is a config file that loads, but fails validation
// (adjustmentTickRate 0 would make time.NewTicker panic in the fan controller)
const invalidTestConfig = `
fanController:
  adjustmentTickRate: 0s
` + validTestConfig

// useConfigFile writes content to a new config file and makes viper use it (until the end of the test).
// Returns the path of the file.
func useConfigFile(t *testing.T, content string) string {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "fan2go.yaml")
	writeConfigFile(t, configPath, content)
	viper.Reset()
	t.Cleanup(viper.Reset)
	configuration.InitConfig(configPath)
	return configPath
}

func writeConfigFile(t *testing.T, path string, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// keepCurrentConfig restores configuration.CurrentConfig at the end of the test.
func keepCurrentConfig(t *testing.T) {
	oldConfig := configuration.CurrentConfig
	t.Cleanup(func() { configuration.CurrentConfig = oldConfig })
}

// captureNotifications replaces notifyError and notifyWarning until the end of the test, and returns
// the channel the notified messages are sent to.
func captureNotifications(t *testing.T) <-chan string {
	notified := make(chan string, 100)
	capture := func(title string, format string, a ...any) {
		notified <- title + ": " + fmt.Sprintf(format, a...)
	}
	oldError, oldWarning := notifyError, notifyWarning
	notifyError, notifyWarning = capture, capture
	t.Cleanup(func() { notifyError, notifyWarning = oldError, oldWarning })
	return notified
}

func receive[T any](t *testing.T, c <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-c:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		var zero T
		return zero
	}
}

// awaitDeferred waits until waitForReload deferred the reload (it created its ticker).
func awaitDeferred(t *testing.T, clock *util.FakeClock) {
	t.Helper()
	require.Eventually(t, func() bool { return clock.Waiters() >= 1 }, 5*time.Second, time.Millisecond, "the reload was not deferred")
}

func drain(c chan struct{}) {
	for {
		select {
		case <-c:
		default:
			return
		}
	}
}

func TestLoadNewConfiguration_InvalidConfigKeepsCurrentConfig(t *testing.T) {
	// GIVEN a config file that loads, but fails validation
	useConfigFile(t, invalidTestConfig)
	keepCurrentConfig(t)
	configuration.CurrentConfig = configuration.Configuration{DbPath: "current"}
	configuration.CurrentConfig.FanController.AdjustmentTickRate = 200 * time.Millisecond

	// WHEN
	_, err := loadNewConfiguration()

	// THEN
	assert.ErrorContains(t, err, "fanController.adjustmentTickRate must be > 0")
	assert.Equal(t, "current", configuration.CurrentConfig.DbPath)
	assert.Equal(t, 200*time.Millisecond, configuration.CurrentConfig.FanController.AdjustmentTickRate)
}

func TestLoadNewConfiguration_ValidConfigDoesNotChangeCurrentConfig(t *testing.T) {
	useConfigFile(t, validTestConfig)
	keepCurrentConfig(t)
	configuration.CurrentConfig = configuration.Configuration{DbPath: "current"}

	newConfig, err := loadNewConfiguration()

	require.NoError(t, err)
	assert.Equal(t, "valid.db", newConfig.DbPath)
	assert.Equal(t, "current", configuration.CurrentConfig.DbPath)
}

// fakeReloadController is a FanController whose initialization state is set by the test.
type fakeReloadController struct {
	controller.FanController
	id           string
	initializing atomic.Bool
	// receives a value after each IsInitializing call
	checked chan struct{}
}

func newFakeReloadController(id string, initializing bool) *fakeReloadController {
	c := &fakeReloadController{id: id, checked: make(chan struct{}, 100)}
	c.initializing.Store(initializing)
	return c
}

func (c *fakeReloadController) GetFanId() string { return c.id }

func (c *fakeReloadController) IsInitializing() bool {
	initializing := c.initializing.Load()
	c.checked <- struct{}{}
	return initializing
}

type waitForReloadResult struct {
	config     *configuration.Configuration
	appExiting bool
}

func startWaitForReload(ctx context.Context, reloadChan <-chan struct{}, controllers map[fans.Fan]controller.FanController, clock util.Clock) <-chan waitForReloadResult {
	done := make(chan waitForReloadResult, 1)
	go func() {
		config, appExiting := waitForReload(ctx, reloadChan, controllers, clock)
		done <- waitForReloadResult{config, appExiting}
	}()
	return done
}

func controllersOf(controllers ...*fakeReloadController) map[fans.Fan]controller.FanController {
	result := map[fans.Fan]controller.FanController{}
	for _, c := range controllers {
		result[&fans.FileFan{Config: configuration.FanConfig{ID: c.id}}] = c
	}
	return result
}

// An invalid config doesn't end the cycle, so the fans stay controlled by the current config.
func TestWaitForReload_InvalidConfig_KeepsCycleRunning(t *testing.T) {
	notified := captureNotifications(t)
	configPath := useConfigFile(t, invalidTestConfig)
	reloadChan := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startWaitForReload(ctx, reloadChan, controllersOf(newFakeReloadController("fan", false)), util.NewFakeClock(time.Unix(1000, 0)))

	// WHEN
	reloadChan <- struct{}{}

	// THEN: the user is notified, and the cycle keeps running
	assert.Contains(t, receive(t, notified, "the notification"), "adjustmentTickRate")
	assert.Empty(t, done, "an invalid config must not end the cycle")

	// WHEN: the config is fixed
	writeConfigFile(t, configPath, validTestConfig)
	reloadChan <- struct{}{}

	// THEN: the cycle ends with the new config
	result := receive(t, done, "waitForReload")
	assert.False(t, result.appExiting)
	require.NotNil(t, result.config)
	assert.Equal(t, "valid.db", result.config.DbPath)
}

func TestWaitForReload_AppExit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := startWaitForReload(ctx, make(chan struct{}), controllersOf(newFakeReloadController("fan", false)), util.NewFakeClock(time.Unix(1000, 0)))

	cancel()

	result := receive(t, done, "waitForReload")
	assert.True(t, result.appExiting)
	assert.Nil(t, result.config)
}

// A reload during the analysis of a fan waits until the analysis finished, instead of starting it over.
func TestWaitForReload_DeferredWhileAFanIsInitializing(t *testing.T) {
	captureNotifications(t)
	useConfigFile(t, validTestConfig)
	clock := util.NewFakeClock(time.Unix(1000, 0))
	reloadChan := make(chan struct{}, 1)
	initializing := newFakeReloadController("initializing", true)
	idle := newFakeReloadController("idle", false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startWaitForReload(ctx, reloadChan, controllersOf(initializing, idle), clock)

	// WHEN: a valid config arrives while a fan is analyzed
	reloadChan <- struct{}{}

	// THEN: the reload is deferred: it checks again on every tick
	awaitDeferred(t, clock)
	assert.Empty(t, done)
	for range 3 {
		drain(initializing.checked)
		clock.Advance(deferredReloadCheckInterval)
		receive(t, initializing.checked, "the next check")
		assert.Empty(t, done, "the reload must wait for the analysis")
	}

	// WHEN: the analysis finished
	initializing.initializing.Store(false)
	clock.Advance(deferredReloadCheckInterval)

	// THEN: the deferred config is applied
	result := receive(t, done, "waitForReload")
	assert.False(t, result.appExiting)
	require.NotNil(t, result.config)
	assert.Equal(t, "valid.db", result.config.DbPath)
}

func TestWaitForReload_Deferred_AppExitDoesNotWait(t *testing.T) {
	captureNotifications(t)
	useConfigFile(t, validTestConfig)
	clock := util.NewFakeClock(time.Unix(1000, 0))
	reloadChan := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := startWaitForReload(ctx, reloadChan, controllersOf(newFakeReloadController("fan", true)), clock)
	reloadChan <- struct{}{}
	awaitDeferred(t, clock)

	cancel()

	result := receive(t, done, "waitForReload")
	assert.True(t, result.appExiting)
}

// A reload request while a reload is deferred reads the file again: the newest valid config is applied,
// and an invalid one drops the deferred config.
func TestWaitForReload_Deferred_NewRequestReplacesOrDropsTheDeferredConfig(t *testing.T) {
	notified := captureNotifications(t)
	configPath := useConfigFile(t, validTestConfig)
	clock := util.NewFakeClock(time.Unix(1000, 0))
	reloadChan := make(chan struct{}, 1)
	fan := newFakeReloadController("fan", true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startWaitForReload(ctx, reloadChan, controllersOf(fan), clock)
	reloadChan <- struct{}{}
	awaitDeferred(t, clock)

	// WHEN: the file becomes invalid, and then the analysis finishes
	writeConfigFile(t, configPath, invalidTestConfig)
	reloadChan <- struct{}{}
	assert.Contains(t, receive(t, notified, "the notification"), "deferred reload")
	fan.initializing.Store(false)
	// THEN: nothing is deferred anymore (no ticker), so nothing is applied when the analysis finishes
	require.Eventually(t, func() bool { return clock.Waiters() == 0 }, 5*time.Second, time.Millisecond)
	assert.Empty(t, done)

	// WHEN: a newer valid config arrives
	writeConfigFile(t, configPath, strings.Replace(validTestConfig, "valid.db", "newest.db", 1))
	reloadChan <- struct{}{}

	// THEN: the newest config is applied, never the dropped one
	result := receive(t, done, "waitForReload")
	require.NotNil(t, result.config)
	assert.Equal(t, "newest.db", result.config.DbPath)
}

func TestInitializingFans_ReturnsSortedIds(t *testing.T) {
	controllers := controllersOf(
		newFakeReloadController("b", true),
		newFakeReloadController("c", false),
		newFakeReloadController("a", true),
	)

	assert.Equal(t, []string{"a", "b"}, initializingFans(controllers))
}

func TestSettingsThatNeedARestart(t *testing.T) {
	startup := configuration.Configuration{
		DbPath:    "/var/lib/fan2go/fan2go.db",
		Profiling: configuration.ProfilingConfig{Enabled: true, Host: "127.0.0.1", Port: 6060},
	}
	tests := []struct {
		name   string
		change func(c *configuration.Configuration)
		want   []string
	}{
		{"unchanged", func(c *configuration.Configuration) {}, nil},
		{"other keys", func(c *configuration.Configuration) { c.RpmPollingRate = time.Hour }, nil},
		{"dbPath", func(c *configuration.Configuration) { c.DbPath = "/tmp/other.db" }, []string{"dbPath"}},
		{"profiling", func(c *configuration.Configuration) {
			c.Profiling = configuration.ProfilingConfig{Enabled: false, Host: "0.0.0.0", Port: 1}
		}, []string{"profiling.enabled", "profiling.host", "profiling.port"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newConfig := startup
			tt.change(&newConfig)
			assert.Equal(t, tt.want, settingsThatNeedARestart(startup, newConfig))
		})
	}

	// the address of a profiling webserver that doesn't run doesn't matter
	disabled := configuration.Configuration{Profiling: configuration.ProfilingConfig{Host: "127.0.0.1", Port: 6060}}
	newConfig := disabled
	newConfig.Profiling.Port = 1
	assert.Empty(t, settingsThatNeedARestart(disabled, newConfig))
}
