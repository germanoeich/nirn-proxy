package libnew

import "github.com/germanoeich/nirn-proxy/libnew/enums"

type RequestInfo struct {
	path string
	routingHash uint64
	queueType enums.QueueType
	token string
}
