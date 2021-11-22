package cmd

import (
	"bytes"
	"github.com/guptarohit/asciigraph"
	"github.com/markusressel/fan2go/internal"
	"github.com/markusressel/fan2go/internal/configuration"
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
		persistence := internal.NewPersistence(configuration.CurrentConfig.DbPath)

		controllers, err := internal.FindControllers()
		if err != nil {
			ui.Fatal("Error detecting devices: %s", err.Error())
		}
		internal.MapConfigToControllers(controllers)

		for _, controller := range controllers {
			if len(controller.Name) <= 0 || len(controller.Fans) <= 0 {
				continue
			}

			for idx, fan := range controller.Fans {
				if fan.GetConfig() == nil {
					continue
				}
				pwmData, fanCurveErr := persistence.LoadFanPwmData(fan)
				if fanCurveErr == nil {
					_ = internal.AttachFanCurveData(&pwmData, fan)
				}

				if idx > 0 {
					ui.Println("")
					ui.Println("")
				}

				// print table
				ui.Println(controller.Name + " -> " + fan.GetName())
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
				ui.Println(tableString)

				// print graph
				if fanCurveErr != nil {
					ui.Println("No fan curve data yet...")
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
				ui.Println(graph)
			}

			ui.Println("")
		}
	},
}

func init() {
	rootCmd.AddCommand(curveCmd)
}
