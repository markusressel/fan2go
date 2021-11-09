package configuration

import (
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/mitchellh/go-homedir"
	"github.com/spf13/viper"
	"os"
	"time"
)

type Configuration struct {
	DbPath string `json:"dbPath"`

	RunFanInitializationInParallel bool    `json:"runFanInitializationInParallel"`
	MaxRpmDiffForSettledFan        float64 `json:"maxRpmDiffForSettledFan"`

	TempSensorPollingRate time.Duration `json:"tempSensorPollingRate"`
	TempRollingWindowSize int           `json:"tempRollingWindowSize"`

	RpmPollingRate       time.Duration `json:"rpmPollingRate"`
	RpmRollingWindowSize int           `json:"rpmRollingWindowSize"`

	ControllerAdjustmentTickRate time.Duration `json:"controllerAdjustmentTickRate"`

	Fans    []FanConfig    `json:"fans"`
	Sensors []SensorConfig `json:"sensors"`
	Curves  []CurveConfig  `json:"curves"`
}

var CurrentConfig Configuration

// InitConfig reads in config file and ENV variables if set.
func InitConfig(cfgFile string) {
	viper.SetConfigName("fan2go")

	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory.
		home, err := homedir.Dir()
		if err != nil {
			ui.Error("Couldn't detect home directory: %v", err)
			os.Exit(1)
		}

		viper.AddConfigPath(".")
		viper.AddConfigPath(home)
		viper.AddConfigPath("/etc/fan2go/")
	}

	viper.AutomaticEnv() // read in environment variables that match

	setDefaultValues()
}

func setDefaultValues() {
	viper.SetDefault("dbpath", "/etc/fan2go/fan2go.db")
	viper.SetDefault("RunFanInitializationInParallel", true)
	viper.SetDefault("MaxRpmDiffForSettledFan", 10.0)
	viper.SetDefault("TempSensorPollingRate", 200*time.Millisecond)
	viper.SetDefault("TempRollingWindowSize", 50)
	viper.SetDefault("RpmPollingRate", 1*time.Second)
	viper.SetDefault("RpmRollingWindowSize", 10)

	viper.SetDefault("ControllerAdjustmentTickRate", 200*time.Millisecond)

	viper.SetDefault("sensors", []SensorConfig{})
	viper.SetDefault("fans", []FanConfig{})
}

func ReadConfigFile() {
	if err := viper.ReadInConfig(); err != nil {
		// config file is required, so we fail here
		ui.Fatal("Error reading config file, %s", err)
	}
	// this is only populated _after_ ReadInConfig()
	ui.Info("Using configuration file at: %s", viper.ConfigFileUsed())

	LoadConfig()
	validateConfig()
}

func LoadConfig() {
	// load default configuration values
	err := viper.Unmarshal(&CurrentConfig)
	if err != nil {
		ui.Fatal("unable to decode into struct, %v", err)
	}
}

func validateConfig() {
	//config := &CurrentConfig
	// nothing yet
}
