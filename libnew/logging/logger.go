package logging

import (
	"github.com/germanoeich/nirn-proxy/libnew/util"
	"github.com/sirupsen/logrus"
	"regexp"
)

type GlobalHook struct {
}

var loggerHookRegex = regexp.MustCompile("(\\/[0-9]{17,26}\\/)[a-zA-Z0-9\\-_]{63,}")

func (h *GlobalHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (h *GlobalHook) Fire(e *logrus.Entry) error {
	e.Message = loggerHookRegex.ReplaceAllString(e.Message, "$1:token")
	if e.Data["path"] != nil {
		e.Data["path"] = loggerHookRegex.ReplaceAllString(e.Data["path"].(string), "$1:token")
	}
	return nil
}

func GetLogger(subsystem string) *logrus.Entry {
	var logger = logrus.New()

	logLevel := util.EnvGet("LOG_LEVEL", "info")
	lvl, err := logrus.ParseLevel(logLevel)

	if err != nil {
		panic("Failed to parse log level")
	}

	logger.SetLevel(lvl)
	return logger.WithField("subsystem", subsystem)
}
