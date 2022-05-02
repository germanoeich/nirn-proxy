package util

import (
	"fmt"
	"strconv"
	"testing"
)

const knownData = "test data"
// Calculated using ISO table
const knownHash = 10232006911339297906

func TestHashWorks(t *testing.T) {
	HashCRC64(knownData)
}

//Test for correctness
func TestHashIsConsistent(t *testing.T) {
	ret := HashCRC64(knownData)
	if ret != knownHash {
		t.Errorf("Invalid hash returned")
	}
}

//Test for consistency when function is used for other data
func TestHashIsConsistentAcrossMultipleRuns(t *testing.T) {
	for i := 0; i < 50000; i++ {
		HashCRC64(strconv.Itoa(i))
	}

	ret := HashCRC64(knownData)
	if ret != knownHash {
		t.Errorf("Invalid hash returned")
	}
}


func TestDiscordSnowflakeCreatedAt(t *testing.T) {
	createdAt, err := GetSnowflakeCreatedAt("227115752396685313")

	if err != nil {
		t.Error(err)
	}

	// 2016-09-18 14:16:54 -0300 -03 = 1474219014
	if createdAt.Unix() != 1474219014 {
		t.Errorf("Snowflake was not properly parsed")
	}
}

func TestIsNumericInput(t *testing.T) {
	var tests = []struct {
		input string
		want bool
	}{
		{"124124124124", true},
		{"123123ab124124", false},
		{"asdbasdasdsad", false},
	}
	for _, tt := range tests {
		testname := fmt.Sprintf("%s", tt.input)
		t.Run(testname, func(t *testing.T) {
			result := IsNumericInput(tt.input)
			if result != tt.want {
				t.Errorf("Expected %t but got %t", tt.want, result)
			}
		})
	}
}

func TestIsSnowflake(t *testing.T) {
	var tests = []struct {
		input string
		want bool
	}{
		{"1234", false},
		{"227115752396685313", true},
		{"22711575239668531a", false},
		{"968994664332017684", true},
	}
	for _, tt := range tests {
		testname := fmt.Sprintf("%s", tt.input)
		t.Run(testname, func(t *testing.T) {
			result := IsSnowflake(tt.input)
			if result != tt.want {
				t.Errorf("Expected %t but got %t", tt.want, result)
			}
		})
	}
}