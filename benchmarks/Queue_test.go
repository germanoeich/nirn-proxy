package benchmarks

import (
	"github.com/germanoeich/nirn-proxy/lib"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/valyala/fasthttp"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func createRequest(method string, uri string) *fasthttp.Request {
	req := fasthttp.AcquireRequest()
	req.SetRequestURI("https://discord.com" + uri)
	req.Header.SetMethod(method)
	return req
}

// before fasthttp/ants:
// BenchmarkQueueExistingChannel-8         	     100	  20148683 ns/op	   33988 B/op	     163 allocs/op
// after:
// BenchmarkQueueExistingChannel-8   	     	 100	  20142445 ns/op	    4535 B/op	      40 allocs/op
func BenchmarkQueueExistingChannel(b *testing.B) {
	genericProcessor := func(item *lib.QueueItem) (*fasthttp.Response, error) {
		req := fasthttp.AcquireRequest()
		defer fasthttp.ReleaseRequest(req)
		req.SetRequestURI(server_200_noheaders.URL)
		res := fasthttp.AcquireResponse()
		fasthttp.Do(req, res)
		return res, nil
	}

	q := lib.NewRequestQueue(genericProcessor, 50, 50)
	reqpre := createRequest("GET", "/api/v9/guilds/915995872213471273/audit-logs")
	respre := fasthttp.AcquireResponse()
	ctxpre := &fasthttp.RequestCtx{
		Request:  *reqpre,
		Response: *respre,
	}
	fasthttp.ReleaseRequest(reqpre)
	fasthttp.ReleaseResponse(respre)
	q.Queue(ctxpre)
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		req := createRequest("GET", "/api/v9/guilds/915995872213471273/audit-logs")
		res := fasthttp.AcquireResponse()
		ctx := &fasthttp.RequestCtx{
			Request:  *req,
			Response: *res,
		}
		q.Queue(ctx)
		fasthttp.ReleaseRequest(req)
		fasthttp.ReleaseResponse(res)
	}
}

// before fasthttp/ants:
// BenchmarkQueueNonExistingChannel-8      	     100	  10475207 ns/op	   35291 B/op	     170 allocs/op
// after:
// BenchmarkQueueNonExistingChannel-8   	     100	  10117743 ns/op	    7286 B/op	      66 allocs/op
func BenchmarkQueueNonExistingChannel(b *testing.B) {
	genericProcessor := func(item *lib.QueueItem) (*fasthttp.Response, error) {
		req := fasthttp.AcquireRequest()
		defer fasthttp.ReleaseRequest(req)
		req.SetRequestURI(server_200_noheaders.URL)
		res := fasthttp.AcquireResponse()
		fasthttp.Do(req, res)
		return res, nil
	}

	q := lib.NewRequestQueue(genericProcessor, 50, 50)

	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		req := createRequest("GET", "/api/v9/guilds/915995872213471273/audit-logs" + strconv.Itoa(n))
		ctx := &fasthttp.RequestCtx{
			Request:  *req,
			Response: fasthttp.Response{},
		}
		q.Queue(ctx)
	}
}