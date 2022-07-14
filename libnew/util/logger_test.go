package util

import (
	"fmt"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestHookStripsWebhookTokensFromPath(t *testing.T) {
	logger, hook := test.NewNullLogger()
	logger.AddHook(&GlobalHook{})
	entry := logger.WithField("path", "/api/v9/webhooks/203039963636301824/VSOzAqY1OZFF5WJVtbIzFtmjGupk-84Hn0A_ZzToF_CHsPIeCk0Q9Uok_mjxR0dNtApI")
	entry.Info("test")

	logStr := hook.LastEntry().Data["path"]
	fmt.Println(logStr)
	assert.NotContains(t, logStr, "VSOzAqY1OZFF5WJVtbIzFtmjGupk-84Hn0A_ZzToF_CHsPIeCk0Q9Uok_mjxR0dNtApI")
	assert.Contains(t, logStr, ":token")
}

func TestHookStripsInteractionTokensFromPath(t *testing.T) {
	logger, hook := test.NewNullLogger()
	logger.AddHook(&GlobalHook{})
	entry := logger.WithField("path", "/api/interactions/203039963636301824/aW50ZXJhY3Rpb246ODg3NTU5MDA01AY4NTUxNDU0OnZwS3QycDhvREk2aVF3U1BqN2prcXBkRmNqNlp4VEhGRjZvSVlXSGh4WG4yb3l6Z3B6NTBPNVc3OHphV05OULLMOHBMa2RTZmVKd3lzVDA2b2h3OTUxaFJ4QlN0dkxXallPcmhnSHNJb0tSV0M5ZzY1NkN4VGRvemFOSHY4b05c/callback")
	entry.Info("test")

	logStr := hook.LastEntry().Data["path"]
	fmt.Println(logStr)
	assert.NotContains(t, logStr, "aW50ZXJhY3Rpb246ODg3NTU5MDA01AY4NTUxNDU0OnZwS3QycDhvREk2aVF3U1BqN2prcXBkRmNqNlp4VEhGRjZvSVlXSGh4WG4yb3l6Z3B6NTBPNVc3OHphV05OULLMOHBMa2RTZmVKd3lzVDA2b2h3OTUxaFJ4QlN0dkxXallPcmhnSHNJb0tSV0M5ZzY1NkN4VGRvemFOSHY4b05c")
	assert.Contains(t, logStr, ":token")
}
