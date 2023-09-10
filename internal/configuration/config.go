package configuration

import (
	"os"
	"time"

	"github.com/markusressel/fan2go/internal/ui"
	"github.com/mitchellh/go-homedir"
	"github.com/spf13/viper"
)

type Configuration struct {
	DbPath string `json:"dbPath"`

	RunFanInitializationInParallel bool    `json:"runFanInitializationInParallel"`
	MaxRpmDiffForSettledFan        float64 `json:"maxRpmDiffForSettledFan"`
	FanResponseDelay               int     `json:"fanResponseDelay"`

	TempSensorPollingRate time.Duration `json:"tempSensorPollingRate"`
	TempRollingWindowSize int           `json:"tempRollingWindowSize"`

	RpmPollingRate       time.Duration `json:"rpmPollingRate"`
	RpmRollingWindowSize int           `json:"rpmRollingWindowSize"`

	ControllerAdjustmentTickRate time.Duration `json:"controllerAdjustmentTickRate"`

	Fans    []FanConfig    `json:"fans"`
	Sensors []SensorConfig `json:"sensors"`
	Curves  []CurveConfig  `json:"curves"`

	Api        ApiConfig        `json:"api"`
	Statistics StatisticsConfig `json:"statistics"`
	Profiling  ProfilingConfig  `json:"profiling"`
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
			ui.ErrorAndNotify("Path Error", "Couldn't detect home directory: %v", err)
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
	viper.SetDefault("MaxRpmDiffForSettledFan", 20.0)
	viper.SetDefault("FanResponseDelay", 2)
	viper.SetDefault("TempSensorPollingRate", 200*time.Millisecond)
	viper.SetDefault("TempRollingWindowSize", 10)
	viper.SetDefault("RpmPollingRate", 1*time.Second)
	viper.SetDefault("RpmRollingWindowSize", 10)

	viper.SetDefault("Statistics", StatisticsConfig{
		Enabled: false,
		Port:    9000,
	})
	viper.SetDefault("Statistics.Port", 9000)

	viper.SetDefault("Api", ApiConfig{
		Enabled: false,
		Host:    "localhost",
		Port:    9001,
	})
	viper.SetDefault("Api.Host", "localhost")
	viper.SetDefault("Api.Port", 9001)

	viper.SetDefault("Profiling", ProfilingConfig{
		Enabled: false,
		Host:    "localhost",
		Port:    6060,
	})
	viper.SetDefault("Profiling.Host", "localhost")
	viper.SetDefault("Profiling.Port", 6060)

	viper.SetDefault("ControllerAdjustmentTickRate", 200*time.Millisecond)

	viper.SetDefault("sensors", []SensorConfig{})
	viper.SetDefault("fans", []FanConfig{})
}

// DetectAndReadConfigFile detects the path of the first existing config file
func DetectAndReadConfigFile() string {
	err := readInConfig()
	if err != nil {
		ui.FatalWithoutStacktrace("Error reading config file, %s", err)
	}
	return GetFilePath()
}

// readInConfig reads and parses the config file
func readInConfig() error {
	return viper.ReadInConfig()
}

// GetFilePath this is only populated _after_ readInConfig()
func GetFilePath() string {
	return viper.ConfigFileUsed()
}

func LoadConfig() {
	// load default configuration values
	err := viper.Unmarshal(&CurrentConfig)
	if err != nil {
		ui.Fatal("unable to decode into struct, %v", err)
	}
}
