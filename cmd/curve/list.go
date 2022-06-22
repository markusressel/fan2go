package curve

import (
	"bytes"
	"github.com/guptarohit/asciigraph"
	"github.com/markusressel/fan2go/cmd/global"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/curves"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/mgutz/ansi"
	"github.com/spf13/cobra"
	"github.com/tomlazar/table"
	"golang.org/x/exp/maps"
	"sort"
)

var curveCmd = &cobra.Command{
	Use:   "list",
	Short: "Print the measured fan curve(s) to console",
	RunE: func(cmd *cobra.Command, args []string) (err error) {
		configPath := configuration.DetectConfigFile()
		ui.Info("Using configuration file at: %s", configPath)
		configuration.LoadConfig()

		err = configuration.Validate(configPath)
		if err != nil {
			ui.Fatal(err.Error())
		}

		for idx, curveConf := range configuration.CurrentConfig.Curves {
			if idx > 0 {
				ui.Printfln("")
				ui.Printfln("")
			}

			curve, err := curves.NewSpeedCurve(curveConf)
			if err != nil {
				return err
			}

			var curveType string
			var graphValues map[int]float64 = nil
			switch curve.(type) {
			case *curves.LinearSpeedCurve:
				curveType = "Linear"

				var keys map[int]float64
				var start int
				var stop int
				if curveConf.Linear.Steps != nil {
					keys = curveConf.Linear.Steps
					start = int(util.Min(maps.Values(keys)))
					stop = int(util.Max(maps.Values(keys)))
				} else {
					keys = map[int]float64{
						curveConf.Linear.Min: 0,
						curveConf.Linear.Max: 255,
					}
					start = curveConf.Linear.Min
					stop = curveConf.Linear.Max
				}

				graphValues = util.InterpolateLinearly(&keys, start, stop)
			case *curves.PidSpeedCurve:
				curveType = "PID"
			case *curves.FunctionSpeedCurve:
				curveType = "Functional"
			default:
				curveType = "Unknown"
			}

			// print table
			tab := table.Table{
				Headers: []string{"ID", "Type"},
				Rows: [][]string{
					{curve.GetId(), curveType},
				},
			}
			var buf bytes.Buffer
			tableErr := tab.WriteTable(&buf, &table.Config{
				ShowIndex:       false,
				Color:           !global.NoColor,
				AlternateColors: true,
				TitleColorCode:  ansi.ColorCode("white+buf"),
				AltColorCodes: []string{
					ansi.ColorCode("white"),
					ansi.ColorCode("white:236"),
				},
			})
			if tableErr != nil {
				panic(tableErr)
			}
			tableString := buf.String()
			ui.Printfln(tableString)

			if graphValues == nil {
				continue
			}

			keys := make([]int, 0, len(graphValues))
			for k := range graphValues {
				keys = append(keys, k)
			}
			sort.Ints(keys)

			values := make([]float64, 0, len(keys))
			for _, k := range keys {
				values = append(values, graphValues[k])
			}

			caption := "RPM / PWM"
			graph := asciigraph.Plot(values, asciigraph.Height(15), asciigraph.Width(100), asciigraph.Caption(caption))
			ui.Printfln(graph)
		}

		return nil
	},
}

func init() {
	Command.AddCommand(curveCmd)
}
