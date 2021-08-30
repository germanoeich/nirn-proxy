package main

import (
	"fmt"
	"github.com/germanoeich/nirn-proxy/lib"
	"github.com/rs/zerolog"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var logger zerolog.Logger

type GenericHandler struct{}

var client *http.Client
// token : queue map
var queues = make(map[string]*lib.RequestQueue)
var queueMu = sync.Mutex{}

func copyHeader(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			fmt.Println(k, v)
			//dst.Set(k, v)
			dst[strings.ToLower(k)] = []string{v}
		}
	}
}

func process(item *lib.QueueItem) *http.Header {
	req := item.Req
	res := *item.Res

	discordReq := &http.Request{
		Method:           req.Method,
		URL:              &url.URL{
			Scheme:      "https",
			Host:        "discord.com",
			Path:        req.URL.Path,
		},
		Body: req.Body,
		Header: req.Header.Clone(),
	}

	discordResp, err := client.Do(discordReq)
	if err != nil {
		logger.Error().Err(err).Send()
		res.WriteHeader(500)
		_, _ = res.Write([]byte(err.Error()))
		return nil
	}

	logger.Info().Msg(strconv.FormatInt(discordResp.ContentLength, 10))

	body, err := ioutil.ReadAll(discordResp.Body)
	if err != nil {
		logger.Error().Err(err).Send()
		res.WriteHeader(500)
		_, _ = res.Write([]byte(err.Error()))
		return nil
	}
	logger.Info().Msg(string(body))

	copyHeader(res.Header(), discordResp.Header)

	_, err = res.Write(body)
	if err != nil {
		logger.Error().Err(err).Send()
		res.WriteHeader(500)
		_, _ = res.Write([]byte(err.Error()))
		return nil
	}
	return &discordResp.Header
}

func (_ *GenericHandler) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	logger.Info().Str("method", req.Method).Str("path", req.URL.String()).Msg("Route hit")

	token := req.Header.Get("Authorization")
	q, ok := queues[token]
	if !ok {
		queueMu.Lock()
		// Check if it wasn't created while we didn't hold the lock
		q, ok = queues[token]
		if !ok {
			q = lib.NewRequestQueue(process)
			queues[token] = q
		}
		queueMu.Unlock()
	}
	
	q.Queue(req, &resp)

	return
}

func main()  {
	logger = zerolog.New(os.Stdout).With().Timestamp().Logger()
	client = &http.Client{}
	s := &http.Server{
		Addr:           ":8080",
		Handler:        &GenericHandler{},
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   1 * time.Hour,
		MaxHeaderBytes: 1 << 20,
	}
	err := s.ListenAndServe()
	if err != nil {
		panic(err)
	}
}