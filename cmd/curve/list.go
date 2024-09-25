package curve

import (
	"bytes"
	"fmt"
	"github.com/guptarohit/asciigraph"
	"github.com/markusressel/fan2go/cmd/global"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/curves"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/mgutz/ansi"
	"github.com/spf13/cobra"
	"github.com/tomlazar/table"
	"sort"
	"strings"
)

var curveCmd = &cobra.Command{
	Use:   "list",
	Short: "Print the measured fan curve(s) to console",
	RunE: func(cmd *cobra.Command, args []string) (err error) {
		configPath := configuration.DetectAndReadConfigFile()
		ui.Info("Using configuration file at: %s", configPath)
		configuration.LoadConfig()

		err = configuration.Validate(configPath)
		if err != nil {
			ui.FatalWithoutStacktrace("configuration validation failed: %w", err)
		}

		curveConfigsToPrint := []configuration.CurveConfig{}
		if curveId != "" {
			curveConf, err := getCurveConfig(curveId, configuration.CurrentConfig.Curves)
			if err != nil {
				return err
			}
			curveConfigsToPrint = append(curveConfigsToPrint, *curveConf)
		} else {
			curveConfigsToPrint = append(curveConfigsToPrint, configuration.CurrentConfig.Curves...)
		}

		for idx, curveConfig := range curveConfigsToPrint {
			if idx > 0 {
				ui.Printfln("")
				ui.Printfln("")
			}

			curve, err := curves.NewSpeedCurve(curveConfig)
			if err != nil {
				return err
			}

			switch curve.(type) {
			case *curves.LinearSpeedCurve:
				printLinearCurveInfo(curve, curveConfig.Linear)
			case *curves.PidSpeedCurve:
				printPidCurveInfo(curve, curveConfig.PID)
			case *curves.FunctionSpeedCurve:
				printFunctionCurveInfo(curve, curveConfig.Function)
			}
		}

		return nil
	},
}

func printLinearCurveInfo(curve curves.SpeedCurve, config *configuration.LinearCurveConfig) {
	curveType := "Linear"

	sensorId := config.Sensor

	if config.Steps != nil {
		stepMappings := map[int]float64{}
		sortedStepKeys := util.SortedKeys(config.Steps)
		for _, x := range sortedStepKeys {
			y := config.Steps[x]
			stepMappings[int(y)] = float64(x)
		}

		headers := []string{"ID", "Type", "Sensor"}
		rows := [][]string{
			{curve.GetId(), curveType, sensorId},
		}

		printInfoTable(headers, rows)

		interpolated := util.InterpolateLinearly(&stepMappings, 0, 255)
		drawGraph(interpolated, "Temp / Curve Value")
	} else {
		headers := []string{"ID", "Type", "Sensor", "Min", "Max"}
		rows := [][]string{
			{curve.GetId(), curveType, sensorId, fmt.Sprint(config.Min), fmt.Sprint(config.Max)},
		}

		printInfoTable(headers, rows)

		/*
			var keys map[int]float64
			// TODO: to draw a graph of what the PWM curve would look like, we would need to
			//  calculate the target pwm for each input pwm for a given curve.
			//
			//  with the current architecture this isn't possible, because the curve instance is hardwired
			//  to a specific sensor.
			//  So to calculate curve targets we would need to set fake sensor values

			// creates a virtual sensor, to simulate inputs and calculate the output of the curve
			sensor := sensors.VirtualSensor{
				Name:  sensorId,
				Value: 0,
			}
			sensors.SensorMap[sensorId] = &sensor

			keys = map[int]float64{}

			for i := config.Min; i <= config.Max; i++ {
				sensor.Value = float64(i * 1000)
				v, _ := curve.Evaluate()
				keys[i] = float64(v)
			}

			start := 0
			stop := 100

			var graphValues map[int]float64 = nil
			graphValues = util.InterpolateLinearly(&keys, start, stop)

			if graphValues != nil {
				drawGraph(graphValues, "RPM / PWM")
			}
		*/
	}

}

func drawGraph(graphValues map[int]float64, caption string) {
	_keys := make([]int, 0, len(graphValues))
	for k := range graphValues {
		_keys = append(_keys, k)
	}
	sort.Ints(_keys)

	values := make([]float64, 0, len(_keys))
	for _, k := range _keys {
		values = append(values, graphValues[k])
	}

	graph := asciigraph.Plot(values, asciigraph.Height(15), asciigraph.Width(100), asciigraph.Caption(caption))
	ui.Println(graph)
}

func printFunctionCurveInfo(curve curves.SpeedCurve, config *configuration.FunctionCurveConfig) {
	curveType := "Functional"

	t := config.Type
	curveIds := config.Curves
	curveIdsText := strings.Join(curveIds, ", ")

	headers := []string{"ID", "Type", "Function", "Curve IDs"}
	rows := [][]string{
		{curve.GetId(), curveType, t, curveIdsText},
	}

	printInfoTable(headers, rows)
}

func printPidCurveInfo(curve curves.SpeedCurve, config *configuration.PidCurveConfig) {
	curveType := "PID"

	headers := []string{"ID", "Type", "P", "I", "D", "Set Point"}
	rows := [][]string{
		{curve.GetId(), curveType, fmt.Sprint(config.P), fmt.Sprint(config.I), fmt.Sprint(config.D), fmt.Sprint(config.SetPoint)},
	}

	printInfoTable(headers, rows)
}

func printInfoTable(headers []string, rows [][]string) {
	tab := table.Table{
		Headers: headers,
		Rows:    rows,
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
	ui.Println(tableString)
}

func init() {
	Command.AddCommand(curveCmd)
}
