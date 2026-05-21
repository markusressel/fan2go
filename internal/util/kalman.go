package util

const (
	defaultKalmanProcessNoise       = 25.0
	defaultKalmanMeasurementNoise   = 400.0
	defaultKalmanInitialEstimateErr = 1000.0
)

var DefaultKalmanConfig = KalmanConfig{
	ProcessNoise:         defaultKalmanProcessNoise,
	MeasurementNoise:     defaultKalmanMeasurementNoise,
	InitialEstimateError: defaultKalmanInitialEstimateErr,
}

type KalmanConfig struct {
	ProcessNoise         float64
	MeasurementNoise     float64
	InitialEstimateError float64
}

type KalmanFilter struct {
	estimate         float64
	errorCovariance  float64
	processNoise     float64
	measurementNoise float64
}

func NewKalmanFilter(cfg KalmanConfig, initialEstimate float64) *KalmanFilter {
	if cfg.ProcessNoise <= 0 {
		cfg.ProcessNoise = defaultKalmanProcessNoise
	}
	if cfg.MeasurementNoise <= 0 {
		cfg.MeasurementNoise = defaultKalmanMeasurementNoise
	}
	if cfg.InitialEstimateError <= 0 {
		cfg.InitialEstimateError = defaultKalmanInitialEstimateErr
	}

	return &KalmanFilter{
		estimate:         initialEstimate,
		errorCovariance:  cfg.InitialEstimateError,
		processNoise:     cfg.ProcessNoise,
		measurementNoise: cfg.MeasurementNoise,
	}
}

func (k *KalmanFilter) Update(measurement float64) float64 {
	k.errorCovariance += k.processNoise
	kalmanGain := k.errorCovariance / (k.errorCovariance + k.measurementNoise)
	k.estimate += kalmanGain * (measurement - k.estimate)
	k.errorCovariance *= (1.0 - kalmanGain)
	return k.estimate
}
