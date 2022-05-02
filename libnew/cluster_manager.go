package libnew

import (
	"github.com/germanoeich/nirn-proxy/libnew/clustering"
	"github.com/germanoeich/nirn-proxy/libnew/config"
	"github.com/hashicorp/memberlist"
	"github.com/sirupsen/logrus"
	"net"
	"sort"
	"sync"
	"time"
)

type ClusterManager struct {
	sync.RWMutex
	cluster *memberlist.Memberlist
	clusterGlobalRateLimiter *GlobalRateLimiter
	orderedClusterMembers []string
	nameToAddressMap map[string]string
	localNodeName string
	localNodeIP              string
	localNodeProxyListenAddr string
	logger *logrus.Entry
}

func NewClusterManager() *ClusterManager {
	q := &ClusterManager{
		cluster: nil,
	}

	cfg := config.Get()

	var knownMembers []string
	if len(cfg.ClusterMembers) != 0 {
		knownMembers = cfg.ClusterMembers
	} else if cfg.ClusterDNS != "" {
		ips, err := net.LookupIP(cfg.ClusterDNS)
		if err != nil {
			panic(err)
		}

		if len(ips) == 0 {
			panic("no ips returned by dns")
		}

		for _, ip := range ips {
			knownMembers = append(knownMembers, ip.String())
		}
	}

	if len(knownMembers) != 0 {
		q.cluster = clustering.InitMemberList(knownMembers, cfg.ClusterPort, cfg.Port, q.getEventDelegate())
	}

	return q
}

func (m *ClusterManager) Shutdown() {
	if m.cluster != nil {
		m.cluster.Leave(30 * time.Second)
	}
}

func (m *ClusterManager) reindexMembers() {
	if m.cluster == nil {
		m.logger.Warn("reindexMembers called but cluster is nil")
		return
	}

	m.Lock()
	defer m.Unlock()

	members := m.cluster.Members()
	var orderedMembers []string
	nameToAddressMap := make(map[string]string)
	for _, m := range members {
		orderedMembers = append(orderedMembers, m.Name)
		nameToAddressMap[m.Name] = m.Addr.String() + ":" + string(m.Meta)
	}
	sort.Strings(orderedMembers)

	m.orderedClusterMembers = orderedMembers
	m.nameToAddressMap = nameToAddressMap
}

func (m *ClusterManager) onNodeJoin(node *memberlist.Node) {
	// Running in goroutine prevents a deadlock inside memberlist
	go m.reindexMembers()
}
func (m *ClusterManager) onNodeLeave(node *memberlist.Node) {
	// Running in goroutine prevents a deadlock inside memberlist
	go m.reindexMembers()
}

func (m *ClusterManager) getEventDelegate() *clustering.NirnEvents {
	return &clustering.NirnEvents{
		OnJoin:        m.onNodeJoin,
		OnLeave:       m.onNodeLeave,
	}
}

func (m *ClusterManager) SetCluster(cluster *memberlist.Memberlist, proxyPort string) {
	m.cluster = cluster
	m.localNodeName = cluster.LocalNode().Name
	m.localNodeIP = cluster.LocalNode().Addr.String()
	m.localNodeProxyListenAddr = m.localNodeIP + ":" + proxyPort
	m.reindexMembers()
}

func (m *ClusterManager) CalculateRoute(pathHash uint64) string {
	if m.cluster == nil {
		// Route to self, proxy in stand-alone mode
		return ""
	}

	if pathHash == 0 {
		return ""
	}

	m.RLock()
	defer m.RUnlock()

	members := m.orderedClusterMembers
	count := uint64(len(members))

	chosenIndex := pathHash % count
	addr := m.nameToAddressMap[members[chosenIndex]]
	if addr == m.localNodeProxyListenAddr {
		return ""
	}
	return addr
}