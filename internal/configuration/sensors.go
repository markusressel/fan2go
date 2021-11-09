package configuration

type SensorConfig struct {
	Id       string `json:"id"`
	Platform string `json:"platform"`
	Index    int    `json:"index"`
}
