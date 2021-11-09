package configuration

type FanConfig struct {
	Id        string `json:"id"`
	Platform  string `json:"platform"`
	Fan       int    `json:"fan"`
	NeverStop bool   `json:"neverstop"`
	Curve     string `json:"curve"`
}
