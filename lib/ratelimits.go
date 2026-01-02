package lib

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// isClose checks if two durations are within `diff` seconds of difference
func isClose(a, b float64, absTol float64) bool {
	return math.Abs(a-b) <= absTol
}

func calculateFixedWindow(resetAt, resetAfter float64) (time.Duration, time.Time) {
	increaseAt := time.Unix(0, int64(resetAt*1_000_000_000))
	period := time.Duration(resetAfter*1_000) * time.Millisecond

	return period, increaseAt
}

func calculateSlidingWindow(remaining, limit int64, resetAt, resetAfter float64) (time.Duration, time.Time) {
	// slidePeriod = resetAfter / (limit - remaining)
	slidePeriod := time.Duration((resetAfter/float64(limit-remaining))*1_000) * time.Millisecond

	// increaseAt = (resetAt - resetAfter) + slidePeriod
	resetAtTime := time.Unix(0, int64(resetAt*1_000_000_000))
	resetAfterDuration := time.Duration(resetAfter*1_000) * time.Millisecond
	increaseAt := resetAtTime.Add(-resetAfterDuration).Add(slidePeriod)

	return slidePeriod, increaseAt
}

// BucketRateLimit is a sliding window ratelimit implementation
type BucketRateLimit struct {
	identifier string
	path       string
	bucket     string
	lock       sync.Mutex
	remaining  int64
	limit      int64
	period     time.Duration
	increaseAt time.Time
	resetAt    float64
	resetAfter float64

	inTransitLock   sync.Mutex
	inTransit       int64
	transitWaitChan chan interface{}

	unknown            bool
	outOfSync          bool
	fixedWindow        bool
	firstSync          bool
	ratelimitAvoidance bool
}

func NewBucketRatelimit(path, identifier string) BucketRateLimit {
	return BucketRateLimit{
		path:       path,
		identifier: identifier,
		limit:      1,
		unknown:    true,
	}
}

// Warning: this MUST be called from a locked state
func (b *BucketRateLimit) isRatelimited(now time.Time) bool {
	if b.unknown {
		// Don't do any waiting logic as we don't have any information on the bucket,
		// just do the request immediately. The first successful request will return the bucket
		// data
		return false
	}

	if now.After(b.increaseAt) || now.Equal(b.increaseAt) {
		if b.fixedWindow || b.ratelimitAvoidance {
			// Fixed windows or ratelimit avoidance just reset the remaining back to the limit
			b.remaining = b.limit
			b.outOfSync = true
			b.increaseAt = now.Add(b.period)
			b.ratelimitAvoidance = false

		} else {
			// We can slide the window along
			gain := int64(math.Floor((now.Sub(b.increaseAt).Seconds())/b.period.Seconds())) + 1
			nowRemaining := b.remaining + gain

			b.remaining = min(nowRemaining, b.limit)

			if b.remaining == b.limit {
				// When a ratelimit resets, we will fall out of sync from the remote, so
				// we want to prevent future sliding
				b.increaseAt = now.Add(b.period)
				b.outOfSync = true
			} else {
				b.increaseAt = b.increaseAt.Add(b.period * time.Duration(gain))
			}
		}
	}

	return b.remaining <= 0
}

// Acquire will request a slot from the ratelimit and sleep until there is one available
// NOTE: This function does not support concurrent calls!
func (b *BucketRateLimit) Acquire(ctx context.Context) error {
	b.inTransitLock.Lock()
	if b.inTransit >= b.limit {
		// Buffer of 1 here to prevent deadlocks in a worst case scenario
		b.transitWaitChan = make(chan interface{}, 1)
		b.inTransitLock.Unlock()
		select {
		case <-ctx.Done():
			b.inTransitLock.Lock()
			if b.transitWaitChan != nil {
				b.transitWaitChan = nil
			} else {
				// Return the slot that was given to us
				b.inTransit--
			}
			b.inTransitLock.Unlock()
			return ctx.Err()
		case <-b.transitWaitChan:
		}
		// We dont update inTransit because the
		// slot was given to us by the goroutine
		// that sent the message through transitWaitChan
	} else {
		b.inTransit++
		b.inTransitLock.Unlock()
	}

	for {
		b.lock.Lock()
		now := time.Now()
		if !b.isRatelimited(now) {
			// b.lock will be unlocked after decrementing remaining
			break
		}
		sleepDuration := b.increaseAt.Sub(now)
		b.lock.Unlock()

		if sleepDuration > 0 {
			logger.WithFields(logrus.Fields{
				"bucket":        b.bucket,
				"path":          b.path,
				"identifier":    b.identifier,
				"sleepDuration": sleepDuration,
			}).Debug("backing off to avoid hitting ratelimits")

			select {
			case <-ctx.Done():
				b.Release()
				return ctx.Err()
			case <-time.After(sleepDuration):
			}
		}
	}

	b.remaining--
	b.lock.Unlock()
	return nil
}

func (b *BucketRateLimit) Release() {
	b.inTransitLock.Lock()
	defer b.inTransitLock.Unlock()

	if b.transitWaitChan != nil && b.inTransit <= b.limit {
		// We dont update inTransit here as we are giving
		// our slot to the one that is waiting
		b.transitWaitChan <- nil
		b.transitWaitChan = nil
		return
	}

	if b.inTransit > 0 {
		b.inTransit--
	}
}

func (b *BucketRateLimit) init(bucket string, remaining, limit int64, resetAt, resetAfter float64) {
	b.bucket = bucket
	b.resetAt = resetAt
	b.resetAfter = resetAfter
	b.remaining = remaining
	b.limit = limit
	b.unknown = false
	b.outOfSync = false
	b.firstSync = true
	b.ratelimitAvoidance = false

	if limit != 1 && remaining == limit-1 {
		// We have the perfect condition for a sliding window, so assume that for now.
		// Turning it into a fixed bucket later is preferable, as we might never get this chance again
		period, increaseAt := calculateSlidingWindow(remaining, limit, resetAt, resetAfter)
		b.fixedWindow = false
		b.period = period
		b.increaseAt = increaseAt
	} else {
		// We can assume its a fixed bucket for now, and hope that in the future we will get
		// the ideal condition
		period, increaseAt := calculateFixedWindow(resetAt, resetAfter)
		b.fixedWindow = true
		b.period = period
		b.increaseAt = increaseAt
	}
}

func (b *BucketRateLimit) Update(bucket string, remaining, limit int64, resetAt, resetAfter float64, ratelimitHit bool) {
	b.lock.Lock()
	defer b.lock.Unlock()

	logger.WithFields(logrus.Fields{
		"bucket":     b.bucket,
		"path":       b.path,
		"identifier": b.identifier,
		"remaining":  remaining,
		"limit":      limit,
		"resetAt":    resetAt,
		"resetAfter": resetAfter,
		"period":     b.period,
	}).Debug("updating bucket ratelimit")

	if b.unknown {
		b.init(bucket, remaining, limit, resetAt, resetAfter)
		return
	}

	if resetAt-resetAfter < b.resetAt-b.resetAfter {
		// Old ratelimit information, ignore
		return
	}

	if b.bucket != bucket {
		logger.WithFields(logrus.Fields{
			"oldBucket":     b.bucket,
			"newBucket":     bucket,
			"path":          b.path,
			"identifier":    b.identifier,
			"oldLimit":      b.limit,
			"oldResetAt":    b.resetAt,
			"oldResetAfter": b.resetAfter,
			"newLimit":      limit,
			"newResetAt":    resetAt,
			"newResetAfter": resetAfter,
		}).Warn("Bucket for route changed. There might be a slight increase in 429s")

		b.init(bucket, remaining, limit, resetAt, resetAfter)
		return
	}

	if ratelimitHit {
		// During ratelimit avoidance, we will treat the bucket as fixed
		// bucket and wait for it to fill up completely
		b.ratelimitAvoidance = true
		b.increaseAt = time.Unix(0, int64(resetAt*1_000_000_000))
		b.remaining = 0
		return
	}

	if b.firstSync && remaining > 0 && remaining != limit-1 {
		resetAtEq := isClose(b.resetAt, resetAt, 0.05)
		b.firstSync = false

		if !b.fixedWindow && resetAtEq {
			logger.WithFields(logrus.Fields{
				"bucket":          b.bucket,
				"path":            b.path,
				"identifier":      b.identifier,
				"storedResetAt":   b.resetAt,
				"receivedResetAt": resetAt,
			}).Info("Bucket detected to be a fixed bucket")
			b.fixedWindow = true
			// Setting this here will have an effect below
			b.outOfSync = true

		} else if b.fixedWindow && !resetAtEq {
			logger.WithFields(logrus.Fields{
				"bucket":          b.bucket,
				"path":            b.path,
				"identifier":      b.identifier,
				"storedResetAt":   b.resetAt,
				"receivedResetAt": resetAt,
			}).Debug("Bucket detected to be a sliding bucket")
			b.fixedWindow = false
			// Setting this here will have an effect below
			b.outOfSync = true
		}
	}

	b.resetAt = resetAt
	b.resetAfter = resetAfter

	if !b.outOfSync {
		return
	}

	if b.fixedWindow {
		period, increaseAt := calculateFixedWindow(resetAt, resetAfter)
		b.period = period
		b.increaseAt = increaseAt
	} else {
		period, increaseAt := calculateSlidingWindow(remaining, limit, resetAt, resetAfter)
		b.period = period
		b.increaseAt = increaseAt
	}

	b.outOfSync = false
}
