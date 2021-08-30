package tests

import (
	"fmt"
	"github.com/germanoeich/nirn-proxy/lib"
	"testing"
)

func TestPaths(t *testing.T) {
	var tests = []struct {
		path, method, want string
	}{
		// Guild Major
		{"guilds/203039963636301824", "GET", "/guilds/203039963636301824"},
		// Channel major
		{"channels/203039963636301824", "GET", "/channels/!"},
		{"channels/203039963636301824/pins", "GET", "/channels/203039963636301824/pins"},
		{"channels/872712139712913438/messages/872712150509047809/reactions/%F0%9F%98%8B", "GET", "/channels/872712139712913438/messages/!/reactions/!/!"},
		{"channels/872712139712913438/messages/872712150509047809/reactions/PandaOhShit:863985751205085195", "GET", "/channels/872712139712913438/messages/!/reactions/!/!"},
		// Hooks major
		{"webhooks/203039963636301824", "GET", "/webhooks/203039963636301824"},
		// No known major
		{"invalid/203039963636301824", "GET", "/invalid/203039963636301824"},
		{"invalid/203039963636301824/route/203039963636301824", "GET", "/invalid/203039963636301824/route/!"},
	}
	for _, tt := range tests {
		testname := fmt.Sprintf("%s-%s", tt.method, tt.path)
		t.Run(testname, func(t *testing.T) {
			bucket := lib.GetOptimisticBucketPath(tt.path, tt.method)
			if bucket != tt.want {
				t.Errorf("Expected %s but got %s", tt.want, bucket)
			}
		})
	}
}