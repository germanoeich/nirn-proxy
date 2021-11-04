package lib

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
	"os"
)

var (
	ErrorCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "nirn_proxy_error",
		Help: "The total number of errors when processing requests",
	})

	RequestSummary = prometheus.NewSummaryVec(prometheus.SummaryOpts{
		Name:        "nirn_proxy_requests",
		Help:        "Request histogram",
		Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
	}, []string{"method", "status", "route", "clientId"})
)

func StartMetrics() {
	port := os.Getenv("METRICS_PORT")
	if port == "" {
		port = "9000"
	}
	prometheus.MustRegister(RequestSummary)
	http.Handle("/metrics", promhttp.Handler())
	logger.Info("Starting metrics server")
	err := http.ListenAndServe(":" + port, nil)
	if err != nil {
		logger.Error(err)
		return
	}
}