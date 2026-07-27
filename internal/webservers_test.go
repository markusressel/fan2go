package internal

import (
	"strings"
	"testing"

	"github.com/markusressel/fan2go/internal/configuration"
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
