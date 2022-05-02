package util

import (
	"github.com/germanoeich/nirn-proxy/libnew/logging"
	"net/http"
	_ "net/http/pprof"
)

var logger = logging.GetLogger("profile")

func StartProfileServer() {
	logger.Info("Profiling endpoints loaded on :7654")
	http.ListenAndServe(":7654", nil)
}