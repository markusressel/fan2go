package sensor

import (
	"fmt"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/hwmon"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
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
	_, err := configuration.LoadAndValidateConfigFile()
	if err != nil {
		return nil, err
	}

	controllers := hwmon.GetChips()

	availableSensorIds := []string{}
	for _, config := range configuration.CurrentConfig.Sensors {
		availableSensorIds = append(availableSensorIds, config.ID)
		if config.ID == id {
			if config.HwMon != nil {
				if err := resolveHwmonSensorConfig(controllers, &config); err != nil {
					return nil, err
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

// resolveHwmonSensorConfig fills in the sysfs temp input of a hwmon sensor
// config, matching it by index or by channel exactly like the daemon does
// (see hwmon.UpdateSensorConfigFromHwMonControllers).
func resolveHwmonSensorConfig(controllers []*hwmon.HwMonController, config *configuration.SensorConfig) error {
	if err := hwmon.UpdateSensorConfigFromHwMonControllers(controllers, config); err != nil {
		return fmt.Errorf("unable to find hwmon sensor %s: %w", config.ID, err)
	}
	if len(config.HwMon.TempInput) <= 0 {
		return fmt.Errorf("unable to find temp input for sensor %s", config.ID)
	}
	return nil
}
