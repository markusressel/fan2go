package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/markusressel/fan2go/internal/configuration"
	"github.com/markusressel/fan2go/internal/curves"
	"github.com/markusressel/fan2go/internal/fans"
	"github.com/markusressel/fan2go/internal/registry"
	"github.com/markusressel/fan2go/internal/sensors"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestWebserverAddress(t *testing.T) {
	testCases := []struct {
		name     string
		host     string
		port     int
		expected string
	}{
		{
			name:     "empty host",
			host:     "",
			port:     9000,
			expected: ":9000",
		},
		{
			name:     "IPv4 host",
			host:     "127.0.0.1",
			port:     9000,
			expected: "127.0.0.1:9000",
		},
		{
			name:     "bracketed IPv6 host with zone",
			host:     "[fe80::18c2:b5ff:fec0:71b8%vmbr1]",
			port:     9000,
			expected: "[fe80::18c2:b5ff:fec0:71b8%vmbr1]:9000",
		},
		{
			name:     "bracketed IPv6 host",
			host:     "[::1]",
			port:     9000,
			expected: "[::1]:9000",
		},
		{
			name:     "REST API host",
			host:     "127.0.0.1",
			port:     9001,
			expected: "127.0.0.1:9001",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.expected, webserverAddress(testCase.host, testCase.port))
		})
	}
}

func TestStatisticsConfigUnmarshalsHost(t *testing.T) {
	v := viper.New()
	v.SetConfigType("yaml")
	require.NoError(t, v.ReadConfig(strings.NewReader(`
statistics:
  enabled: true
  host: "[fe80::18c2:b5ff:fec0:71b8%vmbr1]"
  port: 9000
`)))

	var config struct {
		Statistics configuration.StatisticsConfig
	}
	require.NoError(t, v.Unmarshal(&config))
	require.Equal(t, "[fe80::18c2:b5ff:fec0:71b8%vmbr1]", config.Statistics.Host)
}

// freeTcpPorts returns n distinct currently unused TCP ports on 127.0.0.1
func freeTcpPorts(t *testing.T, n int) []int {
	t.Helper()
	var ports []int
	for range n {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer func() { _ = listener.Close() }()
		ports = append(ports, listener.Addr().(*net.TCPAddr).Port)
	}
	return ports
}

func httpGet(t *testing.T, url string) (int, string) {
	t.Helper()
	var resp *http.Response
	var err error
	// the servers are started in the background, retry until they accept connections
	require.Eventually(t, func() bool {
		resp, err = http.Get(url)
		return err == nil
	}, 5*time.Second, 20*time.Millisecond, "GET %s failed", url)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(body)
}

func TestStartWebservers_ServeAndShutdown(t *testing.T) {
	// GIVEN
	originalConfig := configuration.CurrentConfig
	t.Cleanup(func() { configuration.CurrentConfig = originalConfig })

	dir := t.TempDir()
	sensorPath := filepath.Join(dir, "temp")
	require.NoError(t, os.WriteFile(sensorPath, []byte("42000"), 0o644))

	ports := freeTcpPorts(t, 2)
	apiPort, statisticsPort := ports[0], ports[1]
	configuration.CurrentConfig.Api = configuration.ApiConfig{Enabled: true, Host: "127.0.0.1", Port: apiPort}
	configuration.CurrentConfig.Statistics = configuration.StatisticsConfig{Enabled: true, Host: "127.0.0.1", Port: statisticsPort}

	reg := registry.NewRegistry()
	sensor, err := sensors.NewSensor(configuration.SensorConfig{
		ID:   "sensor1",
		File: &configuration.FileSensorConfig{Path: sensorPath},
	})
	require.NoError(t, err)
	reg.RegisterSensor(sensor)
	curve, err := curves.NewSpeedCurve(configuration.CurveConfig{
		ID:     "curve1",
		Linear: &configuration.LinearCurveConfig{Sensor: "sensor1", Min: 20, Max: 80},
	})
	require.NoError(t, err)
	reg.RegisterCurve(curve)
	fan, err := fans.NewFan(configuration.FanConfig{
		ID:    "fan1",
		Curve: "curve1",
		File:  &configuration.FileFanConfig{Path: filepath.Join(dir, "pwm"), RpmPath: filepath.Join(dir, "rpm")},
	})
	require.NoError(t, err)
	reg.RegisterFan(fan)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup

	// WHEN
	startWebservers(ctx, reg, &wg)

	// THEN
	apiUrl := fmt.Sprintf("http://127.0.0.1:%d", apiPort)

	status, _ := httpGet(t, apiUrl+"/alive")
	require.Equal(t, http.StatusOK, status)

	for path, id := range map[string]string{"/fan": "fan1", "/sensor/": "sensor1", "/curve/": "curve1"} {
		status, body := httpGet(t, apiUrl+path)
		require.Equal(t, http.StatusOK, status, path)
		var items map[string]any
		require.NoError(t, json.Unmarshal([]byte(body), &items), path)
		require.Contains(t, items, id, path)
	}

	status, body := httpGet(t, apiUrl+"/fan/fan1")
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, `"id": "fan1"`)

	status, body = httpGet(t, apiUrl+"/fan/unknown/")
	require.Equal(t, http.StatusNotFound, status)
	require.JSONEq(t, `{"name": "Not found", "message": "No item with id 'unknown' found"}`, body)

	status, body = httpGet(t, fmt.Sprintf("http://127.0.0.1:%d/metrics", statisticsPort))
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, "go_goroutines")

	// servers must stop and release their ports when the context is cancelled
	cancel()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("webservers did not shut down")
	}
	for _, port := range []int{apiPort, statisticsPort} {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		require.NoError(t, err, "port %d is still in use", port)
		_ = listener.Close()
	}
}
