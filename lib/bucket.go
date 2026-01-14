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

// Bucket is a Discord bucket ratelimiter
type Bucket struct {
	increaseAt      time.Time
	transitWaitChan chan interface{}

	// under stateLock
	bucket          string
	remaining       int64
	limit           int64
	period          time.Duration
	resetAt         time.Time
	serverUpdatedAt time.Time
	// under inTransitLock
	inTransit int64

	stateLock     sync.Mutex
	inTransitLock sync.Mutex
	acquireLock   sync.Mutex

	outOfSync   bool
	fixedWindow bool
	firstSeen   bool
}

func NewBucket(bucket string, remaining, limit int64, resetAt, resetAfter float64) *Bucket {
	var period time.Duration
	var increaseAt time.Time
	var fixedWindow bool

	if limit != 1 && remaining == limit-1 {
		// We have the perfect condition for a sliding window, so assume that for now.
		// Turning it into a fixed bucket later is preferable, as we might never get this chance again
		period, increaseAt = calculateSlidingWindow(remaining, limit, resetAfter)
		fixedWindow = false
	} else {
		// We can assume its a fixed bucket for now, and hope that in the future we will get
		// the ideal condition
		period, increaseAt = calculateFixedWindow(resetAt, resetAfter)
		fixedWindow = true
	}

	return &Bucket{
		bucket:      bucket,
		remaining:   remaining,
		limit:       limit,
		resetAt:     time.Unix(0, int64(resetAt*1_000_000_000)),
		period:      period,
		increaseAt:  increaseAt,
		fixedWindow: fixedWindow,
		firstSeen:   true,
	}
}

// Warning: this MUST be called from a locked state
func (b *Bucket) isRatelimited(now time.Time) bool {
	canIncrease := now.After(b.increaseAt)
	canReset := now.After(b.resetAt)

	if (canIncrease && !b.outOfSync) || canReset {
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
			gain := int64(math.Ceil((now.Sub(b.increaseAt).Seconds()) / b.period.Seconds()))

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

func (b *Bucket) Update(remaining, limit int64, resetAt, resetAfter float64, ratelimitHit bool) {
	b.stateLock.Lock()
	defer b.stateLock.Unlock()

	resetAtTime := time.Unix(0, int64(resetAt*1_000_000_000))
	resetAfterDuration := time.Duration(resetAfter*1_000) * time.Millisecond
	serverUpdatedAt := resetAtTime.Add(-resetAfterDuration)

	if b.serverUpdatedAt.Before(serverUpdatedAt) {
		b.serverUpdatedAt = serverUpdatedAt
	}

	if ratelimitHit {
		// During ratelimit avoidance, we will treat the bucket as fixed
		// bucket and wait for it to fill up completely
		b.increaseAt = resetAtTime
		b.resetAt = resetAtTime
		b.remaining = 0
		b.outOfSync = false
		return
	}

	if b.firstSeen && !b.outOfSync && remaining > 0 && remaining != limit-1 {
		b.firstSeen = false
		resetAtEq := isClose(float64(b.resetAt.UnixMilli())/1_000, resetAt, 0.05)

		if resetAtEq {
			logger.WithFields(logrus.Fields{
				"bucket":          b.bucket,
				"storedResetAt":   b.resetAt,
				"receivedResetAt": resetAt,
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
				"receivedResetAt": resetAt,
			}).Debug("bucket detected to be a sliding bucket")

			if b.fixedWindow {
				b.fixedWindow = false
				// Setting this here will have an effect below
				b.outOfSync = true
			}
		}
	}

	if b.resetAt.Before(resetAtTime) {
		b.resetAt = resetAtTime
	}

	if b.outOfSync || (limit != 1 && remaining == limit-1) {
		if b.fixedWindow {
			period, increaseAt := calculateFixedWindow(resetAt, resetAfter)
			b.period = period
			b.increaseAt = increaseAt
		} else {
			period, increaseAt := calculateSlidingWindow(remaining, limit, resetAfter)
			b.period = period
			b.increaseAt = increaseAt
		}

		b.outOfSync = false
	}
}
