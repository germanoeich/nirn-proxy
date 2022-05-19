package lib

import (
	"context"
	"errors"
	"github.com/Clever/leakybucket"
	"github.com/Clever/leakybucket/memory"
	"github.com/sirupsen/logrus"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type QueueItem struct {
	Req *http.Request
	Res *http.ResponseWriter
	doneChan chan *http.Response
	errChan chan error
	// -1 means no abort
	abortTime int
}

type QueueChannel struct {
	sync.Mutex
	ch chan *QueueItem
	waitingUntil time.Time
	lastUsed time.Time
}

type RequestQueue struct {
	sync.RWMutex
	globalLockedUntil *int64
	// bucket path hash as key
	queues map[uint64]*QueueChannel
	processor func(ctx context.Context, item *QueueItem) (*http.Response, error)
	globalBucket leakybucket.Bucket
	// bufferSize Defines the size of the request channel buffer for each bucket
	bufferSize int
	user *BotUserResponse
	identifier string
	isTokenInvalid *int64
	botLimit uint
	queueType QueueType
}


func NewRequestQueue(processor func(ctx context.Context, item *QueueItem) (*http.Response, error), token string, bufferSize int) (*RequestQueue, error) {
	queueType := NoAuth
	var user *BotUserResponse
	var err error
	if !strings.HasPrefix(token, "Bearer") {
		user, err = GetBotUser(token)
		if err != nil && token != "" {
			return nil, err
		}
	} else {
		queueType = Bearer
	}

	limit, err := GetBotGlobalLimit(token, user)
	memStorage := memory.New()
	globalBucket, _ := memStorage.Create("global", limit, 1 * time.Second)
	if err != nil {
		if strings.HasPrefix(err.Error(), "invalid token") {
			// Return a queue that will only return 401s
			var invalid = new(int64)
			*invalid = 999
			return &RequestQueue{
				queues:            make(map[uint64]*QueueChannel),
				processor:         processor,
				globalBucket:      globalBucket,
				globalLockedUntil: new(int64),
				bufferSize: 	   bufferSize,
				user: 			   nil,
				identifier: 	   "InvalidTokenQueue",
				isTokenInvalid:    invalid,
				botLimit: limit,
			}, nil
		}
		return nil, err
	}

	identifier := "NoAuth"
	if user != nil {
		queueType = Bot
		identifier = user.Username + "#" + user.Discrim
	}

	if queueType == Bearer {
		identifier = "Bearer"
	}

	ret := &RequestQueue{
		queues:            make(map[uint64]*QueueChannel),
		processor:         processor,
		globalBucket:      globalBucket,
		globalLockedUntil: new(int64),
		bufferSize: 	   bufferSize,
		user: 			   user,
		identifier: 	   identifier,
		isTokenInvalid:    new(int64),
		botLimit: 		   limit,
		queueType: 		   queueType,
	}

	if queueType != Bearer {
		logger.WithFields(logrus.Fields{"globalLimit": limit, "identifier": identifier, "bufferSize": bufferSize}).Info("Created new queue")
		// Only sweep bot queues, bearer queues get completely destroyed and hold way less endpoints
		go ret.tickSweep()
	} else {
		logger.WithFields(logrus.Fields{"globalLimit": limit, "identifier": identifier, "bufferSize": bufferSize}).Debug("Created new bearer queue")
	}

	return ret, nil
}

func (q *RequestQueue) destroy() {
	q.Lock()
	defer q.Unlock()
	logger.Debug("Destroying queue")
	for _, val := range q.queues {
		close(val.ch)
	}
}

func (q *RequestQueue) sweep() {
	q.Lock()
	defer q.Unlock()
	logger.Info("Sweep start")
	sweptEntries := 0
	for key, val := range q.queues {
		if time.Since(val.lastUsed) > 10 * time.Minute {
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

func safeSend(queue *QueueChannel, value *QueueItem) {
	queue.Lock()
	defer queue.Unlock() // Will be called after the deferred function below

	defer func() {
		if recover() != nil {
			value.errChan <- errors.New("failed to send due to closed channel, sending 429 for client to retry")
			Generate429(value.Res)
		}
	}()

	queue.ch <- value
}

func (q *RequestQueue) Queue(req *http.Request, res *http.ResponseWriter, path string, pathHash uint64, defaultAbort int) error {
	logger.WithFields(logrus.Fields{
		"bucket": path,
		"path": req.URL.Path,
		"method": req.Method,
	}).Trace("Inbound request")

	var abort int
	abortHeader := req.Header.Get("X-RateLimit-Abort-After")
	if abortHeader != "" {
		valParsed, err := strconv.ParseInt(abortHeader, 10, 64)
		if err != nil {
			return err
		}

		abort = int(valParsed)
	} else {
		abort = defaultAbort
	}

	ch := q.getQueueChannel(path, pathHash)
	// waitingUntil may be a past time, making time.Until() negative. This is still handled like
	// any time, assuming abort is positive.
	if abort != -1 && abort < int(time.Until(ch.waitingUntil).Seconds()) {
		return generate408Aborted(res)
	}

	doneChan := make(chan *http.Response)
	errChan := make(chan error)

	safeSend(ch, &QueueItem{ req, res, doneChan, errChan, abort })

	select {
	case <-doneChan:
		return nil
	case err := <-errChan:
		return err
	}
}

func (q *RequestQueue) getQueueChannel(path string, pathHash uint64) *QueueChannel {
	t := time.Now()
	q.Lock()
	defer q.Unlock()
	ch, ok := q.queues[pathHash]
	if !ok {
		ch = &QueueChannel{
			ch: make(chan *QueueItem, q.bufferSize),
			waitingUntil: t,
			lastUsed: t,
		}
		q.queues[pathHash] = ch
		// It's important that we only have 1 goroutine per channel
		go q.subscribe(ch, path, pathHash)
	} else {
		ch.lastUsed = t
	}
	return ch
}

func parseHeaders(headers *http.Header, preferRetryAfter bool) (int64, int64, time.Duration, bool, error) {
	if headers == nil {
		return 0, 0, 0, false, errors.New("null headers")
	}

	limit := headers.Get("x-ratelimit-limit")
	remaining := headers.Get("x-ratelimit-remaining")
	resetAfter := headers.Get("x-ratelimit-reset-after")
	retryAfter := headers.Get("retry-after")
	if resetAfter == "" || (preferRetryAfter && retryAfter != "") {
		// Globals return no x-ratelimit-reset-after headers, shared ratelimits have a wrong reset-after
		// this is the best option without parsing the body
		resetAfter = headers.Get("retry-after")
	}
	isGlobal := headers.Get("x-ratelimit-global") == "true"

	var resetParsed float64
	var reset time.Duration = 0
	var err error
	if resetAfter != "" {
		resetParsed, err = strconv.ParseFloat(resetAfter, 64)
		if err != nil {
			return 0, 0, 0, false, err
		}

		// Convert to MS instead of seconds to preserve decimal precision
		reset = time.Duration(int(resetParsed * 1000)) * time.Millisecond
	}

	if isGlobal {
		return 0, 0, reset, isGlobal, nil
	}

	if limit == "" {
		return 0, 0, reset, false, nil
	}

	limitParsed, err := strconv.ParseInt(limit, 10, 32)
	if err != nil {
		return 0, 0, 0, false, err
	}

	remainingParsed, err := strconv.ParseInt(remaining, 10, 32)
	if err != nil {
		return 0, 0, 0, false, err
	}

	return limitParsed, remainingParsed, reset, isGlobal, nil
}

func return404webhook(item *QueueItem) {
	res := *item.Res
	res.WriteHeader(404)
	body := "{\n  \"message\": \"Unknown Webhook\",\n  \"code\": 10015\n}"
	_, err := res.Write([]byte(body))
	if err != nil {
		item.errChan <- err
		return
	}
	item.doneChan <- nil

}

func return401(item *QueueItem) {
	res := *item.Res
	res.WriteHeader(401)
	body := "{\n\t\"message\": \"401: Unauthorized\",\n\t\"code\": 0\n}"
	_, err := res.Write([]byte(body))
	if err != nil {
		item.errChan <- err
		return
	}
	item.doneChan <- nil
}

func generate408Aborted(resp *http.ResponseWriter) error {
	res := *resp

	res.Header().Set("Generated-By-Proxy", "true")
	res.WriteHeader(408)

	_, err := res.Write([]byte("{\n  \"message\": \"Request aborted because of ratelimits\",\n  \"code\": 0\n}"))
	return err
}

func isInteraction(url string) bool {
	parts := strings.Split(strings.SplitN(url, "?", 1)[0], "/")
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
		ctx := context.WithValue(item.Req.Context(), "identifier", q.identifier)
		if ret404 {
			return404webhook(item)
			continue
		}


		if atomic.LoadInt64(q.isTokenInvalid) > 0 {
			return401(item)
			continue
		}

		resp, err := q.processor(ctx, item)
		if err != nil {
			item.errChan <- err
			continue
		}

		scope := resp.Header.Get("x-ratelimit-scope")

		_, remaining, resetAfter, isGlobal, err := parseHeaders(&resp.Header, scope != "user")

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
			continue
		}
		item.doneChan <- resp

		if resp.StatusCode == 429 && scope != "shared"{
			logger.WithFields(logrus.Fields{
				"prevRemaining": prevRem,
				"prevResetAfter": prevReset,
				"remaining": remaining,
				"resetAfter": resetAfter,
				"bucket": path,
				"route": item.Req.URL.String(),
				"method": item.Req.Method,
				"isGlobal": isGlobal,
				"pathHash": pathHash,
				// TODO: Remove this when 429s are not a problem anymore
				"discordBucket": resp.Header.Get("x-ratelimit-bucket"),
				"ratelimitScope": resp.Header.Get("x-ratelimit-scope"),
			}).Warn("Unexpected 429")
		}

		if resp.StatusCode == 404 && strings.HasPrefix(path, "/webhooks/") && !isInteraction(item.Req.URL.String()) {
			logger.WithFields(logrus.Fields{
				"bucket": path,
				"route": item.Req.URL.String(),
				"method": item.Req.Method,
			}).Info("Setting fail fast 404 for webhook")
			ret404 = true
		}

		if resp.StatusCode == 401 && !isInteraction(item.Req.URL.String()) && q.queueType != NoAuth {
			// Permanently lock this queue
			logger.WithFields(logrus.Fields{
				"bucket": path,
				"route": item.Req.URL.String(),
				"method": item.Req.Method,
				"identifier": q.identifier,
				"status": resp.StatusCode,
			}).Error("Received 401 during normal operation, assuming token is invalidated, locking bucket permanently")

			if EnvGet("DISABLE_401_LOCK", "false") != "true" {
				atomic.StoreInt64(q.isTokenInvalid, 999)
			}
		}

		if remaining == 0 || resp.StatusCode == 429 {
			// Before sleeping for the ratelimit, check if there are any requests that would like to be aborted
			ch.Lock()
			ch.waitingUntil = time.Now().Add(resetAfter)
			duration := time.Until(ch.waitingUntil)
			seconds := int(duration.Seconds())

			length := len(ch.ch)
			for i := 0; i < length; i++ {
				abortItem := <-ch.ch

				if abortItem.abortTime == -1 {
					ch.ch <- abortItem
					continue
				}

				abortItem.abortTime -= seconds
				if abortItem.abortTime < 0 {
					err = generate408Aborted(abortItem.Res)
					if err != nil {
						abortItem.errChan <- err
					} else {
						abortItem.doneChan <- nil
					}
				} else {
					ch.ch <- abortItem
				}
			}
			ch.Unlock()

			time.Sleep(duration)
		}
		prevRem, prevReset = remaining, resetAfter
	}
}
