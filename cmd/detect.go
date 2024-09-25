package cmd

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/markusressel/fan2go/cmd/global"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/hwmon"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/mgutz/ansi"
	"github.com/spf13/cobra"
	"github.com/tomlazar/table"
)

var detectCmd = &cobra.Command{
	Use:   "detect",
	Short: "Detect fans and sensors",
	Long:  `Detect fans and sensors on your system and print them to console.`,
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

			fanSlice := controller.Fans
			sensorMap := controller.Sensors

			if len(fanSlice) <= 0 && len(sensorMap) <= 0 {
				continue
			}

			ui.Printfln("> %s", controller.Name)

			var fanRows [][]string
			for _, fan := range fanSlice {
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
					"", strconv.Itoa(fan.Index), strconv.Itoa(fan.Config.HwMon.RpmChannel), fan.Label, rpmText, pwmText, fmt.Sprintf("%v", isAuto),
				})
			}
			var fanHeaders = []string{"Fans   ", "Index", "Channel", "Label", "RPM", "PWM", "Auto"}

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
					ui.Println(tableString)
				}
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(detectCmd)
}
