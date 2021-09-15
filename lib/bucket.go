package lib

import (
	"github.com/sirupsen/logrus"
	"time"
)

type IBucket interface {
	Take()
	Update(limit int64, remaining int64, reset time.Time)
}
// Bucket Sequential bucket for discord requests. NOT THREAD SAFE
type Bucket struct {
	limit int64
	remaining int64
	resetAt time.Time
}

// Take Consumes a token or waits until one is available
func (b *Bucket) Take() {
	// Noop Bucket
	if b.limit == 0 {
		return
	}

	if time.Now().After(b.resetAt) {
		b.unlock()
	}

	if b.remaining <= 0 {
		b.waitUntilReset()
	}
	b.remaining -= 1
	logger.WithFields(logrus.Fields{"remaining": b.remaining, "limit": b.limit, "resetAfter": b.resetAt.Sub(time.Now())}).Debug("Bucket take")
}

func (b *Bucket) Update(limit int64, remaining int64, reset time.Time) {
	b.limit = limit
	b.remaining = remaining
	b.resetAt = reset
}

func (b *Bucket) unlock() {
	b.remaining = b.limit
}

func (b *Bucket) waitUntilReset() {
	<- time.After(b.resetAt.Sub(time.Now()))
	b.unlock()
}

func NewBucket(limit int64, initialTokens int64, resetAfter time.Time) *Bucket {
	return &Bucket{
		limit:     limit,
		remaining: initialTokens,
		resetAt:   resetAfter,
	}
}