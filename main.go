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
var queueMu = sync.Mutex{}

type GenericHandler struct{}
func (_ *GenericHandler) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	// No token will work and fall under "" on the map
	token := req.Header.Get("Authorization")
	_, isInvalid := invalidTokens[token]
	if isInvalid {
		resp.WriteHeader(401)
		_, err := resp.Write([]byte("Known bad token - nirn-proxy"))
		if err != nil {
			logger.Error(err)
		}
		return
	}
	q, ok := queues[token]
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
			q = lib.NewRequestQueue(lib.ProcessRequest, limit)
			clientId := lib.GetBotId(token)
			logger.WithFields(logrus.Fields{ "globalLimit": limit, "clientId": clientId }).Info("Created new queue")
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

func main()  {
	logLevel := os.Getenv("LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}
	lvl, err := logrus.ParseLevel(logLevel)

	if err != nil {
		panic("Failed to parse log level")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	logger.SetLevel(lvl)
	logger.Info("Starting proxy on :" + port)
	lib.SetLogger(logger)
	s := &http.Server{
		Addr:           ":" + port,
		Handler:        &GenericHandler{},
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   1 * time.Hour,
		MaxHeaderBytes: 1 << 20,
	}

	if os.Getenv("ENABLE_PPROF") == "true" {
		go lib.StartProfileServer()
	}

	if os.Getenv("ENABLE_METRICS") != "false" {
		port := os.Getenv("METRICS_PORT")
		if port == "" {
			port = "9000"
		}
		go lib.StartMetrics(port)
	}

	err = s.ListenAndServe()
	if err != nil {
		panic(err)
	}
}