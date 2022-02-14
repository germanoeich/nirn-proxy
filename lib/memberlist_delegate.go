package lib

import "github.com/hashicorp/memberlist"

type NirnDelegate struct {
	memberlist.Delegate
	proxyPort string
}

func (d NirnDelegate) NodeMeta(limit int) []byte {
	return []byte(d.proxyPort)
}

func (d NirnDelegate) NotifyMsg(msg []byte) {}

func (d NirnDelegate) GetBroadcasts(overhead int, limit int) [][]byte {
	return [][]byte{}
}

func (d NirnDelegate) LocalState(join bool) []byte {
	return []byte{}
}

func (d NirnDelegate) MergeRemoteState(buf []byte, join bool) {}
