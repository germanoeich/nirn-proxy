package benchmarks

import (
	"github.com/germanoeich/nirn-proxy/lib"
	"github.com/sirupsen/logrus/hooks/test"
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

// BenchmarkQueueExistingChannel-8         	     100	  20148683 ns/op	   33988 B/op	     163 allocs/op
func BenchmarkQueueExistingChannel(b *testing.B) {
	genericProcessor := func(item *lib.QueueItem) (*http.Response, error) {
		req, _ := http.NewRequest(http.MethodGet, server_200_noheaders.URL, nil)
		res, _ := http.DefaultClient.Do(req)
		return res, nil
	}

	q := lib.NewRequestQueue(genericProcessor, 50, 50)
	req := httptest.NewRequest("GET", "/api/v9/guilds/915995872213471273/audit-logs", nil)
	q.Queue(req, nil)
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		req := httptest.NewRequest("GET", "/api/v9/guilds/915995872213471273/audit-logs", nil)
		q.Queue(req, nil)
	}
}

// BenchmarkQueueNonExistingChannel-8      	     100	  10475207 ns/op	   35291 B/op	     170 allocs/op
func BenchmarkQueueNonExistingChannel(b *testing.B) {
	genericProcessor := func(item *lib.QueueItem) (*http.Response, error) {
		req, _ := http.NewRequest(http.MethodGet, server_200_noheaders.URL, nil)
		res, _ := http.DefaultClient.Do(req)
		return res, nil
	}
	q := lib.NewRequestQueue(genericProcessor, 50, 50)

	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		req := httptest.NewRequest("GET", "/api/v9/guilds/915995872213471273/audit-logs" + strconv.Itoa(n), nil)
		q.Queue(req, nil)
	}
}