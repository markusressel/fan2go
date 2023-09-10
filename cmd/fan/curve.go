package fan

import (
	"bytes"
	"github.com/guptarohit/asciigraph"
	"github.com/markusressel/fan2go/cmd/global"
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
	Run: func(cmd *cobra.Command, args []string) {
		configPath := configuration.DetectAndReadConfigFile()
		ui.Info("Using configuration file at: %s", configPath)
		configuration.LoadConfig()
		err := configuration.Validate(configPath)
		if err != nil {
			ui.FatalWithoutStacktrace(err.Error())
		}

		persistence := persistence.NewPersistence(configuration.CurrentConfig.DbPath)

		var fanList []fans.Fan
		for _, config := range configuration.CurrentConfig.Fans {
			fan, err := fans.NewFan(config)
			if err != nil {
				ui.Fatal("Unable to process fan configuration: %s", config.ID)
			}
			fanList = append(fanList, fan)
		}

		for idx, fan := range fanList {
			if fan.GetId() != fanId {
				continue
			}

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
					{"Min PWM", strconv.Itoa(fan.GetMinPwm())},
					{"Start PWM", strconv.Itoa(fan.GetStartPwm())},
					{"Max PWM", strconv.Itoa(fan.GetMaxPwm())},
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
				values = append(values, pwmData[k])
			}

			caption := "RPM / PWM"
			graph := asciigraph.Plot(values, asciigraph.Height(15), asciigraph.Width(100), asciigraph.Caption(caption))
			ui.Printfln(graph)
		}
	},
}

func init() {
	Command.AddCommand(curveCmd)
}
