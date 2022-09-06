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
	ProcessRequest(ctx context.Context, req *http.Request) (*http.Response, error)
}

type QueueItem struct {
	Req      *http.Request
	DoneChan chan QueueItemResult
	Context  context.Context
}

type QueueItemResult struct {
	Res *http.Response
	Err error
}

type RequestQueue struct {
	sync.Mutex
	LastUsed  time.Time
	deque     *deque.Deque[*QueueItem]
	ticker    *time.Ticker
	ctx       context.Context
	cancel    context.CancelFunc
	processor Processor
}

func NewRequestQueue(ctx context.Context, processor Processor) *RequestQueue {
	newCtx, cancel := context.WithCancel(ctx)
	q := &RequestQueue{
		deque: deque.NewDeque[*QueueItem](),
		// 1 ms may seem like a lot but this isn't actually the delay between processing individual requests,
		// its just a way to keep the bucket from stalling. Think of this as "Sleep for 1ms if there is no work to do"
		ticker:    time.NewTicker(1 * time.Millisecond),
		ctx:       newCtx,
		processor: processor,
		cancel:    cancel,
		LastUsed:  time.Now(),
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

func (r *RequestQueue) Destroy() {
	r.cancel()
}

func (r *RequestQueue) Queue(ctx context.Context, req *http.Request) chan QueueItemResult {
	r.Lock()
	defer r.Unlock()

	item := QueueItemPool.Get().(*QueueItem)
	item.Req = req
	item.Context = ctx
	item.DoneChan = make(chan QueueItemResult)
	r.deque.PushBack(item)

	return item.DoneChan
}

func (r *RequestQueue) safeTryDequeue() (*QueueItem, bool) {
	r.Lock()
	defer r.Unlock()
	return r.deque.TryDequeue()
}

func (r *RequestQueue) Process() {
	for v, ok := r.safeTryDequeue(); ok; v, ok = r.safeTryDequeue() {
		r.LastUsed = time.Now()
		// process
		res, err := r.processor.ProcessRequest(v.Context, v.Req)
		v.DoneChan <- QueueItemResult{Res: res, Err: err}
		// cleanup
		v.Req = nil
		v.DoneChan = nil
		v.Context = nil
		QueueItemPool.Put(v)
	}
}
