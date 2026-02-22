package sensors

import (
	"fmt"
	"testing"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newAcpiSensor(method string, conversion configuration.AcpiSensorConversion) *AcpiSensor {
	return &AcpiSensor{
		Config: configuration.SensorConfig{
			ID: "acpi_test",
			Acpi: &configuration.AcpiSensorConfig{
				Method:     method,
				Conversion: conversion,
			},
		},
	}
}

func TestAcpiSensor_GetId(t *testing.T) {
	s := newAcpiSensor(`\_SB.METH`, "")
	assert.Equal(t, "acpi_test", s.GetId())
}

func TestAcpiSensor_GetLabel(t *testing.T) {
	s := newAcpiSensor(`\_SB.METH`, "")
	assert.Equal(t, "ACPI Sensor acpi_test", s.GetLabel())
}

func TestAcpiSensor_GetConfig(t *testing.T) {
	s := newAcpiSensor(`\_SB.METH`, "")
	cfg := s.GetConfig()
	assert.Equal(t, "acpi_test", cfg.ID)
	assert.Equal(t, `\_SB.METH`, cfg.Acpi.Method)
}

func TestAcpiSensor_MovingAvg(t *testing.T) {
	s := newAcpiSensor(`\_SB.METH`, "")
	s.SetMovingAvg(42000)
	assert.Equal(t, float64(42000), s.GetMovingAvg())
}

func TestAcpiSensor_GetValue_Celsius(t *testing.T) {
	s := newAcpiSensor(`\_SB.METH`, configuration.AcpiSensorConversionCelsius)
	mockCall := func(method, args string) (int64, error) {
		return 42, nil // 42°C
	}

	val, err := s.getValueAt(mockCall)
	require.NoError(t, err)
	assert.Equal(t, float64(42000), val) // 42 * 1000
}

func TestAcpiSensor_GetValue_CelsiusDefault(t *testing.T) {
	s := newAcpiSensor(`\_SB.METH`, "") // empty = default (celsius)
	mockCall := func(method, args string) (int64, error) {
		return 38, nil
	}

	val, err := s.getValueAt(mockCall)
	require.NoError(t, err)
	assert.Equal(t, float64(38000), val)
}

func TestAcpiSensor_GetValue_Millicelsius(t *testing.T) {
	s := newAcpiSensor(`\_SB.METH`, configuration.AcpiSensorConversionMillicelsius)
	mockCall := func(method, args string) (int64, error) {
		return 42000, nil
	}

	val, err := s.getValueAt(mockCall)
	require.NoError(t, err)
	assert.Equal(t, float64(42000), val) // pass-through
}

func TestAcpiSensor_GetValue_Raw(t *testing.T) {
	s := newAcpiSensor(`\_SB.METH`, configuration.AcpiSensorConversionRaw)
	mockCall := func(method, args string) (int64, error) {
		return 12345, nil
	}

	val, err := s.getValueAt(mockCall)
	require.NoError(t, err)
	assert.Equal(t, float64(12345), val) // pass-through
}

func TestAcpiSensor_GetValue_Error(t *testing.T) {
	s := newAcpiSensor(`\_SB.METH`, "")
	mockCall := func(method, args string) (int64, error) {
		return 0, fmt.Errorf("acpi_call: write failed")
	}

	_, err := s.getValueAt(mockCall)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "acpi_test")
}

func TestAcpiSensor_GetValue_PassesMethodAndArgs(t *testing.T) {
	s := &AcpiSensor{
		Config: configuration.SensorConfig{
			ID: "test",
			Acpi: &configuration.AcpiSensorConfig{
				Method: `\_SB.AMW3.WMAX`,
				Args:   "0 0x13",
			},
		},
	}

	var gotMethod, gotArgs string
	mockCall := func(method, args string) (int64, error) {
		gotMethod = method
		gotArgs = args
		return 50, nil
	}

	_, err := s.getValueAt(mockCall)
	require.NoError(t, err)
	assert.Equal(t, `\_SB.AMW3.WMAX`, gotMethod)
	assert.Equal(t, "0 0x13", gotArgs)
}
