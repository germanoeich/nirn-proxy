package lib

import (
	"github.com/hashicorp/memberlist"
	"os"
	"time"
)

func InitMemberList(knownMembers []string, port int, proxyPort string, manager *QueueManager) *memberlist.Memberlist {
	config := memberlist.DefaultLANConfig()
	config.BindPort = port
	config.AdvertisePort = port
	config.Delegate = NirnDelegate{
		proxyPort: proxyPort,
	}

	config.Events = manager.GetEventDelegate()

	//DEBUG CODE
	if os.Getenv("NODE_NAME") != "" {
		config.Name = os.Getenv("NODE_NAME")
		config.DeadNodeReclaimTime = 1 * time.Nanosecond
	}

	list, err := memberlist.Create(config)
	if err != nil {
		panic("Failed to create memberlist: " + err.Error())
	}

	manager.SetCluster(list, proxyPort)

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