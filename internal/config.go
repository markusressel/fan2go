package internal

import (
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/mitchellh/go-homedir"
	"github.com/spf13/viper"
	"os"
	"time"
)

type Configuration struct {
	DbPath                         string         `json:"dbPath"`
	RunFanInitializationInParallel bool           `json:"runFanInitializationInParallel"`
	TempSensorPollingRate          time.Duration  `json:"tempSensorPollingRate"`
	RpmPollingRate                 time.Duration  `json:"rpmPollingRate"`
	ControllerAdjustmentTickRate   time.Duration  `json:"controllerAdjustmentTickRate"`
	TempRollingWindowSize          int            `json:"tempRollingWindowSize"`
	RpmRollingWindowSize           int            `json:"rpmRollingWindowSize"`
	Sensors                        []SensorConfig `json:"sensors"`
	Curves                         []CurveConfig  `json:"curves"`
	Fans                           []FanConfig    `json:"fans"`
	MaxRpmDiffForSettledFan        float64        `json:"maxRpmDiffForSettledFan"`
}

type SensorConfig struct {
	Id       string `json:"id"`
	Platform string `json:"platform"`
	Index    int    `json:"index"`
}

type FanConfig struct {
	Id        string `json:"id"`
	Platform  string `json:"platform"`
	Fan       int    `json:"fan"`
	NeverStop bool   `json:"neverstop"`
	Curve     string `json:"curve"`
}

type CurveConfig struct {
	Id     string                 `json:"id"`
	Type   string                 `json:"type"`
	Params map[string]interface{} `json:"params"`
}

const (
	LinearCurveType   = "linear"
	FunctionCurveType = "function"
)

type LinearCurveConfig struct {
	Sensor  string      `json:"sensor"`
	MinTemp int         `json:"minTemp"`
	MaxTemp int         `json:"maxTemp"`
	Steps   map[int]int `json:"steps"`
}

const (
	FunctionAverage = "average"
	FunctionMinimum = "minimum"
	FunctionMaximum = "maximum"
)

type FunctionCurveConfig struct {
	Function string   `json:"function"`
	Curves   []string `json:"curves"`
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
