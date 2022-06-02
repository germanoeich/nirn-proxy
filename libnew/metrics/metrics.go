package metrics

import (
	"github.com/germanoeich/nirn-proxy/libnew/logging"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
)

var logger = logging.GetLogger("metrics")

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

	ConnectionsOpen = promauto.NewGauge(prometheus.GaugeOpts{
		Name:        "nirn_proxy_open_connections",
		Help:        "Gauge for client connections currently open with the proxy",
	})

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

func ObserveRequestHistogram(route string, status string, method string, clientId string, elapsed float64) {
	RequestHistogram.With(map[string]string{"route": route, "status": status, "method": method, "clientId": clientId}).Observe(elapsed)
}