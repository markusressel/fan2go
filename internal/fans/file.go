package fans

import (
	"encoding/json"
	"os/user"
	"path/filepath"
	"strings"
	"sync"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/ui"
	"github.com/markusressel/fan2go/internal/util"
)

type FileFan struct {
	Config    configuration.FanConfig `json:"config"`
	MovingAvg float64                 `json:"movingAvg"`

	Pwm int `json:"pwm"`
	Rpm int `json:"rpm"`

	// mu protects all fields above, since the fan is accessed from different goroutines.
	// It is a leaf lock: no I/O and no calls to other fan methods while holding it.
	mu sync.RWMutex
}

// fileFanJson has the same fields and json tags as FileFan, without the mutex.
type fileFanJson struct {
	Config    configuration.FanConfig `json:"config"`
	MovingAvg float64                 `json:"movingAvg"`

	Pwm int `json:"pwm"`
	Rpm int `json:"rpm"`
}

// MarshalJSON serializes a snapshot of the fan that is taken under its lock.
func (fan *FileFan) MarshalJSON() ([]byte, error) {
	fan.mu.RLock()
	snapshot := fileFanJson{
		Config:    fan.Config,
		MovingAvg: fan.MovingAvg,
		Pwm:       fan.Pwm,
		Rpm:       fan.Rpm,
	}
	fan.mu.RUnlock()
	return json.Marshal(snapshot)
}

// fileConfig returns the file specific part of the fan config.
func (fan *FileFan) fileConfig() configuration.FileFanConfig {
	fan.mu.RLock()
	defer fan.mu.RUnlock()
	if fan.Config.File == nil {
		return configuration.FileFanConfig{}
	}
	return *fan.Config.File
}

func (fan *FileFan) GetId() string {
	fan.mu.RLock()
	defer fan.mu.RUnlock()
	return fan.Config.ID
}

func (fan *FileFan) GetLabel() string {
	return "File Fan " + fan.GetId()
}

func (fan *FileFan) GetIndex() int {
	return 1
}

func (fan *FileFan) GetStartPwm() int {
	return 1
}

func (fan *FileFan) SetStartPwm(pwm int, force bool) {
}

func (fan *FileFan) GetMinPwm() int {
	return MinPwmValue
}

func (fan *FileFan) SetMinPwm(pwm int, force bool) {
	// not supported
}

func (fan *FileFan) GetMaxPwm() int {
	return MaxPwmValue
}

func (fan *FileFan) SetMaxPwm(pwm int, force bool) {
	// not supported
}

func (fan *FileFan) GetRpm() (result int, err error) {
	filePath := fan.fileConfig().RpmPath
	// resolve home dir path
	if strings.HasPrefix(filePath, "~") {
		currentUser, err := user.Current()
		if err != nil {
			return result, err
		}

		filePath = filepath.Join(currentUser.HomeDir, filePath[1:])
	}

	integer, err := util.ReadIntFromFile(filePath)
	if err != nil {
		return 0, err
	}
	result = integer
	fan.mu.Lock()
	fan.Rpm = result
	fan.mu.Unlock()
	return result, err
}

func (fan *FileFan) GetRpmAvg() float64 {
	fan.mu.RLock()
	defer fan.mu.RUnlock()
	return float64(fan.Rpm)
}

func (fan *FileFan) SetRpmAvg(rpm float64) {
	fan.mu.Lock()
	defer fan.mu.Unlock()
	fan.Rpm = int(rpm)
}

func (fan *FileFan) GetPwm() (result int, err error) {
	filePath := fan.fileConfig().Path
	// resolve home dir path
	if strings.HasPrefix(filePath, "~") {
		currentUser, err := user.Current()
		if err != nil {
			return result, err
		}

		filePath = filepath.Join(currentUser.HomeDir, filePath[1:])
	}

	integer, err := util.ReadIntFromFile(filePath)
	if err != nil {
		return MinPwmValue, err
	}
	result = integer
	fan.mu.Lock()
	fan.Pwm = result
	fan.mu.Unlock()
	return result, err
}

func (fan *FileFan) SetPwm(pwm int) (err error) {
	filePath := fan.fileConfig().Path
	// resolve home dir path
	if strings.HasPrefix(filePath, "~") {
		currentUser, err := user.Current()
		if err != nil {
			return err
		}

		filePath = filepath.Join(currentUser.HomeDir, filePath[1:])
	}

	err = util.WriteIntToFileAtomic(pwm, filePath)
	if err != nil {
		ui.Error("Unable to write to file: %v", fan.fileConfig().Path)
		return err
	}
	return nil
}

var interpolated map[int]float64

func init() {
	var err error
	interpolated, err = util.InterpolateLinearly(&map[int]float64{0: 0, 255: 255}, 0, 255)
	if err != nil {
		panic(err)
	}
}

func (fan *FileFan) GetFanRpmCurveData() *map[int]float64 {
	return &interpolated
}

func (fan *FileFan) AttachFanRpmCurveData(curveData *map[int]float64) (err error) {
	// not supported
	return
}

func (fan *FileFan) UpdateFanRpmCurveValue(pwm int, rpm float64) {
	// not supported
}

func (fan *FileFan) GetCurveId() string {
	fan.mu.RLock()
	defer fan.mu.RUnlock()
	return fan.Config.Curve
}

func (fan *FileFan) ShouldNeverStop() bool {
	fan.mu.RLock()
	defer fan.mu.RUnlock()
	return fan.Config.NeverStop
}

func (fan *FileFan) GetControlMode() (ControlMode, error) {
	return 1, nil
}

func (fan *FileFan) SetControlMode(value ControlMode) (err error) {
	// nothing to do
	return nil
}

func (fan *FileFan) GetConfig() configuration.FanConfig {
	fan.mu.RLock()
	defer fan.mu.RUnlock()
	return fan.Config
}

func (fan *FileFan) SetConfig(config configuration.FanConfig) {
	fan.mu.Lock()
	defer fan.mu.Unlock()
	fan.Config = config
}

func (fan *FileFan) Supports(feature FeatureFlag) bool {
	switch feature {
	case FeatureControlModeWrite:
		return false
	case FeatureControlModeRead:
		return false
	case FeaturePwmSensor:
		_, err := util.ReadIntFromFile(fan.fileConfig().Path)
		return err == nil
	case FeatureRpmSensor:
		if len(fan.fileConfig().RpmPath) > 0 {
			return true
		}
	}
	return false
}
