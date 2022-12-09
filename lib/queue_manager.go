package lib

import (
	"context"
	"errors"
	lru "github.com/hashicorp/golang-lru"
	"github.com/hashicorp/memberlist"
	"github.com/sirupsen/logrus"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type QueueType int64

const (
	Bot QueueType = iota
	NoAuth
	Bearer
)

// Some routes that have @me on the path don't really spread out through the cluster, causing issues
// and exacerbating tail latency hits from Discord. Only routes with no ratelimit headers should be put here
var pathsToRouteLocally = map[uint64]struct{}{
	HashCRC64("/users/@me/channels"): {},
	HashCRC64("/users/@me"):          {},
}

type QueueManager struct {
	sync.RWMutex
	queues                   map[string]*RequestQueue
	bearerQueues             *lru.Cache
	bearerMu                 sync.RWMutex
	bufferSize               int
	cluster                  *memberlist.Memberlist
	clusterGlobalRateLimiter *ClusterGlobalRateLimiter
	orderedClusterMembers    []string
	nameToAddressMap         map[string]string
	localNodeName            string
	localNodeIP              string
	localNodeProxyListenAddr string
}

func onEvictLruItem(key interface{}, value interface{}) {
	go value.(*RequestQueue).destroy()
}

func NewQueueManager(bufferSize int, maxBearerLruSize int) *QueueManager {
	bearerMap, err := lru.NewWithEvict(maxBearerLruSize, onEvictLruItem)

	if err != nil {
		panic(err)
	}

	q := &QueueManager{
		queues:                   make(map[string]*RequestQueue),
		bearerQueues:             bearerMap,
		bufferSize:               bufferSize,
		cluster:                  nil,
		clusterGlobalRateLimiter: NewClusterGlobalRateLimiter(),
	}

	return q
}

func (m *QueueManager) Shutdown() {
	if m.cluster != nil {
		m.cluster.Leave(30 * time.Second)
	}
}

func (m *QueueManager) reindexMembers() {
	if m.cluster == nil {
		logger.Warn("reindexMembers called but cluster is nil")
		return
	}

	m.Lock()
	defer m.Unlock()
	m.bearerMu.Lock()
	defer m.bearerMu.Unlock()

	members := m.cluster.Members()
	var orderedMembers []string
	nameToAddressMap := make(map[string]string)
	for _, m := range members {
		orderedMembers = append(orderedMembers, m.Name)
		nameToAddressMap[m.Name] = m.Addr.String() + ":" + string(m.Meta)
	}
	sort.Strings(orderedMembers)

	m.orderedClusterMembers = orderedMembers
	m.nameToAddressMap = nameToAddressMap
}

func (m *QueueManager) onNodeJoin(node *memberlist.Node) {
	// Running in goroutine prevents a deadlock inside memberlist
	go m.reindexMembers()
}
func (m *QueueManager) onNodeLeave(node *memberlist.Node) {
	// Running in goroutine prevents a deadlock inside memberlist
	go m.reindexMembers()
}

func (m *QueueManager) GetEventDelegate() *NirnEvents {
	return &NirnEvents{
		OnJoin:  m.onNodeJoin,
		OnLeave: m.onNodeLeave,
	}
}

func (m *QueueManager) SetCluster(cluster *memberlist.Memberlist, proxyPort string) {
	m.cluster = cluster
	m.localNodeName = cluster.LocalNode().Name
	m.localNodeIP = cluster.LocalNode().Addr.String()
	m.localNodeProxyListenAddr = m.localNodeIP + ":" + proxyPort
	m.reindexMembers()
}

func (m *QueueManager) calculateRoute(pathHash uint64) string {
	if m.cluster == nil {
		// Route to self, proxy in stand-alone mode
		return ""
	}

	if pathHash == 0 {
		return ""
	}

	if _, ok := pathsToRouteLocally[pathHash]; ok {
		return ""
	}

	m.RLock()
	defer m.RUnlock()
	m.bearerMu.RLock()
	defer m.bearerMu.RUnlock()

	members := m.orderedClusterMembers
	count := uint64(len(members))

	if count == 0 {
		return ""
	}

	chosenIndex := pathHash % count
	addr := m.nameToAddressMap[members[chosenIndex]]
	if addr == m.localNodeProxyListenAddr {
		return ""
	}
	return addr
}

func (m *QueueManager) routeRequest(addr string, req *http.Request) (*http.Response, error) {
	nodeReq, err := http.NewRequestWithContext(req.Context(), req.Method, "http://"+addr+req.URL.Path+"?"+req.URL.RawQuery, req.Body)
	nodeReq.Header = req.Header.Clone()
	nodeReq.Header.Set("nirn-routed-to", addr)
	if err != nil {
		return nil, err
	}

	logger.WithFields(logrus.Fields{
		"to":     addr,
		"path":   req.URL.Path,
		"method": req.Method,
	}).Trace("Routing request to node in cluster")
	resp, err := client.Do(nodeReq)
	logger.WithFields(logrus.Fields{
		"to":     addr,
		"path":   req.URL.Path,
		"method": req.Method,
	}).Trace("Received response from node")
	if err == nil {
		RequestsRoutedSent.Inc()
	} else {
		RequestsRoutedError.Inc()
	}

	return resp, err
}

func Generate429(resp *http.ResponseWriter) {
	writer := *resp
	writer.Header().Set("generated-by-proxy", "true")
	writer.Header().Set("x-ratelimit-scope", "user")
	writer.Header().Set("x-ratelimit-limit", "1")
	writer.Header().Set("x-ratelimit-remaining", "0")
	writer.Header().Set("x-ratelimit-reset", strconv.FormatInt(time.Now().Add(1*time.Second).Unix(), 10))
	writer.Header().Set("x-ratelimit-after", "1")
	writer.Header().Set("retry-after", "1")
	writer.Header().Set("content-type", "application/json")
	writer.WriteHeader(429)
	writer.Write([]byte("{\n\t\"global\": false,\n\t\"message\": \"You are being rate limited.\",\n\t\"retry_after\": 1\n}"))
}

func (m *QueueManager) getOrCreateBotQueue(token string) (*RequestQueue, error) {
	m.RLock()
	q, ok := m.queues[token]
	m.RUnlock()

	if !ok {
		m.Lock()
		defer m.Unlock()
		// Check if it wasn't created while we didn't hold the lock
		q, ok = m.queues[token]
		if !ok {
			var err error
			q, err = NewRequestQueue(ProcessRequest, token, m.bufferSize)

			if err != nil {
				return nil, err
			}

			m.queues[token] = q
		}
	}

	return q, nil
}

func (m *QueueManager) getOrCreateBearerQueue(token string) (*RequestQueue, error) {
	m.bearerMu.RLock()
	q, ok := m.bearerQueues.Get(token)
	m.bearerMu.RUnlock()

	if !ok {
		m.bearerMu.Lock()
		defer m.bearerMu.Unlock()
		// Check if it wasn't created while we didn't hold the lock
		q, ok = m.bearerQueues.Get(token)
		if !ok {
			var err error
			q, err = NewRequestQueue(ProcessRequest, token, 5)

			if err != nil {
				return nil, err
			}

			m.bearerQueues.Add(token, q)
		}
	}

	return q.(*RequestQueue), nil
}

func (m *QueueManager) DiscordRequestHandler(resp http.ResponseWriter, req *http.Request) {
	reqStart := time.Now()
	metricsPath := GetMetricsPath(req.URL.Path)
	ConnectionsOpen.With(map[string]string{"route": metricsPath, "method": req.Method}).Inc()
	defer ConnectionsOpen.With(map[string]string{"route": metricsPath, "method": req.Method}).Dec()

	token := req.Header.Get("Authorization")
	routingHash, path, queueType := m.GetRequestRoutingInfo(req, token)

	m.fulfillRequest(&resp, req, queueType, path, routingHash, token, reqStart)
}

func (m *QueueManager) GetRequestRoutingInfo(req *http.Request, token string) (routingHash uint64, path string, queueType QueueType) {
	path = GetOptimisticBucketPath(req.URL.Path, req.Method)
	queueType = NoAuth
	if strings.HasPrefix(token, "Bearer") {
		queueType = Bearer
		routingHash = HashCRC64(token)
	} else {
		queueType = Bot
		routingHash = HashCRC64(path)
	}
	return
}

func (m *QueueManager) fulfillRequest(resp *http.ResponseWriter, req *http.Request, queueType QueueType, path string, pathHash uint64, token string, reqStart time.Time) {
	logEntry := logger.WithField("clientIp", req.RemoteAddr)
	forwdFor := req.Header.Get("X-Forwarded-For")
	if forwdFor != "" {
		logEntry = logEntry.WithField("forwardedFor", forwdFor)
	}
	routeTo := m.calculateRoute(pathHash)

	routeToHeader := req.Header.Get("nirn-routed-to")
	req.Header.Del("nirn-routed-to")

	if routeToHeader != "" {
		RequestsRoutedRecv.Inc()
	}

	var err error
	if routeTo == "" || routeToHeader != "" {
		var q *RequestQueue
		var err error
		if queueType == Bearer {
			q, err = m.getOrCreateBearerQueue(token)
		} else {
			q, err = m.getOrCreateBotQueue(token)
		}

		if err != nil {
			if strings.HasPrefix(err.Error(), "429") {
				Generate429(resp)
				logEntry.WithFields(logrus.Fields{"function": "getOrCreateQueue", "queueType": queueType}).Warn(err)
			} else {
				(*resp).WriteHeader(500)
				(*resp).Write([]byte(err.Error()))
				ErrorCounter.Inc()
				logEntry.WithFields(logrus.Fields{"function": "getOrCreateQueue", "queueType": queueType}).Error(err)
			}
			return
		}

		if q.identifier != "NoAuth" {
			var botHash uint64 = 0
			if q.user != nil {
				botHash = HashCRC64(q.user.Id)
			}

			botLimit := q.botLimit
			globalRouteTo := m.calculateRoute(botHash)

			if globalRouteTo == "" || queueType == Bearer {
				m.clusterGlobalRateLimiter.Take(botHash, botLimit)
			} else {
				err = m.clusterGlobalRateLimiter.FireGlobalRequest(req.Context(), globalRouteTo, botHash, botLimit)
				if err != nil {
					logEntry.WithField("function", "FireGlobalRequest").Error(err)
					ErrorCounter.Inc()
					Generate429(resp)
					return
				}
			}
		}
		err = q.Queue(req, resp, path, pathHash)
		if err != nil {
			log := logEntry.WithField("function", "Queue")
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				log.WithField("waitedFor", time.Since(reqStart)).Warn(err)
			} else {
				log.Error(err)
			}
		}
	} else {
		var res *http.Response
		res, err = m.routeRequest(routeTo, req)
		if err == nil {
			err = CopyResponseToResponseWriter(res, resp)
			if err != nil {
				logEntry.WithField("function", "CopyResponseToResponseWriter").Error(err)
			}
		} else {
			logEntry = logEntry.WithField("function", "routeRequest")
			if !errors.Is(err, context.Canceled) {
				logEntry.Error(err)
			} else {
				logEntry.Warn(err)
			}
			// if it's a context canceled on the client it won't get the 429 anyway, if it's within the cluster we should retry
			Generate429(resp)
		}
	}
}

func (m *QueueManager) HandleGlobal(w http.ResponseWriter, r *http.Request) {
	botHashStr := r.Header.Get("bot-hash")
	botLimitStr := r.Header.Get("bot-limit")

	botHash, err := strconv.ParseUint(botHashStr, 10, 64)
	if err != nil {
		w.WriteHeader(400)
		return
	}

	botLimit, err := strconv.ParseUint(botLimitStr, 10, 64)
	if err != nil {
		w.WriteHeader(400)
		return
	}

	m.clusterGlobalRateLimiter.Take(botHash, uint(botLimit))
	logger.Trace("Returned OK for global request")
}

func (m *QueueManager) CreateMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", m.DiscordRequestHandler)
	mux.HandleFunc("/nirn/global", m.HandleGlobal)
	mux.HandleFunc("/nirn/healthz", func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(200)
	})
	return mux
}
