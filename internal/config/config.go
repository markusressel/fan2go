package config

import (
	"github.com/spf13/viper"
	"log"
	"time"
)

type Configuration struct {
	DbPath                       string
	TempSensorPollingRate        time.Duration
	RpmPollingRate               time.Duration
	ControllerAdjustmentTickRate time.Duration
	TempRollingWindowSize        int
	RpmRollingWindowSize         int
	Sensors                      []SensorConfig
	Fans                         []FanConfig
}

type SensorConfig struct {
	Id       string
	Platform string
	Index    int
	Min      int
	Max      int
}

type FanConfig struct {
	Id        string `json:"id"`
	Platform  string `json:"platform"`
	Fan       int    `json:"fan"`
	NeverStop bool   `json:"neverstop"`
	Sensor    string `json:"sensor"`
}

var CurrentConfig Configuration

// one time setup for the configuration file.go
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
		log.Fatalf("Error reading config file.go, %s", err)
	}

	err := viper.Unmarshal(&CurrentConfig)
	if err != nil {
		log.Fatalf("unable to decode into struct, %v", err)
	}
}

func setDefaultValues() {
	viper.SetDefault("dbpath", "fan2go.db")
	viper.SetDefault("TempSensorPollingRate", 200*time.Millisecond)
	viper.SetDefault("RpmPollingRate", 1*time.Second)
	viper.SetDefault("TempRollingWindowSize", 100)
	viper.SetDefault("RpmRollingWindowSize", 10)
	viper.SetDefault("updateTickRate", 100*time.Millisecond)
}
