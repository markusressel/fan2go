package configuration

type StatisticsConfig struct {
	Enabled bool `json:"enabled"`
	Port    int  `json:"port,omitempty"`
}
