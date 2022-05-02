package libnew

import (
	"github.com/germanoeich/nirn-proxy/libnew/bucket"
	"github.com/germanoeich/nirn-proxy/libnew/enums"
	"github.com/germanoeich/nirn-proxy/libnew/metrics"
	"github.com/germanoeich/nirn-proxy/libnew/util"
	"net/http"
	"strings"
)

type HttpHandler struct {
	globalRateLimiter GlobalRateLimiter
}

func (h*HttpHandler) GetRequestRoutingInfo(req *http.Request, token string) RequestInfo {
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