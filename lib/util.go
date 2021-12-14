package lib

import (
	"bytes"
	"encoding/base64"
	"hash/crc64"
	"reflect"
	"strconv"
	"time"
	"unsafe"
)

var table = crc64.MakeTable(crc64.ISO)

func HashCRC64(data string) uint64 {
	h := crc64.New(table)
	h.Write([]byte(data))
	return h.Sum64()
}

const EpochDiscord = 1420070400000

func GetSnowflakeCreatedAt(snowflake string) (time.Time, error) {
	parsedId, err := strconv.ParseUint(snowflake, 10, 64)
	if err != nil {
		return time.Now(), err
	}
	epoch := (parsedId >> uint64(22)) + EpochDiscord
	return time.Unix(int64(epoch)/1000, 0), nil
}

func GetBotId(token []byte) string {
	var clientId string
	if len(token) == 0 {
		clientId = "NoAuth"
	} else {
		token = bytes.ReplaceAll(token, []byte("Bot "), []byte(""))
		token = bytes.ReplaceAll(token, []byte("Bearer "), []byte(""))
		token = bytes.Split(token, []byte("."))[0]
		token, err := base64.StdEncoding.DecodeString(B2S(token))
		if err != nil {
			clientId = "Unknown"
		} else {
			clientId = B2S(token)
		}
	}
	return clientId
}

func B2S(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}

func S2B(s string) (b []byte) {
	bh := (*reflect.SliceHeader)(unsafe.Pointer(&b))
	sh := (*reflect.StringHeader)(unsafe.Pointer(&s))
	bh.Data = sh.Data
	bh.Cap = sh.Len
	bh.Len = sh.Len
	return b
}