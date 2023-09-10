package fan

import (
	"fmt"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/hwmon"
	"github.com/markusressel/fan2go/internal/ui"
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
	configPath := configuration.DetectAndReadConfigFile()
	ui.Info("Using configuration file at: %s", configPath)
	configuration.LoadConfig()
	err := configuration.Validate(configPath)
	if err != nil {
		ui.FatalWithoutStacktrace(err.Error())
	}

	controllers := hwmon.GetChips()

	availableFanIds := []string{}
	for _, config := range configuration.CurrentConfig.Fans {
		availableFanIds = append(availableFanIds, config.ID)
		if config.ID == id {
			if config.HwMon != nil {
				_ = hwmon.UpdateFanConfigFromHwMonControllers(controllers, &config)
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
