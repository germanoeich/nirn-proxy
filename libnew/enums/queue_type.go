package enums

type QueueType int

const (
	Bot QueueType = iota
	NoAuth
	Bearer
)