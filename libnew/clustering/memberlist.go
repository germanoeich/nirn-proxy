package clustering

import (
	"github.com/germanoeich/nirn-proxy/libnew/logging"
	"github.com/hashicorp/memberlist"
	"os"
	"time"
)

var logger = logging.GetLogger("memberlist")

func InitMemberList(knownMembers []string, port int, proxyPort string, delegate *NirnEvents) *memberlist.Memberlist {
	config := memberlist.DefaultLANConfig()
	config.BindPort = port
	config.AdvertisePort = port
	config.Delegate = NirnDelegate{
		proxyPort: proxyPort,
	}

	config.Events = delegate

	//DEBUG CODE
	if os.Getenv("NODE_NAME") != "" {
		config.Name = os.Getenv("NODE_NAME")
		config.DeadNodeReclaimTime = 1 * time.Nanosecond
	}

	list, err := memberlist.Create(config)
	if err != nil {
		panic("Failed to create memberlist: " + err.Error())
	}

	_, err = list.Join(knownMembers)
	if err != nil {
		logger.Info("Failed to join existing cluster, ok if this is the first node")
		logger.Error(err)
	}

	var members string
	for _, member := range list.Members() {
		members += member.Name + " "
	}

	logger.Info("Connected to cluster nodes: [ " + members + "]")
	return list
}