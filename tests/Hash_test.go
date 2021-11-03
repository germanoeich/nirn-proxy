package tests

import (
	"github.com/germanoeich/nirn-proxy/lib"
	"strconv"
	"testing"
)

const knownData = "test data"
// Calculated using ISO table
const knownHash = 10232006911339297906

func TestHashWorks(t *testing.T) {
	lib.HashCRC64(knownData)
}

//Test for correctness
func TestHashIsConsistent(t *testing.T) {
	ret := lib.HashCRC64(knownData)
	if ret != knownHash {
		t.Errorf("Invalid hash returned")
	}
}

//Test for consistency when function is used for other data
func TestHashIsConsistentAcrossMultipleRuns(t *testing.T) {
	for i := 0; i < 50000; i++ {
		lib.HashCRC64(strconv.Itoa(i))
	}

	ret := lib.HashCRC64(knownData)
	if ret != knownHash {
		t.Errorf("Invalid hash returned")
	}
}
