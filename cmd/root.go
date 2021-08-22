package cmd

import (
	"bytes"
	"fmt"
	"github.com/guptarohit/asciigraph"
	"github.com/markusressel/fan2go/internal"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/mgutz/ansi"
	"github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/tomlazar/table"
	"log"
	"os"
	"sort"
	"strconv"
	"time"
)

var cfgFile string
var verbose bool

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "fan2go",
	Short: "A daemon to control the fans of a computer.",
	Long: `fan2go is a simple daemon that controls the fans
on your computer based on temperature sensors.`,
	// this is the default command to run when no subcommand is specified
	Run: func(cmd *cobra.Command, args []string) {
		readConfigFile()
		internal.Run(verbose)
	},
}

var detectCmd = &cobra.Command{
	Use:   "detect",
	Short: "Detect devices",
	Long:  `Detects all fans and sensors and prints them as a list`,
	Run: func(cmd *cobra.Command, args []string) {
		// load default configuration values
		err := viper.Unmarshal(&internal.CurrentConfig)
		if err != nil {
			log.Fatalf("unable to decode into struct, %v", err)
		}

		controllers, err := internal.FindControllers()
		if err != nil {
			log.Fatalf("Error detecting devices: %s", err.Error())
		}

		// === Print detected devices ===
		fmt.Printf("Detected Devices:\n")

		for _, controller := range controllers {
			if len(controller.Name) <= 0 {
				continue
			}

			fmt.Printf("%s\n", controller.Name)
			for _, fan := range controller.Fans {
				pwm := internal.GetPwm(fan)
				rpm := internal.GetRpm(fan)
				isAuto, _ := internal.IsPwmAuto(controller.Path)
				fmt.Printf("  %d: %s (%s): RPM: %d PWM: %d Auto: %v\n", fan.Index, fan.Label, fan.Name, rpm, pwm, isAuto)
			}

			for _, sensor := range controller.Sensors {
				value, _ := util.ReadIntFromFile(sensor.Input)
				fmt.Printf("  %d: %s (%s): %d\n", sensor.Index, sensor.Label, sensor.Name, value)
			}
		}
	},
}

var curveCmd = &cobra.Command{
	Use:   "curve",
	Short: "Print the measured fan curve(s) to console",
	//Long:  `All software has versions. This is fan2go's`,
	Run: func(cmd *cobra.Command, args []string) {
		readConfigFile()
		db := internal.OpenPersistence(internal.CurrentConfig.DbPath)
		defer db.Close()

		controllers, err := internal.FindControllers()
		if err != nil {
			log.Fatalf("Error detecting devices: %s", err.Error())
		}

		for _, controller := range controllers {
			if len(controller.Name) <= 0 || len(controller.Fans) <= 0 {
				continue
			}

			for idx, fan := range controller.Fans {
				pwmData, fanCurveErr := internal.LoadFanPwmData(db, fan)
				err = internal.AttachFanCurveData(db, fan)

				if idx > 0 {
					fmt.Println("")
					fmt.Println("")
				}

				// print table
				fmt.Println(controller.Name + " -> " + fan.Name)
				tab := table.Table{
					Headers: []string{"", ""},
					Rows: [][]string{
						{"Start PWM", strconv.Itoa(fan.StartPwm)},
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
				fmt.Println(tableString)

				// print graph
				if fanCurveErr != nil {
					fmt.Println("No fan curve data yet...")
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
				fmt.Println(graph)
			}

			fmt.Println("")
		}
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of fan2go",
	Long:  `All software has versions. This is fan2go's`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("0.0.16")
	},
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	viper.SetConfigName("fan2go")

	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory.
		home, err := homedir.Dir()
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		// Search config in home directory with name ".system-control" (without extension).
		viper.AddConfigPath(".")
		viper.AddConfigPath(home)
		viper.AddConfigPath("/etc/fan2go/")
	}

	viper.AutomaticEnv() // read in environment variables that match

	setDefaultValues()
}

func readConfigFile() {
	if err := viper.ReadInConfig(); err != nil {
		// config file is required, so we fail here
		log.Fatalf("Error reading config file, %s", err)
	}

	err := viper.Unmarshal(&internal.CurrentConfig)
	if err != nil {
		log.Fatalf("unable to decode into struct, %v", err)
	}

	validateConfig()
}

func validateConfig() {
	//config := &internal.CurrentConfig
	// nothing yet
}

func setDefaultValues() {
	viper.SetDefault("dbpath", "/etc/fan2go/fan2go.db")
	viper.SetDefault("TempSensorPollingRate", 200*time.Millisecond)
	viper.SetDefault("TempRollingWindowSize", 50)
	viper.SetDefault("RpmPollingRate", 1*time.Second)
	viper.SetDefault("RpmRollingWindowSize", 10)

	viper.SetDefault("ControllerAdjustmentTickRate", 200*time.Millisecond)

	viper.SetDefault("sensors", []internal.SensorConfig{})
	viper.SetDefault("fans", []internal.FanConfig{})
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	cobra.OnInitialize(initConfig)

	rootCmd.AddCommand(detectCmd)
	rootCmd.AddCommand(curveCmd)
	rootCmd.AddCommand(versionCmd)

	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file (default is $HOME/.fan2go.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "More verbose output")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
