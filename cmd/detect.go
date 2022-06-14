package cmd

import (
	"bytes"
	"fmt"
	"github.com/markusressel/fan2go/cmd/global"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/hwmon"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/mgutz/ansi"
	"github.com/spf13/cobra"
	"github.com/tomlazar/table"
	"path/filepath"
	"sort"
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
			Color:           !global.NoColor,
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

			fanMap := controller.Fans
			sensorMap := controller.Sensors

			if len(fanMap) <= 0 && len(sensorMap) <= 0 {
				continue
			}

			ui.Printfln("> %s", controller.Name)

			fanMapKeys := make([]int, 0, len(fanMap))
			for k := range fanMap {
				fanMapKeys = append(fanMapKeys, k)
			}
			sort.Ints(fanMapKeys)

			var fanRows [][]string
			for _, index := range fanMapKeys {
				fan := fanMap[index]

				pwmText := "N/A"
				pwm, err := fan.GetPwm()
				if err == nil {
					pwmText = strconv.Itoa(pwm)
				}

				rpmText := "N/A"
				if fan.Supports(fans.FeatureRpmSensor) {
					rpm, err := fan.GetRpm()
					if err == nil {
						rpmText = strconv.Itoa(rpm)
					}
				}

				isAuto, _ := fan.IsPwmAuto()
				fanRows = append(fanRows, []string{
					"", strconv.Itoa(fan.Index), fan.Label, rpmText, pwmText, fmt.Sprintf("%v", isAuto),
				})
			}
			var fanHeaders = []string{"Fans   ", "Index", "Label", "RPM", "PWM", "Auto"}

			fanTable := table.Table{
				Headers: fanHeaders,
				Rows:    fanRows,
			}

			sensorMapKeys := make([]int, 0, len(sensorMap))
			for k := range sensorMap {
				sensorMapKeys = append(sensorMapKeys, k)
			}
			sort.Ints(sensorMapKeys)

			var sensorRows [][]string
			for _, index := range sensorMapKeys {
				sensor := sensorMap[index]
				value, err := sensor.GetValue()
				valueText := "N/A"
				if err == nil {
					valueText = strconv.Itoa(int(value))
				}

				_, file := filepath.Split(sensor.Input)
				labelAndFile := fmt.Sprintf("%s (%s)", sensor.Label, file)

				sensorRows = append(sensorRows, []string{
					"", strconv.Itoa(sensor.Index), labelAndFile, valueText,
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
