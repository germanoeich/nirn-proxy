package libnew

import (
	"context"
	"github.com/germanoeich/nirn-proxy/libnew/discord"
	"github.com/germanoeich/nirn-proxy/libnew/enums"
	"github.com/sirupsen/logrus"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	ValidToken = iota
	InvalidToken
)

type QueueManager struct {
	queuesMutex   sync.Mutex
	queues        map[uint64]*RequestQueue
	ctx           context.Context
	cancel        context.CancelFunc
	tokenStatus   *int64
	user          *discord.BotUserResponse
	identifier    string
	botLimit      uint
	queueType     enums.QueueType
	discordClient discord.Client
}

func NewQueueManager(ctx context.Context, token string, discordClient discord.Client) (*QueueManager, error) {
	newCtx, cancel := context.WithCancel(ctx)
	queueType := enums.NoAuth
	var user *discord.BotUserResponse
	var err error
	if !strings.HasPrefix(token, "Bearer") {
		user, err = discordClient.GetBotUser(token)
		if err != nil && token != "" {
			return nil, err
		}
	} else {
		queueType = enums.Bearer
	}

	limit, err := discordClient.GetBotGlobalLimit(token, user)
	if err != nil {
		if strings.HasPrefix(err.Error(), "invalid token") {
			// Return a queue that will only return 401s
			var invalid = new(int64)
			*invalid = InvalidToken
			return &QueueManager{
				queues:        make(map[uint64]*RequestQueue),
				ctx:           newCtx,
				cancel:        cancel,
				tokenStatus:   invalid,
				user:          nil,
				identifier:    "InvalidTokenQueue",
				botLimit:      0,
				queueType:     queueType,
				discordClient: discordClient,
			}, nil
		}
		return nil, err
	}

	identifier := "NoAuth"
	if user != nil {
		queueType = enums.Bot
		identifier = user.Username + "#" + user.Discrim
	}

	if queueType == enums.Bearer {
		identifier = "Bearer"
	}

	ret := &QueueManager{
		queues:        make(map[uint64]*RequestQueue),
		ctx:           newCtx,
		cancel:        cancel,
		tokenStatus:   new(int64),
		user:          user,
		identifier:    identifier,
		botLimit:      limit,
		queueType:     queueType,
		discordClient: discordClient,
	}

	if queueType != enums.Bearer {
		logger.WithFields(logrus.Fields{"globalLimit": limit, "identifier": identifier}).Info("Created new queue")
		// Only sweep bot queues, bearer queues get completely destroyed and hold way less endpoints
		go ret.tickSweep()
	} else {
		logger.WithFields(logrus.Fields{"globalLimit": limit, "identifier": identifier}).Debug("Created new bearer queue")
	}

	return ret, nil
}

func (q *QueueManager) sweep() {
	q.queuesMutex.Lock()
	defer q.queuesMutex.Unlock()
	logger.Info("Sweep start")
	sweptEntries := 0
	for key, val := range q.queues {
		if time.Since(val.LastUsed) > 10*time.Minute {
			val.Destroy()
			delete(q.queues, key)
			sweptEntries++
		}
	}
	logger.WithFields(logrus.Fields{"sweptEntries": sweptEntries}).Info("Finished sweep")
}

func (q *QueueManager) tickSweep() {
	t := time.NewTicker(5 * time.Minute)

	for {
		select {
		case <-t.C:
			q.sweep()
		case <-q.ctx.Done():
			t.Stop()
			return
		}
	}
}

func (q *QueueManager) QueueRequest(ctx context.Context, req *http.Request, routing RequestInfo) QueueItemResult {
	logger.WithFields(logrus.Fields{
		"path":   req.URL.Path,
		"method": req.Method,
	}).Trace("Queueing request")

	queue := q.GetOrCreateQueue(ctx, routing.routingHash)
	res := <-queue.Queue(ctx, req)
	return res
}

func (q *QueueManager) ProcessRequest(ctx context.Context, req *http.Request) (*http.Response, error) {
	return nil, nil
	// note to future gin:
	// need to implement the discord client in order to implement this method
	// the http handler needs to be wired up
	// http handler must handle the queue manager classes
	// need to handle eviction of bearer queues
}

func (q *QueueManager) GetOrCreateQueue(ctx context.Context, path uint64) *RequestQueue {
	q.queuesMutex.Lock()
	defer q.queuesMutex.Unlock()

	queue, ok := q.queues[path]
	if !ok {
		queue = NewRequestQueue(ctx, q)
		q.queues[path] = queue
	}

	return queue
}
