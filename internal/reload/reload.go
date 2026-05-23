// Package reload implements hot reloading of the fan2go configuration file.
//
// The ReloadManager watches the active configuration file for changes via fsnotify and
// also responds to SIGHUP signals.  When a change is detected:
//  1. The file is re-read and validated with ReadAndValidateConfig.
//  2. If validation passes, CurrentConfig is updated, every registered SpeedCurve is
//     recreated from the new config, each fan's stored config is updated (including PWM
//     boundaries and control algorithm), and fan controllers receive the new curve and
//     control loop via SetCurve / SetControlLoop.  Any running state (e.g. PID integral)
//     is intentionally reset on reload.
//  3. If validation fails the daemon keeps running with the previous configuration and
//     logs a warning – no state is changed.
package reload

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/control_loop"
	"github.com/markusressel/fan2go/internal/controller"
	"github.com/markusressel/fan2go/internal/curves"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/ui"
)

const debounceDelay = 500 * time.Millisecond

// ReloadManager watches the configuration file and SIGHUP signal for changes.
// On a valid change it rebuilds SpeedCurve objects from the new config and
// updates each fan controller's curve reference without stopping the daemon.
type ReloadManager struct {
	configPath     string
	fanControllers map[fans.Fan]controller.FanController
}

// NewReloadManager creates a new ReloadManager for the given config file path and
// the set of running fan controllers that should be updated on reload.
func NewReloadManager(
	configPath string,
	fanControllers map[fans.Fan]controller.FanController,
) *ReloadManager {
	return &ReloadManager{
		configPath:     configPath,
		fanControllers: fanControllers,
	}
}

// Run starts the reload manager.  It blocks until ctx is cancelled.
// File-watch errors are non-fatal: if fsnotify cannot be set up the manager
// falls back to SIGHUP-only reloads and logs a warning.
func (r *ReloadManager) Run(ctx context.Context) error {
	sighupCh := make(chan os.Signal, 1)
	signal.Notify(sighupCh, syscall.SIGHUP)
	defer signal.Stop(sighupCh)

	watcher, watchErr := fsnotify.NewWatcher()
	if watchErr != nil {
		ui.Warning("Config hot-reload: failed to create file watcher (%v); only SIGHUP reloads will work.", watchErr)
		return r.runSighupOnly(ctx, sighupCh)
	}
	defer watcher.Close()

	if err := watcher.Add(r.configPath); err != nil {
		ui.Warning("Config hot-reload: failed to watch '%s' (%v); only SIGHUP reloads will work.", r.configPath, err)
	} else {
		ui.Info("Config hot-reload: watching '%s' for changes.", r.configPath)
	}

	var debounceTimer *time.Timer
	for {
		select {
		case <-ctx.Done():
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			return nil

		case <-sighupCh:
			ui.Info("Config hot-reload: received SIGHUP.")
			r.reload(ctx)

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				if debounceTimer != nil {
					debounceTimer.Stop()
				}
				debounceTimer = time.AfterFunc(debounceDelay, func() {
					ui.Info("Config hot-reload: config file changed.")
					r.reload(ctx)
				})
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			ui.Warning("Config hot-reload: file watcher error: %v", err)
		}
	}
}

// runSighupOnly is a fallback loop used when the file watcher cannot be created.
func (r *ReloadManager) runSighupOnly(ctx context.Context, sighupCh <-chan os.Signal) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-sighupCh:
			ui.Info("Config hot-reload: received SIGHUP.")
			r.reload(ctx)
		}
	}
}

// reload reads, validates, and – if valid – applies the configuration file.
// If ctx is already done (daemon shutting down) the apply step is skipped.
func (r *ReloadManager) reload(ctx context.Context) {
	newConfig, err := configuration.ReadAndValidateConfig(r.configPath)
	if err != nil {
		ui.Warning("Config hot-reload: rejected (validation failed): %v", err)
		return
	}
	select {
	case <-ctx.Done():
		return // daemon is shutting down; skip the apply step
	default:
		r.applyNewConfig(newConfig)
	}
}

// applyNewConfig updates the global CurrentConfig, recreates all SpeedCurve objects
// from the new configuration, propagates per-fan config changes to fan objects, and
// updates each fan controller's curve and control loop references.
func (r *ReloadManager) applyNewConfig(newConfig *configuration.Configuration) {
	configuration.CurrentConfig = *newConfig

	var rebuildErrors []string
	for _, curveConfig := range newConfig.Curves {
		newCurve, err := curves.NewSpeedCurve(curveConfig)
		if err != nil {
			rebuildErrors = append(rebuildErrors, fmt.Sprintf("curve '%s': %v", curveConfig.ID, err))
			continue
		}
		curves.RegisterSpeedCurve(newCurve)
	}
	if len(rebuildErrors) > 0 {
		for _, e := range rebuildErrors {
			ui.Warning("Config hot-reload: failed to rebuild %s", e)
		}
	}

	// Build a lookup table from fan ID to new fan config for O(1) access.
	newFanConfigByID := make(map[string]configuration.FanConfig, len(newConfig.Fans))
	for _, fc := range newConfig.Fans {
		newFanConfigByID[fc.ID] = fc
	}

	for fan, ctrl := range r.fanControllers {
		fanID := fan.GetId()

		// Propagate new fan-specific config to the live fan object so that all
		// per-fan settings (neverStop, sanityCheck, useUnscaledCurveValues,
		// pwmSetDelay, controlMode, etc.) take effect immediately.
		if newFanCfg, ok := newFanConfigByID[fanID]; ok {
			fan.SetConfig(newFanCfg)
			// Apply explicitly configured PWM boundary overrides.
			if newFanCfg.MinPwm != nil {
				fan.SetMinPwm(*newFanCfg.MinPwm, true)
			}
			if newFanCfg.MaxPwm != nil {
				fan.SetMaxPwm(*newFanCfg.MaxPwm, true)
			}
			if newFanCfg.StartPwm != nil {
				fan.SetStartPwm(*newFanCfg.StartPwm, true)
			}

			// Rebuild the control loop from the new config and hand it to the controller.
			ctrl.SetControlLoop(buildControlLoopForFan(newFanCfg))
		}

		curveID := fan.GetCurveId()
		newCurve, found := curves.GetSpeedCurve(curveID)
		if !found {
			ui.Warning("Config hot-reload: curve '%s' for fan '%s' not found after rebuild", curveID, fan.GetId())
			continue
		}
		ctrl.SetCurve(newCurve)
	}

	ui.Info("Config hot-reload: configuration applied successfully.")
}

// buildControlLoopForFan constructs the appropriate ControlLoop for the given fan config,
// mirroring the logic used during initial fan controller setup in backend.go.
func buildControlLoopForFan(config configuration.FanConfig) control_loop.ControlLoop {
	// Deprecated ControlLoop field takes precedence for backward compatibility.
	if config.ControlLoop != nil { //nolint:all
		return control_loop.NewPidControlLoop(
			config.ControlLoop.P, //nolint:all
			config.ControlLoop.I, //nolint:all
			config.ControlLoop.D, //nolint:all
		)
	}
	if config.ControlAlgorithm != nil {
		if config.ControlAlgorithm.Pid != nil {
			return control_loop.NewPidControlLoop(
				config.ControlAlgorithm.Pid.P,
				config.ControlAlgorithm.Pid.I,
				config.ControlAlgorithm.Pid.D,
			)
		}
		if config.ControlAlgorithm.Direct != nil {
			return control_loop.NewDirectControlLoop(
				config.ControlAlgorithm.Direct.MaxPwmChangePerCycle,
			)
		}
	}
	return control_loop.NewPidControlLoop(
		control_loop.DefaultPidConfig.P,
		control_loop.DefaultPidConfig.I,
		control_loop.DefaultPidConfig.D,
	)
}
