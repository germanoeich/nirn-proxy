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
		Buckets: 	 []float64{.1, .25, 1, 2.5, 5, 20},
	}, []string{"method", "status", "route", "clientId"})

	ConnectionsOpen = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name:        "nirn_proxy_open_connections",
		Help:        "Gauge for client connections currently open with the proxy",
	}, []string{"method", "route"})

	RequestsRoutedSent = promauto.NewCounter(prometheus.CounterOpts{
		Name:		"nirn_proxy_requests_routed_sent",
		Help:		"Counter for requests routed from this node into other nodes",
	})

	RequestsRoutedRecv = promauto.NewCounter(prometheus.CounterOpts{
		Name:		"nirn_proxy_requests_routed_received",
		Help:		"Counter for requests received from other nodes",
	})

	RequestsRoutedError = promauto.NewCounter(prometheus.CounterOpts{
		Name:		"nirn_proxy_requests_routed_error",
		Help:		"Counter for failed requests routed from this node",
	})
)

func StartMetrics(addr string) {
	prometheus.MustRegister(RequestHistogram)
	http.Handle("/metrics", promhttp.Handler())
	logger.Info("Starting metrics server on " + addr)
	err := http.ListenAndServe(addr, nil)
	if err != nil {
		logger.Error(err)
		return
	}
}