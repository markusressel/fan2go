package config

import (
	"github.com/spf13/viper"
	"log"
	"time"
)

type Configuration struct {
	PollingRate       time.Duration
	RollingWindowSize int
	UpdateTickRate    time.Duration
	Sensors           []SensorConfig
	Fans              []FanConfig
	//FanCurves []FanCurve
}

type SensorConfig struct {
	Id       string
	Platform string
	Index    int
	Min      int
	Max      int
}

type FanConfig struct {
	Id        string
	Platform  string
	Fan       int
	NeverStop bool
	Sensor    string
}

var CurrentConfig Configuration

// one time setup for the configuration file
func init() {
	viper.SetConfigName("fan2go")

	viper.AddConfigPath(".")
	viper.AddConfigPath("/etc/fan2go/")
	viper.AddConfigPath("$HOME/.fan2go")

	setDefaultValues()
	readConfigFile()
}

func readConfigFile() {
	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Error reading config file, %s", err)
	}

	err := viper.Unmarshal(&CurrentConfig)
	if err != nil {
		log.Fatalf("unable to decode into struct, %v", err)
	}
}

func setDefaultValues() {
	viper.SetDefault("pollingRate", 200*time.Millisecond)
	viper.SetDefault("rollingWindowSize", 100)
	viper.SetDefault("updateTickRate", 100*time.Millisecond)
}
