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

func calculateSlidingWindow(remaining, limit int64, resetAfter float64) (time.Duration, time.Time) {
	// slidePeriod = resetAfter / (limit - remaining)
	slidePeriod := time.Duration(math.Ceil((resetAfter/float64(limit-remaining))*1_000)) * time.Millisecond
	increaseAt := time.Now().Add(slidePeriod)

	return slidePeriod, increaseAt
}

func isFirstValidHeaders(remaining, limit int64) bool {
	return remaining == limit-1 && limit != 1
}

// Bucket is a Discord bucket ratelimiter
type Bucket struct {
	increaseAt      time.Time
	transitWaitChan chan interface{}

	// under stateLock
	bucket        string
	remaining     int64
	limit         int64
	period        time.Duration
	resetAt       time.Time
	lastUpdatedAt time.Time
	// under inTransitLock
	inTransit int64

	stateLock     sync.Mutex
	inTransitLock sync.Mutex
	acquireLock   sync.Mutex

	outOfSync         bool
	fixedWindow       bool
	typeChangeAllowed bool
	closedChan        chan struct{}
}

func NewBucket(bucket string, remaining, limit int64, resetAt, resetAfter float64) *Bucket {
	b := &Bucket{
		bucket:            bucket,
		remaining:         remaining,
		limit:             limit,
		outOfSync:         false,
		closedChan:        make(chan struct{}, 1),
		typeChangeAllowed: true,
		lastUpdatedAt:     time.Now(),
	}

	if isFirstValidHeaders(remaining, limit) {
		// We have the perfect condition for a sliding window, so assume that for now.
		// Turning it into a fixed bucket later is preferable, as we might never get this chance again
		b.period, b.increaseAt = calculateSlidingWindow(remaining, limit, resetAfter)
		b.fixedWindow = false
		b.resetAt = time.Now().Add(b.period * time.Duration(b.limit))
	} else {
		// We can assume its a fixed bucket for now, and hope that in the future we will get
		// the ideal condition
		b.period, b.increaseAt = calculateFixedWindow(resetAt, resetAfter)
		b.fixedWindow = true
		b.resetAt = time.Unix(0, int64(resetAt*1_000_000_000))

		if limit == 1 {
			// Bucket is 100% a fixed bucket
			b.typeChangeAllowed = false
		}
	}

	return b
}

// Warning: this MUST be called from a locked state
// Warning: `now` must be the current time. Passing a present or past value is undefined behaviour
func (b *Bucket) isRatelimited(now time.Time) bool {
	canIncrease := now.After(b.increaseAt)
	canReset := now.After(b.resetAt)

	// inTransit check is performed to avoid deadlocks
	if (canIncrease && !b.outOfSync && b.inTransit != 1) || canReset {
		if b.fixedWindow {
			// Fixed windows just reset the remaining back to the limit
			b.remaining = b.limit
			b.increaseAt = now.Add(b.period)
			b.resetAt = b.increaseAt
			b.outOfSync = true
		} else if canReset {
			// Sliding bucket being fully reset
			b.remaining = b.limit
			b.increaseAt = now.Add(b.period)
			b.resetAt = now.Add(b.period * time.Duration(b.limit))
			b.outOfSync = true
		} else {
			// Slide window along
			gain := int64(math.Floor((now.Sub(b.increaseAt).Seconds())/b.period.Seconds())) + 1

			b.remaining = min(b.remaining+gain, b.limit)

			if b.remaining == b.limit {
				// When a ratelimit resets, we will fall out of sync from the remote, so
				// we want to prevent future sliding
				b.increaseAt = now.Add(b.period)
				b.resetAt = now.Add(b.period * time.Duration(b.limit))
				b.outOfSync = true
			} else {
				b.increaseAt = b.increaseAt.Add(b.period * time.Duration(gain))
				b.resetAt = b.resetAt.Add(b.period * time.Duration(gain))
			}
		}
	}

	return b.remaining <= 0
}

// Acquire will request a slot from the ratelimit or sleep until there is one available
func (b *Bucket) Acquire(ctx context.Context) error {
	b.acquireLock.Lock()
	defer b.acquireLock.Unlock()
	if b.closedChan == nil {
		return nil
	}

	b.inTransitLock.Lock()
	if b.inTransit >= b.limit {
		// Buffer of 1 here to prevent deadlocks in a worst case scenario
		b.transitWaitChan = make(chan interface{}, 1)
		b.inTransitLock.Unlock()
		select {
		case <-b.closedChan:
			break
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
		b.stateLock.Lock()
		now := time.Now()
		if !b.isRatelimited(now) {
			// b.lock will be unlocked after decrementing remaining
			break
		}
		sleepDuration := b.increaseAt.Sub(now)
		b.stateLock.Unlock()

		if sleepDuration > 0 {
			logger.WithFields(logrus.Fields{
				"bucket":        b.bucket,
				"sleepDuration": sleepDuration,
			}).Debug("backing off to avoid hitting ratelimits")
		} else {
			sleepDuration = time.Duration(0)
		}

		select {
		case <-b.closedChan:
			break
		case <-ctx.Done():
			b.Release()
			return ctx.Err()
		case <-time.After(sleepDuration):
		}
	}

	b.remaining--
	b.stateLock.Unlock()
	return nil
}

// Release returns the slot to the bucket
func (b *Bucket) Release() {
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

// Update updates the bucket with new ratelimit information
func (b *Bucket) Update(remaining, limit int64, resetAt, resetAfter float64, ratelimitHit bool) {
	b.stateLock.Lock()
	defer b.stateLock.Unlock()

	b.lastUpdatedAt = time.Now()
	resetAtTime := time.Unix(0, int64(resetAt*1_000_000_000))

	if ratelimitHit {
		// During ratelimit avoidance, we will treat the bucket as fixed
		// bucket and wait for it to fill up completely
		b.increaseAt = resetAtTime
		b.resetAt = resetAtTime
		b.remaining = 0
		b.outOfSync = false
		return
	}

	firstValidHeaders := isFirstValidHeaders(remaining, limit)

	if b.typeChangeAllowed && !b.outOfSync && !firstValidHeaders && remaining > 0 {
		b.typeChangeAllowed = false
		resetAtEq := isClose(float64(b.resetAt.UnixMilli())/1_000, resetAt, 0.05)

		if resetAtEq {
			logger.WithFields(logrus.Fields{
				"bucket":          b.bucket,
				"storedResetAt":   b.resetAt,
				"receivedResetAt": resetAtTime,
			}).Debug("bucket detected to be a fixed bucket")

			if !b.fixedWindow {
				b.fixedWindow = true
				// Setting this here will have an effect below
				b.outOfSync = true
			}

		} else {
			logger.WithFields(logrus.Fields{
				"bucket":          b.bucket,
				"storedResetAt":   b.resetAt,
				"receivedResetAt": resetAtTime,
			}).Debug("bucket detected to be a sliding bucket")

			if b.fixedWindow {
				b.fixedWindow = false
				// Setting this here will have an effect below
				b.outOfSync = true
			}
		}
	}

	b.resetAt = resetAtTime

	if b.outOfSync || firstValidHeaders {
		var period time.Duration
		var increaseAt time.Time

		if b.fixedWindow {
			period, increaseAt = calculateFixedWindow(resetAt, resetAfter)
		} else {
			period, increaseAt = calculateSlidingWindow(remaining, limit, resetAfter)
		}

		b.outOfSync = false

		// Prevent both from decreasing
		if b.increaseAt.Before(increaseAt) {
			b.increaseAt = increaseAt
		}
		b.period = period
	}
}

// Close marks the bucket as closed and immediately returns from any and future Acquire calls.
// The bucket should not be used from this point onwards
func (b *Bucket) Close() {
	if b.closedChan == nil {
		return
	}

	logger.WithFields(logrus.Fields{
		"bucket": b.bucket,
	}).Debug("bucket closed")

	closedChan := b.closedChan
	b.closedChan = nil
	closedChan <- struct{}{}
}
