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
	"github.com/markusressel/fan2go/internal/nvidia"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/mgutz/ansi"
	"github.com/spf13/cobra"
	"github.com/tomlazar/table"
)

func printTables(tables []table.Table) {
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
			ui.Print(tableString)
		} else {
			ui.Println(tableString)
		}
	}
}

var detectCmd = &cobra.Command{
	Use:   "detect",
	Short: "Detect fans and sensors",
	Long:  `Detect fans and sensors on your system and print them to console.`,
	Run: func(cmd *cobra.Command, args []string) {
		configuration.LoadConfig()

		controllers := hwmon.GetChips()

		// === Print detected devices ===

		if len(controllers) > 0 {
			ui.Println("=========== hwmon: ============\n")
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

			ui.Printfln("> Platform: %s", controller.Name)

			var fanRows [][]string
			for _, fan := range fanSlice {

				pwmChannelText := "N/A"
				if fan.Config.HwMon != nil && fan.Config.HwMon.PwmChannel >= 0 {
					pwmChannelText = strconv.Itoa(fan.Config.HwMon.PwmChannel)
				}

				rpmChannelText := "N/A"
				if fan.Config.HwMon != nil && fan.Config.HwMon.RpmChannel >= 0 {
					rpmChannelText = strconv.Itoa(fan.Config.HwMon.RpmChannel)
				}

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

				controlModeText := "N/A"
				if fan.Supports(fans.FeatureControlMode) {
					controlMode, err := fan.GetControlMode()
					if err == nil {
						controlModeText = controlModeToString(controlMode)
					}
				}
				fanRows = append(fanRows, []string{
					"", strconv.Itoa(fan.Index), pwmChannelText, rpmChannelText, fan.Label, rpmText, pwmText, fmt.Sprintf("%v", controlModeText),
				})
			}
			var fanHeaders = []string{"Fans   ", "Index", "PWM Channel", "RPM Channel", "Label", "RPM", "PWM", "Mode"}

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

			printTables([]table.Table{fanTable, sensorTable})
		}

		nvControllers := nvidia.GetDevices()

		if len(nvControllers) > 0 {
			ui.Println("=========== nvidia: ===========\n")
		}

		for _, ctrl := range nvControllers {

			if len(ctrl.Fans) <= 0 && len(ctrl.Sensors) <= 0 {
				continue
			}

			ui.Printfln("> Device: %s", ctrl.Identifier)
			ui.Printfln("    Name: %s", ctrl.Name)

			var fanRows [][]string
			for _, fan := range ctrl.Fans {
				pwmText := "N/A"
				pwm, err := fan.GetPwm()
				if err == nil {
					pwmText = strconv.Itoa(pwm)
				}

				rpmText := "N/A"
				rpm, err := fan.GetRpm()
				if err == nil {
					rpmText = strconv.Itoa(rpm)
				}

				controlModeText := "N/A"
				if fan.Supports(fans.FeatureControlMode) {
					controlMode, err := fan.GetControlMode()
					if err == nil {
						controlModeText = controlModeToString(controlMode)
					}
				}
				row := []string{
					"", strconv.Itoa(fan.Index), fan.Label, pwmText, rpmText, fmt.Sprintf("%v", controlModeText),
				}
				fanRows = append(fanRows, row)
			}
			var fanHeaders = []string{"Fans   ", "Index", "Label", "PWM", "RPM", "Mode"}
			fanTable := table.Table{
				Headers: fanHeaders,
				Rows:    fanRows,
			}

			var sensorRows [][]string
			for _, sensor := range ctrl.Sensors {
				value, err := sensor.GetValue()
				valueText := "N/A"
				if err == nil {
					valueText = strconv.Itoa(int(value))
				}

				row := []string{"", "1", sensor.Label, valueText}
				sensorRows = append(sensorRows, row)
			}
			var sensorHeaders = []string{"Sensors", "Index", "Label", "Value"}
			sensorTable := table.Table{
				Headers: sensorHeaders,
				Rows:    sensorRows,
			}

			printTables([]table.Table{fanTable, sensorTable})
		}
	},
}

func controlModeToString(mode fans.ControlMode) string {
	switch mode {
	case fans.ControlModeAutomatic:
		return "Auto"
	case fans.ControlModePWM:
		return "Manual"
	case fans.ControlModeDisabled:
		return "Disabled"
	default:
		return "Unknown"
	}

}

func init() {
	rootCmd.AddCommand(detectCmd)
}
