package clustering

import (
	"github.com/hashicorp/memberlist"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestNodeTransmitsCorrectPortAsMetadata(t *testing.T) {
	c1 := InitMemberList([]string{"localhost:7946", "localhost:7947"}, 7946, "8080", nil, "local1")
	c2 := InitMemberList([]string{"localhost:7946", "localhost:7947"}, 7947, "8080", nil, "local2")

	defer c1.Shutdown()
	defer c2.Shutdown()

	time.Sleep(300 * time.Millisecond)

	assert.Equal(t, c1.LocalNode().Meta, []byte("8080"))
	assert.Equal(t, c2.LocalNode().Meta, []byte("8080"))

	assert.Equal(t, c1.Members()[0].Meta, []byte("8080"))
	assert.Equal(t, c1.Members()[1].Meta, []byte("8080"))
	assert.Equal(t, c2.Members()[0].Meta, []byte("8080"))
	assert.Equal(t, c2.Members()[1].Meta, []byte("8080"))
}

func TestJoinLeaveEvents(t *testing.T) {
	joinCalls := 0
	leaveCalls := 0
	onJoin := func(node *memberlist.Node) {
		joinCalls++
	}

	onLeave := func(node *memberlist.Node) {
		leaveCalls++
	}

	delegate := NirnEvents{
		OnJoin:  onJoin,
		OnLeave: onLeave,
	}

	c1 := InitMemberList([]string{"localhost:7946", "localhost:7947"}, 7946, "8080", nil, "local1")
	c2 := InitMemberList([]string{"localhost:7946", "localhost:7947"}, 7947, "8080", &delegate, "local2")

	defer c1.Shutdown()
	defer c2.Shutdown()

	time.Sleep(300 * time.Millisecond)

	joinCalls = 0
	leaveCalls = 0

	c1.Leave(0 * time.Second)
	c1.Shutdown()

	time.Sleep(300 * time.Millisecond)

	assert.Equal(t, leaveCalls, 1)

	c3 := InitMemberList([]string{"localhost:7946", "localhost:7947"}, 7948, "8080", nil, "local3")

	defer c3.Shutdown()
	
	time.Sleep(300 * time.Millisecond)

	assert.Equal(t, joinCalls, 1)
}
