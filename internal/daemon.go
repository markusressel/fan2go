package internal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"os/user"
	"sync"
	"syscall"

	"github.com/fsnotify/fsnotify"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/controller"
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

	fanControllers, err := initializeFanControllers(pers, fanMap, reg)
	if err != nil {
		ui.Fatal("Error initializing fan controllers: %v", err)
	}

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
		func() error { return runOrchestrator(ctx, reloadChan, pers, reg, fanControllers) },
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

func runOrchestrator(ctx context.Context, reloadChan <-chan struct{}, pers persistence.Persistence, reg *registry.Registry, fanControllers map[fans.Fan]controller.FanController) error {
	if len(reg.SnapshotFans()) == 0 {
		ui.FatalWithoutStacktrace("No valid fan configurations, exiting.")
	}

	for {
		// Run the active cycle. Blocks until app exit or reload signal.
		appExiting := runOrchestratorCycle(ctx, reloadChan, reg, fanControllers)
		if appExiting {
			return nil
		}

		// Handle Configuration Reload
		newReg, newControllers, err := reloadConfiguration(pers)
		if err != nil {
			ui.Error("Reload failed: %v. Keeping current configuration.", err)
			continue
		}

		// Safely swap the active state to the new objects!
		reg = newReg
		fanControllers = newControllers
		ui.Info("Configuration reloaded successfully. Starting new monitors and controllers...")
	}
}

// runOrchestratorCycle manages the localized context for sensors and webservers.
// It returns true if the application is shutting down, and false if a reload was requested.
func runOrchestratorCycle(ctx context.Context, reloadChan <-chan struct{}, reg *registry.Registry, fanControllers map[fans.Fan]controller.FanController) bool {
	orchestratorCtx, cancelOrchestrator := context.WithCancel(ctx)
	var orchestratorWg sync.WaitGroup

	defer func() {
		cancelOrchestrator()
		orchestratorWg.Wait()
	}()

	startSensorMonitors(orchestratorCtx, reg, &orchestratorWg)
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

func reloadConfiguration(pers persistence.Persistence) (*registry.Registry, map[fans.Fan]controller.FanController, error) {
	ui.Info("Reloading configuration...")

	err := configuration.ReadInConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("error reading config file: %w", err)
	}
	newConfig, err := configuration.LoadConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("parsing failed: %w", err)
	}

	oldConfig := configuration.CurrentConfig
	configuration.CurrentConfig = newConfig

	fanMap, newReg, err := InitializeObjects()
	if err != nil {
		configuration.CurrentConfig = oldConfig
		return nil, nil, fmt.Errorf("error re-initializing objects: %w", err)
	}

	// Build brand new controllers mapped to the brand new fans!
	newControllers, err := initializeFanControllers(pers, fanMap, newReg)
	if err != nil {
		configuration.CurrentConfig = oldConfig
		return nil, nil, fmt.Errorf("error re-initializing controllers: %w", err)
	}

	return newReg, newControllers, nil
}

func runSignalHandler(ctx context.Context, reloadChan chan<- struct{}) error {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer close(sig)

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
				return nil
			}
		case <-ctx.Done():
			return nil
		}
	}
}

func startSensorMonitors(ctx context.Context, reg *registry.Registry, wg *sync.WaitGroup) {
	// === sensor monitoring
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
