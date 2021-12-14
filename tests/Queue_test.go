package tests

import (
	"bytes"
	"github.com/germanoeich/nirn-proxy/lib"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var logger, hook = test.NewNullLogger()
func Init() int {
	rand.Seed(time.Now().Unix())
	lib.SetLogger(logger)
	return 0
}
var _ = Init()

var server_200_noheaders = httptest.NewServer(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
	res.WriteHeader(200)
	res.Write([]byte("body"))
}))

// global, reset: 500ms
var server_429_global_500 = httptest.NewServer(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
	res.Header()["x-ratelimit-global"] = []string{"true"}
	res.Header()["x-ratelimit-reset-after"] = []string{"0.5"}
	res.WriteHeader(429)
	res.Write([]byte("body"))
}))

// remain: 0, limit: 1, reset: 500ms
var server_429_0_1_500 = httptest.NewServer(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
	res.Header()["x-ratelimit-reset-after"] = []string{"0.5"}
	res.Header()["x-ratelimit-remaining"] = []string{"0"}
	res.Header()["x-ratelimit-limit"] = []string{"1"}
	res.WriteHeader(429)
	res.Write([]byte("body"))
}))

// remain: 0, limit: 1, reset: 1ms
var server_429_0_1_1 = httptest.NewServer(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
	res.Header()["x-ratelimit-reset-after"] = []string{"0.001"}
	res.Header()["x-ratelimit-remaining"] = []string{"0"}
	res.Header()["x-ratelimit-limit"] = []string{"1"}
	res.WriteHeader(429)
	res.Write([]byte("body"))
}))

func createRequest(method string, uri string) *fasthttp.Request {
	req := fasthttp.AcquireRequest()
	req.SetRequestURI("https://discord.com" + uri)
	req.Header.SetMethod(method)
	return req
}

var fastclient = fasthttp.Client{
	NoDefaultUserAgentHeader:      true,
	DisableHeaderNamesNormalizing: true,
	DisablePathNormalizing:        true,
}

func TestQueueWorks(t *testing.T) {
	var count int64 = 0

	genericProcessor := func(item *lib.QueueItem) (*fasthttp.Response, error) {
		req := fasthttp.AcquireRequest()
		defer fasthttp.ReleaseRequest(req)
		req.SetRequestURI(server_200_noheaders.URL)
		defer fasthttp.ReleaseRequest(req)
		res := fasthttp.AcquireResponse()
		fastclient.Do(req, res)
		go atomic.AddInt64(&count, 1)
		return res, nil
	}

	q := lib.NewRequestQueue(genericProcessor, 50, 50)
	for i := 0; i < 50; i++ {
		req := createRequest("GET", "/api/v9/guilds/915995872213471273/audit-logs")
		res := fasthttp.AcquireResponse()
		ctx := &fasthttp.RequestCtx{
			Request:  *req,
			Response: *res,
		}
		go func() {
			q.Queue(ctx)
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(res)
		}()
	}

	<- time.After(100 * time.Millisecond)
	assert.Equal(t, int64(50), count)
}

func TestQueueFiresSequentially(t *testing.T) {
	var count int64 = 0
	mu := sync.RWMutex{}
	mu.Lock()

	genericProcessor := func(item *lib.QueueItem) (*fasthttp.Response, error) {
		mu.RLock()
		req := fasthttp.AcquireRequest()
		defer fasthttp.ReleaseRequest(req)
		req.SetRequestURI(server_200_noheaders.URL)
		res := fasthttp.AcquireResponse()
		fastclient.Do(req, res)
		if bytes.Contains(item.Ctx.Path(), []byte("2")) {
			<- time.After(250 * time.Millisecond)
		}
		go atomic.AddInt64(&count, 1)
		return res, nil
	}

	q := lib.NewRequestQueue(genericProcessor, 100, 100)
	for i := 0; i < 100; i++ {
		// Force a "sequence" inside the internal channel
		time.Sleep(3 * time.Millisecond)
		uri := "/api/v9/guilds/111111111111111111/messages/111111111111111111"
		if i == 30 {
			uri = "/api/v9/guilds/111111111111111111/messages/111111111111111112"
		}
		req := createRequest("GET", uri)
		res := fasthttp.AcquireResponse()
		ctx := &fasthttp.RequestCtx{
			Request:  *req,
			Response: *res,
		}
		go func() {
			q.Queue(ctx)
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(res)
		}()
	}

	mu.Unlock()
	<- time.After(100 * time.Millisecond)
	assert.Equal(t, int64(30), count)
	<- time.After(500 * time.Millisecond)
	assert.Equal(t, int64(100), count)
}

func TestQueueLocksOnDiscordGlobal(t *testing.T) {
	var count int64 = 0
	mu := sync.RWMutex{}
	mu.Lock()

	genericProcessor := func(item *lib.QueueItem) (*fasthttp.Response, error) {
		mu.RLock()
		req := fasthttp.AcquireRequest()
		defer fasthttp.ReleaseRequest(req)
		req.SetRequestURI(server_429_global_500.URL)
		res := fasthttp.AcquireResponse()
		fastclient.Do(req, res)
		go atomic.AddInt64(&count, 1)
		return res, nil
	}

	q := lib.NewRequestQueue(genericProcessor, 50, 100)
	for i := 0; i < 2; i++ {
		uri := "/api/v9/guilds/111111111111111111/messages/111111111111111111"
		req := createRequest("GET", uri)
		res := fasthttp.AcquireResponse()
		ctx := &fasthttp.RequestCtx{
			Request:  *req,
			Response: *res,
		}
		go func() {
			q.Queue(ctx)
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(res)
		}()
	}

	mu.Unlock()
	<- time.After(100 * time.Millisecond)
	assert.Equal(t, int64(1), count)
	<- time.After(550 * time.Millisecond)
	assert.Equal(t, int64(2), count)
}

func TestQueueGlobalRatelimitWorks(t *testing.T) {
	var count int64 = 0
	mu := sync.RWMutex{}
	mu.Lock()

	genericProcessor := func(item *lib.QueueItem) (*fasthttp.Response, error) {
		mu.RLock()
		req := fasthttp.AcquireRequest()
		defer fasthttp.ReleaseRequest(req)
		req.SetRequestURI(server_200_noheaders.URL)
		res := fasthttp.AcquireResponse()
		fastclient.Do(req, res)
		go atomic.AddInt64(&count, 1)
		return res, nil
	}

	q := lib.NewRequestQueue(genericProcessor, 50, 100)
	for i := 0; i < 70; i++ {
		uri := "/api/v9/guilds/111111111111111111/messages/111111111111111111"
		req := createRequest("GET", uri)
		res := fasthttp.AcquireResponse()
		ctx := &fasthttp.RequestCtx{
			Request:  *req,
			Response: *res,
		}
		go func() {
			q.Queue(ctx)
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(res)
		}()
	}

	mu.Unlock()
	<- time.After(100 * time.Millisecond)
	assert.Equal(t, int64(50), count)
	<- time.After(1100 * time.Millisecond)
	assert.Equal(t, int64(70), count)
}

func TestQueueWorksOnMultipleChannels(t *testing.T) {
	// This test relies on the fact that a bucket will lock when it encounters a 429
	var count int64 = 0
	mu := sync.RWMutex{}
	mu.Lock()
	genericProcessor := func(item *lib.QueueItem) (*fasthttp.Response, error) {
		mu.RLock()
		req := fasthttp.AcquireRequest()
		defer fasthttp.ReleaseRequest(req)
		req.SetRequestURI(server_429_0_1_500.URL)
		res := fasthttp.AcquireResponse()
		fastclient.Do(req, res)
		go atomic.AddInt64(&count, 1)
		return res, nil
	}

	q := lib.NewRequestQueue(genericProcessor, 9999, 100)

	for i := 0; i < 99; i++ {
		indexstr := strconv.Itoa(i)
		//Generate a unique bucket per route
		uri := "/api/v9/guilds/1111111111111111" + indexstr + "/messages/111111111111111111"
		req := createRequest("GET", uri)
		res := fasthttp.AcquireResponse()
		ctx := &fasthttp.RequestCtx{
			Request:  *req,
			Response: *res,
		}
		go func() {
			q.Queue(ctx)
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(res)
		}()
	}

	mu.Unlock()
	<- time.After(1000 * time.Millisecond)
	assert.Equal(t, int64(99), count)
}

func TestQueueBucketLocksUnlocksOn429(t *testing.T) {
	var count int64 = 0
	mu := sync.RWMutex{}
	mu.Lock()
	genericProcessor := func(item *lib.QueueItem) (*fasthttp.Response, error) {
		mu.RLock()
		req := fasthttp.AcquireRequest()
		defer fasthttp.ReleaseRequest(req)
		req.SetRequestURI(server_429_0_1_500.URL)
		res := fasthttp.AcquireResponse()
		fastclient.Do(req, res)
		go atomic.AddInt64(&count, 1)
		return res, nil
	}


	q := lib.NewRequestQueue(genericProcessor, 9999, 100)
	for i := 0; i < 3; i++ {
		uri := "/api/v9/guilds/111111111111111111/messages/111111111111111111"
		req := createRequest("GET", uri)
		res := fasthttp.AcquireResponse()
		ctx := &fasthttp.RequestCtx{
			Request:  *req,
			Response: *res,
		}
		go func() {
			q.Queue(ctx)
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(res)
		}()
	}

	mu.Unlock()
	<- time.After(100 * time.Millisecond)
	assert.Equal(t, int64(1), count)
	<- time.After(500 * time.Millisecond)
	assert.Equal(t, int64(2), count)
	<- time.After(500 * time.Millisecond)
	assert.Equal(t, int64(3), count)
}
// This test is non-deterministic and random in nature
func TestQueueRandomPermutationsFireSimultaneously(t *testing.T) {
	var count int64 = 0
	mu := sync.RWMutex{}
	mu.Lock()
	genericProcessor := func(item *lib.QueueItem) (*fasthttp.Response, error) {
		mu.RLock()
		req := fasthttp.AcquireRequest()
		defer fasthttp.ReleaseRequest(req)
		req.SetRequestURI(server_200_noheaders.URL)
		res := fasthttp.AcquireResponse()
		fastclient.Do(req, res)
		go atomic.AddInt64(&count, 1)
		return res, nil
	}

	q := lib.NewRequestQueue(genericProcessor, 1000, 100)
	for i := 0; i < 3000; i++ {
		uri := GetRandomRoute()
		req := createRequest("GET", uri)
		res := fasthttp.AcquireResponse()
		ctx := &fasthttp.RequestCtx{
			Request:  *req,
			Response: *res,
		}
		go func() {
			q.Queue(ctx)
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(res)
		}()
	}

	mu.Unlock()
	<- time.After(5000 * time.Millisecond)
	assert.Equal(t, int64(3000), count)
}

// This test is non-deterministic and random in nature
func TestQueueRandomPermutationsFireSequentially(t *testing.T) {
	var count int64 = 0
	genericProcessor := func(item *lib.QueueItem) (*fasthttp.Response, error) {
		req := fasthttp.AcquireRequest()
		defer fasthttp.ReleaseRequest(req)
		req.SetRequestURI(server_200_noheaders.URL)
		res := fasthttp.AcquireResponse()
		fastclient.Do(req, res)
		go atomic.AddInt64(&count, 1)
		return res, nil
	}

	q := lib.NewRequestQueue(genericProcessor, 1000, 100)
	for i := 0; i < 3000; i++ {
		uri := GetRandomRoute()
		req := createRequest("GET", uri)
		res := fasthttp.AcquireResponse()
		ctx := &fasthttp.RequestCtx{
			Request:  *req,
			Response: *res,
		}

		q.Queue(ctx)
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(res)
	}

	<- time.After(100 * time.Millisecond)
	assert.Equal(t, int64(3000), count)
}

// This test is non-deterministic and random in nature
func TestQueueRandomPermutationsFireRandomDelay(t *testing.T) {
	var count int64 = 0
	genericProcessor := func(item *lib.QueueItem) (*fasthttp.Response, error) {
		req := fasthttp.AcquireRequest()
		defer fasthttp.ReleaseRequest(req)
		req.SetRequestURI(server_200_noheaders.URL)
		res := fasthttp.AcquireResponse()
		fastclient.Do(req, res)
		go atomic.AddInt64(&count, 1)
		return res, nil
	}

	q := lib.NewRequestQueue(genericProcessor, 1000, 100)
	for i := 0; i < 3000; i++ {
		go func() {
			// between 0 and 2ms
			time.Sleep(time.Duration(rand.Intn(2000)) * time.Microsecond)
			uri := GetRandomRoute()
			req := createRequest("GET", uri)
			res := fasthttp.AcquireResponse()
			ctx := &fasthttp.RequestCtx{
				Request:  *req,
				Response: *res,
			}

			q.Queue(ctx)
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(res)
		}()

	}

	<- time.After(2 * 3000 * time.Millisecond)
	assert.Equal(t, int64(3000), count)
}

// This test is non-deterministic and random in nature
func TestQueueFixedPermutationsFireRandomDelay(t *testing.T) {
	var routes []string
	for i := 0; i < 15; i++ {
		routes = append(routes, GetRandomRoute())
	}
	var count int64 = 0
	genericProcessor := func(item *lib.QueueItem) (*fasthttp.Response, error) {
		req := fasthttp.AcquireRequest()
		defer fasthttp.ReleaseRequest(req)
		req.SetRequestURI(server_200_noheaders.URL)
		res := fasthttp.AcquireResponse()
		fastclient.Do(req, res)
		go atomic.AddInt64(&count, 1)
		return res, nil
	}

	q := lib.NewRequestQueue(genericProcessor, 1000, 100)
	for i := 0; i < 3000; i++ {
		go func() {
			// between 0 and 2ms
			time.Sleep(time.Duration(rand.Intn(2000)) * time.Microsecond)
			uri := routes[rand.Intn(len(routes))]
			req := createRequest("GET", uri)
			res := fasthttp.AcquireResponse()
			ctx := &fasthttp.RequestCtx{
				Request:  *req,
				Response: *res,
			}

			q.Queue(ctx)
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(res)
		}()

	}

	<- time.After(2 * 3000 * time.Millisecond)
	assert.Equal(t, int64(3000), count)
}

// This test is non-deterministic and random in nature
func TestQueueFixedPermutationsFireRandomDelayAll429s(t *testing.T) {
	var routes []string
	for i := 0; i < 15; i++ {
		routes = append(routes, GetRandomRoute())
	}
	var count int64 = 0
	genericProcessor := func(item *lib.QueueItem) (*fasthttp.Response, error) {
		req := fasthttp.AcquireRequest()
		defer fasthttp.ReleaseRequest(req)
		req.SetRequestURI(server_429_0_1_1.URL)
		res := fasthttp.AcquireResponse()
		fastclient.Do(req, res)
		go atomic.AddInt64(&count, 1)
		return res, nil
	}

	q := lib.NewRequestQueue(genericProcessor, 1000, 100)
	for i := 0; i < 3000; i++ {
		go func() {
			// between 0 and 2ms
			time.Sleep(time.Duration(rand.Intn(2000)) * time.Microsecond)
			uri := routes[rand.Intn(len(routes))]
			req := createRequest("GET", uri)
			res := fasthttp.AcquireResponse()
			ctx := &fasthttp.RequestCtx{
				Request:  *req,
				Response: *res,
			}

			q.Queue(ctx)
			fasthttp.ReleaseRequest(req)
			fasthttp.ReleaseResponse(res)
		}()

	}

	<- time.After(3 * 3000 * time.Millisecond)
	assert.Equal(t, int64(3000), count)
}
