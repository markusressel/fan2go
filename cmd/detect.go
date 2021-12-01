package cmd

import (
	"bytes"
	"fmt"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/hwmon"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/mgutz/ansi"
	"github.com/spf13/cobra"
	"github.com/tomlazar/table"
	"path/filepath"
	"strconv"
)

var detectCmd = &cobra.Command{
	Use:   "detect",
	Short: "Detect devices",
	Long:  `Detects all fans and sensors and prints them as a list`,
	Run: func(cmd *cobra.Command, args []string) {
		configuration.LoadConfig()

		controllers := hwmon.GetChips()

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

			fanList := controller.Fans
			sensorList := controller.Sensors

			if len(fanList) <= 0 && len(sensorList) <= 0 {
				continue
			}

			ui.Printfln("> %s", controller.Name)

			var fanRows [][]string
			for _, fan := range fanList {
				pwm := fan.GetPwm()
				rpm := fan.GetRpm()
				isAuto, _ := fan.IsPwmAuto()
				fanRows = append(fanRows, []string{
					"", strconv.Itoa(fan.Index), fan.Label, strconv.Itoa(rpm), strconv.Itoa(pwm), fmt.Sprintf("%v", isAuto),
				})
			}
			var fanHeaders = []string{"Fans   ", "Index", "Label", "RPM", "PWM", "Auto"}

			fanTable := table.Table{
				Headers: fanHeaders,
				Rows:    fanRows,
			}

			var sensorRows [][]string
			for _, sensor := range sensorList {
				value, _ := sensor.GetValue()

				_, file := filepath.Split(sensor.Input)
				labelAndFile := fmt.Sprintf("%s (%s)", sensor.Label, file)

				sensorRows = append(sensorRows, []string{
					"", strconv.Itoa(sensor.Index), labelAndFile, strconv.Itoa(int(value)),
				})
			}
			var sensorHeaders = []string{"Sensors", "Index", "Label", "Value"}

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
