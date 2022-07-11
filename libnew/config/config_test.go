package config

import (
	"github.com/stretchr/testify/assert"
	"os"
	"testing"
	"time"
)

func TestParseGlobalOverridesProperly(t *testing.T) {
	const data = `1:133,2:50,4:100,6:300`
	expected := map[string]uint{
		"1": 133,
		"2": 50,
		"4": 100,
		"6": 300,
	}

	actual := parseGlobalOverrides(data)
	if len(actual) != len(expected) {
		t.Errorf("Expected %v, got %v", expected, actual)
	}

	for k, v := range expected {
		if actual[k] != v {
			t.Errorf("Expected %v, got %v", expected, actual)
		}
	}
}

func TestEnvVarsResolveToCorrectValues(t *testing.T) {
	defer os.Clearenv()

	// Env values must either be unique strings, or in the case of bools and ints, diff from the default
	envVars := map[string]string{
		"LOG_LEVEL":               "test_value",
		"PORT":                    "test_value",
		"METRICS_PORT":            "test_value",
		"ENABLE_METRICS":          "false",
		"ENABLE_PPROF":            "true",
		"BUFFER_SIZE":             "133",
		"OUTBOUND_IP":             "test_value",
		"BIND_IP":                 "test_value",
		"REQUEST_TIMEOUT":         "133",
		"CLUSTER_PORT":            "133",
		"CLUSTER_MEMBERS":         "test_value",
		"CLUSTER_DNS":             "test_value",
		"MAX_BEARER_COUNT":        "133",
		"DISABLE_HTTP_2":          "false",
		"BOT_RATELIMIT_OVERRIDES": "1:1",
		"RATELIMIT_ABORT_AFTER":   "133",
	}

	for k, v := range envVars {
		os.Setenv(k, v)
	}

	Parse()
	cfg := Get()
	assert.Equal(t, "test_value", cfg.LogLevel)
	assert.Equal(t, "test_value", cfg.Port)
	assert.Equal(t, "test_value", cfg.MetricsPort)
	assert.Equal(t, false, cfg.EnableMetrics)
	assert.Equal(t, true, cfg.EnablePProf)
	assert.Equal(t, 133, cfg.BufferSize)
	assert.Equal(t, "test_value", cfg.OutboundIP)
	assert.Equal(t, "test_value", cfg.BindIP)
	assert.Equal(t, 133*time.Millisecond, cfg.RequestTimeout)
	assert.Equal(t, 133, cfg.ClusterPort)
	assert.Equal(t, "test_value", cfg.ClusterMembers[0])
	assert.Equal(t, "test_value", cfg.ClusterDNS)
	assert.Equal(t, 133, cfg.MaxBearerCount)
	assert.Equal(t, false, cfg.DisableHTTP2)
	assert.Equal(t, uint(1), cfg.BotRatelimitOverride["1"])
	assert.Equal(t, 133, cfg.RatelimitAbortAfter)
}
