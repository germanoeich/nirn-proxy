package discord

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"github.com/germanoeich/nirn-proxy/libnew/bucket"
	"github.com/germanoeich/nirn-proxy/libnew/config"
	"github.com/germanoeich/nirn-proxy/libnew/metrics"
	"io"
	"io/ioutil"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	client         *http.Client
	contextTimeout time.Duration
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

func configureDiscordHTTPClient(config ClientConfig) *http.Client {
	transport := createTransport(config.Ip, config.DisableHttp2)
	client := &http.Client{
		Transport: transport,
		Timeout:   90 * time.Second,
	}

	return client
}

func NewDiscordClient(config ClientConfig) Client {
	return Client{
		client: configureDiscordHTTPClient(config),
	}
}

func (c *Client) GetBotGlobalLimit(token string, user *BotUserResponse) (uint, error) {
	if token == "" {
		return math.MaxUint32, nil
	}

	if user != nil {
		limitOverride, ok := config.Get().BotRatelimitOverride[user.Id]
		if ok {
			return limitOverride, nil
		}
	}

	if strings.HasPrefix(token, "Bearer") {
		return 50, nil
	}

	bot, err := c.Do(context.Background(), "/api/v9/gateway/bot", "GET", nil, map[string][]string{"Authorization": {token}}, "")

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

func (c *Client) GetBotUser(token string) (*BotUserResponse, error) {
	if token == "" {
		return nil, errors.New("no token provided")
	}

	bot, err := c.Do(context.Background(), "/api/v9/users/@me", "GET", nil, map[string][]string{"Authorization": {token}}, "")

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

func (c *Client) Do(ctx context.Context, path string, method string, body io.ReadCloser, header http.Header, query string) (*http.Response, error) {
	discordReq, err := http.NewRequestWithContext(ctx, method, "https://discord.com"+path+"?"+query, body)
	discordReq.Header = header
	if err != nil {
		return nil, err
	}

	startTime := time.Now()
	discordResp, err := c.client.Do(discordReq)

	identifier := ctx.Value("identifier")
	if identifier == nil {
		// Queues always have an identifier, if there's none in the context, we called the method from outside a queue
		identifier = "Internal"
	}

	if err == nil {
		route := bucket.GetMetricsPath(path)
		status := discordResp.Status
		method := discordResp.Request.Method
		elapsed := time.Since(startTime).Seconds()

		if discordResp.StatusCode == 429 {
			if discordResp.Header.Get("x-ratelimit-scope") == "shared" {
				status = "429 Shared"
			}
		}
		metrics.RequestHistogram.With(map[string]string{"route": route, "status": status, "method": method, "clientId": identifier.(string)}).Observe(elapsed)
	}
	return discordResp, err
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

//
//func ProcessRequest(ctx context.Context, item *QueueItem) (*http.Response, error) {
//	req := item.Req
//	res := *item.Res
//
//	ctx, cancel := context.WithTimeout(ctx, contextTimeout)
//	defer cancel()
//	discordResp, err := doDiscordReq(ctx, req.URL.Path, req.Method, req.Body, req.Header.Clone(), req.URL.RawQuery)
//
//	if err != nil {
//		if ctx.Err() == context.DeadlineExceeded {
//			res.WriteHeader(408)
//		} else {
//			res.WriteHeader(500)
//		}
//		_, _ = res.Write([]byte(err.Error()))
//		return nil, err
//	}
//
//	logger.WithFields(logrus.Fields{
//		"method": req.Method,
//		"path":   req.URL.String(),
//		"status": discordResp.Status,
//		// TODO: Remove this when 429s are not a problem anymore
//		"discordBucket": discordResp.Header.Get("x-ratelimit-bucket"),
//	}).Debug("Discord request")
//
//	err = CopyResponseToResponseWriter(discordResp, item.Res)
//
//	if err != nil {
//		return nil, err
//	}
//
//	return discordResp, nil
//}
