package main

import (
	"github.com/germanoeich/nirn-proxy/lib"
	_ "github.com/joho/godotenv/autoload"
	"github.com/sirupsen/logrus"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var logger = logrus.New()
// token : queue map
var queues = make(map[string]*lib.RequestQueue)
// Store invalid tokens to prevent a storm when a token gets reset
var invalidTokens = make(map[string]bool)
var queueMu = sync.RWMutex{}
var bufferSize = 50

type GenericHandler struct{}
func (_ *GenericHandler) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	lib.ConnectionsOpen.Inc()
	defer lib.ConnectionsOpen.Dec()

	token := req.Header.Get("Authorization")
	queueMu.RLock()
	// No token will work and fall under "" on the map
	_, isInvalid := invalidTokens[token]
	if isInvalid {
		resp.WriteHeader(401)
		_, err := resp.Write([]byte("Known bad token - nirn-proxy"))
		if err != nil {
			logger.Error(err)
		}
		queueMu.RUnlock()
		return
	}
	q, ok := queues[token]
	queueMu.RUnlock()
	if !ok {
		queueMu.Lock()
		// Check if it wasn't created while we didn't hold the lock
		q, ok = queues[token]
		if !ok {
			limit, err := lib.GetBotGlobalLimit(token)
			if err != nil {
				if strings.HasPrefix(err.Error(), "invalid token") {
					invalidTokens[token] = true
				}
				logger.Error(err)
				resp.WriteHeader(500)
				_, err := resp.Write([]byte("Unable to fetch gateway info - nirn-proxy"))
				if err != nil {
					logger.Error(err)
				}
				queueMu.Unlock()
				return
			}

			user, _ := lib.GetBotUser(token)

			q = lib.NewRequestQueue(lib.ProcessRequest, limit, bufferSize, user)
			clientId := lib.GetBotId(token)
			logger.WithFields(logrus.Fields{ "globalLimit": limit, "clientId": clientId, "bufferSize": bufferSize }).Info("Created new queue")
			queues[token] = q
		}
		queueMu.Unlock()
	}

	_, _, err := q.Queue(req, &resp)
	if err != nil {
		logger.Error(err)
		lib.ErrorCounter.Inc()
		return
	}
}

func setupLogger() {
	logLevel := lib.EnvGet("LOG_LEVEL", "info")
	lvl, err := logrus.ParseLevel(logLevel)

	if err != nil {
		panic("Failed to parse log level")
	}

	logger.SetLevel(lvl)
	lib.SetLogger(logger)
}

func main()  {
	outboundIp := os.Getenv("OUTBOUND_IP")

	timeout := lib.EnvGetInt("REQUEST_TIMEOUT", 5000)

	lib.ConfigureDiscordHTTPClient(outboundIp, time.Duration(timeout) * time.Millisecond)

	port := lib.EnvGet("PORT", "8080")
	bindIp := lib.EnvGet("BIND_IP", "0.0.0.0")

	setupLogger()
	logger.Info("Starting proxy on " + bindIp + ":" + port)

	s := &http.Server{
		Addr:           bindIp + ":" + port,
		Handler:        &GenericHandler{},
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   1 * time.Hour,
		MaxHeaderBytes: 1 << 20,
	}

	if os.Getenv("ENABLE_PPROF") == "true" {
		go lib.StartProfileServer()
	}

	if os.Getenv("ENABLE_METRICS") != "false" {
		port := lib.EnvGet("METRICS_PORT", "9000")
		go lib.StartMetrics(bindIp + ":" + port)
	}

	bufferSize = lib.EnvGetInt("BUFFER_SIZE", 50)

	err := s.ListenAndServe()
	if err != nil {
		panic(err)
	}
}