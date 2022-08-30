package libnew

import (
	"context"
	"github.com/edwingeng/deque/v2"
	"github.com/germanoeich/nirn-proxy/libnew/util"
	"net/http"
	"sync"
	"time"
)

var logger = util.GetLogger("RequestQueue")
var QueueItemPool = sync.Pool{
	New: func() interface{} {
		return &QueueItem{}
	},
}

type Processor interface {
	Do(ctx context.Context, req *http.Request) (*http.Response, error)
}

type QueueItem struct {
	Req      *http.Request
	DoneChan chan QueueItemResult
}

type QueueItemResult struct {
	Res *http.Response
	Err error
}

type RequestQueue struct {
	sync.RWMutex
	deque     *deque.Deque[*QueueItem]
	ticker    *time.Ticker
	ctx       context.Context
	processor Processor
}

func NewRequestQueue(ctx context.Context, processor Processor) *RequestQueue {
	q := &RequestQueue{
		deque: deque.NewDeque[*QueueItem](),
		// 1 ms may seem like a lot but this isn't actually the delay between processing individual requests,
		// its just a way to keep the bucket from stalling. Think of this as "Sleep for 1ms if there is no work to do"
		ticker:    time.NewTicker(1 * time.Millisecond),
		ctx:       ctx,
		processor: processor,
	}

	go func() {
		for {
			select {
			case <-q.ticker.C:
				q.Process()
			case <-ctx.Done():
				q.ticker.Stop()
				// Process any remaining requests
				q.Process()
				return
			}
		}
	}()

	return q
}

func (r *RequestQueue) Queue(req *http.Request) chan QueueItemResult {
	r.Lock()
	defer r.Unlock()

	item := QueueItemPool.Get().(*QueueItem)
	item.Req = req
	item.DoneChan = make(chan QueueItemResult)
	r.deque.PushBack(item)

	return item.DoneChan
}

func (r *RequestQueue) safeTryDequeue() (*QueueItem, bool) {
	r.RLock()
	defer r.RUnlock()
	return r.deque.TryDequeue()
}

func (r *RequestQueue) Process() {
	for v, ok := r.safeTryDequeue(); ok; v, ok = r.safeTryDequeue() {
		// process
		res, err := r.processor.Do(r.ctx, v.Req)
		v.DoneChan <- QueueItemResult{Res: res, Err: err}
		// cleanup
		v.Req = nil
		v.DoneChan = nil
		QueueItemPool.Put(v)
	}
}
