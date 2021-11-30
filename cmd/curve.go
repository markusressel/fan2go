package cmd

import (
	"bytes"
	"github.com/guptarohit/asciigraph"
	"github.com/markusressel/fan2go/internal"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/persistence"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/mgutz/ansi"
	"github.com/spf13/cobra"
	"github.com/tomlazar/table"
	"sort"
	"strconv"
)

var curveCmd = &cobra.Command{
	Use:   "curve",
	Short: "Print the measured fan curve(s) to console",
	//Long:  `All software has versions. This is fan2go's`,
	Run: func(cmd *cobra.Command, args []string) {
		configuration.ReadConfigFile()
		persistence := persistence.NewPersistence(configuration.CurrentConfig.DbPath)

		controllers, err := internal.FindControllers()
		if err != nil {
			ui.Fatal("Error detecting devices: %s", err.Error())
		}

		var fanList []fans.Fan
		for _, config := range configuration.CurrentConfig.Fans {
			if config.HwMon != nil {
				for _, controller := range controllers {
					if controller.Platform == config.HwMon.Platform {
						config.HwMon.PwmOutput = controller.PwmInputs[config.HwMon.Index-1]
						config.HwMon.RpmInput = controller.FanInputs[config.HwMon.Index-1]
						break
					}
				}
			}

			fan, err := fans.NewFan(config)
			if err != nil {
				ui.Fatal("Unable to process fan configuration: %s", config.ID)
			}
			fanList = append(fanList, fan)
		}

		for idx, fan := range fanList {
			pwmData, fanCurveErr := persistence.LoadFanPwmData(fan)
			if fanCurveErr == nil {
				_ = fan.AttachFanCurveData(&pwmData)
			}

			if idx > 0 {
				ui.Printfln("")
				ui.Printfln("")
			}

			// print table
			ui.Printfln(fan.GetId())
			tab := table.Table{
				Headers: []string{"", ""},
				Rows: [][]string{
					{"Start PWM", strconv.Itoa(fan.GetMinPwm())},
					{"Max PWM", strconv.Itoa(fan.GetMaxPwm())},
				},
			}
			var buf bytes.Buffer
			tableErr := tab.WriteTable(&buf, &table.Config{
				ShowIndex:       false,
				Color:           !noColor,
				AlternateColors: true,
				TitleColorCode:  ansi.ColorCode("white+buf"),
				AltColorCodes: []string{
					ansi.ColorCode("white"),
					ansi.ColorCode("white:236"),
				},
			})
			if tableErr != nil {
				panic(err)
			}
			tableString := buf.String()
			ui.Printfln(tableString)

			// print graph
			if fanCurveErr != nil {
				ui.Printfln("No fan curve data yet...")
				continue
			}

			keys := make([]int, 0, len(pwmData))
			for k := range pwmData {
				keys = append(keys, k)
			}
			sort.Ints(keys)

			values := make([]float64, 0, len(keys))
			for _, k := range keys {
				values = append(values, pwmData[k][0])
			}

			caption := "RPM / PWM"
			graph := asciigraph.Plot(values, asciigraph.Height(15), asciigraph.Width(100), asciigraph.Caption(caption))
			ui.Printfln(graph)
		}
	},
}

func init() {
	rootCmd.AddCommand(curveCmd)
}
