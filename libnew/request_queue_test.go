package libnew

import (
	"context"
	"errors"
	"github.com/stretchr/testify/assert"
	"net/http"
	"sync"
	"testing"
	"time"
)

var dummyReq = &http.Request{}

func TestCreateRequestQueue(t *testing.T) {
	ctx := context.Background()
	q := NewRequestQueue(ctx, &TestProcessor{})
	if q == nil {
		t.Error("Expected non-nil RequestQueue")
	}
}

type TestProcessor struct {
	Processor
	calledTimes    int
	emitError      bool
	waitFor        time.Duration
	useWaitChan    bool
	waitChan       chan struct{}
	lastCallParams *http.Request
	useCallParams  bool
	callParams     chan *http.Request
}

func (t *TestProcessor) ProcessRequest(ctx context.Context, req *http.Request) (*http.Response, error) {
	if t.emitError {
		return nil, errors.New("test error")
	}
	if t.waitFor != 0 {
		time.Sleep(t.waitFor)
	}
	if t.useWaitChan {
		<-t.waitChan
	}
	if t.useCallParams {
		t.callParams <- req
	}
	t.calledTimes++
	t.lastCallParams = req
	return nil, nil
}

func TestProcessorIsCorrectlyCalled(t *testing.T) {
	ctx := context.Background()
	p := &TestProcessor{}
	q := NewRequestQueue(ctx, p)
	<-q.Queue(context.Background(), dummyReq)
	assert.Equal(t, 1, q.processor.(*TestProcessor).calledTimes)
}

func TestProcessorErrorIsEmitted(t *testing.T) {
	ctx := context.Background()
	testProcessor := &TestProcessor{emitError: true}
	q := NewRequestQueue(ctx, testProcessor)
	result := <-q.Queue(context.Background(), dummyReq)
	assert.EqualError(t, result.Err, "test error")
}

func TestMultipleCallsBlocking(t *testing.T) {
	ctx := context.Background()
	p := &TestProcessor{}
	q := NewRequestQueue(ctx, p)
	<-q.Queue(context.Background(), dummyReq)
	<-q.Queue(context.Background(), dummyReq)
	<-q.Queue(context.Background(), dummyReq)
	assert.Equal(t, 3, q.processor.(*TestProcessor).calledTimes)
}

func TestMultipleCallsAsync(t *testing.T) {
	ctx := context.Background()
	p := &TestProcessor{}
	q := NewRequestQueue(ctx, p)
	queueItem := func() {
		<-q.Queue(context.Background(), dummyReq)
	}
	go queueItem()
	go queueItem()
	go queueItem()
	time.Sleep(5 * time.Millisecond)
	assert.Equal(t, 3, q.processor.(*TestProcessor).calledTimes)
}

func TestIsSequential(t *testing.T) {
	ctx := context.Background()
	ch := make(chan struct{})
	p := &TestProcessor{useWaitChan: true, waitChan: ch}
	q := NewRequestQueue(ctx, p)
	queueItem := func() {
		<-q.Queue(context.Background(), dummyReq)
	}
	go queueItem()
	go queueItem()
	go queueItem()
	time.Sleep(5 * time.Millisecond)
	assert.Equal(t, 0, q.processor.(*TestProcessor).calledTimes)
	ch <- struct{}{}
	time.Sleep(5 * time.Millisecond)
	assert.Equal(t, 1, q.processor.(*TestProcessor).calledTimes)
	ch <- struct{}{}
	time.Sleep(5 * time.Millisecond)
	assert.Equal(t, 2, q.processor.(*TestProcessor).calledTimes)
	ch <- struct{}{}
	time.Sleep(5 * time.Millisecond)
	assert.Equal(t, 3, q.processor.(*TestProcessor).calledTimes)
}

func TestOrderingIsPreserved(t *testing.T) {
	ctx := context.Background()
	ch := make(chan *http.Request, 3)
	tp := &TestProcessor{useCallParams: true, callParams: ch}
	q := NewRequestQueue(ctx, tp)
	queueItem := func(arg *http.Request) {
		<-q.Queue(ctx, arg)
	}

	arg1, _ := http.NewRequest("GET", "https://example1.com", nil)
	arg2, _ := http.NewRequest("GET", "https://example2.com", nil)
	arg3, _ := http.NewRequest("GET", "https://example3.com", nil)

	go queueItem(arg1)
	time.Sleep(1 * time.Millisecond)
	go queueItem(arg2)
	time.Sleep(1 * time.Millisecond)
	go queueItem(arg3)
	time.Sleep(1 * time.Millisecond)

	assert.Same(t, <-tp.callParams, arg1)
	assert.Same(t, <-tp.callParams, arg2)
	assert.Same(t, <-tp.callParams, arg3)
}

func TestBigBatchSize(t *testing.T) {
	ctx := context.Background()
	tp := &TestProcessor{}
	q := NewRequestQueue(ctx, tp)
	wg := &sync.WaitGroup{}
	queueItem := func(arg *http.Request) {
		wg.Add(1)
		<-q.Queue(ctx, arg)
		wg.Done()
	}

	for i := 0; i < 10000; i++ {
		go queueItem(dummyReq)
	}

	wg.Wait()
	assert.Equal(t, 10000, tp.calledTimes)
}

func TestContextCancelProperlyProcessesPendingRequests(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	tp := &TestProcessor{waitFor: 1 * time.Millisecond}
	q := NewRequestQueue(ctx, tp)
	wg := &sync.WaitGroup{}
	queueItem := func(arg *http.Request) {
		wg.Add(1)
		<-q.Queue(ctx, arg)
		wg.Done()
	}

	for i := 0; i < 50; i++ {
		go queueItem(dummyReq)
	}

	time.Sleep(5 * time.Millisecond)
	cancel()

	wg.Wait()
	assert.Equal(t, 50, tp.calledTimes)
}
