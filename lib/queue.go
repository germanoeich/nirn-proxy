package lib

import (
	"bytes"
	"errors"
	"github.com/Clever/leakybucket"
	"github.com/Clever/leakybucket/memory"
	"github.com/panjf2000/ants/v2"
	"github.com/sirupsen/logrus"
	"github.com/valyala/fasthttp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type QueueItem struct {
	Ctx      *fasthttp.RequestCtx
	doneChan chan bool
	errChan chan error
}

type QueueChannel struct {
	ch chan *QueueItem
	lastUsed *time.Time
}

type RequestQueue struct {
	sync.RWMutex
	channelMu         sync.Mutex
	globalLockedUntil *int64
	// bucket path hash as key
	queues map[uint64]*QueueChannel
	processor func(item *QueueItem) (*fasthttp.Response, error)
	globalBucket leakybucket.Bucket
	// bufferSize Defines the size of the request channel buffer for each bucket
	bufferSize int64
	workerPool *ants.Pool
}

func NewRequestQueue(processor func(item *QueueItem) (*fasthttp.Response, error), globalLimit uint, bufferSize int64) RequestQueue {
	// This is absurdly large, we don't want to limit goroutine count but rather be able to reuse goroutines
	poll, err := ants.NewPool(int(globalLimit * 1000), ants.WithPreAlloc(false))
	memStorage := memory.New()
	globalBucket, err := memStorage.Create("global", globalLimit, 1 * time.Second)
	if err != nil {
		panic(err)
	}

	ret := RequestQueue{
		queues:            make(map[uint64]*QueueChannel),
		processor:         processor,
		globalBucket:      globalBucket,
		globalLockedUntil: new(int64),
		bufferSize: 	   bufferSize,
		workerPool: 	   poll,
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
		if time.Since(*val.lastUsed) > 10 * time.Minute {
			close(val.ch)
			delete(q.queues, key)
			sweptEntries++
		}
	}
	logger.WithFields(logrus.Fields{"sweptEntries": sweptEntries}).Info("Finished sweep")
}

func (q *RequestQueue) tickSweep() {
	t := time.NewTicker(5 * time.Minute)

	for range t.C {
		q.sweep()
	}
}

func (q *RequestQueue) Queue(ctx *fasthttp.RequestCtx) (error) {
	reqPath := B2S(ctx.Path())
	reqMethod := B2S(ctx.Method())
	path := GetOptimisticBucketPath(reqPath, reqMethod)
	logger.WithFields(logrus.Fields{
		"bucket": path,
		"path": reqPath,
		"method": reqMethod,
	}).Trace("Inbound request")
	q.RLock()
	ch, err := q.getQueueChannel(path)

	if err != nil {
		return errors.New("failed to get/create queue channel")
	}

	doneChan := make(chan bool)
	errChan := make(chan error)
	ch.ch <- &QueueItem{ctx, doneChan,errChan }
	q.RUnlock()
	select {
	case <-doneChan:
		return nil
	case err := <-errChan:
		return err
	}
}

func (q *RequestQueue) getQueueChannel(path string) (*QueueChannel, error) {
	pathHash := HashCRC64(path)
	q.channelMu.Lock()
	defer q.channelMu.Unlock()
	t := time.Now()
	ch, ok := q.queues[pathHash]
	if !ok {
		ch = &QueueChannel{ make(chan *QueueItem, q.bufferSize), &t }
		q.queues[pathHash] = ch

		err := q.workerPool.Submit(func() {
			q.subscribe(ch, path, pathHash)
		})

		if err != nil {
			logger.WithFields(logrus.Fields{
				"poolSize": q.workerPool.Cap(),
				"poolLeft": q.workerPool.Free(),
				"poolClosed": q.workerPool.IsClosed(),
				"route": path,
			}).Error(err)
			return nil, err
		}
	} else {
		ch.lastUsed = &t
	}
	return ch, nil
}

func parseHeaders(headers *fasthttp.ResponseHeader) (int64, int64, time.Duration, bool, error) {
	if headers == nil {
		return 0, 0, 0, false, errors.New("null headers")
	}

	limit := headers.Peek("x-ratelimit-limit")
	remaining := headers.Peek("x-ratelimit-remaining")
	resetAfter := headers.Peek("x-ratelimit-reset-after")
	if len(resetAfter) == 0 {
		// Globals return no x-ratelimit-reset-after headers, this is the best option without parsing the body
		resetAfter = headers.Peek("retry-after")
	}
	isGlobal := B2S(headers.Peek("x-ratelimit-global")) == "true"

	var resetParsed float64
	var reset time.Duration
	var err error
	if len(resetAfter) > 0 {
		resetParsed, err = strconv.ParseFloat(B2S(resetAfter), 64)
		if err != nil {
			return 0, 0, 0, false, err
		}

		// Convert to MS instead of seconds to preserve decimal precision
		reset = time.Duration(int(resetParsed * 1000)) * time.Millisecond
	}

	if isGlobal {
		return 0, 0, reset, isGlobal, nil
	}

	if len(limit) == 0 {
		return 0, 0, 0, false, nil
	}

	limitParsed, err := strconv.ParseInt(B2S(limit), 10, 32)
	if err != nil {
		return 0, 0, 0, false, err
	}

	remainingParsed, err := strconv.ParseInt(B2S(remaining), 10, 32)
	if err != nil {
		return 0, 0, 0, false, err
	}

	return limitParsed, remainingParsed, reset, isGlobal, nil
}

func (q *RequestQueue) takeGlobal(path string) {
takeGlobal:
	waitTime := atomic.LoadInt64(q.globalLockedUntil)
	if waitTime > 0 {
		logger.WithFields(logrus.Fields{
			"bucket": path,
			"waitTime": waitTime,
		}).Trace("Waiting for existing global to clear")
		time.Sleep(time.Until(time.Unix(0, waitTime)))
		sw := atomic.CompareAndSwapInt64(q.globalLockedUntil, waitTime, 0)
		if sw {
			logger.Info("Unlocked global bucket")
		}
	}
	_, err := q.globalBucket.Add(1)
	if err != nil {
		reset := q.globalBucket.Reset()
		logger.WithFields(logrus.Fields{
			"bucket": path,
			"waitTime": time.Until(reset),
		}).Trace("Failed to grab global token, sleeping for a bit")
		time.Sleep(time.Until(reset))
		goto takeGlobal
	}
}

func return404webhook(item *QueueItem) {
	item.Ctx.SetStatusCode(404)
	item.Ctx.WriteString("{\n  \"message\": \"Unknown Webhook\",\n  \"code\": 10015\n}")
}

func isInteraction(url []byte) bool {
	parts := bytes.Split(bytes.SplitN(url,[]byte("?"), 1)[0], []byte("/"))
	for _, p := range parts {
		if len(p) > 128 {
			return true
		}
	}
	return false
}

func (q *RequestQueue) subscribe(ch *QueueChannel, path string, pathHash uint64) {
	// This function has 1 goroutine for each bucket path
	// Locking here is not needed

	//Only used for logging
	var prevRem int64 = 0
	var prevReset time.Duration = 0

	// Fail fast path for webhook 404s
	var ret404 = false
	for item := range ch.ch {
		if ret404 {
			return404webhook(item)
			item.doneChan <- false
			continue
		}

		q.takeGlobal(path)

		resp, err := q.processor(item)

		if err != nil {
			item.errChan <- err
			fasthttp.ReleaseResponse(resp)
			continue
		}

		_, remaining, resetAfter, isGlobal, err := parseHeaders(&resp.Header)

		if isGlobal {
			//Lock global
			sw := atomic.CompareAndSwapInt64(q.globalLockedUntil, 0, time.Now().Add(resetAfter).UnixNano())
			if sw {
				logger.WithFields(logrus.Fields{
					"until": time.Now().Add(resetAfter),
					"resetAfter": resetAfter,
				}).Warn("Global reached, locking")
			}
		}

		if err != nil {
			item.errChan <- err
			fasthttp.ReleaseResponse(resp)
			continue
		}
		item.doneChan <- true

		if resp.StatusCode() == 429 {
			logger.WithFields(logrus.Fields{
				"prevRemaining": prevRem,
				"prevResetAfter": prevReset,
				"remaining": remaining,
				"resetAfter": resetAfter,
				"bucket": path,
				"route": B2S(item.Ctx.RequestURI()),
				"method": B2S(item.Ctx.Method()),
				"isGlobal": isGlobal,
				"pathHash": pathHash,
				// TODO: Remove this when 429s are not a problem anymore
				"discordBucket": B2S(resp.Header.Peek("x-ratelimit-bucket")),
				"ratelimitScope": B2S(resp.Header.Peek("x-ratelimit-scope")),
			}).Warn("Unexpected 429")
		}

		if resp.StatusCode() == 404 && strings.HasPrefix(path, "/webhooks/") && !isInteraction(item.Ctx.RequestURI()) {
			logger.WithFields(logrus.Fields{
				"bucket": path,
				"route": B2S(item.Ctx.RequestURI()),
				"method": B2S(item.Ctx.Method()),
			}).Info("Setting fail fast 404 for webhook")
			ret404 = true
		}
		fasthttp.ReleaseResponse(resp)
		if remaining == 0 || resp.StatusCode() == 429 {
			time.Sleep(time.Until(time.Now().Add(resetAfter)))
		}
		prevRem, prevReset = remaining, resetAfter
	}
}