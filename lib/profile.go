package lib

import (
	"net/http"
	_ "net/http/pprof"
)

func StartProfileServer() {
	logger.Info("Profiling endpoints loaded on :7654")
	http.ListenAndServe(":7654", nil)
}