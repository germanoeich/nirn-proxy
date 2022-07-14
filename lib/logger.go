package lib

import (
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
	if logrus.ErrorLevel >= e.Level {
		ErrorCounter.Inc()
	}
	return nil
}

var logger *logrus.Logger

func SetLogger(l *logrus.Logger) {
	logger = l
	logger.AddHook(&GlobalHook{})
}
