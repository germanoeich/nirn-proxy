package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/germanoeich/nirn-proxy/lib"
	"github.com/sirupsen/logrus"
	"io"
	"io/ioutil"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type BotGatewayResponse struct {
	SessionStartLimit map[string]int `json:"session_start_limit"`
}

var logger = logrus.New()
var client *http.Client
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
			limit, err := getBotGlobalLimit(token)
			if err != nil {
				logger.Error(err)
				resp.WriteHeader(500)
				_, err := resp.Write([]byte("Unable to fetch gateway info - nirn-proxy"))
				if err != nil {
					logger.Error(err)
				}
				queueMu.Unlock()
				return
			}
			q = lib.NewRequestQueue(process, limit)
			clientId := getBotId(token)
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

func getBotGlobalLimit(token string) (uint, error) {
	if token == "" {
		return math.MaxUint32, nil
	}

	bot, err := doDiscordReq("/api/v9/gateway/bot", "GET", nil, map[string][]string{ "Authorization": {token} }, "")

	if err != nil {
		return 0, err
	}

	switch {
	case bot.StatusCode == 401:
		invalidTokens[token] = true
		return 0, errors.New("invalid token - nirn-proxy")
	case bot.StatusCode == 429:
		return 0, errors.New("429 on gateway/bot")
	case bot.StatusCode == 500:
		return 0, errors.New("500 on gateway/bot")
	}

	body, _ := ioutil.ReadAll(bot.Body)

	var s BotGatewayResponse

	err = json.Unmarshal(body, &s)
	if err != nil {
		return 0, err
	}

	concurrency := s.SessionStartLimit["max_concurrency"]

	if concurrency == 1 {
		return 50, nil
	} else {
		if 25 * concurrency > 500 {
			return uint(25 * concurrency), nil
		}
		return 500, nil
	}
}

func copyHeader(dst, src http.Header) {
	dst["Date"] = nil
	dst["Content-Type"] = nil
	for k, vv := range src {
		for _, v := range vv {
			if k != "Content-Length" {
				dst[strings.ToLower(k)] = []string{v}
			}
		}
	}
}

func doDiscordReq(path string, method string, body io.ReadCloser, header http.Header, query string) (*http.Response, error) {
	discordReq, err := http.NewRequest(method, "https://discord.com" + path + "?" + query, body)
	discordReq.Header = header
	if err != nil {
		return nil, err
	}

	token := discordReq.Header.Get("Authorization")
	clientId := getBotId(token)
	startTime := time.Now()
	discordResp, err := client.Do(discordReq)
	if err == nil {
		route := lib.GetMetricsPath(path)
		status := discordResp.Status
		method := discordResp.Request.Method
		elapsed := time.Since(startTime).Seconds()
		lib.RequestSummary.With(map[string]string{"route": route, "status": status, "method": method, "clientId": clientId}).Observe(elapsed)
	}
	return discordResp, err
}

func getBotId(token string) string {
	var clientId string
	if token == "" {
		clientId = "NoAuth"
	} else {
		token = strings.ReplaceAll(token, "Bot ", "")
		token = strings.ReplaceAll(token, "Bearer ", "")
		token = strings.Split(token, ".")[0]
		token, err := base64.StdEncoding.DecodeString(token)
		if err != nil {
			clientId = "Unknown"
		} else {
			clientId = string(token)
		}
	}
	return clientId
}

func process(item *lib.QueueItem) *http.Response {
	req := item.Req
	res := *item.Res

	discordResp, err := doDiscordReq(req.URL.Path, req.Method, req.Body, req.Header.Clone(), req.URL.RawQuery)

	if err != nil {
		logger.Error(err)
		res.WriteHeader(500)
		_, _ = res.Write([]byte(err.Error()))
		return nil
	}

	logger.WithFields(logrus.Fields{
		"method": req.Method,
		"path": req.URL.String(),
		"status": discordResp.Status,
		// TODO: Remove this when 429s are not a problem anymore
		"discordBucket": discordResp.Header.Get("x-ratelimit-bucket"),
	}).Debug("Discord request")

	body, err := ioutil.ReadAll(discordResp.Body)
	if err != nil {
		logger.Error(err)
		res.WriteHeader(500)
		_, _ = res.Write([]byte(err.Error()))
		return nil
	}

	copyHeader(res.Header(), discordResp.Header)
	res.WriteHeader(discordResp.StatusCode)

	_, err = res.Write(body)
	if err != nil {
		logger.Error(err)
		return nil
	}

	return discordResp
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
	logger.Info("Starting proxy")
	lib.SetLogger(logger)
	client = &http.Client{}
	s := &http.Server{
		Addr:           ":" + port,
		Handler:        &GenericHandler{},
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   1 * time.Hour,
		MaxHeaderBytes: 1 << 20,
	}
	go lib.StartMetrics()
	err = s.ListenAndServe()
	if err != nil {
		panic(err)
	}
}