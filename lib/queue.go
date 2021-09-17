package lib

import (
	"errors"
	"github.com/Clever/leakybucket"
	"github.com/Clever/leakybucket/memory"
	"github.com/sirupsen/logrus"
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
const QueueChannelBufferSize = 200

type QueueItem struct {
	Req *http.Request
	Res *http.ResponseWriter
	doneChan chan *http.Response
	errChan chan error
}

type QueueChannel struct {
	ch chan *QueueItem
	lastUsed *time.Time
}

type RequestQueue struct {
	sync.RWMutex
	channelMu sync.RWMutex
	sweepTicker *time.Ticker
	// bucket path as key
	queues map[string]*QueueChannel
	processor func(item *QueueItem) *http.Response
	globalBucket leakybucket.Bucket
}

func NewRequestQueue(processor func(item *QueueItem) *http.Response, globalLimit uint) *RequestQueue {
	memStorage := memory.New()
	globalBucket, err := memStorage.Create("global", globalLimit, 1 * time.Second)
	if err != nil {
		panic(err)
	}
	ret := &RequestQueue{
		queues:    make(map[string]*QueueChannel),
		processor: processor,
		globalBucket: globalBucket,
	}
	go ret.tickSweep()
	return ret
}
func (q *RequestQueue) sweep() {
	q.Lock()
	q.channelMu.Lock()
	defer q.Unlock()
	defer q.channelMu.Unlock()
	logger.Info("Sweep start")
	sweptEntries := 0
	for key, val := range q.queues {
		if time.Since(*val.lastUsed) > 5 * time.Minute {
			logger.Debugf("Deleting %s\n", key)
			close(val.ch)
			delete(q.queues, key)
			sweptEntries++
		}
	}
	logger.WithFields(logrus.Fields{"sweptEntries": sweptEntries}).Info("Finished sweep")
}

func (q *RequestQueue) tickSweep() {
	q.sweepTicker = time.NewTicker(5 * time.Minute)

	for _ = range q.sweepTicker.C {
		q.sweep()
	}
}

func (q *RequestQueue) Queue(req *http.Request, res *http.ResponseWriter) (string, *http.Response, error) {
	path := GetOptimisticBucketPath(req.URL.Path, req.Method)
	q.RLock()
	ch := q.getQueueChannel(path)

	doneChan := make(chan *http.Response)
	errChan := make(chan error)
	ch.ch <- &QueueItem{req, res, doneChan, errChan }
	q.RUnlock()
	select {
	case resp := <-doneChan:
		return path, resp, nil
	case err := <-errChan:
		return path, nil, err
	}
}

func (q *RequestQueue) getQueueChannel(path string) *QueueChannel {
	q.channelMu.Lock()
	defer q.channelMu.Unlock()
	t := time.Now()
	ch, ok := q.queues[path]
	if !ok {
		// Check again to see if the queue channel wasn't created
		// While we didn't hold the exclusive lock
		ch, ok = q.queues[path]
		if !ok {
			ch = &QueueChannel{ make(chan *QueueItem, QueueChannelBufferSize), &t }
			q.queues[path] = ch
			// It's important that we only have 1 goroutine per channel
			go q.subscribe(ch, path)
		}
	} else {
		ch.lastUsed = &t
	}
	return ch
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

func (q *RequestQueue) takeGlobal() {
takeGlobal:
	_, err := q.globalBucket.Add(1)
	if err != nil {
		reset := q.globalBucket.Reset()
		<- time.After(time.Until(reset))
		goto takeGlobal
	}
}

func (q *RequestQueue) subscribe(ch *QueueChannel, path string) {
	// This function has 1 goroutine for each bucket path
	// Locking here is not needed

	//Only used for logging
	var prevRem int64 = 0
	var prevReset time.Duration = 0
	for item := range ch.ch {
		q.takeGlobal()
		resp := q.processor(item)
		_, remaining, resetAfter, err := parseHeaders(&resp.Header)

		if err != nil {
			item.errChan <- err
			continue
		}
		item.doneChan <- resp

		if resp.StatusCode == 429 {
			logger.WithFields(logrus.Fields{
				"prevRemaining": prevRem,
				"prevResetAfter": prevReset,
				"remaining": remaining,
				"resetAfter": resetAfter,
				"bucket": path,
				"route": item.Req.URL.String(),
				"method": item.Req.Method,
			}).Warn("Unexpected 429")
		}
		if remaining == 0 {
			time.Sleep(time.Until(time.Now().Add(resetAfter)))
		}
		prevRem, prevReset = remaining, resetAfter
	}
}