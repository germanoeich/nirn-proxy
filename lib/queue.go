package lib

import (
	"errors"
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
	doneChan chan bool
	errChan chan error
}

type RequestQueue struct {
	sync.RWMutex
	// bucket path as key
	queues map[string]chan *QueueItem
	buckets map[string]IBucket
	processor func(item *QueueItem) *http.Header
}

func NewRequestQueue(processor func(item *QueueItem) *http.Header) *RequestQueue {
	return &RequestQueue{
		queues:    make(map[string]chan *QueueItem),
		buckets:   make(map[string]IBucket),
		processor: processor,
	}
}

func (q *RequestQueue) Queue(req *http.Request, res *http.ResponseWriter) {
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
	doneChan := make(chan bool)
	errChan := make(chan error)
	ch <- &QueueItem{req, res, doneChan, errChan }

	select {
	case <-doneChan:
	case <-errChan:
	}
}

func (q *RequestQueue) NewQueueMemory(processor func(item *QueueItem) *http.Header) *RequestQueue {
	return &RequestQueue{
		queues: make(map[string]chan *QueueItem),
		buckets: make(map[string]IBucket),
		processor: processor,
	}
}

func parseHeaders(headers *http.Header) (int64, time.Duration, error) {
	if headers == nil {
		return 0, 0, errors.New("null headers")
	}

	limit := headers.Get("x-ratelimit-limit")
	resetAfter := headers.Get("x-ratelimit-reset-after")

	if limit == "" {
		return 0, 0, nil
	}

	limitParsed, errI := strconv.ParseInt(limit, 10, 32)
	if errI != nil {
		return 0, 0, errI
	}

	resetParsed, errR := strconv.ParseFloat(resetAfter, 64)
	if errR != nil {
		return 0, 0, errR
	}
	// This serves the purpose of keeping the precision of resetAfter.
	// Since it's a float, converting directly to duration would drop the decimals
	reset := time.Duration(int(resetParsed * 1000)) * time.Millisecond

	return limitParsed, reset, nil
}

func (q *RequestQueue) subscribe(ch chan *QueueItem, path string) {
	// This function has 1 goroutine for each bucket path
	// Locking here is not needed
	for item := range ch {
		bucket, ok := q.buckets[path]
		if !ok {
			// This is the first request for this bucket, in this case, we synchronously fire it and wait for headers
			headers := q.processor(item)
			limit, resetAfter, err := parseHeaders(headers)
			if err != nil {
				item.errChan <- err
				return
			}
			if limit == 0 {
				bucket := NewNoopBucket()
				q.buckets[path] = bucket
			} else {
				bucket := NewBucket(limit, time.Now().Add(resetAfter))
				q.buckets[path] = bucket
			}
			item.doneChan <- true
		} else {
			bucket.Take()
			headers := q.processor(item)
			_, resetAfter, err := parseHeaders(headers)
			if err == nil {
				bucket.SetResetAt(time.Now().Add(resetAfter))
			}
			item.doneChan <- true
		}
	}
}