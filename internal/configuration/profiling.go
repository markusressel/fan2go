package configuration

type ProfilingConfig struct {
	Enabled bool   `json:"enabled"`
	Host    string `json:"host"`
	Port    int    `json:"port,omitempty"`
}
