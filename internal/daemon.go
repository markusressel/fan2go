package internal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/controller"
	"github.com/markusressel/fan2go/internal/curves"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/persistence"
	"github.com/markusressel/fan2go/internal/registry"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/oklog/run"
)

// RunDaemon runs the daemon until it is stopped by SIGINT/SIGTERM (nil) or fails (an error).
// Errors caused by the config are configuration.ConfigErrors.
func RunDaemon() error {
	checkProcessOwner()

	pers := persistence.NewPersistence(configuration.CurrentConfig.DbPath)
	// only one process may control the fans of a database: refuse to run next to `fan2go fan init` or another daemon
	if err := pers.Init(); err != nil {
		return fmt.Errorf("unable to create the database directory: %w", err)
	}
	releaseLock, err := persistence.AcquireProcessLock(configuration.CurrentConfig.DbPath)
	if err != nil {
		return fmt.Errorf("unable to start: %w (e.g. 'fan2go fan init', a 'fan2go fan speed/mode' that is "+
			"writing, or a second daemon); stop it first", err)
	}
	defer releaseLock()

	fanMap, reg, err := InitializeObjects()
	if err != nil {
		// all errors returned here are caused by the config, missing hardware is skipped
		return configuration.NewConfigError(fmt.Errorf("error initializing objects: %w", err))
	}

	fanControllers, evaluator, err := initializeFanControllers(pers, fanMap, reg)
	if err != nil {
		// errors caused by the config (the curve evaluator) are ConfigErrors already, others (e.g. the
		// statistics collectors can't be registered) are not
		return fmt.Errorf("error initializing fan controllers: %w", err)
	}

	forgetOriginalStatesOfUnconfiguredFans(pers, configuration.CurrentConfig.Fans)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reloadChan := make(chan struct{}, 1)

	// subscribe to the signals before any fan controller is started, so a SIGTERM never kills
	// the process with its default action after a fan has been taken over
	sig := subscribeSignals()

	var g run.Group

	if configuration.CurrentConfig.Profiling.Enabled {
		profilingConfig := configuration.CurrentConfig.Profiling
		profilingAddress := webserverAddress(profilingConfig.Host, profilingConfig.Port)
		g.Add(
			func() error { return runProfilingWebserver(ctx, profilingAddress) },
			func(err error) { /* handled by context cancellation */ },
		)
	}

	// === ACTOR 1: Orchestrator ===
	g.Add(
		func() error { return runOrchestrator(ctx, reloadChan, pers, reg, fanControllers, evaluator) },
		func(err error) { cancel() },
	)

	// === ACTOR 2: Signal Handler ===
	g.Add(
		func() error { return handleSignals(ctx, sig, reloadChan) },
		func(err error) { cancel() },
	)

	// === ACTOR 3: Config File Watcher ===
	g.Add(
		func() error { return runFileWatcher(ctx, configuration.GetFilePath(), reloadChan) },
		func(err error) { /* handled by context cancellation */ },
	)

	err = g.Run()

	cancel()
	if err != nil {
		return err
	}

	ui.Info("Done.")
	return nil
}

// forgetOriginalStatesOfUnconfiguredFans deletes the saved original states (see controller.FanStateSnapshot)
// of fans that are not in the config anymore (fans that are configured, but couldn't be created, keep theirs).
// Such a state means that fan2go didn't hand the fan back completely the last time it ran (e.g. it was killed),
// so it is logged as a warning, once. Controllers delete the state of their own fan
// when they hand it back, so this is only needed at startup. Best effort: errors are logged.
func forgetOriginalStatesOfUnconfiguredFans(pers persistence.Persistence, fanConfigs []configuration.FanConfig) {
	if err := pers.Init(); err != nil {
		ui.Warning("Cannot check the saved original fan states: %v", err)
		return
	}
	states, err := pers.LoadFanOriginalStates()
	if err != nil {
		ui.Warning("Cannot check the saved original fan states: %v", err)
		return
	}
	configured := map[string]bool{}
	for _, fanConfig := range fanConfigs {
		configured[fanConfig.ID] = true
	}
	for id, state := range states {
		if configured[id] {
			continue
		}
		ui.Warning("Fan %s is not configured anymore, but fan2go didn't hand it back completely the last time it ran (killed, crashed or incomplete restore?). Check the fan (%s), its original state was: %s. Deleting the saved state.",
			id, state.Hardware, controller.FormatFanOriginalState(state))
		if err := pers.DeleteFanOriginalState(id); err != nil {
			ui.Warning("Cannot delete the saved original state of fan %s: %v", id, err)
		}
	}
}

// configWatchDebounce is the time the config file watcher waits after the last change of the config file
// before it reads the file. Editors and tools often change the file in several steps (e.g. write a temporary
// file and rename it over the config file), and the reload should only see the final content.
const configWatchDebounce = 500 * time.Millisecond

// notifyError logs an error and notifies the user about it, replaceable in tests
var notifyError = ui.ErrorAndNotify

// notifyWarning logs a warning and notifies the user about it, replaceable in tests
var notifyWarning = func(title string, format string, a ...any) {
	ui.Warning(format, a...)
	ui.NotifyWarn(title, fmt.Sprintf(format, a...))
}

// configWatchHook is called with what the config file watcher did with an event or a debounced change
// ("ignored:<file name>", "debounced", "unreadable", "unchanged", "reload", "dropped"), nil outside of tests.
var configWatchHook func(what string)

// runFileWatcher requests a reload whenever the content of the config file changes. The automatic reload is
// optional: if the watcher can't be set up or stops working, the user is warned, and it waits until ctx is
// done without stopping the daemon (SIGHUP still reloads the config).
func runFileWatcher(ctx context.Context, configPath string, reloadChan chan<- struct{}) error {
	err := watchConfigFile(ctx, configPath, reloadChan, util.RealClock)
	if err != nil {
		notifyWarning("Config File Watcher", "Automatic config reload is disabled: %v. Send SIGHUP to fan2go to reload the config.", err)
		<-ctx.Done()
	}
	return nil
}

// configFileWatch tracks the paths the config file watcher is interested in.
type configFileWatch struct {
	watcher *fsnotify.Watcher
	// absolute path of the config file
	path string
	// true if path is a symlink
	isSymlink bool
	// the resolved path of the config file (path itself, if it isn't a symlink)
	target string
	// the directory of target, if it is watched in addition to the directory of path, else ""
	watchedTargetDir string
}

// watchConfigFile watches the config file until ctx is done (returns nil) or the watcher fails (returns an error).
//
// It watches the directory of the config file instead of the file itself: editors and tools that save atomically
// replace the file (write a temporary file, then rename it over the config file), which would end a watch on the
// file. Events of other files in the directory are ignored. If the config file is a symlink, the directory of its
// target is watched as well, and every change in the directory of the symlink is considered, since it may
// retarget the symlink (directly or through other symlinks in that directory).
// After a change, it waits for configWatchDebounce without further changes, then requests a reload if the
// content of the file changed.
func watchConfigFile(ctx context.Context, configPath string, reloadChan chan<- struct{}, clock util.Clock) error {
	path, err := filepath.Abs(configPath)
	if err != nil {
		return fmt.Errorf("could not resolve the config file path %s: %w", configPath, err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("could not create the file watcher: %w", err)
	}
	defer func(watcher *fsnotify.Watcher) {
		err := watcher.Close()
		if err != nil {
			ui.Warning("Could not close file watcher: %v", err)
		}
	}(watcher)

	dir := filepath.Dir(path)
	if err := watcher.Add(dir); err != nil {
		return fmt.Errorf("could not watch the config directory %s: %w", dir, err)
	}
	w := &configFileWatch{watcher: watcher, path: path, target: path}
	w.updateTarget()

	// Store the last known hash to compare against
	lastHash, err := util.HashFile(path)
	if err != nil {
		ui.Warning("Could not compute initial hash for config file: %v", err)
	}

	hook := func(what string) {
		if configWatchHook != nil {
			configWatchHook(what)
		}
	}

	// receives once the config file didn't change for configWatchDebounce (nil if there is no pending change)
	var debounced <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-watcher.Events:
			if !ok {
				return errors.New("the file watcher stopped (event channel closed)")
			}
			if filepath.Clean(event.Name) == dir && (event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename)) {
				// inotify drops the watch then, and a new directory at the same path isn't watched
				return fmt.Errorf("the config directory %s was removed or renamed", dir)
			}
			if !w.isRelevant(event) {
				hook("ignored:" + filepath.Base(event.Name))
				continue
			}
			ui.Debug("Config file event: %v", event)
			// (re)start the debounce timer, an earlier timer is ignored
			debounced = clock.After(configWatchDebounce)
			hook("debounced")
		case <-debounced:
			debounced = nil
			w.updateTarget()
			newHash, err := util.HashFile(path)
			if err != nil {
				// e.g. removed during an atomic save: the file watcher sees the new file being created
				ui.Debug("Could not read the config file after a change, waiting for the next change: %v", err)
				hook("unreadable")
				continue
			}
			if newHash == lastHash {
				ui.Debug("Config file changed, but its content is identical. Skipping reload.")
				hook("unchanged")
				continue
			}
			ui.Info("Config file content change detected.")
			lastHash = newHash
			select {
			case reloadChan <- struct{}{}:
				hook("reload")
			default:
				ui.Warning("Reload already pending, ignoring config file change.")
				hook("dropped")
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return errors.New("the file watcher stopped (error channel closed)")
			}
			ui.Warning("File watcher error: %v", err)
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				// events may have been lost: check the file in case one of them was a change of it
				debounced = clock.After(configWatchDebounce)
				hook("debounced")
			}
		}
	}
}

// isRelevant returns true if event may have changed the content of the config file.
func (w *configFileWatch) isRelevant(event fsnotify.Event) bool {
	if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) && !event.Has(fsnotify.Rename) && !event.Has(fsnotify.Remove) {
		// Chmod
		return false
	}
	name := filepath.Clean(event.Name)
	if name == w.path || name == w.target {
		return true
	}
	// a change in the directory of a symlinked config file may retarget the symlink
	return w.isSymlink && filepath.Dir(name) == filepath.Dir(w.path)
}

// updateTarget resolves the config file path, and watches the directory of the resolved file in addition to the
// directory of the config file, if it differs. Keeps the last known target if the path can't be resolved right now.
func (w *configFileWatch) updateTarget() {
	info, err := os.Lstat(w.path)
	if err != nil {
		return
	}
	w.isSymlink = info.Mode()&os.ModeSymlink != 0
	target, err := filepath.EvalSymlinks(w.path)
	if err != nil {
		return
	}
	w.target = target

	targetDir := filepath.Dir(target)
	// the directory of the config file itself is always watched; it may be reached through symlinks as well
	if realDir, err := filepath.EvalSymlinks(filepath.Dir(w.path)); err == nil && realDir == targetDir {
		targetDir = ""
	}
	if targetDir == w.watchedTargetDir {
		return
	}
	if w.watchedTargetDir != "" {
		_ = w.watcher.Remove(w.watchedTargetDir)
		w.watchedTargetDir = ""
	}
	if targetDir != "" {
		if err := w.watcher.Add(targetDir); err != nil {
			ui.Warning("Could not watch the directory %s of the config file target %s: %v", targetDir, target, err)
			return
		}
		w.watchedTargetDir = targetDir
	}
}

func checkProcessOwner() {
	owner, err := getProcessOwner()
	if err != nil {
		ui.Warning("Unable to verify process owner: %v", err)
	} else if owner != "root" {
		ui.Info("fan2go is running as a non-root user '%s'. If you encounter errors, make sure to give this user the required permissions.", owner)
	}
}

func runOrchestrator(ctx context.Context, reloadChan <-chan struct{}, pers persistence.Persistence, reg *registry.Registry, fanControllers map[fans.Fan]controller.FanController, evaluator *curves.CurveEvaluator) error {
	if len(reg.SnapshotFans()) == 0 {
		// not a config error: the fans may have been skipped because their hardware is missing (yet)
		return errors.New("no valid fan configurations, exiting")
	}

	// the config the persistence and the profiling webserver were created with (they are not recreated on reload)
	startupConfig := configuration.CurrentConfig

	for {
		if ctx.Err() != nil {
			// e.g. a shutdown signal while applyConfiguration created the objects of a reload: don't start a
			// cycle that would only take over the fans to hand them back right away
			return nil
		}
		// Run the active cycle. Blocks until app exit or until a reload request yields a valid config.
		newConfig, appExiting := runOrchestratorCycle(ctx, reloadChan, reg, fanControllers, evaluator)
		if appExiting {
			return nil
		}

		// Handle Configuration Reload
		previousConfig := configuration.CurrentConfig
		newReg, newControllers, newEvaluator, err := applyConfiguration(pers, *newConfig)
		if err != nil {
			notifyError("Config Reload Failed", "Reload failed: %v. Keeping current configuration.", err)
			continue
		}
		warnAboutSettingsThatNeedARestart(startupConfig, previousConfig, *newConfig)

		// Safely swap the active state to the new objects!
		reg = newReg
		fanControllers = newControllers
		evaluator = newEvaluator
		ui.Info("Configuration reloaded successfully. Starting new monitors and controllers...")
	}
}

// runOrchestratorCycle manages the localized context for sensors, the curve evaluator, fan controllers and webservers.
// It returns true if the application is shutting down. Otherwise, it returns the new, valid config once a reload
// was requested (see waitForReload). All workers of the cycle have stopped when it returns.
func runOrchestratorCycle(ctx context.Context, reloadChan <-chan struct{}, reg *registry.Registry, fanControllers map[fans.Fan]controller.FanController, evaluator *curves.CurveEvaluator) (newConfig *configuration.Configuration, appExiting bool) {
	orchestratorCtx, cancelOrchestrator := context.WithCancel(ctx)
	var orchestratorWg sync.WaitGroup
	// the curve evaluator is stopped only after the fan controllers, which are driven by its cycles
	// (they don't need it to stop and restore the fans, though)
	evaluatorCtx, cancelEvaluator := context.WithCancel(context.WithoutCancel(ctx))
	var evaluatorWg sync.WaitGroup

	defer func() {
		cancelOrchestrator()
		orchestratorWg.Wait()
		cancelEvaluator()
		evaluatorWg.Wait()
	}()

	startSensorMonitors(orchestratorCtx, reg, &orchestratorWg)
	startCurveEvaluator(evaluatorCtx, evaluator, &evaluatorWg)
	startFanControllers(orchestratorCtx, fanControllers, &orchestratorWg)
	startWebservers(orchestratorCtx, reg, &orchestratorWg)

	newConfig, appExiting = waitForReload(ctx, reloadChan, fanControllers, util.RealClock)
	if !appExiting {
		ui.Info("Stopping old controllers, sensor monitors, and webservers...")
	}
	return newConfig, appExiting
}

// deferredReloadCheckInterval is the interval in which a deferred config reload checks whether the fan
// initializations it waits for have finished.
const deferredReloadCheckInterval = time.Second

// waitForReload waits until ctx is done (returns true) or a reload request yields a config that can be applied
// (returns it). The workers of the current cycle keep running meanwhile:
//   - Each reload request reads, parses and validates the config file. If that fails, the error is logged and
//     notified, and the current config stays active without touching any fan.
//   - A valid config is applied only when no fan controller is analyzing its fan: cancelling the analysis would
//     start it over on the next run. Until then, the reload is deferred (a new reload request replaces the
//     deferred config, or drops it if the new file is invalid).
func waitForReload(ctx context.Context, reloadChan <-chan struct{}, fanControllers map[fans.Fan]controller.FanController, clock util.Clock) (newConfig *configuration.Configuration, appExiting bool) {
	// the validated config to apply, nil if there is none
	var pending *configuration.Configuration
	// ticks while a reload is deferred
	var ticker util.Ticker
	var tickC <-chan time.Time
	stopTicker := func() {
		if ticker != nil {
			ticker.Stop()
			ticker = nil
			tickC = nil
		}
	}
	defer stopTicker()

	for {
		requested := false
		select {
		case <-ctx.Done():
			return nil, true
		case <-reloadChan:
			requested = true
			config, err := loadNewConfiguration()
			if err != nil {
				if pending != nil {
					err = fmt.Errorf("%w (the deferred reload of the previous version of the config file is dropped too)", err)
				}
				notifyError("Config Reload Failed", "Reload failed: %v. Keeping current configuration.", err)
				pending = nil
				stopTicker()
				continue
			}
			pending = &config
		case <-tickC:
		}
		if pending == nil {
			continue
		}

		if ctx.Err() != nil {
			// the shutdown wins, also if the reload request arrived at the same time
			return nil, true
		}
		initializing := initializingFans(fanControllers)
		if len(initializing) == 0 {
			return pending, false
		}
		if requested {
			ui.Info("Config reload deferred until the analysis of fan(s) %s has finished.", strings.Join(initializing, ", "))
		}
		if ticker == nil {
			ticker = clock.NewTicker(deferredReloadCheckInterval)
			tickC = ticker.C()
		}
	}
}

// initializingFans returns the sorted ids of the fans whose controllers are analyzing them (see
// FanController.IsInitializing).
func initializingFans(fanControllers map[fans.Fan]controller.FanController) []string {
	var ids []string
	for _, c := range fanControllers {
		if c.IsInitializing() {
			ids = append(ids, c.GetFanId())
		}
	}
	sort.Strings(ids)
	return ids
}

// loadNewConfiguration reads, parses and validates the config file. It changes neither CurrentConfig nor any
// object of the running config.
func loadNewConfiguration() (configuration.Configuration, error) {
	ui.Info("Reloading configuration...")

	// viper replaces its config only if the file could be read and parsed
	err := configuration.ReadInConfig()
	if err != nil {
		return configuration.Configuration{}, fmt.Errorf("error reading config file: %w", err)
	}
	newConfig, err := configuration.LoadConfig()
	if err != nil {
		return configuration.Configuration{}, fmt.Errorf("parsing failed: %w", err)
	}
	err = configuration.ValidateConfig(&newConfig, configuration.GetFilePath())
	if err != nil {
		return configuration.Configuration{}, fmt.Errorf("validation failed: %w", err)
	}
	return newConfig, nil
}

// applyConfiguration makes newConfig the current config, and creates the registry, the fan controllers
// and the curve evaluator for it (new curves are evaluated by the new evaluator only).
// On error, the previous config stays the current config.
// Must only be called while no worker of the previous config is running: they read CurrentConfig.
func applyConfiguration(pers persistence.Persistence, newConfig configuration.Configuration) (*registry.Registry, map[fans.Fan]controller.FanController, *curves.CurveEvaluator, error) {
	oldConfig := configuration.CurrentConfig
	configuration.CurrentConfig = newConfig

	fanMap, newReg, err := InitializeObjects()
	if err != nil {
		configuration.CurrentConfig = oldConfig
		return nil, nil, nil, fmt.Errorf("error re-initializing objects: %w", err)
	}

	// Build brand new controllers mapped to the brand new fans!
	newControllers, newEvaluator, err := initializeFanControllers(pers, fanMap, newReg)
	if err != nil {
		configuration.CurrentConfig = oldConfig
		return nil, nil, nil, fmt.Errorf("error re-initializing controllers: %w", err)
	}

	return newReg, newControllers, newEvaluator, nil
}

// warnAboutSettingsThatNeedARestart warns about each setting that is only applied on startup, and that
// newConfig changed: it differs from the previous config and from the config the daemon was started with.
// (A setting that a later reload doesn't change again isn't warned about again.)
func warnAboutSettingsThatNeedARestart(startupConfig configuration.Configuration, previousConfig configuration.Configuration, newConfig configuration.Configuration) {
	for _, key := range changedSettingsThatNeedARestart(startupConfig, previousConfig, newConfig) {
		ui.Warning("Config key '%s' changed, restart fan2go to apply it. The value fan2go was started with stays in effect until then.", key)
	}
}

// changedSettingsThatNeedARestart returns the keys of settingsThatNeedARestart(startupConfig, newConfig)
// that also differ between previousConfig and newConfig.
func changedSettingsThatNeedARestart(startupConfig configuration.Configuration, previousConfig configuration.Configuration, newConfig configuration.Configuration) []string {
	changed := settingsThatNeedARestart(previousConfig, newConfig)
	var keys []string
	for _, key := range settingsThatNeedARestart(startupConfig, newConfig) {
		if slices.Contains(changed, key) {
			keys = append(keys, key)
		}
	}
	return keys
}

// settingsThatNeedARestart returns the config keys that differ between startupConfig and newConfig,
// but are only applied on startup.
func settingsThatNeedARestart(startupConfig configuration.Configuration, newConfig configuration.Configuration) []string {
	var keys []string
	if startupConfig.DbPath != newConfig.DbPath {
		keys = append(keys, "dbPath")
	}
	if startupConfig.Profiling.Enabled != newConfig.Profiling.Enabled {
		keys = append(keys, "profiling.enabled")
	}
	// the address only matters if the profiling webserver runs (it is started with the startup config)
	if startupConfig.Profiling.Enabled {
		if startupConfig.Profiling.Host != newConfig.Profiling.Host {
			keys = append(keys, "profiling.host")
		}
		if startupConfig.Profiling.Port != newConfig.Profiling.Port {
			keys = append(keys, "profiling.port")
		}
	}
	return keys
}

// subscribeSignals returns a channel that receives SIGINT, SIGTERM and SIGHUP from now on, for handleSignals.
func subscribeSignals() <-chan os.Signal {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	// Neither close sig (os/signal keeps sending to it, and sending on a closed channel panics),
	// nor call signal.Stop: the signals would then terminate the process (their default action)
	// during shutdown, before the fan controllers have restored the fan settings.
	return sig
}

// handleSignals forwards SIGHUP to the orchestrator as a reload request. It returns on
// SIGTERM/SIGINT or when ctx is done, so the daemon shuts down. Signals that arrive after that
// are logged and ignored until the process exits (see ignoreSignalsDuringShutdown).
func handleSignals(ctx context.Context, sig <-chan os.Signal, reloadChan chan<- struct{}) error {
	for {
		select {
		case s := <-sig:
			if s == syscall.SIGHUP {
				ui.Info("Received SIGHUP signal, notifying orchestrator...")
				select {
				case reloadChan <- struct{}{}:
				default:
					ui.Warning("Reload already in progress, ignoring SIGHUP.")
				}
			} else {
				ui.Info("Received SIGTERM/SIGINT signal, exiting...")
				go ignoreSignalsDuringShutdown(sig)
				return nil
			}
		case <-ctx.Done():
			go ignoreSignalsDuringShutdown(sig)
			return nil
		}
	}
}

// ignoreSignalsDuringShutdown keeps receiving signals while the daemon shuts down and tells the
// user that another SIGTERM/SIGINT/SIGHUP (e.g. pressing Ctrl-C twice) doesn't stop the restore
// of the fan settings. It only returns once sig is closed.
func ignoreSignalsDuringShutdown(sig <-chan os.Signal) {
	for s := range sig {
		ui.Warning("Received %v signal while shutting down, waiting for the fan settings to be restored (send SIGKILL to force exit).", s)
	}
}

func startSensorMonitors(ctx context.Context, reg *registry.Registry, wg *sync.WaitGroup) {
	// === sensor monitoring
	ui.Info("Sensor values are considered stale %s after their last successful reading (sensorStaleTimeout)",
		configuration.CurrentConfig.GetSensorStaleTimeout())
	sensorMapData := reg.SnapshotSensors()
	for _, sensor := range sensorMapData {
		s := sensor
		pollingRate := configuration.CurrentConfig.TempSensorPollingRate
		mon := NewSensorMonitor(s, pollingRate)

		wg.Add(1)
		go func() {
			defer wg.Done()
			err := mon.Run(ctx)
			ui.Info("Sensor Monitor for sensor %s stopped.", s.GetId())
			if err != nil && !errors.Is(err, context.Canceled) {
				ui.Warning("Sensor monitor exited with error: %v", err)
			}
		}()
	}
}

// curveEvaluatorRestartDelay is the time to wait before restarting the curve evaluator after a panic
const curveEvaluatorRestartDelay = time.Second

// startCurveEvaluator runs the curve evaluator until ctx is done. If it stops because of a panic,
// it is restarted, since the fan controllers only update the fans on its cycles.
func startCurveEvaluator(ctx context.Context, evaluator *curves.CurveEvaluator, wg *sync.WaitGroup) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			runCurveEvaluator(ctx, evaluator)
			select {
			case <-ctx.Done():
				ui.Info("Curve evaluator stopped.")
				return
			case <-time.After(curveEvaluatorRestartDelay):
			}
		}
	}()
}

// runCurveEvaluator runs the evaluator until ctx is done or it panics. A panic is logged.
func runCurveEvaluator(ctx context.Context, evaluator *curves.CurveEvaluator) {
	defer func() {
		if r := recover(); r != nil {
			ui.Error("Panic in curve evaluator, restarting it: %v\n%s", r, debug.Stack())
		}
	}()
	err := evaluator.Run(ctx)
	if err != nil {
		ui.Warning("Curve evaluator exited with error: %v", err)
	}
}

func startFanControllers(ctx context.Context, fanControllers map[fans.Fan]controller.FanController, wg *sync.WaitGroup) {
	// === fan controllers
	for f, c := range fanControllers {
		fan := f
		fanController := c
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := fanController.Run(ctx)
			ui.Info("Fan controller for fan %s stopped.", fan.GetId())
			if err != nil && !errors.Is(err, context.Canceled) {
				ui.WarningAndNotify(fmt.Sprintf("Fan Controller: %s", fan.GetId()), "Something went wrong: %v", err)
			}
		}()
	}
}

func getProcessOwner() (string, error) {
	currentUser, err := user.Current()
	if err != nil {
		return "", err
	}

	return currentUser.Username, nil
}
