package configuration

type StatisticsConfig struct {
	Enabled bool   `json:"enabled"`
	Host    string `json:"host"`
	Port    int    `json:"port,omitempty"`
}
