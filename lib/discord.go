package lib

import (
	"context"
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

var client = &http.Client{}

type BotGatewayResponse struct {
	SessionStartLimit map[string]int `json:"session_start_limit"`
}

func ConfigureDiscordHTTPClient(ip string) {
	addr, err := net.ResolveTCPAddr("tcp", ip + ":0")

	if err != nil {
		panic(err)
	}

	dialer := &net.Dialer{LocalAddr: addr}

	dialContext := func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dialer.Dial(network, addr)
		return conn, err
	}

	transport := &http.Transport{DialContext: dialContext}
	client = &http.Client{
		Transport: transport,
	}
}

func GetBotGlobalLimit(token string) (uint, error) {
	if token == "" {
		return math.MaxUint32, nil
	}

	bot, err := doDiscordReq("/api/v9/gateway/bot", "GET", nil, map[string][]string{ "Authorization": {token} }, "")

	if err != nil {
		return 0, err
	}

	switch {
	case bot.StatusCode == 401:
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
	clientId := GetBotId(token)
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
		RequestSummary.With(map[string]string{"route": route, "status": status, "method": method, "clientId": clientId}).Observe(elapsed)
	}
	return discordResp, err
}

func ProcessRequest(item *QueueItem) *http.Response {
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
