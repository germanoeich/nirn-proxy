package libnew

import (
	"fmt"
	"github.com/stretchr/testify/assert"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestTakeFirstCallDoesntBlock(t *testing.T) {
	g := NewGlobalRateLimiter()
	timeout := time.After(100 * time.Millisecond)
	done := make(chan bool)
	go func() {
		g.Take(1, 1)
		done <- true
	}()

	select {
	case <-timeout:
		t.Fatal("Test didn't finish in time")
	case <-done:
	}
}

func TestTakeTimingIsCorrect(t *testing.T) {
	g := NewGlobalRateLimiter()
	timeout := time.After(2000 * time.Millisecond)
	done := make(chan bool)
	go func() {
		g.Take(1, 1)
		now := time.Now()
		g.Take(1, 1)
		fmt.Println(time.Since(now))
		if time.Since(now) < 999*time.Millisecond || time.Since(now) > 1001*time.Millisecond {
			t.Error("Unlocked early or late")
		}
		done <- true
	}()

	select {
	case <-timeout:
		t.Fatal("Test didn't finish in time")
	case <-done:
	}
}

func TestTakeUnlocksProperly(t *testing.T) {
	g := NewGlobalRateLimiter()
	b, _ := g.memStorage.Create("1", 50, 1*time.Millisecond)
	g.globalBucketsMap[1] = &b
	wg := sync.WaitGroup{}
	for i := 0; i < 50000; i++ {
		wg.Add(1)
	}
	done := make(chan bool)
	go func() {
		// we should be able to do 50 calls every 1ms, totalling 50000 calls in 1 second in ideal conditions.
		for i := 0; i < 50000; i++ {
			g.Take(1, 50)
			wg.Done()
		}
		wg.Wait()
		done <- true
	}()

	// wg + loop adds some overhead
	timeout := time.After(1200 * time.Millisecond)
	select {
	case <-timeout:
		t.Fatal("Test didn't finish in time")
	case <-done:
	}
}

func TestGetOrCreateIsConcurrencySafe(t *testing.T) {
	g := NewGlobalRateLimiter()
	wg := sync.WaitGroup{}
	wg.Add(1)
	for i := 0; i < 100000; i++ {
		go func() {
			wg.Add(1)
			g.getOrCreate(1, 1)
			wg.Done()
		}()
	}
	wg.Done()
	wg.Wait()
}

func TestHandleGlobalRequestBlocksCorrectly(t *testing.T) {
	g := NewGlobalRateLimiter()
	spy := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("bot-hash", "1")
	req.Header.Set("bot-limit", "1")

	g.Take(1, 1)
	now := time.Now()
	g.HandleGlobalRequest(spy, req)
	if time.Since(now) < 999*time.Millisecond || time.Since(now) > 1001*time.Millisecond {
		t.Error("Unlocked early or late")
	}
	if spy.Code != 200 {
		t.Error("Expected 200, got", spy.Code)
	}
}

func TestHandleGlobalRequestRespectHeaders(t *testing.T) {
	g := NewGlobalRateLimiter()
	spy := httptest.NewRecorder()
	spy2 := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("bot-hash", "1")
	req.Header.Set("bot-limit", "1")
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.Header.Set("bot-hash", "2")
	req2.Header.Set("bot-limit", "1")

	timeout := time.After(10 * time.Millisecond)
	done := make(chan bool)
	go func() {
		g.HandleGlobalRequest(spy, req)
		g.HandleGlobalRequest(spy2, req2)
		done <- true
	}()

	select {
	case <-timeout:
		t.Fatal("Test didn't finish in time")
	case <-done:
		assert.NotNil(t, g.globalBucketsMap[1])
		assert.NotNil(t, g.globalBucketsMap[2])
	}
}
