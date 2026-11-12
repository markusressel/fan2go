package configuration

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/creasty/defaults"
	"github.com/go-viper/mapstructure/v2"
	"github.com/markusressel/fan2go/internal/control_loop"

	"github.com/markusressel/fan2go/internal/ui"
	"github.com/mitchellh/go-homedir"
	"github.com/spf13/viper"
)

type Configuration struct {
	DbPath string `json:"dbPath"`

	RunFanInitializationInParallel bool    `json:"runFanInitializationInParallel"`
	MaxRpmDiffForSettledFan        float64 `json:"maxRpmDiffForSettledFan"`
	// FanResponseDelay is a plain number of seconds (e.g. 2) or a duration string (e.g. "500ms")
	FanResponseDelay SecondsOrDuration `json:"fanResponseDelay"`

	TempSensorPollingRate time.Duration `json:"tempSensorPollingRate"`
	TempRollingWindowSize int           `json:"tempRollingWindowSize"`
	// SensorStaleTimeout is the time after the last successful reading of a sensor after which its value
	// is considered stale, and the curves using it can't be evaluated anymore.
	// nil means unset, see GetSensorStaleTimeout for the effective value.
	SensorStaleTimeout *time.Duration `json:"sensorStaleTimeout,omitempty"`

	RpmPollingRate       time.Duration `json:"rpmPollingRate"`
	RpmRollingWindowSize int           `json:"rpmRollingWindowSize"`

	// Deprecated: use FanControllerConfig.AdjustmentTickRate instead
	ControllerAdjustmentTickRate time.Duration `json:"controllerAdjustmentTickRate"`

	Analysis      AnalysisConfig      `json:"analysis"`
	FanController FanControllerConfig `json:"fanController"`

	Fans    []FanConfig    `json:"fans"`
	Sensors []SensorConfig `json:"sensors"`
	Curves  []CurveConfig  `json:"curves"`

	Api        ApiConfig        `json:"api"`
	Statistics StatisticsConfig `json:"statistics"`
	Profiling  ProfilingConfig  `json:"profiling"`
}

var CurrentConfig Configuration

const (
	// minDefaultSensorStaleTimeout is the lower bound of the default sensorStaleTimeout
	minDefaultSensorStaleTimeout = 10 * time.Second
	// defaultSensorStaleTimeoutPollingPeriods is the number of tempSensorPollingRate periods
	// of the default sensorStaleTimeout
	defaultSensorStaleTimeoutPollingPeriods = 10
)

// GetSensorStaleTimeout returns the effective sensorStaleTimeout: the configured value, or
// max(10s, 10 * tempSensorPollingRate) if it is not set.
func (c *Configuration) GetSensorStaleTimeout() time.Duration {
	if c.SensorStaleTimeout != nil {
		return *c.SensorStaleTimeout
	}
	return max(minDefaultSensorStaleTimeout, defaultSensorStaleTimeoutPollingPeriods*c.TempSensorPollingRate)
}

// InitConfig reads in config file and ENV variables if set.
// InitConfig sets up viper to find the config file. The returned error is a ConfigError.
func InitConfig(cfgFile string) error {
	viper.SetConfigName("fan2go")

	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory.
		home, err := homedir.Dir()
		if err != nil {
			return NewConfigError(fmt.Errorf("couldn't detect home directory: %w", err))
		}

		viper.AddConfigPath(".")
		viper.AddConfigPath(home)
		viper.AddConfigPath("/etc/fan2go/")
	}

	viper.AutomaticEnv() // read in environment variables that match

	setDefaultValues()
	return nil
}

func setDefaultValues() {
	viper.SetDefault("dbpath", "/var/lib/fan2go/fan2go.db")
	viper.SetDefault("RunFanInitializationInParallel", true)
	viper.SetDefault("MaxRpmDiffForSettledFan", 20.0)
	viper.SetDefault("FanResponseDelay", 2)
	viper.SetDefault("TempSensorPollingRate", 200*time.Millisecond)
	viper.SetDefault("TempRollingWindowSize", 10)
	viper.SetDefault("RpmPollingRate", 1*time.Second)
	viper.SetDefault("RpmRollingWindowSize", 10)

	viper.SetDefault("Analysis", AnalysisConfig{
		CoarseStep:    16,
		SampleCount:   3,
		SampleDelay:   200 * time.Millisecond,
		SettleTimeout: 30 * time.Second,
	})
	viper.SetDefault("Analysis.CoarseStep", 16)
	viper.SetDefault("Analysis.SampleCount", 3)
	viper.SetDefault("Analysis.SampleDelay", 200*time.Millisecond)
	viper.SetDefault("Analysis.SettleTimeout", 30*time.Second)

	viper.SetDefault("Statistics", StatisticsConfig{
		Enabled: false,
		Host:    "127.0.0.1",
		Port:    9000,
	})
	viper.SetDefault("Statistics.Host", "127.0.0.1")
	viper.SetDefault("Statistics.Port", 9000)

	viper.SetDefault("Api", ApiConfig{
		Enabled: false,
		Host:    "127.0.0.1",
		Port:    9001,
	})
	viper.SetDefault("Api.Host", "127.0.0.1")
	viper.SetDefault("Api.Port", 9001)

	viper.SetDefault("Profiling", ProfilingConfig{
		Enabled: false,
		Host:    "127.0.0.1",
		Port:    6060,
	})
	viper.SetDefault("Profiling.Host", "127.0.0.1")
	viper.SetDefault("Profiling.Port", 6060)

	// set default of deprecated value to 0 to detect unset value
	viper.SetDefault("ControllerAdjustmentTickRate", 0*time.Millisecond)
	viper.SetDefault("FanController.AdjustmentTickRate", 200*time.Millisecond)
	viper.SetDefault("FanController.PwmSetDelay", 5*time.Millisecond)

	viper.SetDefault("sensors", []SensorConfig{})
	viper.SetDefault("fans", []FanConfig{})
}

// DetectAndReadConfigFile detects the path of the first existing config file and reads it.
// The returned error is a ConfigError.
func DetectAndReadConfigFile() (string, error) {
	err := ReadInConfig()
	if err != nil {
		return "", fmt.Errorf("error reading config file: %w", err)
	}
	return GetFilePath(), nil
}

// LoadAndValidateConfigFile detects, reads, decodes and validates the config file, and sets CurrentConfig to it.
// It returns the path of the config file. The returned error is a ConfigError.
func LoadAndValidateConfigFile() (string, error) {
	configPath, err := DetectAndReadConfigFile()
	if err != nil {
		return "", err
	}
	ui.Info("Using configuration file at: %s", configPath)
	CurrentConfig, err = LoadConfig()
	if err != nil {
		return configPath, fmt.Errorf("configuration parsing failed: %w", err)
	}
	err = ValidateConfig(&CurrentConfig, configPath)
	if err != nil {
		return configPath, fmt.Errorf("configuration validation failed: %w", err)
	}
	return configPath, nil
}

// ReadInConfig reads and parses the config file. The returned error is a ConfigError.
func ReadInConfig() error {
	return NewConfigError(viper.ReadInConfig())
}

// GetFilePath this is only populated _after_ ReadInConfig()
func GetFilePath() string {
	return viper.ConfigFileUsed()
}

// LoadConfig decodes the config file read by ReadInConfig, with the default values applied.
// The returned error is a ConfigError.
func LoadConfig() (Configuration, error) {
	cfg, err := loadConfig()
	return cfg, NewConfigError(err)
}

func loadConfig() (Configuration, error) {
	// load default configuration values
	cfg := Configuration{}
	if err := defaults.Set(&cfg); err != nil {
		return cfg, err
	}

	var md mapstructure.Metadata
	err := viper.Unmarshal(
		&cfg,
		viper.DecodeHook(
			mapstructure.ComposeDecodeHookFunc(
				pwmMapPointsHookFunc(),
				DefaultTrueBoolHookFunc(),
				secondsOrDurationHookFunc(),
				durationMillisecondsHookFunc(),
				mapstructure.StringToTimeDurationHookFunc(),
				mapstructure.StringToSliceHookFunc(","),
				mapstructure.TextUnmarshallerHookFunc(),
			),
		),
		func(dc *mapstructure.DecoderConfig) {
			dc.Metadata = &md
		},
	)
	if err != nil {
		return cfg, err
	}

	WarnUnknownKeys(&cfg, md.Unused, viper.Get)

	// apply default values again to set any nested struct defaults that were
	// created after the initial parsing pass
	if err = defaults.Set(&cfg); err != nil {
		return cfg, err
	}

	if err = applyTransformations(&cfg); err != nil {
		return cfg, err
	}

	if err = applyDeprecations(&cfg); err != nil {
		return cfg, err
	}

	return cfg, nil
}

// apply transformations between different formats available to configure fan curves
func applyTransformations(cfg *Configuration) error {
	// convert steps in linear curves from strings (with plain numbers or percent values) to floats between 0 and 255
	for i, curve := range cfg.Curves {
		if curve.Linear != nil && len(curve.Linear.InSteps) > 0 {
			if err := transformCurveSteps(&curve.ID, &curve.Linear.Steps, &curve.Linear.InSteps); err != nil {
				return err
			}
			cfg.Curves[i] = curve
		}
		if curve.Staircase != nil {
			if len(curve.Staircase.InSteps) > 0 {
				if err := transformCurveSteps(&curve.ID, &curve.Staircase.Steps, &curve.Staircase.InSteps); err != nil {
					return err
				}
				cfg.Curves[i] = curve
			} else {
				return fmt.Errorf("missing steps in curve %s", curve.ID)
			}
		}
	}
	return nil
}

func transformCurveSteps(ID *string, Steps *map[int]float64, InSteps *map[int]string) error {
	*Steps = make(map[int]float64)

	for temp, origstr := range *InSteps {
		str := strings.TrimSpace(origstr)
		l := len(str)
		isPercent := false
		if l > 1 && str[l-1] == '%' {
			isPercent = true
			str = str[:l-1] // cut off '%' because ParseFloat() wouldn't like it
		}
		speed, err := strconv.ParseFloat(str, 64)
		// ParseFloat accepts "NaN" and "Inf", which the range checks below don't catch (NaN compares false)
		if err == nil && (math.IsNaN(speed) || math.IsInf(speed, 0)) {
			err = fmt.Errorf("not a finite number")
		}
		if err != nil {
			return fmt.Errorf("invalid curve step value '%s' in %s - must be either just a number or a number followed by '%%'", origstr, *ID)
		}

		if isPercent {
			if speed < 0 || speed > 100 {
				return fmt.Errorf("invalid curve step value '%s' (=> %f) in %s - must be between 0%% and 100%%", origstr, speed, *ID)
			}
			// convert 0-100% into [0..255]
			if speed < 1 {
				// less than 1% always turns into 0
				speed = 0
			} else {
				// 1% turns into 1, 100% turns into 255
				// => convert 1..100% to 1..255
				// => 0..99 to 0..254 and then add 1
				speed = (speed-1)*(254.0/99.0) + 1
			}
		} else if speed < 0 || speed > 255 {
			return fmt.Errorf("invalid curve step value '%s' (=> %f) in %s - must be between 0 and 255", origstr, speed, *ID)
		}
		(*Steps)[temp] = speed
	}

	return nil
}

// apply deprecations and migrate values
func applyDeprecations(cfg *Configuration) error {
	if cfg.ControllerAdjustmentTickRate > 0 {
		ui.Warning("controllerAdjustmentTickRate is deprecated, use fanController.adjustmentTickRate instead")
		cfg.FanController.AdjustmentTickRate = cfg.ControllerAdjustmentTickRate
	}
	return nil
}

// UnmarshalText handles string shorthand forms for PwmMapConfig: "autodetect" and "identity".
func (c *PwmMapConfig) UnmarshalText(text []byte) error {
	switch string(text) {
	case "autodetect":
		*c = PwmMapConfig{Autodetect: &PwmMapAutodetectConfig{}}
	case "identity":
		*c = PwmMapConfig{Identity: &PwmMapIdentityConfig{}}
	default:
		return fmt.Errorf("unknown pwmMap value %q (expected: autodetect, identity, or a map with linear/values key)", string(text))
	}
	return nil
}

// UnmarshalText handles string shorthands for OnExitConfig: "restore" and "none".
func (c *OnExitConfig) UnmarshalText(text []byte) error {
	switch strings.ToLower(string(text)) {
	case "restore":
		*c = OnExitConfig{Restore: &OnExitRestoreConfig{}}
	case "none":
		*c = OnExitConfig{None: &OnExitNoneConfig{}}
	default:
		return fmt.Errorf("unknown onExit value %q (expected: restore, none, or a map with mode/speed keys)", string(text))
	}
	return nil
}

// UnmarshalText handles string shorthand forms for SetPwmToGetPwmMapConfig: "autodetect" and "identity".
func (c *SetPwmToGetPwmMapConfig) UnmarshalText(text []byte) error {
	switch string(text) {
	case "autodetect":
		*c = SetPwmToGetPwmMapConfig{Autodetect: &SetPwmToGetPwmMapAutodetectConfig{}}
	case "identity":
		*c = SetPwmToGetPwmMapConfig{Identity: &SetPwmToGetPwmMapIdentityConfig{}}
	default:
		return fmt.Errorf("unknown setPwmToGetPwmMap value %q (expected: autodetect, identity, or a map with linear/values key)", string(text))
	}
	return nil
}

// UnmarshalText is a custom unmarshaler for ControlAlgorithmConfig to handle string enum values
func (s *ControlAlgorithmConfig) UnmarshalText(text []byte) error {
	controlAlgorithm := string(text)

	// check if the value matches one of the enum values
	switch controlAlgorithm {
	case string(Pid):
		// default configuration for PID control algorithm
		*s = ControlAlgorithmConfig{
			Pid: &PidControlAlgorithmConfig{
				control_loop.DefaultPidConfig.P,
				control_loop.DefaultPidConfig.I,
				control_loop.DefaultPidConfig.D,
			},
		}
	case string(Direct):
		// default configuration for Direct control algorithm
		*s = ControlAlgorithmConfig{
			Direct: &DirectControlAlgorithmConfig{
				nil,
			},
		}
	default:
		// if the value is not one of the enum values, try to unmarshal into a ControlAlgorithmConfig struct
		config := ControlAlgorithmConfig{}
		err := json.Unmarshal(text, &config)
		if err != nil {
			return fmt.Errorf("invalid control algorithm config: %s", controlAlgorithm)
		} else {
			*s = config
		}
	}

	return nil
}
