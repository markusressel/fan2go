//go:build disable_nvml

package fans

import (
	"errors"

	"github.com/markusressel/fan2go/internal/configuration"
)

func CreateNvidiaFan(config configuration.FanConfig) (Fan, error) {
	return nil, errors.New("This version of fan2go was built without NVIDIA (nvml) support")
}
