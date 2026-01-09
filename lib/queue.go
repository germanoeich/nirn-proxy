package lib

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Clever/leakybucket"
	"github.com/Clever/leakybucket/memory"
	"github.com/sirupsen/logrus"
)

// A pool of bucketsContextManager
var bucketsContextManagerPool = sync.Pool{
	New: func() interface{} {
		return &bucketsContextManager{
			buckets: make([]*Bucket, 0, 1),
		}
	},
}

type bucketsContextManager struct {
	buckets []*Bucket
}

func (b *bucketsContextManager) Acquire(ctx context.Context) error {
	// We count till what position we reach instead of using a slice to prevent allocations
	var acquiredBucketsCount int
	var err error

	for _, bucket := range b.buckets {
		err = bucket.Acquire(ctx)
		if err != nil {
			break
		}

		acquiredBucketsCount++
	}

	if err != nil {
		// Make sure we release all the buckets we have already acquired before the error
		for idx, bucket := range b.buckets {
			if idx >= acquiredBucketsCount {
				break
			}

			bucket.Release()
		}
	}

	return err
}

func (b *bucketsContextManager) Release() {
	for _, bucket := range b.buckets {
		bucket.Release()
	}
}

type ItemProcessFunction func(ctx context.Context, item *QueueItem) (*http.Response, error)

type QueueItem struct {
	Req      *http.Request
	Res      *http.ResponseWriter
	doneChan chan *http.Response
	errChan  chan error
	ReqBody  []byte
}

type QueueChannel struct {
	lastUsed  time.Time
	ch        chan *QueueItem
	lockerFun func(item *QueueItem)
	buckets   []string
	sync.Mutex
}

type RequestQueue struct {
	globalBucket      leakybucket.Bucket
	globalLockedUntil *int64
	// bucket path hash as key
	queues         map[uint64]*QueueChannel
	buckets        map[string]*Bucket
	processor      ItemProcessFunction
	user           *BotUserResponse
	isTokenInvalid *int64
	identifier     string
	// bufferSize Defines the size of the request channel buffer for each bucket
	bufferSize int
	botLimit   uint
	queueType  QueueType
	sync.Mutex
}

func NewRequestQueue(processor ItemProcessFunction, token string, bufferSize int) (*RequestQueue, error) {
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
				buckets:           make(map[string]*Bucket),
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
		buckets:           make(map[string]*Bucket),
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

func (q *RequestQueue) sweepQueues() {
	q.Lock()
	defer q.Unlock()
	logger.Info("Queues Sweep start")
	sweptEntries := 0
	for key, val := range q.queues {
		if time.Since(val.lastUsed) > 10*time.Minute {
			close(val.ch)
			delete(q.queues, key)
			sweptEntries++
		}
	}
	logger.WithFields(logrus.Fields{"sweptEntries": sweptEntries}).Info("Finished queues sweep")
}

func (q *RequestQueue) sweepBuckets() {
	q.Lock()
	defer q.Unlock()
	logger.Debug("Buckets sweep start")
	sweptEntries := 0
	for key, val := range q.buckets {
		// This is technically a data race, but we are looking for buckets that are insanely
		// unused, so we can afford the data race
		if val.inTransit == 0 && time.Since(val.increaseAt) > 3*val.period {
			delete(q.buckets, key)
			sweptEntries++
		}
	}
	logger.WithFields(logrus.Fields{"sweptEntries": sweptEntries}).Debug("Finished buckets sweep")
}

func (q *RequestQueue) tickSweep() {
	t := time.NewTicker(5 * time.Minute)
	t2 := time.NewTicker(30 * time.Second)

	for {
		select {
		case <-t.C:
			q.sweepQueues()
		case <-t2.C:
			q.sweepBuckets()
		}
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

	safeSend(ch, &QueueItem{Req: req, Res: res, errChan: errChan, doneChan: doneChan})

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
			ch:       make(chan *QueueItem, q.bufferSize),
			buckets:  make([]string, 0, 1),
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

func (q *RequestQueue) getBucketsContextManager(ch *QueueChannel) *bucketsContextManager {
	q.Lock()
	defer q.Unlock()
	ch.Lock()
	defer ch.Unlock()

	if len(ch.buckets) == 0 {
		return nil
	}

	contextManager := bucketsContextManagerPool.Get().(*bucketsContextManager)
	contextManager.buckets = contextManager.buckets[:0]

	for idx := 0; idx < len(ch.buckets); {
		bucket, ok := q.buckets[ch.buckets[idx]]
		if ok {
			contextManager.buckets = append(contextManager.buckets, bucket)
			idx++
			continue
		}

		// The bucket no longer exists, so remove it from the channel slice
		ch.buckets = append(ch.buckets[:idx], ch.buckets[idx+1:]...)
	}

	if len(contextManager.buckets) == 0 {
		bucketsContextManagerPool.Put(contextManager)
		return nil
	}

	return contextManager
}

func (q *RequestQueue) doRequest(ctx context.Context, item *QueueItem, ch *QueueChannel, buckets *bucketsContextManager, path, pathHash string) {
	if buckets != nil {
		defer func() {
			buckets.Release()
			bucketsContextManagerPool.Put(buckets)
		}()
	}

	resp, err := q.processor(ctx, item)
	if err != nil {
		item.errChan <- err
		return
	}

	bucketHash, remaining, limit, resetAfter, resetAt, scope, err := parseHeaders(&resp.Header)
	if err != nil {
		item.errChan <- err
		return
	}

	item.doneChan <- resp

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
		return
	}

	// TODO: Consider handling special retry case for POST /users/@me/channels

	ratelimitHit := resp.StatusCode == 429

	if bucketHash != "" || ratelimitHit {
		if bucketHash == "" {
			// We might have hit a Cloudflare 429, so we create a special bucket for that
			bucketHash = "route"
		}

		// Bucket hashes are per path hash
		bucketHash += ":" + pathHash

		q.Lock()
		bucket, ok := q.buckets[bucketHash]
		if !ok {
			logger.WithFields(logrus.Fields{
				"bucket":     bucketHash,
				"remaining":  remaining,
				"limit":      limit,
				"resetAt":    resetAt,
				"resetAfter": resetAfter,
				"identifier": q.identifier,
				"route":      item.Req.URL.String(),
				"method":     item.Req.Method,
			}).Debug("creating new bucket")

			q.buckets[bucketHash] = NewBucket(bucketHash, remaining, limit, resetAt, resetAfter)
		} else {
			logger.WithFields(logrus.Fields{
				"bucket":     bucketHash,
				"remaining":  remaining,
				"limit":      limit,
				"resetAt":    resetAt,
				"resetAfter": resetAfter,
				"identifier": q.identifier,
				"route":      item.Req.URL.String(),
				"method":     item.Req.Method,
			}).Debug("updating existing bucket")

			bucket.Update(remaining, limit, resetAt, resetAfter, ratelimitHit)
		}
		q.Unlock()

		ch.Lock()
		if !slices.Contains(ch.buckets, bucketHash) {
			logger.WithFields(logrus.Fields{
				"bucket":     bucketHash,
				"identifier": q.identifier,
				"route":      item.Req.URL.String(),
				"method":     item.Req.Method,
			}).Debug("linking new bucket to route")

			ch.buckets = append(ch.buckets, bucketHash)
		}
		ch.Unlock()

	}

	if ratelimitHit && scope != "shared" {
		logger.WithFields(logrus.Fields{
			"remaining":  remaining,
			"resetAfter": resetAfter,
			"bucket":     path,
			"identifier": q.identifier,
			"route":      item.Req.URL.String(),
			"method":     item.Req.Method,
			"pathHash":   pathHash,
			// TODO: Remove this when 429s are not a problem anymore
			"discordBucket":  bucketHash,
			"ratelimitScope": scope,
		}).Warn("Unexpected 429")
		return
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
		return
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
		return
	}
}

func (q *RequestQueue) subscribe(ch *QueueChannel, path string, pathHashInt uint64) {
	// This function has 1 goroutine for each bucket path
	// Locking here is not needed

	pathHash := strconv.FormatUint(pathHashInt, 10)

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
			ch.Unlock()
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

		buckets := q.getBucketsContextManager(ch)

		if buckets != nil {
			if err = buckets.Acquire(ctx); err != nil {
				bucketsContextManagerPool.Put(buckets)
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
		if buckets == nil || !allowConcurrentRequests {
			q.doRequest(ctx, item, ch, buckets, path, pathHash)
		} else {
			go q.doRequest(ctx, item, ch, buckets, path, pathHash)
		}
	}
}
