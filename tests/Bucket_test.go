package tests

import (
	"github.com/germanoeich/nirn-proxy/lib"
	"testing"
	"time"
)

func TestNoopBucketAllowsAll(t *testing.T) {
	b := lib.NewBucket(0, 0, time.Now())
	done := make(chan bool)
	timeout := time.After(100 * time.Millisecond)
	go func() {
		for i := 0; i < 1000; i++ {
			b.Take()
		}
		done <- true
	}()

	select {
		case <- done:
		case <- timeout:
			t.Error("Timed out")
	}
}

func TestBucketAllowsEnoughTakes(t *testing.T) {
	b := lib.NewBucket(5, 5, time.Now().Add(3 * time.Second))
	done := make(chan bool)
	timeout := time.After(100 * time.Millisecond)
	go func() {
		for i := 0; i < 5; i++ {
			b.Take()
		}
		done <- true
	}()

	select {
	case <- done:
	case <- timeout:
		t.Error("Timed out")
	}
}

func TestBucketDeniesWhenFull(t *testing.T) {
	b := lib.NewBucket(5, 5, time.Now().Add(3 * time.Second))
	done := make(chan bool)
	timeout := time.After(1 * time.Second)
	go func() {
		for i := 0; i < 6; i++ {
			b.Take()
		}
		done <- true
	}()

	select {
	case <- done:
		t.Error("Bucket didn't lock")
	case <- timeout:
	}
}

func TestBucketResetsProperly(t *testing.T) {
	b := lib.NewBucket(5, 5, time.Now().Add(1 * time.Second))
	done := make(chan bool)
	timeout := time.After(2 * time.Second)
	go func() {
		for i := 0; i < 6; i++ {
			b.Take()
		}
		done <- true
	}()

	select {
	case <- done:
	case <- timeout:
		t.Error("Bucket didn't reset")
	}
}

