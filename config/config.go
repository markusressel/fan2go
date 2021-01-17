package config

import (
	"github.com/spf13/viper"
	"log"
	"time"
)

type Configuration struct {
	PollingRate       time.Duration
	RollingwindowSize int
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

	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Error reading config file, %s", err)
	}
	err := viper.Unmarshal(&CurrentConfig)
	if err != nil {
		log.Fatalf("unable to decode into struct, %v", err)
	}

	setDefaultValues()
}

func setDefaultValues() {
}
