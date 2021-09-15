package lib

import (
	"github.com/sirupsen/logrus"
)

var logger *logrus.Logger

func SetLogger(l *logrus.Logger) {
	logger = l
}
