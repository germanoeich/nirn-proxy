package lib

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Clever/leakybucket"
	"github.com/Clever/leakybucket/memory"
	"github.com/sirupsen/logrus"
)

type QueueItem struct {
	Req      *http.Request
	Res      *http.ResponseWriter
	ReqBody  []byte
	doneChan chan *http.Response
	errChan  chan error
}

type QueueChannel struct {
	sync.Mutex
	ch        chan *QueueItem
	lastUsed  time.Time
	ratelimit *BucketRateLimit
	lockerFun func(item *QueueItem)
}

type RequestQueue struct {
	sync.Mutex
	globalLockedUntil *int64
	// bucket path hash as key
	queues       map[uint64]*QueueChannel
	processor    func(ctx context.Context, item *QueueItem) (*http.Response, error)
	globalBucket leakybucket.Bucket
	// bufferSize Defines the size of the request channel buffer for each bucket
	bufferSize     int
	user           *BotUserResponse
	identifier     string
	isTokenInvalid *int64
	botLimit       uint
	queueType      QueueType
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
	globalBucket, _ := memStorage.Create("global", limit, 1*time.Second)
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
				bufferSize:        bufferSize,
				user:              nil,
				identifier:        "InvalidTokenQueue",
				isTokenInvalid:    invalid,
				botLimit:          limit,
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
		bufferSize:        bufferSize,
		user:              user,
		identifier:        identifier,
		isTokenInvalid:    new(int64),
		botLimit:          limit,
		queueType:         queueType,
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
		if time.Since(val.lastUsed) > 10*time.Minute {
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
	defer func() {
		if recover() != nil {
			value.errChan <- errors.New("failed to send due to closed channel, sending 429 for client to retry")
			Generate429(value.Res)
		}
	}()

	queue.ch <- value
}

func (q *RequestQueue) Queue(req *http.Request, res *http.ResponseWriter, path string, pathHash uint64) error {
	logger.WithFields(logrus.Fields{
		"bucket": path,
		"path":   req.URL.Path,
		"method": req.Method,
	}).Trace("Inbound request")

	ch := q.getQueueChannel(path, pathHash)

	doneChan := make(chan *http.Response)
	errChan := make(chan error)

	safeSend(ch, &QueueItem{req, res, nil, doneChan, errChan})

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
			ch:        make(chan *QueueItem, q.bufferSize),
			lastUsed:  t,
			ratelimit: nil,
		}
		q.queues[pathHash] = ch
		// It's important that we only have 1 goroutine per channel
		go q.subscribe(ch, path, pathHash)
	} else {
		ch.lastUsed = t
	}
	return ch
}

func parseHeaders(headers *http.Header) (string, int64, int64, float64, float64, string, error) {
	if headers == nil {
		return "", 0, 0, 0, 0, "", errors.New("null headers")
	}

	bucket := headers.Get("x-ratelimit-bucket")
	limit := headers.Get("x-ratelimit-limit")
	remaining := headers.Get("x-ratelimit-remaining")
	resetAt := headers.Get("x-ratelimit-reset")
	resetAfter := headers.Get("x-ratelimit-reset-after")
	retryAfter := headers.Get("retry-after")
	scope := headers.Get("x-ratelimit-scope")

	if scope == "" {
		scope = "route"
	}

	if resetAfter == "" || (scope != "user" && retryAfter != "") {
		// Globals return no x-ratelimit-reset-after headers, shared ratelimits have a wrong reset-after
		// this is the best option without parsing the body
		resetAfter = headers.Get("retry-after")
	}

	var err error

	var resetAfterParsed float64 = 0
	if resetAfter != "" {
		resetAfterParsed, err = strconv.ParseFloat(resetAfter, 64)
		if err != nil {
			return "", 0, 0, 0, 0, "", err
		}
	}

	if scope == "global" {
		return bucket, 0, 0, resetAfterParsed, 0, scope, nil
	}

	if limit == "" {
		return "", 0, 0, resetAfterParsed, 0, scope, nil
	}

	limitParsed, err := strconv.ParseInt(limit, 10, 32)
	if err != nil {
		return "", 0, 0, 0, 0, "", err
	}

	remainingParsed, err := strconv.ParseInt(remaining, 10, 32)
	if err != nil {
		return "", 0, 0, 0, 0, "", err
	}

	resetAtParsed, err := strconv.ParseFloat(resetAt, 64)
	if err != nil {
		return "", 0, 0, 0, 0, "", err
	}

	return bucket, remainingParsed, limitParsed, resetAfterParsed, resetAtParsed, scope, nil
}

func return404webhook(item *QueueItem) {
	res := *item.Res
	res.WriteHeader(404)
	body := "{\n  \"message\": \"Unknown Webhook\",\n  \"code\": 10015\n}"
	_, err := res.Write([]byte(body))
	if err != nil {
		item.errChan <- err
	} else {
		item.doneChan <- nil
	}
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

func isInteraction(url string) bool {
	parts := strings.Split(strings.SplitN(url, "?", 1)[0], "/")
	for _, p := range parts {
		if len(p) > 128 {
			return true
		}
	}
	return false
}

func (item *QueueItem) doRequest(ctx context.Context, q *RequestQueue, ch *QueueChannel, path string, pathHash uint64) {
	// This is fine to do as if ch.ratelimit is nil, then we have sole access to the RequestQueue resources, so there
	// is no need for a lock
	if ch.ratelimit != nil {
		defer ch.ratelimit.Release()
	}

	resp, err := q.processor(ctx, item)
	if err != nil {
		item.errChan <- err
		return
	}

	bucket, remaining, limit, resetAfter, resetAt, scope, err := parseHeaders(&resp.Header)

	if scope == "global" {
		// Lock global
		resetAfterDuration := time.Duration(resetAfter*1_000) * time.Millisecond
		sw := atomic.CompareAndSwapInt64(q.globalLockedUntil, 0, time.Now().Add(resetAfterDuration).UnixNano())
		if sw {
			logger.WithFields(logrus.Fields{
				"until":      time.Now().Add(resetAfterDuration),
				"resetAfter": resetAfterDuration,
			}).Warn("Global reached, locking")
		}
	}

	if err != nil {
		item.errChan <- err
		return
	}

	// TODO: Consider handling special retry case for POST /users/@me/channels
	item.doneChan <- resp

	if bucket != "" {
		if ch.ratelimit == nil {
			// We can safely do this as it is ensured that if ch.ratelimit is not set, we will always
			// make sequential requests and not concurrent ones. The first request that gets a ratelimit bucket
			// will set the ratelimit and it wont be set back to nil afterwards
			ch.ratelimit = NewBucketRatelimit(remaining, limit, resetAt, resetAfter, bucket, path, q.identifier)
		} else {
			ch.ratelimit.Update(bucket, remaining, limit, resetAt, resetAfter)
		}
	}

	if resp.StatusCode == 429 && scope != "shared" {
		logger.WithFields(logrus.Fields{
			"remaining":  remaining,
			"resetAfter": resetAfter,
			"bucket":     path,
			"route":      item.Req.URL.String(),
			"method":     item.Req.Method,
			"scope":      scope,
			"pathHash":   pathHash,
			// TODO: Remove this when 429s are not a problem anymore
			"discordBucket":  bucket,
			"ratelimitScope": scope,
		}).Warn("Unexpected 429")
	}

	if resp.StatusCode == 404 && strings.HasPrefix(path, "/webhooks/") && !isInteraction(item.Req.URL.String()) {
		logger.WithFields(logrus.Fields{
			"bucket": path,
			"route":  item.Req.URL.String(),
			"method": item.Req.Method,
		}).Info("Setting fail fast 404 for webhook")

		ch.Lock()
		ch.lockerFun = return404webhook
		ch.Unlock()
	}

	if resp.StatusCode == 401 && !isInteraction(item.Req.URL.String()) && q.queueType != NoAuth {
		// Permanently lock this queue
		logger.WithFields(logrus.Fields{
			"bucket":     path,
			"route":      item.Req.URL.String(),
			"method":     item.Req.Method,
			"identifier": q.identifier,
			"status":     resp.StatusCode,
		}).Error("Received 401 during normal operation, assuming token is invalidated, locking bucket permanently")

		if EnvGet("DISABLE_401_LOCK", "false") != "true" {
			atomic.StoreInt64(q.isTokenInvalid, 999)
		}
	}
}

func (q *RequestQueue) subscribe(ch *QueueChannel, path string, pathHash uint64) {
	// This function has 1 goroutine for each bucket path
	// Locking here is not needed

	for item := range ch.ch {
		ctx := context.WithValue(item.Req.Context(), "identifier", q.identifier)

		if atomic.LoadInt64(q.isTokenInvalid) > 0 {
			return401(item)
			continue
		}

		if globalUnlockedUntil := atomic.LoadInt64(q.globalLockedUntil); globalUnlockedUntil > 0 {
			if d := time.Until(time.Unix(0, globalUnlockedUntil)); d > 0 {
				time.Sleep(d)
			}
			_ = atomic.CompareAndSwapInt64(q.globalLockedUntil, globalUnlockedUntil, 0)
		}

		ch.Lock()
		if ch.lockerFun != nil {
			ch.lockerFun(item)
			continue
		}
		ch.Unlock()

		// This is unfortunate, but we need to read the body here so that the ctx gets closed properly
		// when the client disconnects, which is very useful for cancelling `ratelimit.Acquire` early
		// see: https://github.com/golang/go/issues/23262
		var err error
		item.ReqBody, err = io.ReadAll(item.Req.Body)
		if err != nil {
			_ = item.Req.Body.Close()
			item.errChan <- err
			continue
		}
		_ = item.Req.Body.Close()

		if ch.ratelimit != nil {
			if err = ch.ratelimit.Acquire(ctx); err != nil {
				item.errChan <- err
				continue
			}
		}

		// We don't have the initial headers, so we do the requests sequentially, which should
		// create and populate the bucket when it's known, of it thats what the user wants
		// If this is a route with no ratelimits, then we will simply execute them all sequentially,
		// which should be fine
		//
		// TODO: Consider if its worth hard coding which routes will never have a bucket
		if ch.ratelimit == nil || !allowConcurrentRequests {
			item.doRequest(ctx, q, ch, path, pathHash)
		} else {
			go item.doRequest(ctx, q, ch, path, pathHash)
		}
	}
}
