package lib

import (
	"sync"
	"time"
)

type IBucket interface {
	Take()
	SetResetAt(t time.Time)
}

type Bucket struct {
	takeMu sync.Mutex
	resetMu sync.Mutex
	limit int64
	remaining int64
	resetAt time.Time
	unlockCh chan bool
	locked bool
}

func (b *Bucket) Take() {
	b.takeMu.Lock()
	defer b.takeMu.Unlock()

	if b.remaining == 0 {
		// The bucket will stay locked until it resets
		// No new Takes will be allowed (because of the mutex)
		b.locked = true
		b.waitUntilReset()
	}

	b.remaining -= 1
}

func (b *Bucket) SetResetAt(t time.Time) {
	b.resetMu.Lock()
	defer b.resetMu.Unlock()

	if b.resetAt.After(t) || b.resetAt.Equal(t) {
		return
	}

	b.resetAt = t
	b.unlock()
}

func (b *Bucket) unlock() {
	b.remaining = b.limit
	if b.locked {
		b.unlockCh <- true
		b.locked = false
	}
}

func (b *Bucket) waitUntilReset() {
	<- b.unlockCh
}

func NewBucket(limit int64, resetAfter time.Time) *Bucket {
	return &Bucket{
		limit:     limit,
		remaining: limit,
		resetAt:   resetAfter,
		unlockCh:  make(chan bool),
		locked: false,
	}
}

type NoopBucket struct {}
func (b *NoopBucket) Take() {}
func (b *NoopBucket) SetResetAt(t time.Time) {}

func NewNoopBucket() *NoopBucket {
	return &NoopBucket{}
}