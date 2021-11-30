package configuration

import (
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
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
	config := &CurrentConfig

	validateSensors(config)
	validateCurves(config)
	validateFans(config)
}

func validateSensors(config *Configuration) {
	for _, sensorConfig := range config.Sensors {
		if sensorConfig.HwMon != nil && sensorConfig.File != nil {
			ui.Fatal("Sensor %s: only one sensor type can be used per sensor definition block", sensorConfig.ID)
		}

		if sensorConfig.HwMon == nil && sensorConfig.File == nil {
			ui.Fatal("Sensor %s: sub-configuration for sensor is missing, use one of: hwmon | file", sensorConfig.ID)
		}

		if !isSensorConfigInUse(sensorConfig, config.Curves) {
			ui.Warning("Unused sensor configuration: %s", sensorConfig.ID)
		}
	}
}

func isSensorConfigInUse(config SensorConfig, curves []CurveConfig) bool {
	for _, curveConfig := range curves {
		if curveConfig.Function != nil {
			// function curves cannot reference sensors
			continue
		}
		if curveConfig.Linear.Sensor == config.ID {
			return true
		}
	}

	return false
}

func validateCurves(config *Configuration) {
	for _, curveConfig := range config.Curves {
		if curveConfig.Linear != nil && curveConfig.Function != nil {
			ui.Fatal("Curve %s: only one curve type can be used per curve definition block", curveConfig.ID)
		}

		if curveConfig.Linear == nil && curveConfig.Function == nil {
			ui.Fatal("Curve %s: sub-configuration for curve is missing, use one of: linear | function", curveConfig.ID)
		}

		if !isCurveConfigInUse(curveConfig, config.Curves, config.Fans) {
			ui.Warning("Unused curve configuration: %s", curveConfig.ID)
		}
	}
}

func isCurveConfigInUse(config CurveConfig, curves []CurveConfig, fans []FanConfig) bool {
	for _, curveConfig := range curves {
		if curveConfig.Linear != nil {
			// linear curves cannot reference curves
			continue
		}

		if util.ContainsString(curveConfig.Function.Curves, config.ID) {
			return true
		}
	}

	for _, fanConfig := range fans {
		if fanConfig.Curve == config.ID {
			return true
		}
	}

	return false
}

func validateFans(config *Configuration) {
	for _, fanConfig := range config.Fans {
		if fanConfig.HwMon != nil && fanConfig.File != nil {
			ui.Fatal("Fans %s: only one fan type can be used per fan definition block", fanConfig.ID)
		}

		if fanConfig.HwMon == nil && fanConfig.File == nil {
			ui.Fatal("Fans %s: sub-configuration for fan is missing, use one of: hwmon | file", fanConfig.ID)
		}
	}
}
