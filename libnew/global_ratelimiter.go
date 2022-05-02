package libnew

import (
	"context"
	"errors"
	"github.com/Clever/leakybucket"
	"github.com/Clever/leakybucket/memory"
	"github.com/germanoeich/nirn-proxy/libnew/logging"
	"github.com/sirupsen/logrus"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type GlobalRateLimiter struct {
	sync.RWMutex
	globalBucketsMap map[uint64]*leakybucket.Bucket
	memStorage *memory.Storage
	logger *logrus.Entry
	client *http.Client
}

func NewGlobalRateLimiter() *GlobalRateLimiter {
	memStorage := memory.New()
	return &GlobalRateLimiter{
		memStorage: memStorage,
		globalBucketsMap: make(map[uint64]*leakybucket.Bucket),
		logger: logging.GetLogger("globalratelimiter"),
		client: http.DefaultClient,
	}
}

func (c *GlobalRateLimiter) Take(botHash uint64, botLimit uint) {
	bucket := *c.getOrCreate(botHash, botLimit)
takeGlobal:
	_, err := bucket.Add(1)
	if err != nil {
		reset := bucket.Reset()
		c.logger.WithFields(logrus.Fields{
			"waitTime": time.Until(reset),
		}).Trace("Failed to grab global token, sleeping for a bit")
		time.Sleep(time.Until(reset))
		goto takeGlobal
	}
}

func (c *GlobalRateLimiter) getOrCreate(botHash uint64, botLimit uint) *leakybucket.Bucket {
	c.RLock()
	b, ok := c.globalBucketsMap[botHash]
	c.RUnlock()
	if !ok {
		c.Lock()
		// Check if it wasn't created while we didnt hold the exclusive lock
		b, ok = c.globalBucketsMap[botHash]
		if ok {
			c.Unlock()
			return b
		}

		globalBucket, _ := c.memStorage.Create(strconv.FormatUint(botHash, 10), botLimit, 1 * time.Second)
		c.globalBucketsMap[botHash] = &globalBucket
		c.Unlock()
		return &globalBucket
	} else {
		return b
	}
}

func (c *GlobalRateLimiter) FireGlobalRequest(ctx context.Context, addr string, botHash uint64, botLimit uint) error {
	globalReq, err := http.NewRequestWithContext(ctx, "GET", "http://" + addr + "/nirn/global", nil)
	if err != nil {
		return err
	}

	globalReq.Header.Set("bot-hash", strconv.FormatUint(botHash, 10))
	globalReq.Header.Set("bot-limit", strconv.FormatUint(uint64(botLimit), 10))

	// The node handling the request will only return if we grabbed a token or an error was thrown
	resp, err := c.client.Do(globalReq)
	c.logger.Trace("Got go-ahead for global")

	if err != nil {
		return err
	}

	if resp.StatusCode != 200 {
		return errors.New("global request failed with status " + resp.Status)
	}

	return nil
}

func (c *GlobalRateLimiter) HandleGlobalRequest(w http.ResponseWriter, r *http.Request) {
	botHashStr := r.Header.Get("bot-hash")
	botLimitStr := r.Header.Get("bot-limit")

	botHash, err := strconv.ParseUint(botHashStr, 10, 64)
	if err != nil {
		w.WriteHeader(400)
		return
	}

	botLimit, err := strconv.ParseUint(botLimitStr, 10, 64)
	if err != nil {
		w.WriteHeader(400)
		return
	}

	c.Take(botHash, uint(botLimit))
	c.logger.Trace("Returned OK for global request")
}