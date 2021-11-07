package lib

import (
	"encoding/base64"
	"hash/crc64"
	"strconv"
	"strings"
	"time"
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

func GetBotId(token string) string {
	var clientId string
	if token == "" {
		clientId = "NoAuth"
	} else {
		token = strings.ReplaceAll(token, "Bot ", "")
		token = strings.ReplaceAll(token, "Bearer ", "")
		token = strings.Split(token, ".")[0]
		token, err := base64.StdEncoding.DecodeString(token)
		if err != nil {
			clientId = "Unknown"
		} else {
			clientId = string(token)
		}
	}
	return clientId
}