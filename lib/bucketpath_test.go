package lib

import (
	"fmt"
	"testing"
)

func TestPaths(t *testing.T) {
	var tests = []struct {
		path, method, want string
	}{
		// Guild Major
		{"/api/v9/guilds/103039963636301824", "GET", "/guilds/103039963636301824"},
		// Channel major
		{"/api/v8/channels/203039963636301824", "GET", "/channels/!"},
		{"/api/v7/channels/203039963636301824/pins", "GET", "/channels/203039963636301824/pins"},
		{"/api/v6/channels/872712139712913438/messages/872712150509047809/reactions/%F0%9F%98%8B", "GET", "/channels/872712139712913438/messages/!/reactions/!/!"},
		{"/api/v10/channels/872712139712913438/messages/872712150509047809/reactions/PandaOhShit:863985751205085195", "GET", "/channels/872712139712913438/messages/!/reactions/!/!"},
		{"/api/v9/channels/872712139712913438/messages/872712150509047809/reactions/PandaOhShit:863985751205085195", "PUT", "/channels/872712139712913438/messages/!/reactions/!modify"},
		{"/api/v9/channels/872712139712913438/messages/872712150509047809/reactions/PandaOhShit:863985751205085195", "DELETE", "/channels/872712139712913438/messages/!/reactions/!modify"},
		{"/api/v9/channels/872712139712913438/messages/872712150509047809/reactions/PandaOhShit:863985751205085195/@me", "DELETE", "/channels/872712139712913438/messages/!/reactions/!modify"},
		{"/api/v9/channels/872712139712913438/messages/872712150509047809/reactions/PandaOhShit:863985751205085195/203039963636301824", "DELETE", "/channels/872712139712913438/messages/!/reactions/!modify"},
		// Hooks major
		{"/api/v9/webhooks/203039963636301824", "GET", "/webhooks/203039963636301824"},
		{"/api/v9/webhooks/203039963636301824/VSOzAqY1OZFF5WJVtbIzFtmjGupk-84Hn0A_ZzToF_CHsPIeCk0Q9Uok_mjxR0dNtApI", "POST", "/webhooks/203039963636301824/!"},
		// Invites major
		{"/api/v9/invites/dyno", "GET", "/invites/!"},
		// Interactions major
		{"/api/v9/interactions/203039963636301824/aW50ZXJhY3Rpb246ODg3NTU5MDA01AY4NTUxNDU0OnZwS3QycDhvREk2aVF3U1BqN2prcXBkRmNqNlp4VEhGRjZvSVlXSGh4WG4yb3l6Z3B6NTBPNVc3OHphV05OULLMOHBMa2RTZmVKd3lzVDA2b2h3OTUxaFJ4QlN0dkxXallPcmhnSHNJb0tSV0M5ZzY1NkN4VGRvemFOSHY4b05c/callback", "GET", "/interactions/203039963636301824/!/callback"},
		// Interaction followup webhooks
		{"/api/v10/webhooks/203039963636301824/aW50ZXJhY3Rpb246MTEwMzA0OTQyMDkzMDU2ODMyMjpOZUllWHdNU2J4RXBFMHVYRjBpU0pHMDdEb3BhM3ZlYklBODlMUmtlUXlRbzlpZzYyTnpLU0dqdWlyVlBvZnBSUlJHbUJHYlJ0N29MbE9KQUJVTFk4bTR4UzFtZEpEeXJyY0hBUERmTEhKVE9wRkNzU1FFWUkwTnlpWFY2WHdrRg/messages/@original", "POST", "/webhooks/203039963636301824/1103049420930568322/messages/@original"},
		// No known major
		{"/api/v9/invalid/203039963636301824", "GET", "/invalid/203039963636301824"},
		{"/api/v9/invalid/203039963636301824/route/203039963636301824", "GET", "/invalid/203039963636301824/route/!"},
		//Special case for /guilds/:id/channels
		{"/api/v9/guilds/203039963636301824/channels", "GET", "/guilds/!/channels"},
		// Wierd routes
		{"/api/v9/guilds/templates/203039963636301824", "GET", "/guilds/templates/!"},
		// Unversioned routes
		{"/api/webhooks/203039963636301824/VSOzAqY1OZFF5WJVtbIzFtmjGupk-84Hn0A_ZzToF_CHsPIeCk0Q9Uok_mjxR0dNtApI", "POST", "/webhooks/203039963636301824/!"},
		{"/api/interactions/203039963636301824/aW50ZXJhY3Rpb246ODg3NTU5MDA01AY4NTUxNDU0OnZwS3QycDhvREk2aVF3U1BqN2prcXBkRmNqNlp4VEhGRjZvSVlXSGh4WG4yb3l6Z3B6NTBPNVc3OHphV05OULLMOHBMa2RTZmVKd3lzVDA2b2h3OTUxaFJ4QlN0dkxXallPcmhnSHNJb0tSV0M5ZzY1NkN4VGRvemFOSHY4b05c/callback", "GET", "/interactions/203039963636301824/!/callback"},
		{"/api/channels/872712139712913438/messages/872712150509047809/reactions/PandaOhShit:863985751205085195", "GET", "/channels/872712139712913438/messages/!/reactions/!/!"},
		{"/api/invites/dyno", "GET", "/invites/!"},
		// Application commands
		{"/api/v9/applications/203039963636301824/commands", "GET", "/applications/203039963636301824/commands"},
		{"/api/v9/applications/203039963636301824/commands/203039963636301824", "GET", "/applications/203039963636301824/commands/!"},
	}
	for _, tt := range tests {
		testname := fmt.Sprintf("%s-%s", tt.method, tt.path)
		t.Run(testname, func(t *testing.T) {
			bucket := GetOptimisticBucketPath(tt.path, tt.method)
			if bucket != tt.want {
				t.Errorf("Expected %s but got %s", tt.want, bucket)
			}
		})
	}
}
