package cmd

import (
	"fmt"
	"github.com/markusressel/fan2go/internal"
	"github.com/markusressel/fan2go/internal/util"
	"github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"log"
	"os"
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
				fmt.Printf("  %s (%d): RPM: %d PWM: %d Auto: %v\n", fan.Name, fan.Index, rpm, pwm, isAuto)
			}

			for _, sensor := range controller.Sensors {
				value, _ := util.ReadIntFromFile(sensor.Input)
				fmt.Printf("  %s (%d): %d\n", sensor.Name, sensor.Index, value)
			}
		}
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number of fan2go",
	Long:  `All software has versions. This is fan2go's`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("0.0.14")
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
	rootCmd.AddCommand(versionCmd)

	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file (default is $HOME/.fan2go.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "More verbose output")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
