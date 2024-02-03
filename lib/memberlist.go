package lib

import (
	"github.com/hashicorp/memberlist"
	"os"
	"time"
	"net"
)

func GetLocalIP() (string) {
    addresses, err := net.InterfaceAddrs()
    if err != nil {
        return "0.0.0.0"
    }

    for _, addr := range addresses {
        if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
            if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
            }
        }
    }

	return "0.0.0.0"
}

func InitMemberList(knownMembers []string, port int, proxyPort string, manager *QueueManager) *memberlist.Memberlist {	
	config := memberlist.DefaultLANConfig()
	config.BindPort = port
	config.AdvertiseAddr = GetLocalIP()
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