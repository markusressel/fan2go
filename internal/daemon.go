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

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/controller"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/persistence"
	"github.com/markusressel/fan2go/internal/registry"
	"github.com/markusressel/fan2go/internal/ui"
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

	fanCtx, cancelFans := context.WithCancel(ctx)
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
		func() error { return runOrchestrator(ctx, fanCtx, reloadChan, reg, fanControllers, cancelFans) },
		func(err error) { cancel() },
	)

	// === ACTOR 2: Signal Handler ===
	g.Add(
		func() error { return runSignalHandler(ctx, reloadChan) },
		func(err error) { cancel() },
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

// --- Extracted Helper Functions ---

func checkProcessOwner() {
	owner, err := getProcessOwner()
	if err != nil {
		ui.Warning("Unable to verify process owner: %v", err)
	} else if owner != "root" {
		ui.Info("fan2go is running as a non-root user '%s'. If you encounter errors, make sure to give this user the required permissions.", owner)
	}
}

func runOrchestrator(ctx, fanCtx context.Context, reloadChan <-chan struct{}, reg *registry.Registry, fanControllers map[fans.Fan]controller.FanController, cancelFans context.CancelFunc) error {
	if len(reg.SnapshotFans()) == 0 {
		ui.FatalWithoutStacktrace("No valid fan configurations, exiting.")
	}

	var fanControllerWg sync.WaitGroup
	startFanControllers(fanCtx, fanControllers, &fanControllerWg)

	defer func() {
		cancelFans()
		fanControllerWg.Wait()
	}()

	for {
		// Run the active cycle. Blocks until app exit or reload signal.
		appExiting := runOrchestratorCycle(ctx, reloadChan, reg)
		if appExiting {
			return nil
		}

		// Handle Configuration Reload
		newReg, err := reloadConfiguration(fanControllers)
		if err != nil {
			ui.Error("Reload failed: %v. Keeping current configuration.", err)
			continue
		}

		reg = newReg
		ui.Info("Configuration reloaded successfully. Starting new monitors...")
	}
}

// runOrchestratorCycle manages the localized context for sensors and webservers.
// It returns true if the application is shutting down, and false if a reload was requested.
func runOrchestratorCycle(ctx context.Context, reloadChan <-chan struct{}, reg *registry.Registry) bool {
	orchestratorCtx, cancelOrchestrator := context.WithCancel(ctx)
	var orchestratorWg sync.WaitGroup

	defer func() {
		cancelOrchestrator()
		orchestratorWg.Wait()
	}()

	startSensorMonitors(orchestratorCtx, reg, &orchestratorWg)
	startWebservers(orchestratorCtx, reg, &orchestratorWg)

	select {
	case <-ctx.Done():
		return true
	case <-reloadChan:
		ui.Info("Stopping old sensor monitors and webservers...")
		return false
	}
}

func reloadConfiguration(fanControllers map[fans.Fan]controller.FanController) (*registry.Registry, error) {
	ui.Info("Reloading configuration...")

	newConfig, err := configuration.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("parsing failed: %w", err)
	}

	err = configuration.Validate(configuration.GetFilePath())
	if err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	oldConfig := configuration.CurrentConfig
	configuration.CurrentConfig = newConfig

	_, newReg, err := InitializeObjects()
	if err != nil {
		configuration.CurrentConfig = oldConfig
		return nil, fmt.Errorf("error re-initializing objects: %w", err)
	}

	ui.Info("Updating fan controllers...")
	for _, ctrl := range fanControllers {
		fanId := ctrl.GetFanId()
		var newCurveId string
		for _, fConfig := range configuration.CurrentConfig.Fans {
			if fConfig.ID == fanId {
				newCurveId = fConfig.Curve
				break
			}
		}
		if newCurveId != "" {
			if newCurve, exists := newReg.GetCurve(newCurveId); exists {
				ctrl.UpdateCurve(newCurve)
				ui.Info("Updated curve of fan controller %s to curve %s", fanId, newCurveId)
			} else {
				ui.Warning("New curve %s not found in registry for fan %s", newCurveId, fanId)
			}
		}
	}

	return newReg, nil
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
