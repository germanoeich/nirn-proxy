package lib

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
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

	ConnectionsOpen = promauto.NewGauge(prometheus.GaugeOpts{
		Name:        "nirn_proxy_open_connections",
		Help:        "Gauge for client connections currently open with the proxy",
	})
)

func StartMetrics(addr string) {
	prometheus.MustRegister(RequestSummary)
	http.Handle("/metrics", promhttp.Handler())
	logger.Info("Starting metrics server on " + addr)
	err := http.ListenAndServe(addr, nil)
	if err != nil {
		logger.Error(err)
		return
	}
}