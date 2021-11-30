package hwmon

import "regexp"

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

var (
	FanInputRegex  = regexp.MustCompile("^fan\\d+_input$")
	PwmRegex       = regexp.MustCompile("^pwm\\d+$")
	TempInputRegex = regexp.MustCompile("^temp\\d+_input$")
)
