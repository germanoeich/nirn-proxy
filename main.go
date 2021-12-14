package main

import (
	"github.com/germanoeich/nirn-proxy/lib"
	_ "github.com/joho/godotenv/autoload"
	"github.com/panjf2000/ants/v2"
	"github.com/sirupsen/logrus"
	"github.com/valyala/fasthttp"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var logger = logrus.New()
// token : queue map
var queues = make(map[string]lib.RequestQueue)
// Store invalid tokens to prevent a storm when a token gets reset
var invalidTokens = make(map[string]bool)
var queueMu = sync.Mutex{}
var bufferSize int64 = 50

func HTTPHandler(ctx *fasthttp.RequestCtx) {
	// No token will work and fall under "" on the map
	token := lib.B2S(ctx.Request.Header.Peek("Authorization"))
	_, isInvalid := invalidTokens[token]
	if isInvalid {
		ctx.Response.SetStatusCode(401)
		_, err := ctx.Write([]byte("Known bad token - nirn-proxy"))
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
				ctx.SetStatusCode(500)
				_, err := ctx.Write([]byte("Unable to fetch gateway info - nirn-proxy"))
				if err != nil {
					logger.Error(err)
				}
				queueMu.Unlock()
				return
			}
			q = lib.NewRequestQueue(lib.ProcessRequest, limit, bufferSize)
			clientId := lib.GetBotId(ctx.Request.Header.Peek("Authorization"))
			logger.WithFields(logrus.Fields{ "globalLimit": limit, "clientId": clientId, "bufferSize": bufferSize }).Info("Created new queue")
			queues[token] = q
		}
		queueMu.Unlock()
	}

	err := q.Queue(ctx)
	if err != nil {
		logger.Error(err)
		lib.ErrorCounter.Inc()
		return
	}
}

func main()  {
	defer ants.Release()
	outboundIp := os.Getenv("OUTBOUND_IP")
	timeoutEnv := os.Getenv("REQUEST_TIMEOUT")
	var timeout int64 = 5000
	if timeoutEnv != "" {
		timeoutParsed, err := strconv.ParseInt(timeoutEnv, 10, 64)
		if err != nil {
			panic("Failed to parse REQUEST_TIMEOUT")
		}
		timeout = timeoutParsed
	}

	lib.ConfigureDiscordHTTPClient(outboundIp, time.Duration(timeout) * time.Millisecond)

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

	bindIp := os.Getenv("BIND_IP")
	if bindIp == "" {
		bindIp = "0.0.0.0"
	}

	logger.SetLevel(lvl)
	logger.Info("Starting proxy on " + bindIp + ":" + port)
	lib.SetLogger(logger)

	if os.Getenv("ENABLE_PPROF") == "true" {
		go lib.StartProfileServer()
	}

	if os.Getenv("ENABLE_METRICS") != "false" {
		port := os.Getenv("METRICS_PORT")
		if port == "" {
			port = "9000"
		}
		go lib.StartMetrics(bindIp + ":" + port)
	}

	bufferEnv := os.Getenv("BUFFER_SIZE")
	if bufferEnv != "" {
		parsedSize, err := strconv.ParseInt(bufferEnv, 10, 64)
		if err != nil {
			logger.Error(err)
			logger.Warn("Failed to parse buffer size, using default")
		} else {
			bufferSize = parsedSize
		}
	}

	err = fasthttp.ListenAndServe(bindIp + ":" + port, HTTPHandler)
	if err != nil {
		panic(err)
	}
}