package util

import "sort"

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

// SmoothMapValuesKalman returns a copy of data where values for keys in [start;stop]
// are Kalman-smoothed in ascending key order. Values outside the range are unchanged.
func SmoothMapValuesKalman(data map[int]float64, start int, stop int, cfg KalmanConfig) map[int]float64 {
	smoothed := make(map[int]float64, len(data))
	for k, v := range data {
		smoothed[k] = v
	}
	if start > stop {
		return smoothed
	}

	keys := make([]int, 0, len(data))
	for k := range data {
		if k >= start && k <= stop {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return smoothed
	}
	sort.Ints(keys)

	filter := NewKalmanFilter(cfg, data[keys[0]])
	for _, key := range keys {
		smoothed[key] = filter.Update(data[key])
	}

	return smoothed
}
