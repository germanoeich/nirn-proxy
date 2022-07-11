package main

import (
	"context"
	lib "github.com/germanoeich/nirn-proxy/libnew"
	"github.com/germanoeich/nirn-proxy/libnew/config"
	"github.com/germanoeich/nirn-proxy/libnew/metrics"
	"github.com/germanoeich/nirn-proxy/libnew/util"
	_ "github.com/joho/godotenv/autoload"
	"github.com/sirupsen/logrus"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	cfg := config.Parse()
	logger := util.GetLogger("Main")

	if cfg.EnablePProf {
		go util.StartProfileServer()
	}

	if cfg.EnableMetrics {
		go metrics.StartMetrics(cfg.BindIP + ":" + cfg.MetricsPort)
	}

	httpHandler := lib.NewHttpHandler()

	go func() {
		if err := httpHandler.Start(); err != nil && err != http.ErrServerClosed {
			logger.WithFields(logrus.Fields{"function": "http.ListenAndServe"}).Panic(err)
		}
	}()

	// Wait for the http server to ready before joining the cluster
	<-time.After(1 * time.Second)

	clusterManager := lib.NewClusterManager()
	httpHandler.SetClusterManager(clusterManager)

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-done
	logger.Info("Server received shutdown signal")

	logger.Info("Broadcasting leave message to cluster, if in cluster mode")
	clusterManager.Shutdown()

	logger.Info("Gracefully shutting down HTTP server")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := httpHandler.Shutdown(ctx); err != nil {
		logger.WithFields(logrus.Fields{"function": "http.Shutdown"}).Error(err)
	}

	logger.Info("Bye bye")
}
