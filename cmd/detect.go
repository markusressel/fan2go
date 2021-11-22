package cmd

import (
	"bytes"
	"fmt"
	"github.com/markusressel/fan2go/internal"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/mgutz/ansi"
	"github.com/spf13/cobra"
	"github.com/tomlazar/table"
	"strconv"
)

var detectCmd = &cobra.Command{
	Use:   "detect",
	Short: "Detect devices",
	Long:  `Detects all fans and sensors and prints them as a list`,
	Run: func(cmd *cobra.Command, args []string) {
		configuration.LoadConfig()

		controllers, err := internal.FindControllers()
		if err != nil {
			ui.Fatal("Error detecting devices: %v", err)
		}

		// === Print detected devices ===
		tableConfig := &table.Config{
			ShowIndex:       false,
			Color:           !noColor,
			AlternateColors: true,
			TitleColorCode:  ansi.ColorCode("white+buf"),
			AltColorCodes: []string{
				ansi.ColorCode("white"),
				ansi.ColorCode("white:236"),
			},
		}

		for _, controller := range controllers {
			if len(controller.Name) <= 0 {
				continue
			}

			ui.Printfln("> %s", controller.Name)

			var fanRows [][]string
			for _, fan := range controller.Fans {
				hwMonFan := fan.(*fans.HwMonFan)

				pwm := fan.GetPwm()
				rpm := fan.GetRpm()
				isAuto, _ := fan.IsPwmAuto()
				fanRows = append(fanRows, []string{
					"", strconv.Itoa(hwMonFan.Index), hwMonFan.Label, hwMonFan.Name, strconv.Itoa(rpm), strconv.Itoa(pwm), fmt.Sprintf("%v", isAuto),
				})
			}
			var fanHeaders = []string{"Fans   ", "Index", "Label", "Name", "RPM", "PWM", "Auto"}

			fanTable := table.Table{
				Headers: fanHeaders,
				Rows:    fanRows,
			}

			var sensorRows [][]string
			for _, sensor := range controller.Sensors {
				value, _ := sensor.GetValue()
				//ui.Printfln("  %d: %s (%s): %d", sensor.Index, sensor.Label, sensor.Name, value)
				hwSensor := sensor.(*sensors.HwmonSensor)

				sensorRows = append(sensorRows, []string{
					"", strconv.Itoa(hwSensor.Index), hwSensor.Label, hwSensor.Name, strconv.Itoa(int(value)),
				})
			}
			var sensorHeaders = []string{"Sensors", "Index", "Label", "Name", "Value"}

			sensorTable := table.Table{
				Headers: sensorHeaders,
				Rows:    sensorRows,
			}

			tables := []table.Table{fanTable, sensorTable}

			for idx, table := range tables {
				if table.Rows == nil {
					continue
				}
				var buf bytes.Buffer
				tableErr := table.WriteTable(&buf, tableConfig)
				if tableErr != nil {
					ui.Fatal("Error printing table: %v", tableErr)
				}
				tableString := buf.String()
				if idx < (len(tables) - 1) {
					ui.Printf(tableString)
				} else {
					ui.Printfln(tableString)
				}
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(detectCmd)
}
