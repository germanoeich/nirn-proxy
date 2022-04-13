package lib

import (
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
	"strings"
	"time"
)

var client *http.Client

var contextTimeout time.Duration

type BotGatewayResponse struct {
	SessionStartLimit map[string]int `json:"session_start_limit"`
}

type BotUserResponse struct {
	Id string `json:"id"`
	Username string `json:"username"`
	Discrim string `json:"discriminator"`
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
			MaxIdleConns:          100,
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
			LocalAddr:     addr,
			Timeout:       30 * time.Second,
			KeepAlive:     30 * time.Second,
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

func ConfigureDiscordHTTPClient(ip string, timeout time.Duration, disableHttp2 bool) {
	transport := createTransport(ip, disableHttp2)
	client = &http.Client{
		Transport: transport,
		Timeout: 90 * time.Second,
	}

	contextTimeout = timeout
}

func GetBotGlobalLimit(token string) (uint, error) {
	if token == "" {
		return math.MaxUint32, nil
	}

	if strings.HasPrefix(token, "Bearer") {
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

	body, _ := ioutil.ReadAll(bot.Body)

	var s BotUserResponse

	err = json.Unmarshal(body, &s)
	if err != nil {
		return nil, err
	}

	return &s, nil
}

func doDiscordReq(ctx context.Context, path string, method string, body io.ReadCloser, header http.Header, query string) (*http.Response, error) {
	discordReq, err := http.NewRequestWithContext(ctx, method, "https://discord.com" + path + "?" + query, body)
	discordReq.Header = header
	if err != nil {
		return nil, err
	}

	startTime := time.Now()
	discordResp, err := client.Do(discordReq)

	identifier := ctx.Value("identifier")
	if identifier == nil {
		// Queues always have an identifier, if there's none in the context, we called the method from outside a queue
		identifier = "Internal"
	}

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
