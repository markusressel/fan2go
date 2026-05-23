// Package reload implements hot reloading of the fan2go configuration file (refs #424).
//
// The ReloadManager watches the active configuration file for changes via fsnotify and
// also responds to SIGHUP signals.  When a change is detected:
//  1. The file is re-read and validated with ReadAndValidateConfig.
//  2. If validation passes, CurrentConfig is updated and every registered SpeedCurve is
//     recreated from the new config.  Fan controllers then receive the new curve via
//     SetCurve.  Any running state (e.g. PID integral) is intentionally reset on reload.
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
			r.reload()

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
					r.reload()
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
			r.reload()
		}
	}
}

// reload reads, validates, and – if valid – applies the configuration file.
func (r *ReloadManager) reload() {
	newConfig, err := configuration.ReadAndValidateConfig(r.configPath)
	if err != nil {
		ui.Warning("Config hot-reload: rejected (validation failed): %v", err)
		return
	}
	r.applyNewConfig(newConfig)
}

// applyNewConfig updates the global CurrentConfig, recreates all SpeedCurve objects
// from the new configuration, and updates each fan controller's curve reference.
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

	for fan, ctrl := range r.fanControllers {
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
