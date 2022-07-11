package config

import (
	"github.com/germanoeich/nirn-proxy/libnew/util"
	"os"
	"strconv"
	"strings"
	"time"
)

var cfgSingleton NirnConfig

type NirnConfig struct {
	LogLevel             string
	Port                 string
	MetricsPort          string
	EnableMetrics        bool
	EnablePProf          bool
	BufferSize           int
	OutboundIP           string
	BindIP               string
	RequestTimeout       time.Duration
	ClusterPort          int
	ClusterMembers       []string
	ClusterDNS           string
	MaxBearerCount       int
	DisableHTTP2         bool
	BotRatelimitOverride map[string]uint
	RatelimitAbortAfter  int
}

func Get() NirnConfig {
	return cfgSingleton
}

func Parse() NirnConfig {
	logLevel := util.EnvGet("LOG_LEVEL", "info")
	port := util.EnvGet("PORT", "8080")
	metricsPort := util.EnvGet("METRICS_PORT", "9000")
	enableMetrics := util.EnvGetBool("ENABLE_METRICS", true)
	enablePprof := util.EnvGetBool("ENABLE_PPROF", false)
	bufferSize := util.EnvGetInt("BUFFER_SIZE", 50)
	outboundIp := os.Getenv("OUTBOUND_IP")
	bindIp := util.EnvGet("BIND_IP", "0.0.0.0")
	timeout := util.EnvGetInt("REQUEST_TIMEOUT", 5000)
	clusterPort := util.EnvGetInt("CLUSTER_PORT", 7946)
	clusterMembers := os.Getenv("CLUSTER_MEMBERS")
	clusterDns := os.Getenv("CLUSTER_DNS")
	maxBearerLruSize := util.EnvGetInt("MAX_BEARER_COUNT", 1024)
	disableHttp2 := util.EnvGetBool("DISABLE_HTTP_2", true)
	globalOverrides := util.EnvGet("BOT_RATELIMIT_OVERRIDES", "")
	ratelimitAbortAfter := util.EnvGetInt("RATELIMIT_ABORT_AFTER", -1)

	cfgSingleton = NirnConfig{
		LogLevel:             logLevel,
		Port:                 port,
		MetricsPort:          metricsPort,
		EnableMetrics:        enableMetrics,
		EnablePProf:          enablePprof,
		BufferSize:           bufferSize,
		OutboundIP:           outboundIp,
		BindIP:               bindIp,
		RequestTimeout:       time.Duration(timeout) * time.Millisecond,
		ClusterPort:          clusterPort,
		ClusterMembers:       strings.Split(clusterMembers, ","),
		ClusterDNS:           clusterDns,
		MaxBearerCount:       maxBearerLruSize,
		DisableHTTP2:         disableHttp2,
		BotRatelimitOverride: parseGlobalOverrides(globalOverrides),
		RatelimitAbortAfter:  ratelimitAbortAfter,
	}

	return cfgSingleton
}

func parseGlobalOverrides(overrides string) map[string]uint {
	// Format: "<bot_id>:<bot_global_limit>,<bot_id>:<bot_global_limit>

	ret := make(map[string]uint)

	if overrides == "" {
		return ret
	}

	overrideList := strings.Split(overrides, ",")
	for _, override := range overrideList {
		opts := strings.Split(override, ":")
		if len(opts) != 2 {
			panic("Invalid bot global ratelimit overrides")
		}

		limit, err := strconv.ParseInt(opts[1], 10, 32)

		if err != nil {
			panic("Failed to parse global ratelimit overrides")
		}

		ret[opts[0]] = uint(limit)
	}

	return ret
}
