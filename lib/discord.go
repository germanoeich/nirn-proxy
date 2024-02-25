package lib

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"github.com/sirupsen/logrus"
	"io"
	"io/ioutil"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

var client *http.Client

var contextTimeout time.Duration

var globalOverrideMap = make(map[string]uint)

var endpointCache = make(map[string]*Cache)

var disableRestLimitDetection = false

var cacheEndpoints = map[string]time.Duration{
	"/api/users/@me":       10 * time.Minute,
	"/api/v9/users/@me":    10 * time.Minute,
	"/api/v10/users/@me":   10 * time.Minute,
	"/api/gateway":         60 * time.Minute,
	"/api/v9/gateway":      60 * time.Minute,
	"/api/v10/gateway":     60 * time.Minute,
	"/api/gateway/bot":     30 * time.Minute,
	"/api/v9/gateway/bot":  30 * time.Minute,
	"/api/v10/gateway/bot": 30 * time.Minute,
	"/api/v9/applications/@me": 5 * time.Minute,
	"/api/v10/applications/@me": 5 * time.Minute,
}

var wsProxy string

func init() {
	if len(os.Args) > 1 {
		for _, arg := range os.Args[1:] {
			argSplit := strings.SplitN(arg, "=", 2)

			if len(argSplit) < 2 {
				continue
			}

			switch argSplit[0] {
			case "ws-proxy":
				wsProxy = argSplit[1]
				break
			case "port":
				os.Setenv("PORT", argSplit[1])
				break
			}
		}
	}

	if os.Getenv("WS_PROXY") != "" {
		wsProxy = os.Getenv("WS_PROXY")
	}
}

type BotGatewayResponse struct {
	SessionStartLimit map[string]int `json:"session_start_limit"`
}

type BotUserResponse struct {
	Id       string `json:"id"`
	Username string `json:"username"`
	Discrim  string `json:"discriminator"`
}

func createTransport(ip string, disableHttp2 bool) http.RoundTripper {
	var transport http.Transport
	if ip == "" {
		// http.DefaultTransport options
		transport = http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          1000,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}
	} else {
		addr, err := net.ResolveTCPAddr("tcp", ip+":0")

		if err != nil {
			panic(err)
		}

		dialer := &net.Dialer{
			LocalAddr: addr,
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}

		dialContext := func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := dialer.Dial(network, addr)
			return conn, err
		}

		transport = http.Transport{
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          1000,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 2 * time.Second,
			DialContext:           dialContext,
			ResponseHeaderTimeout: 0,
		}
	}

	if disableHttp2 {
		transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
		transport.ForceAttemptHTTP2 = false
	}

	return &transport
}

func parseGlobalOverrides(overrides string) {
	// Format: "<bot_id>:<bot_global_limit>,<bot_id>:<bot_global_limit>

	if overrides == "" {
		return
	}

	overrideList := strings.Split(overrides, ",")
	for _, override := range overrideList {
		opts := strings.Split(override, ":")
		if len(opts) != 2 {
			panic("Invalid bot global ratelimit overrides")
		}

		limit, err := strconv.ParseInt(opts[1], 10, 32)

		if err != nil {
			panic("Failed to parse global ratelimit overrides")
		}

		globalOverrideMap[opts[0]] = uint(limit)
	}
}

func ConfigureDiscordHTTPClient(ip string, timeout time.Duration, disableHttp2 bool, globalOverrides string, disableRestDetection bool) {
	transport := createTransport(ip, disableHttp2)
	client = &http.Client{
		Transport: transport,
		Timeout:   90 * time.Second,
	}

	contextTimeout = timeout

	disableRestLimitDetection = disableRestDetection

	parseGlobalOverrides(globalOverrides)
}

func GetBotGlobalLimit(token string, user *BotUserResponse) (uint, error) {
	if token == "" {
		return math.MaxUint32, nil
	}

	if user != nil {
		limitOverride, ok := globalOverrideMap[user.Id]
		if ok {
			return limitOverride, nil
		}
	}

	if strings.HasPrefix(token, "Bearer") {
		return 50, nil
	}

	if disableRestLimitDetection {
		return 50, nil
	}

	bot, err := doDiscordReq(context.Background(), "/api/v9/gateway/bot", "GET", nil, map[string][]string{"Authorization": {token}}, "")

	if err != nil {
		return 0, err
	}

	switch {
	case bot.StatusCode == 401:
		// In case a 401 is encountered, we return math.MaxUint32 to allow requests through to fail fast
		return math.MaxUint32, errors.New("invalid token - nirn-proxy")
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
		if 25*concurrency > 500 {
			return uint(25 * concurrency), nil
		}
		return 500, nil
	}
}

func GetBotUser(token string) (*BotUserResponse, error) {
	if token == "" {
		return nil, errors.New("no token provided")
	}

	bot, err := doDiscordReq(context.Background(), "/api/v9/users/@me", "GET", nil, map[string][]string{"Authorization": {token}}, "")

	if err != nil {
		return nil, err
	}

	switch {
	case bot.StatusCode == 429:
		return nil, errors.New("429 on users/@me")
	case bot.StatusCode == 500:
		return nil, errors.New("500 on users/@me")
	}

	body, _ := io.ReadAll(bot.Body)

	var s BotUserResponse

	err = json.Unmarshal(body, &s)
	if err != nil {
		return nil, err
	}

	return &s, nil
}

func doDiscordReq(ctx context.Context, path string, method string, body io.ReadCloser, header http.Header, query string) (*http.Response, error) {
	identifier := ctx.Value("identifier")
	if identifier == nil {
		// Queues always have an identifier, if there's none in the context, we called the method from outside a queue
		identifier = "Internal"
	}

	logger.Info(method, path+"?"+query)

	identifierStr, ok := identifier.(string)

	if ok {
		// Check endpoint cache
		if endpointCache[identifierStr] != nil {
			cacheEntry := endpointCache[identifierStr].Get(path)

			if cacheEntry != nil {
				// Send cached response
				logger.WithFields(logrus.Fields{
					"method": method,
					"path":   path,
					"status": "200 (cached)",
				}).Debug("Discord request")

				headers := cacheEntry.Headers.Clone()
				headers.Set("X-Cached", "true")
				// Set cache headers so bot won't be perpetually stuck
				headers.Set("X-RateLimit-Limit", "5")
				headers.Set("X-RateLimit-Remaining", "5")
				headers.Set("X-RateLimit-Bucket", "cache")

				return &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(bytes.NewBuffer(cacheEntry.Data)),
					Header:     headers,
				}, nil
			}
		} else {
			endpointCache[identifierStr] = NewCache()
		}
	}

	discordReq, err := http.NewRequestWithContext(ctx, method, "https://discord.com"+path+"?"+query, body)
	if err != nil {
		return nil, err
	}

	discordReq.Header = header
	startTime := time.Now()
	discordResp, err := client.Do(discordReq)

	if err == nil {
		route := GetMetricsPath(path)
		status := discordResp.Status
		method := discordResp.Request.Method
		elapsed := time.Since(startTime).Seconds()

		if discordResp.StatusCode == 429 {
			if discordResp.Header.Get("x-ratelimit-scope") == "shared" {
				status = "429 Shared"
			}
		}

		RequestHistogram.With(map[string]string{"route": route, "status": status, "method": method, "clientId": identifier.(string)}).Observe(elapsed)
	}

	if wsProxy != "" {
		if path == "/api/gateway" || path == "/api/v9/gateway" || path == "/api/gateway/bot" || path == "/api/v10/gateway/bot" {
			var data map[string]any

			err := json.NewDecoder(discordResp.Body).Decode(&data)

			if err != nil {
				return nil, err
			}

			data["url"] = wsProxy

			bytes, err := json.Marshal(data)

			if err != nil {
				return nil, err
			}

			discordResp.Body = io.NopCloser(strings.NewReader(string(bytes)))
		}
	}

	if expiry, ok := cacheEndpoints[path]; ok {
		if discordResp.StatusCode == 200 {
			body, _ := io.ReadAll(discordResp.Body)
			endpointCache[identifierStr].Set(path, &CacheEntry{
				Data:      body,
				CreatedAt: time.Now(),
				ExpiresIn: &expiry,
				Headers:   discordResp.Header,
			})

			// Put body back into response
			discordResp.Body = io.NopCloser(bytes.NewBuffer(body))
		}
	}

	return discordResp, err
}

func ProcessRequest(ctx context.Context, item *QueueItem) (*http.Response, error) {
	req := item.Req
	res := *item.Res

	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	discordResp, err := doDiscordReq(ctx, req.URL.Path, req.Method, req.Body, req.Header.Clone(), req.URL.RawQuery)

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			res.WriteHeader(408)
		} else {
			res.WriteHeader(500)
		}
		_, _ = res.Write([]byte(err.Error()))
		return nil, err
	}

	logger.WithFields(logrus.Fields{
		"method": req.Method,
		"path":   req.URL.String(),
		"status": discordResp.Status,
		// TODO: Remove this when 429s are not a problem anymore
		"discordBucket": discordResp.Header.Get("x-ratelimit-bucket"),
	}).Debug("Discord request")

	err = CopyResponseToResponseWriter(discordResp, item.Res)

	if err != nil {
		return nil, err
	}

	return discordResp, nil
}
