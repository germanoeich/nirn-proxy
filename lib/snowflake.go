package lib

import (
	"strconv"
	"time"
)

const EpochDiscord = 1420070400000

func GetSnowflakeCreatedAt(snowflake string) (time.Time, error) {
	parsedId, err := strconv.ParseUint(snowflake, 10, 64)
	if err != nil {
		return time.Now(), err
	}
	epoch := (parsedId >> uint64(22)) + EpochDiscord
	return time.Unix(int64(epoch)/1000, 0), nil
}