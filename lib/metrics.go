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

	RequestHistogram = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:        "nirn_proxy_requests",
		Help:        "Request histogram",
	}, []string{"method", "status", "route", "clientId"})
)

func StartMetrics() {
	prometheus.MustRegister(RequestHistogram)
	http.Handle("/metrics", promhttp.Handler())
	logger.Info("Starting metrics server")
	err := http.ListenAndServe(":9000", nil)
	if err != nil {
		logger.Error(err)
		return
	}
}