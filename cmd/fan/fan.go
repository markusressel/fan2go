package fan

import (
	"errors"
	"fmt"
	"os"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/hwmon"
	"github.com/markusressel/fan2go/internal/persistence"
	"github.com/spf13/cobra"
)

var fanId string

var Command = &cobra.Command{
	Use:              "fan",
	Short:            "Fan related commands",
	Long:             ``,
	TraverseChildren: true,
}

func init() {
	Command.PersistentFlags().StringVarP(
		&fanId,
		"id", "i",
		"",
		"Fan ID as specified in the config",
	)
	_ = Command.MarkPersistentFlagRequired("id")
}

func getFan(id string) (fans.Fan, error) {
	_, err := configuration.LoadAndValidateConfigFile()
	if err != nil {
		return nil, err
	}

	controllers := hwmon.GetChips()

	availableFanIds := []string{}
	for _, config := range configuration.CurrentConfig.Fans {
		availableFanIds = append(availableFanIds, config.ID)
		if config.ID == id {
			if config.HwMon != nil {
				err = hwmon.UpdateFanConfigFromHwMonControllers(controllers, &config)
				if err != nil {
					return nil, fmt.Errorf("fan %s: couldn't find the hwmon fan: %w", config.ID, err)
				}
			}

			fan, err := fans.NewFan(config)
			if err != nil {
				return nil, err
			}

			return fan, nil
		}
	}

	return nil, fmt.Errorf("no fan with id found: %s, options: %s", id, availableFanIds)
}

// openPersistenceExclusively creates the database directory if needed and takes the process lock of the
// database (see persistence.AcquireProcessLock), so the command doesn't run next to the daemon, which would
// write to the same fans and keep using its old data. Call release when the command is done.
func openPersistenceExclusively(dbPath string) (p persistence.Persistence, release func(), err error) {
	p = persistence.NewPersistence(dbPath)
	if err := p.Init(); err != nil {
		return nil, nil, fmt.Errorf("unable to create the database directory: %w", err)
	}
	release, err = persistence.AcquireProcessLock(dbPath)
	if err != nil {
		var locked *persistence.ProcessLockedError
		if errors.As(err, &locked) {
			return nil, nil, fmt.Errorf("%w. Is the fan2go daemon running? Stop it first (e.g. 'sudo systemctl stop fan2go'), "+
				"then run this command again", err)
		}
		if errors.Is(err, os.ErrPermission) {
			return nil, nil, fmt.Errorf("%w. Run this command as root (like the daemon)", err)
		}
		return nil, nil, err
	}
	return p, release, nil
}
