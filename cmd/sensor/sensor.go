package sensor

import (
	"fmt"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/hwmon"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"regexp"
)

var sensorId string

var Command = &cobra.Command{
	Use:              "sensor",
	Short:            "Sensor related commands",
	Long:             ``,
	TraverseChildren: true,
	Args:             cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		pterm.DisableOutput()

		sensor, err := getSensor(sensorId)
		if err != nil {
			return err
		}

		value, err := sensor.GetValue()
		if err != nil {
			return err
		}
		fmt.Printf("%d", int(value))
		return nil
	},
}

func init() {
	Command.PersistentFlags().StringVarP(
		&sensorId,
		"id", "i",
		"",
		"Sensor ID as specified in the config",
	)
	_ = Command.MarkPersistentFlagRequired("id")
}

func getSensor(id string) (sensors.Sensor, error) {
	configPath := configuration.DetectAndReadConfigFile()
	ui.Info("Using configuration file at: %s", configPath)
	configuration.LoadConfig()
	err := configuration.Validate(configPath)
	if err != nil {
		ui.FatalWithoutStacktrace("%v", err)
	}

	controllers := hwmon.GetChips()

	availableSensorIds := []string{}
	for _, config := range configuration.CurrentConfig.Sensors {
		availableSensorIds = append(availableSensorIds, config.ID)
		if config.ID == id {
			if config.HwMon != nil {
				for _, controller := range controllers {
					matched, err := regexp.MatchString("(?i)"+config.HwMon.Platform, controller.Platform)
					if err != nil {
						return nil, fmt.Errorf("Failed to match platform regex of %s (%s) against controller platform %s", config.ID, config.HwMon.Platform, controller.Platform)
					}
					if matched {
						sensor, exists := controller.Sensors[config.HwMon.Index]
						if exists {
							if len(sensor.Input) <= 0 {
								return nil, fmt.Errorf("unable to find temp input for sensor %s", id)
							}
							config.HwMon.TempInput = sensor.Input
							break
						}
					}
				}
			}

			sensor, err := sensors.NewSensor(config)
			if err != nil {
				return nil, err
			}

			return sensor, nil
		}
	}

	return nil, fmt.Errorf("no sensor with id found: %s, options: %s", id, availableSensorIds)
}
