package cmd

import (
	"bytes"
	"fmt"
	"github.com/guptarohit/asciigraph"
	"github.com/markusressel/fan2go/internal"
	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/mgutz/ansi"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"github.com/tomlazar/table"
	"os"
	"sort"
	"strconv"
)

var (
	cfgFile string
	noColor bool
	noStyle bool
	verbose bool
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "fan2go",
	Short: "A daemon to control the fans of a computer.",
	Long: `fan2go is a simple daemon that controls the fans
on your computer based on temperature sensors.`,
	// this is the default command to run when no subcommand is specified
	Run: func(cmd *cobra.Command, args []string) {
		setupUi()
		printHeader()

		configuration.ReadConfigFile()
		internal.Run(verbose)
	},
}

var detectCmd = &cobra.Command{
	Use:   "detect",
	Short: "Detect devices",
	Long:  `Detects all fans and sensors and prints them as a list`,
	Run: func(cmd *cobra.Command, args []string) {
		configuration.LoadConfig()

		controllers, err := internal.FindControllers()
		if err != nil {
			ui.Fatal("Error detecting devices: %v", err)
		}

		// === Print detected devices ===
		tableConfig := &table.Config{
			ShowIndex:       false,
			Color:           true,
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

			ui.Println("> %s", controller.Name)

			var fanRows [][]string
			for _, fan := range controller.Fans {
				pwm := internal.GetPwm(fan)
				rpm := internal.GetRpm(fan)
				isAuto, _ := internal.IsPwmAuto(controller.Path)
				fanRows = append(fanRows, []string{
					"", strconv.Itoa(fan.Index), fan.Label, fan.Name, strconv.Itoa(rpm), strconv.Itoa(pwm), fmt.Sprintf("%v", isAuto),
				})
			}
			var fanHeaders = []string{"Fans   ", "Index", "Label", "Name", "RPM", "PWM", "Auto"}

			fanTable := table.Table{
				Headers: fanHeaders,
				Rows:    fanRows,
			}

			var sensorRows [][]string
			for _, sensor := range controller.Sensors {
				value, _ := sensor.GetValue()
				//ui.Println("  %d: %s (%s): %d", sensor.Index, sensor.Label, sensor.Name, value)
				hwSensor := sensor.(*sensors.HwmonSensor)

				sensorRows = append(sensorRows, []string{
					"", strconv.Itoa(hwSensor.Index), hwSensor.Label, hwSensor.Name, strconv.Itoa(int(value)),
				})
			}
			var sensorHeaders = []string{"Sensors", "Index", "Label", "Name", "Value"}

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

var curveCmd = &cobra.Command{
	Use:   "curve",
	Short: "Print the measured fan curve(s) to console",
	//Long:  `All software has versions. This is fan2go's`,
	Run: func(cmd *cobra.Command, args []string) {
		configuration.ReadConfigFile()
		db := internal.OpenPersistence(configuration.CurrentConfig.DbPath)
		defer db.Close()

		controllers, err := internal.FindControllers()
		if err != nil {
			ui.Fatal("Error detecting devices: %s", err.Error())
		}

		for _, controller := range controllers {
			if len(controller.Name) <= 0 || len(controller.Fans) <= 0 {
				continue
			}

			for idx, fan := range controller.Fans {
				pwmData, fanCurveErr := internal.LoadFanPwmData(db, fan)
				if fanCurveErr == nil {
					internal.AttachFanCurveData(&pwmData, fan)
				}

				if idx > 0 {
					ui.Println("")
					ui.Println("")
				}

				// print table
				ui.Println(controller.Name + " -> " + fan.Name)
				tab := table.Table{
					Headers: []string{"", ""},
					Rows: [][]string{
						{"Start PWM", strconv.Itoa(fan.MinPwm)},
						{"Max PWM", strconv.Itoa(fan.MaxPwm)},
					},
				}
				var buf bytes.Buffer
				tableErr := tab.WriteTable(&buf, &table.Config{
					ShowIndex:       false,
					Color:           true,
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

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of fan2go",
	Long:  `All software has versions. This is fan2go's`,
	Run: func(cmd *cobra.Command, args []string) {
		ui.Println("0.0.18")
	},
}

func setupUi() {
	if noColor {
		pterm.DisableColor()
	}
	if noStyle {
		pterm.DisableStyling()
	}
}

// Print a large text with the LetterStyle from the standard theme.
func printHeader() {
	err := pterm.DefaultBigText.WithLetters(
		pterm.NewLettersFromStringWithStyle("fan", pterm.NewStyle(pterm.FgLightBlue)),
		pterm.NewLettersFromStringWithStyle("2", pterm.NewStyle(pterm.FgWhite)),
		pterm.NewLettersFromStringWithStyle("go", pterm.NewStyle(pterm.FgLightBlue)),
	).Render()
	if err != nil {
		fmt.Println("fan2go")
	}
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	cobra.OnInitialize(func() {
		configuration.InitConfig(cfgFile)
	})

	rootCmd.AddCommand(detectCmd)
	rootCmd.AddCommand(curveCmd)
	rootCmd.AddCommand(versionCmd)

	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file (default is $HOME/.fan2go.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&noColor, "no-color", "", false, "Disable all terminal output coloration")
	rootCmd.PersistentFlags().BoolVarP(&noStyle, "no-style", "", false, "Disable all terminal output styling")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "More verbose output")

	if err := rootCmd.Execute(); err != nil {
		ui.Error("%v", err)
		os.Exit(1)
	}
}
