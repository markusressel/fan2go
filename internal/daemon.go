package internal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"os/user"
	"runtime/debug"
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

func RunDaemon() {
	checkProcessOwner()

	pers := persistence.NewPersistence(configuration.CurrentConfig.DbPath)

	fanMap, reg, err := InitializeObjects()
	if err != nil {
		ui.Fatal("Error initializing objects: %v", err)
	}

	fanControllers, evaluator, err := initializeFanControllers(pers, fanMap, reg)
	if err != nil {
		ui.Fatal("Error initializing fan controllers: %v", err)
	}

	forgetOriginalStatesOfUnconfiguredFans(pers, configuration.CurrentConfig.Fans)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reloadChan := make(chan struct{}, 1)

	var g run.Group

	if configuration.CurrentConfig.Profiling.Enabled {
		g.Add(
			func() error { return startProfilingWebserver(ctx) },
			func(err error) {
				if err != nil {
					ui.Warning("Error stopping profiling webserver: %v", err)
				} else {
					ui.Debug("Profiling webserver stopped.")
				}
			},
		)
	}

	// === ACTOR 1: Orchestrator ===
	g.Add(
		func() error { return runOrchestrator(ctx, reloadChan, pers, reg, fanControllers, evaluator) },
		func(err error) { cancel() },
	)

	// === ACTOR 2: Signal Handler ===
	g.Add(
		func() error { return runSignalHandler(ctx, reloadChan) },
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
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ui.Info("Done.")
	os.Exit(0)
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

func runFileWatcher(ctx context.Context, configPath string, reloadChan chan<- struct{}) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer func(watcher *fsnotify.Watcher) {
		err := watcher.Close()
		if err != nil {
			ui.Warning("Could not close file watcher: %v", err)
		}
	}(watcher)

	// Store the last known hash to compare against
	lastHash, err := util.HashFile(configPath)
	if err != nil {
		ui.Warning("Could not compute initial hash for config file: %v", err)
	}

	err = watcher.Add(configPath)
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}

			// Trigger on Write
			if event.Op&fsnotify.Write == fsnotify.Write {
				newHash, err := util.HashFile(configPath)
				if err != nil {
					ui.Warning("Could not compute hash after file change: %v", err)
					continue
				}

				// Only notify if the content actually changed
				if newHash != lastHash {
					ui.Info("Config file content change detected.")
					lastHash = newHash

					select {
					case reloadChan <- struct{}{}:
					default:
						ui.Warning("Reload already in progress, ignoring SIGHUP.")
					}
				} else {
					ui.Debug("File write detected, but content is identical. Skipping reload.")
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			ui.Warning("File watcher error: %v", err)
		}
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
		ui.FatalWithoutStacktrace("No valid fan configurations, exiting.")
	}

	for {
		// Run the active cycle. Blocks until app exit or reload signal.
		appExiting := runOrchestratorCycle(ctx, reloadChan, reg, fanControllers, evaluator)
		if appExiting {
			return nil
		}

		// Handle Configuration Reload
		newReg, newControllers, newEvaluator, err := reloadConfiguration(pers)
		if err != nil {
			ui.Error("Reload failed: %v. Keeping current configuration.", err)
			continue
		}

		// Safely swap the active state to the new objects!
		reg = newReg
		fanControllers = newControllers
		evaluator = newEvaluator
		ui.Info("Configuration reloaded successfully. Starting new monitors and controllers...")
	}
}

// runOrchestratorCycle manages the localized context for sensors, the curve evaluator, fan controllers and webservers.
// It returns true if the application is shutting down, and false if a reload was requested.
func runOrchestratorCycle(ctx context.Context, reloadChan <-chan struct{}, reg *registry.Registry, fanControllers map[fans.Fan]controller.FanController, evaluator *curves.CurveEvaluator) bool {
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

	select {
	case <-ctx.Done():
		return true
	case <-reloadChan:
		ui.Info("Stopping old controllers, sensor monitors, and webservers...")
		return false
	}
}

// reloadConfiguration reads and validates the config file, and creates the registry, the fan controllers
// and the curve evaluator for it (new curves are evaluated by the new evaluator only).
func reloadConfiguration(pers persistence.Persistence) (*registry.Registry, map[fans.Fan]controller.FanController, *curves.CurveEvaluator, error) {
	ui.Info("Reloading configuration...")

	err := configuration.ReadInConfig()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("error reading config file: %w", err)
	}
	newConfig, err := configuration.LoadConfig()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parsing failed: %w", err)
	}
	err = configuration.ValidateConfig(&newConfig, configuration.GetFilePath())
	if err != nil {
		return nil, nil, nil, fmt.Errorf("validation failed: %w", err)
	}

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

func runSignalHandler(ctx context.Context, reloadChan chan<- struct{}) error {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	// Neither close sig (os/signal keeps sending to it, and sending on a closed channel panics),
	// nor call signal.Stop: the signals would then terminate the process (their default action)
	// during shutdown, before the fan controllers have restored the fan settings.
	return handleSignals(ctx, sig, reloadChan)
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
