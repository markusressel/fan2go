package config

import (
	"github.com/spf13/viper"
	"log"
)

type Configuration struct {
	Sensors []SensorConfig
	Fans    []FanConfig
	//FanCurves []FanCurve
}

type SensorConfig struct {
	Id       string
	Platform string
	Sensor   string
	Min      int
	Max      int
}

type FanConfig struct {
	Id        string
	Platform  string
	Fan       int
	NeverStop bool
	Temps     []string
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
