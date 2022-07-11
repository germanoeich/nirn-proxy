package libnew

import (
	"context"
	"github.com/germanoeich/nirn-proxy/libnew/bucket"
	"github.com/germanoeich/nirn-proxy/libnew/config"
	"github.com/germanoeich/nirn-proxy/libnew/enums"
	"github.com/germanoeich/nirn-proxy/libnew/metrics"
	"github.com/germanoeich/nirn-proxy/libnew/util"
	"github.com/sirupsen/logrus"
	"net/http"
	"strings"
	"time"
)

type HttpHandler struct {
	globalRateLimiter *GlobalRateLimiter
	clusterManager    *ClusterManager
	s                 *http.Server
	logger            *logrus.Entry
}

func NewHttpHandler() *HttpHandler {
	return &HttpHandler{
		logger:            util.GetLogger("HttpHandler"),
		globalRateLimiter: NewGlobalRateLimiter(),
	}
}

// Not included in New due to startup order issues, http server has to start before clustering
func (h *HttpHandler) SetClusterManager(manager *ClusterManager) {
	h.clusterManager = manager
}

func (h *HttpHandler) Start() error {
	cfg := config.Get()
	h.s = &http.Server{
		Addr:           cfg.BindIP + ":" + cfg.Port,
		Handler:        h.CreateMux(),
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   1 * time.Hour,
		MaxHeaderBytes: 1 << 20,
	}

	h.logger.Info("Starting HTTP Handler on " + cfg.BindIP + ":" + cfg.Port)

	return h.s.ListenAndServe()
}

func (h *HttpHandler) Shutdown(ctx context.Context) error {
	h.logger.Info("Shutting down http handler")
	return h.s.Shutdown(ctx)
}

func (h *HttpHandler) GetRequestRoutingInfo(req *http.Request, token string) RequestInfo {
	info := RequestInfo{}
	info.path = bucket.GetOptimisticBucketPath(req.URL.Path, req.Method)
	info.token = token
	info.queueType = enums.NoAuth
	if strings.HasPrefix(token, "Bearer") {
		info.queueType = enums.Bearer
		info.routingHash = util.HashCRC64(token)
	} else {
		info.queueType = enums.Bot
		info.routingHash = util.HashCRC64(info.path)
	}
	return info
}

func (h *HttpHandler) DiscordRequestHandler(resp http.ResponseWriter, req *http.Request) {
	metrics.ConnectionsOpen.Inc()
	defer metrics.ConnectionsOpen.Dec()

	token := req.Header.Get("Authorization")
	routingInfo := h.GetRequestRoutingInfo(req, token)

	h.RouteRequest(&resp, req, routingInfo)
}

func (h *HttpHandler) RouteRequest(respPtr *http.ResponseWriter, req *http.Request, routingInfo RequestInfo) {

}

func (h *HttpHandler) CreateMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.DiscordRequestHandler)
	mux.HandleFunc("/nirn/global", h.globalRateLimiter.HandleGlobalRequest)
	mux.HandleFunc("/nirn/healthz", func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(200)
	})
	return mux
}
