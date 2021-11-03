package lib

import "hash/crc64"

var table = crc64.MakeTable(crc64.ISO)

func HashCRC64(data string) uint64 {
	h := crc64.New(table)
	h.Write([]byte(data))
	return h.Sum64()
}