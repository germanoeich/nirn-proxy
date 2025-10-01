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
	slidePeriod := time.Duration(math.Ceil((resetAfter/float64(limit-remaining))*1_000)) * time.Millisecond

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

	inTransitLock   sync.Mutex
	inTransit       int64
	transitWaitChan chan interface{}

	unknown     bool
	outOfSync   bool
	fixedWindow bool
}

func NewBucketRatelimit(path, identifier string) BucketRateLimit {
	return BucketRateLimit{
		path:       path,
		identifier: identifier,
		limit:      1,
		unknown:    true,
	}
}

// Note: this MUST be called from a locked state
func (b *BucketRateLimit) isRatelimited(now time.Time) bool {
	if b.unknown {
		// Don't do any waiting logic as we don't have any information on the bucket,
		// just do the request immediately
		return false
	}

	// If we are out of sync, we shouldn't slide the window along, as we will be off due to
	// network latency.
	// The second part of this 'if' is to account for some cases where there can be a race
	// condition and we receive rate limit updates out of order, and we cannot update `outOfSync`
	if now.After(b.increaseAt) || now.Equal(b.increaseAt) && (!b.outOfSync || now.Sub(b.increaseAt) > b.period) {
		if b.fixedWindow {
			// Fixed windows just reset the remaining back to the limit
			b.remaining = b.limit
			b.outOfSync = true
			b.increaseAt = now.Add(b.period)

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

func (b *BucketRateLimit) Update(bucket string, remaining, limit int64, resetAt, resetAfter float64) {
	b.lock.Lock()
	defer b.lock.Unlock()

	if b.unknown {
		period, increaseAt := calculateSlidingWindow(remaining, limit, resetAt, resetAfter)

		b.bucket = bucket
		b.period = period
		b.resetAt = resetAt
		b.increaseAt = increaseAt
		b.remaining = remaining
		b.limit = limit
		b.outOfSync = false
		b.fixedWindow = false
		b.unknown = false
		return
	}

	if resetAt < b.resetAt {
		// Old ratelimit information, ignore
		return
	}

	b.bucket = bucket

	if !b.outOfSync {
		resetAtEq := isClose(b.resetAt, resetAt, 0.05)

		if !b.fixedWindow && resetAtEq {
			logger.WithFields(logrus.Fields{
				"bucket":          b.bucket,
				"path":            b.path,
				"identifier":      b.identifier,
				"storedResetAt":   b.resetAt,
				"receivedResetAt": resetAt,
			}).Debug("Bucket detected to be a fixed bucket bucket")
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
			}).Debug("Bucket stopped being a fixed bucket")
			b.fixedWindow = false
			// Setting this here will have an effect below
			b.outOfSync = true
		}
	}

	b.resetAt = resetAt

	if b.limit != limit {
		if b.limit > limit {
			logger.WithFields(logrus.Fields{
				"bucket":     b.bucket,
				"path":       b.path,
				"identifier": b.identifier,
				"newLimit":   limit,
				"oldLimit":   b.limit,
			}).Warn("Bucket decreased its limit. It is possible you will see a small increase in 429s")
		}

		b.limit = limit
		b.remaining = min(b.remaining, b.limit)
	}

	if b.fixedWindow {
		// We want to update the period only, and only if:
		//   1. The bucket is out of sync (ie, we reset the full window)
		//   2. We receive the first usage of the bucket, which will always have correct period
		if b.outOfSync || remaining == limit-1 {
			period, increaseAt := calculateFixedWindow(b.resetAt, resetAfter)
			b.period = period
			b.increaseAt = increaseAt

			b.outOfSync = false
		}
		return
	}

	// We want to update the slide period only, and only if:
	//   1. The bucket is out of sync (ie, we reset the full window)
	//   2. We receive the first usage of the bucket, which will always have the most accurate slide period
	//   3. The slide period increased
	//   4. The slide period greatly changed
	//      Note: 0.3 and 0.5 are chosen arbitrarily after some testing
	slidePeriod, increaseAt := calculateSlidingWindow(remaining, limit, resetAt, resetAfter)
	if b.outOfSync || remaining == limit-1 || slidePeriod > b.period || !isClose(slidePeriod.Seconds(), b.period.Seconds(), 0.3) {
		if !isClose(slidePeriod.Seconds(), b.period.Seconds(), 0.5) {
			logger.WithFields(logrus.Fields{
				"bucket":         b.bucket,
				"path":           b.path,
				"identifier":     b.identifier,
				"newSlidePeriod": slidePeriod,
				"oldSlidePeriod": b.period,
			}).Warn("Bucket greatly changed its slide period. It is possible you will see a small increase in 429s")
		}

		b.outOfSync = false
		b.period = slidePeriod
		b.increaseAt = increaseAt
	}
}
