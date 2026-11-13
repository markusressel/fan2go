package fan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/guptarohit/asciigraph"
	"github.com/markusressel/fan2go/cmd/global"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/persistence"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/mgutz/ansi"
	"github.com/spf13/cobra"
	"github.com/tomlazar/table"
)

var curveCmd = &cobra.Command{
	Use:   "curve",
	Short: "Print the measured fan curve(s) to console",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, err := configuration.LoadAndValidateConfigFile()
		if err != nil {
			return err
		}

		persistence := persistence.NewPersistence(configuration.CurrentConfig.DbPath)

		var fanList []fans.Fan
		for _, config := range configuration.CurrentConfig.Fans {
			fan, err := fans.NewFan(config)
			if err != nil {
				return fmt.Errorf("unable to process fan configuration: %s: %w", config.ID, err)
			}
			fanList = append(fanList, fan)
		}

		for idx, fan := range fanList {
			if fan.GetId() != fanId {
				continue
			}

			pwmData, fanCurveErr := persistence.LoadFanRpmData(fan)
			if fanCurveErr == nil {
				_ = fan.AttachFanRpmCurveData(&pwmData)
			}

			measuredStartPwm := "-"
			measuredMaxPwm := "-"
			if fanCurveErr == nil {
				measuredStartPwm, measuredMaxPwm = measuredPwmBoundaries(fan, pwmData)
			}

			if idx > 0 {
				ui.Printfln("")
				ui.Printfln("")
			}

			// print table
			ui.Println(fan.GetId())
			tab := table.Table{
				Headers: []string{"", ""},
				Rows: [][]string{
					{"Min PWM", strconv.Itoa(fan.GetMinPwm())},
					{"Start PWM", strconv.Itoa(fan.GetStartPwm())},
					{"Max PWM", strconv.Itoa(fan.GetMaxPwm())},
					{"Measured Start PWM", measuredStartPwm},
					{"Measured Max PWM", measuredMaxPwm},
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
				return fmt.Errorf("error printing table: %w", tableErr)
			}
			tableString := buf.String()
			ui.Println(tableString)

			// print graph
			if fanCurveErr != nil {
				ui.Println("No fan curve data yet...")
				return nil
			}

			for _, warning := range analyzeCurveDataQuality(pwmData) {
				ui.Warning("Fan %s: %s", fan.GetId(), warning)
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

			caption := fmt.Sprintf("RPM / PWM (%d points)", len(keys))
			graph := asciigraph.Plot(values, asciigraph.Height(15), asciigraph.Width(100), asciigraph.Caption(caption))
			ui.Println(graph)

			if global.Verbose {
				// Print raw persisted curve data for easy copy/paste.
				rawData, jsonErr := json.MarshalIndent(pwmData, "", "  ")
				if jsonErr != nil {
					ui.Error("Unable to serialize fan curve data to JSON: %v", jsonErr)
				} else {
					ui.Println(string(rawData))
				}
			}

			return nil
		}

		return fmt.Errorf("no fan with id found: %s", fanId)
	},
}

func analyzeCurveDataQuality(pwmData map[int]float64) []string {
	warnings := make([]string, 0, 4)
	if len(pwmData) == 0 {
		return []string{"curve data is empty"}
	}

	keys := make([]int, 0, len(pwmData))
	for k := range pwmData {
		keys = append(keys, k)
	}
	sort.Ints(keys)

	if _, ok := pwmData[fans.MinPwmValue]; !ok {
		warnings = append(warnings, "missing PWM 0 anchor in persisted curve data")
	}
	if _, ok := pwmData[fans.MaxPwmValue]; !ok {
		warnings = append(warnings, "missing PWM 255 anchor in persisted curve data")
	}

	minKey := keys[0]
	maxKey := keys[len(keys)-1]
	if minKey > fans.MinPwmValue || maxKey < fans.MaxPwmValue {
		warnings = append(warnings, fmt.Sprintf("curve domain is truncated to [%d..%d]", minKey, maxKey))
	}

	expectedRangeSize := maxKey - minKey + 1
	if len(keys) < expectedRangeSize {
		warnings = append(warnings, fmt.Sprintf("curve has %d missing PWM keys in [%d..%d]", expectedRangeSize-len(keys), minKey, maxKey))
	}

	return warnings
}

// measuredPwmBoundaries returns the start and max PWM derived from the given curve data of the fan, without config
// overrides ("-" if there is no data). The max PWM of a flat curve is the full range (see
// fans.ComputeFanPwmBoundariesFromCurveData).
func measuredPwmBoundaries(fan fans.Fan, pwmData map[int]float64) (startPwm string, maxPwm string) {
	if len(pwmData) == 0 {
		return "-", "-"
	}
	start, end := fans.ComputeFanPwmBoundariesFromCurveData(fan, pwmData, fans.MaxPwmValue)
	return strconv.Itoa(start), strconv.Itoa(end)
}

func init() {
	Command.AddCommand(curveCmd)
}
