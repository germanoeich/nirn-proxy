package clustering

import (
	"github.com/germanoeich/nirn-proxy/libnew/util"
	"github.com/hashicorp/memberlist"
	"os"
	"time"
)

var logger = util.GetLogger("memberlist")

func InitMemberList(knownMembers []string, port int, proxyPort string, delegate *NirnEvents, name string) *memberlist.Memberlist {
	config := memberlist.DefaultLANConfig()
	config.BindPort = port
	config.AdvertisePort = port
	config.Delegate = NirnDelegate{
		proxyPort: proxyPort,
	}

	if delegate != nil {
		config.Events = delegate
	}

	// Should default to os.Hostname unless in a test env
	config.Name = name

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
