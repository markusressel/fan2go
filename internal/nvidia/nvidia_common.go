package nvidia

import (
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/sensors"
)

type NvidiaController struct {
	Identifier string // e.g. "nvidia-10de2489-0400"
	Name       string // e.g. "NVIDIA GeForce RTX 3060 Ti"

	Fans    []fans.Fan
	Sensors []sensors.Sensor
}
