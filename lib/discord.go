package lib

import (
	"encoding/json"
	"errors"
	"github.com/sirupsen/logrus"
	"github.com/valyala/fasthttp"
	"math"
	"net"
	"net/http"
	"strings"
	"time"
)

var defaultTimeouts = 60 * time.Second
var client *fasthttp.Client

var contextTimeout time.Duration

type BotGatewayResponse struct {
	SessionStartLimit map[string]int `json:"session_start_limit"`
}

func ConfigureDiscordHTTPClient(ip string, timeout time.Duration) {
	addr, err := net.ResolveTCPAddr("tcp", ip)
	dialer := fasthttp.TCPDialer{
		LocalAddr: addr,
	}

	if err != nil {
		panic(err)
	}

	client = &fasthttp.Client{
		NoDefaultUserAgentHeader:      true,
		Dial:                          dialer.Dial,
		MaxIdleConnDuration:           defaultTimeouts,
		MaxConnDuration:               defaultTimeouts,
		DisableHeaderNamesNormalizing: true,
		DisablePathNormalizing:        true,
	}

	contextTimeout = timeout
}

func GetBotGlobalLimit(token string) (uint, error) {
	if token == "" {
		return math.MaxUint32, nil
	}

	headers := fasthttp.RequestHeader{}
	headers.Set("Authorization", token)
	headers.SetMethod("GET")
	bot, err := doDiscordReq(
		[]byte("/api/v9/gateway/bot"),
		nil,
		headers,
		[]byte(""),
	)

	if err != nil {
		return 0, err
	}

	switch {
	case bot.StatusCode() == 401:
		return 0, errors.New("invalid token - nirn-proxy")
	case bot.StatusCode() == 429:
		return 0, errors.New("429 on gateway/bot")
	case bot.StatusCode() == 500:
		return 0, errors.New("500 on gateway/bot")
	}

	body := bot.Body()

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

func doDiscordReq(path []byte, body []byte, header fasthttp.RequestHeader, query []byte) (*fasthttp.Response, error) {
	uri := strings.Builder{}
	uri.WriteString("https://discord.com")
	uri.Write(path)
	uri.WriteByte('?')
	uri.Write(query)

	discordReq := fasthttp.AcquireRequest()
	discordResp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(discordReq)

	header.CopyTo(&discordReq.Header)
	discordReq.SetRequestURI(uri.String())
	discordReq.SetBodyRaw(body)

	token := discordReq.Header.Peek("Authorization")
	clientId := GetBotId(token)
	startTime := time.Now()
	err := client.DoTimeout(discordReq, discordResp, contextTimeout)

	if err == nil {
		route := GetMetricsPath(B2S(path))
		status := discordResp.StatusCode()
		method := discordReq.Header.Method()
		elapsed := time.Since(startTime).Seconds()
		promStatus := http.StatusText(status)
		if status == 429 {
			if B2S(discordResp.Header.Peek("x-ratelimit-scope")) == "shared" {
				promStatus = "429 Shared"
			}
		}
		RequestSummary.With(map[string]string{"route": route, "status": promStatus, "method": B2S(method), "clientId": clientId}).Observe(elapsed)
	}

	return discordResp, err
}

func ProcessRequest(item *QueueItem) (*fasthttp.Response, error) {
	discordResp, err := doDiscordReq(item.Ctx.Path(), item.Ctx.PostBody(), item.Ctx.Request.Header, item.Ctx.QueryArgs().QueryString())

	if err != nil {
		if err == fasthttp.ErrTimeout {
			item.Ctx.SetStatusCode(408)
		} else {
			item.Ctx.SetStatusCode(500)
		}
		item.Ctx.SetBody(S2B(err.Error()))
		return discordResp, err
	}

	logger.WithFields(logrus.Fields{
		"method": B2S(item.Ctx.Method()),
		"path": B2S(item.Ctx.Path()),
		"status": discordResp.StatusCode(),
		"discordBucket": B2S(discordResp.Header.Peek("x-ratelimit-bucket")),
	}).Debug("Discord request")

	body := discordResp.Body()

	discordResp.Header.CopyTo(&item.Ctx.Response.Header)
	item.Ctx.SetStatusCode(discordResp.StatusCode())

	item.Ctx.SetBody(body)

	return discordResp, nil
}
