package api

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/control_loop"
	"github.com/markusressel/fan2go/internal/controller"
	"github.com/markusressel/fan2go/internal/curves"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type constantCurve struct{}

func (c constantCurve) GetId() string              { return "constant_curve" }
func (c constantCurve) Evaluate() (float64, error) { return 100, nil }
func (c constantCurve) CurrentValue() float64      { return 100 }
func (c constantCurve) CurrentResult() curves.CurveResult {
	return curves.CurveResult{Value: 100, EvaluatedAt: time.Unix(0, 0)}
}

// When the fan controller disables sanity checks at runtime (the fan can't read back its PWM value
// or its control mode), the REST API shows them as disabled in the fan config.
func TestRestApi_FanConfigShowsSanityChecksDisabledAtRuntime(t *testing.T) {
	// GIVEN a file fan whose files don't exist, so it can't read back its PWM value
	reg := registry.NewRegistry()
	fan, err := fans.NewFan(configuration.FanConfig{
		ID:    "sanity_fan",
		Curve: "constant_curve",
		File:  &configuration.FileFanConfig{Path: "/nonexistent/fan2go-test/pwm", RpmPath: "/nonexistent/fan2go-test/rpm"},
	})
	require.NoError(t, err)
	reg.RegisterFan(fan)
	fanController := controller.NewFanController(nil, fan, constantCurve{}, control_loop.NewDirectControlLoop(nil), time.Second, false)

	// WHEN
	_ = fanController.UpdateFanSpeed()
	code, body := get(t, CreateRestService(reg), "/fan/sanity_fan/")

	// THEN
	golden := filepath.Join("testdata", "fan_sanity_fan_after_update.json")
	if *updateGolden {
		require.NoError(t, os.WriteFile(golden, []byte(body), 0644))
	}
	expected, err := os.ReadFile(golden)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, string(expected), body)
}
