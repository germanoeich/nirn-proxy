package libnew

import (
	"github.com/ef-ds/deque"
	"net/http"
)

type QueueItem struct {
	Req *http.Request
	Res *http.ResponseWriter
	doneChan chan *http.Response
	errChan chan error
}

func (q *QueueItem) New(req *http.Request, respWriter *http.ResponseWriter) QueueItem {
	return QueueItem{
		Req: req,
		Res: respWriter,
		doneChan: make(chan *http.Response),
		errChan: make(chan error),
	}
}

type RequestQueue struct {
	queue deque.Deque
}


