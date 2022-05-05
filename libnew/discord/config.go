package discord

import "time"

type ClientConfig struct {
	ip string
	timeout time.Duration
	disableHttp2 bool
}