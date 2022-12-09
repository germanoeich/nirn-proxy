package main

import (
	"context"
	"github.com/germanoeich/nirn-proxy/lib"
	"github.com/hashicorp/memberlist"
	_ "github.com/joho/godotenv/autoload"
	"github.com/sirupsen/logrus"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

var logger = logrus.New()

// token : queue map
var bufferSize = 50

func setupLogger() {
	logLevel := lib.EnvGet("LOG_LEVEL", "info")
	lvl, err := logrus.ParseLevel(logLevel)

	if err != nil {
		panic("Failed to parse log level")
	}

	logger.SetLevel(lvl)
	lib.SetLogger(logger)
}

func initCluster(proxyPort string, manager *lib.QueueManager) *memberlist.Memberlist {
	port := lib.EnvGetInt("CLUSTER_PORT", 7946)

	memberEnv := os.Getenv("CLUSTER_MEMBERS")
	dns := os.Getenv("CLUSTER_DNS")

	if memberEnv == "" && dns == "" {
		logger.Info("Running in stand-alone mode")
		return nil
	}

	logger.Info("Attempting to create/join cluster")
	var members []string
	if memberEnv != "" {
		members = strings.Split(memberEnv, ",")
	} else {
		ips, err := net.LookupIP(dns)
		if err != nil {
			logger.Panic(err)
		}

		if len(ips) == 0 {
			logger.Panic("no ips returned by dns")
		}

		for _, ip := range ips {
			members = append(members, ip.String())
		}
	}

	return lib.InitMemberList(members, port, proxyPort, manager)
}

func main() {
	outboundIp := os.Getenv("OUTBOUND_IP")

	timeout := lib.EnvGetInt("REQUEST_TIMEOUT", 5000)

	disableHttp2 := lib.EnvGetBool("DISABLE_HTTP_2", true)

	globalOverrides := lib.EnvGet("BOT_RATELIMIT_OVERRIDES", "")

	disableGlobalRatelimitDetection := lib.EnvGetBool("DISABLE_GLOBAL_RATELIMIT_DETECTION", false)

	lib.ConfigureDiscordHTTPClient(outboundIp, time.Duration(timeout)*time.Millisecond, disableHttp2, globalOverrides, disableGlobalRatelimitDetection)

	port := lib.EnvGet("PORT", "8080")
	bindIp := lib.EnvGet("BIND_IP", "0.0.0.0")

	setupLogger()

	bufferSize = lib.EnvGetInt("BUFFER_SIZE", 50)
	maxBearerLruSize := lib.EnvGetInt("MAX_BEARER_COUNT", 1024)

	manager := lib.NewQueueManager(bufferSize, maxBearerLruSize)

	mux := manager.CreateMux()

	s := &http.Server{
		Addr:              bindIp + ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       10 * time.Second,
		WriteTimeout:      1 * time.Hour,
		MaxHeaderBytes:    1 << 20,
	}

	if os.Getenv("ENABLE_PPROF") == "true" {
		go lib.StartProfileServer()
	}

	if os.Getenv("ENABLE_METRICS") != "false" {
		port := lib.EnvGet("METRICS_PORT", "9000")
		go lib.StartMetrics(bindIp + ":" + port)
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.WithFields(logrus.Fields{"function": "http.ListenAndServe"}).Panic(err)
		}
	}()

	logger.Info("Started proxy on " + bindIp + ":" + port)

	// Wait for the http server to ready before joining the cluster
	<-time.After(1 * time.Second)
	initCluster(port, manager)

	<-done
	logger.Info("Server received shutdown signal")

	logger.Info("Broadcasting leave message to cluster, if in cluster mode")
	manager.Shutdown()

	logger.Info("Gracefully shutting down HTTP server")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := s.Shutdown(ctx); err != nil {
		logger.WithFields(logrus.Fields{"function": "http.Shutdown"}).Error(err)
	}

	logger.Info("Bye bye")
}
