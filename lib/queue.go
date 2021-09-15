package lib

import (
	"errors"
	"github.com/Clever/leakybucket"
	"github.com/Clever/leakybucket/memory"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// QueueChannelBufferSize Defines the size of the request channel buffer for each bucket
// Realistically, this should be as high as possible to prevent blocking sends
// While blocking sends aren't a problem in itself, they are unordered, meaning
// in a high load situation, if this number is too low, it would cause requests to
// fight to send, which messes up the ordering of requests.
const QueueChannelBufferSize = 1000

type QueueItem struct {
	Req *http.Request
	Res *http.ResponseWriter
	doneChan chan *http.Response
	errChan chan error
}

type RequestQueue struct {
	sync.RWMutex
	bucketMu sync.RWMutex
	// bucket path as key
	queues map[string]chan *QueueItem
	buckets map[string]*Bucket
	processor func(item *QueueItem) *http.Response
	globalBucket leakybucket.Bucket
}

func NewRequestQueue(processor func(item *QueueItem) *http.Response, globalLimit uint) *RequestQueue {
	memStorage := memory.New()
	globalBucket, err := memStorage.Create("global", globalLimit, 1 * time.Second)
	if err != nil {
		panic(err)
	}
	return &RequestQueue{
		queues:    make(map[string]chan *QueueItem),
		buckets:   make(map[string]*Bucket),
		processor: processor,
		globalBucket: globalBucket,
	}
}

func (q *RequestQueue) Queue(req *http.Request, res *http.ResponseWriter) (string, *http.Response, error) {
	q.RLock()
	path := GetOptimisticBucketPath(req.URL.Path, req.Method)
	ch, ok := q.queues[path]
	if !ok {
		q.RUnlock()
		q.Lock()
		// Check again to see if the queue channel wasn't created
		// While we didn't hold the exclusive lock
		ch, ok = q.queues[path]
		if !ok {
			ch = make(chan *QueueItem, QueueChannelBufferSize)
			q.queues[path] = ch
			// It's important that we only have 1 goroutine per channel
			go q.subscribe(ch, path)
		}
		q.Unlock()
	} else {
		// It's safe to unlock early (before sending to the ch) because the channels are never replaced, only created
		q.RUnlock()
	}
	doneChan := make(chan *http.Response)
	errChan := make(chan error)
	ch <- &QueueItem{req, res, doneChan, errChan }

	select {
	case resp := <-doneChan:
		return path, resp, nil
	case err := <-errChan:
		return path, nil, err
	}
}

func parseHeaders(headers *http.Header) (int64, int64, time.Duration, error) {
	if headers == nil {
		return 0, 0, 0, errors.New("null headers")
	}

	limit := headers.Get("x-ratelimit-limit")
	remaining := headers.Get("x-ratelimit-remaining")
	resetAfter := headers.Get("x-ratelimit-reset-after")

	if limit == "" {
		return 0, 0, 0, nil
	}

	limitParsed, err := strconv.ParseInt(limit, 10, 32)
	if err != nil {
		return 0, 0, 0, err
	}

	remainingParsed, err := strconv.ParseInt(remaining, 10, 32)
	if err != nil {
		return 0, 0, 0, err
	}

	resetParsed, err := strconv.ParseFloat(resetAfter, 64)
	if err != nil {
		return 0, 0, 0, err
	}
	// This serves the purpose of keeping the precision of resetAfter.
	// Since it's a float, converting directly to duration would drop the decimals
	reset := time.Duration(int(resetParsed * 1000)) * time.Millisecond

	return limitParsed, remainingParsed, reset, nil
}

func (q *RequestQueue) subscribe(ch chan *QueueItem, path string) {
	// This function has 1 goroutine for each bucket path
	// Locking here is not needed
	for item := range ch {
	takeGlobal:
		_, err := q.globalBucket.Add(1)
		if err != nil {
			reset := q.globalBucket.Reset()
			<- time.After(time.Until(reset))
			goto takeGlobal
		}
		q.bucketMu.RLock()
		bucket, ok := q.buckets[path]
		if !ok {
			q.bucketMu.RUnlock()
			q.bucketMu.Lock()
			bucket, ok := q.buckets[path]
			if !ok {
				bucket = NewBucket(1, 1, time.Now().Add(5*time.Second))
				q.buckets[path] = bucket
			}
			q.bucketMu.Unlock()
		} else {
			q.bucketMu.RUnlock()
		}

		bucket.Take()
		resp := q.processor(item)
		limit, remaining, resetAfter, err := parseHeaders(&resp.Header)
		if err != nil {
			item.errChan <- err
			return
		}
		bucket.Update(limit, remaining, time.Now().Add(resetAfter))
		item.doneChan <- resp
	}
}