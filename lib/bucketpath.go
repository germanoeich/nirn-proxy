package lib

import (
	"fmt"
	"strings"
)

const (
	MajorUnknown = "unk"
	MajorChannels = "channels"
	MajorGuilds = "guilds"
	MajorWebhooks = "webhooks"
)

func IsSnowflake(str string) bool {
	l := len(str)
	if l < 17 || l > 20 {
		return false
	}
	for _, d := range str {
		if d < '0' || d > '9' {
			return false
		}
	}
	return true
}

func GetOptimisticBucketPath(url string, method string) string {
	var bucket = "/"
	cleanUrl := strings.SplitN(url, "?", 1)[0]
	parts := strings.Split(cleanUrl, "/")
	numParts := len(parts)

	if numParts <= 1 {
		return cleanUrl
	}

	currMajor := MajorUnknown
	// ! stands for any replaceable id
	switch parts[0] {
	case MajorChannels:
		if numParts == 2 {
			// Return the same bucket for all reqs to /channels/id
			// In this case, the discord bucket is the same regardless of the id
			bucket += MajorChannels + "/!"
			return bucket
		}
		bucket += MajorChannels + "/" + parts[1]
		currMajor = MajorChannels
	case MajorGuilds:
		bucket += MajorGuilds + "/" + parts[1]
		currMajor = MajorGuilds
	case MajorWebhooks:
		bucket += MajorWebhooks + "/" + parts[1]
		currMajor = MajorWebhooks
	default:
		bucket += parts[0] + "/" + parts[1]
	}

	if numParts == 2 {
		return bucket
	}

	if currMajor == MajorUnknown {
		fmt.Println(parts)
	}

	// At this point, the major + id part is already accounted for
	// In this loop, we only need to strip all remaining snowflakes, emoji names and webhook tokens(optional)
	for _, part := range parts[2:] {
		if IsSnowflake(part) {
			bucket += "/!"
		} else {
			if currMajor == MajorChannels && part == "reactions" {
				//All reaction stuff falls under the same bucket, so it's irrelevant if the user
				//is passing userid, emoji, etc.
				bucket += "/reactions/!/!"
				//Reactions can only be followed by emoji/userid combo, since we don't care, break
				break
			}
			bucket += "/" + part
		}
	}

	return bucket
}
