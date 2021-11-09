package internal

type Controller struct {
	Name     string   `json:"name"`
	DType    string   `json:"dtype"`
	Modalias string   `json:"modalias"`
	Platform string   `json:"platform"`
	Path     string   `json:"path"`
	Fans     []Fan    `json:"fans"`
	Sensors  []Sensor `json:"sensors"`
}
