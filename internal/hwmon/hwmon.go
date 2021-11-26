package hwmon

type HwMonController struct {
	Name       string
	DType      string
	Modalias   string
	Platform   string
	Path       string
	FanInputs  []string
	PwmInputs  []string
	TempInputs []string
}
