package libnew

import (
	"context"
	"github.com/germanoeich/nirn-proxy/libnew/bucket"
	"github.com/germanoeich/nirn-proxy/libnew/config"
	"github.com/germanoeich/nirn-proxy/libnew/enums"
	"github.com/germanoeich/nirn-proxy/libnew/metrics"
	"github.com/germanoeich/nirn-proxy/libnew/util"
	lru "github.com/hashicorp/golang-lru"
	"github.com/im7mortal/kmutex"
	"github.com/sirupsen/logrus"
	"net/http"
	"strings"
	"sync"
	"time"
)

type HttpHandler struct {
	globalRateLimiter   *GlobalRateLimiter
	clusterManager      *ClusterManager
	s                   *http.Server
	logger              *logrus.Entry
	bearerQueues        *lru.Cache
	botQueues           sync.Map
	queueCreationKMutex *kmutex.Kmutex
	cancel              context.CancelFunc
	ctx                 context.Context
}

func onEvictLruItem(key interface{}, value interface{}) {
	go value.(*RequestQueue).Destroy()
}

func NewHttpHandler(ctx context.Context) *HttpHandler {
	ctx, cancel := context.WithCancel(ctx)
	cfg := config.Get()
	bearerMap, err := lru.NewWithEvict(cfg.MaxBearerCount, onEvictLruItem)

	if err != nil {
		panic(err)
	}

	return &HttpHandler{
		logger:              util.GetLogger("HttpHandler"),
		globalRateLimiter:   NewGlobalRateLimiter(),
		bearerQueues:        bearerMap,
		botQueues:           sync.Map{},
		queueCreationKMutex: kmutex.New(),
		ctx:                 ctx,
		cancel:              cancel,
	}
}

func (h *HttpHandler) Destroy() {
	h.cancel()
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
	route := h.clusterManager.CalculateRoute(routingInfo.routingHash)
	if route == "" {
		// Route locally
		q := h.getOrCreateQueue(routingInfo)
		if q == nil {
			(*respPtr).WriteHeader(http.StatusInternalServerError)
			return
		}
		ç
		q.QueueRequest(respPtr, req, routingInfo)
	}
}

func (h *HttpHandler) getOrCreateQueue(routingInfo RequestInfo) *QueueManager {
	var queue *QueueManager
	requestQueue := h.getQueue(routingInfo)
	if requestQueue != nil {
		return requestQueue
	}

	h.queueCreationKMutex.Lock(routingInfo.token)
	defer h.queueCreationKMutex.Unlock(routingInfo.token)

	// Check again if queue was created while waiting for lock
	requestQueue = h.getQueue(routingInfo)
	if requestQueue != nil {
		return requestQueue
	}

	var err error
	// Create new queue
	queue, err = NewQueueManager(h.ctx, routingInfo.token)

	if err != nil {
		logger.Error(err)
		return nil
	}

	if routingInfo.queueType == enums.Bearer {
		h.bearerQueues.Add(routingInfo.token, queue)
	} else {
		h.botQueues.Store(routingInfo.token, queue)
	}

	return queue
}

func (h *HttpHandler) getQueue(routingInfo RequestInfo) *QueueManager {
	if routingInfo.queueType == enums.Bearer {
		q, ok := h.bearerQueues.Get(routingInfo.token)
		if ok {
			return q.(*QueueManager)
		}
	} else {
		q, ok := h.botQueues.Load(routingInfo.routingHash)
		if ok {
			return q.(*QueueManager)
		}
	}
	return nil
}

func (h *HttpHandler) CreateMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.DiscordRequestHandler)
	mux.HandleFunc("/nirn/global", h.globalRateLimiter.HandleGlobalRequest)
	mux.HandleFunc("/nirn/healthz", func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(200)
		writer.Header()
	})
	return mux
}
