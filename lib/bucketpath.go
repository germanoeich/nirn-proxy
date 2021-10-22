package lib

import (
	"strings"
	"time"
)

const (
	MajorUnknown = "unk"
	MajorChannels = "channels"
	MajorGuilds = "guilds"
	MajorWebhooks = "webhooks"
	MajorInvites = "invites"
	MajorInteractions = "interactions"
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

func IsNumericInput(str string) bool {
	l := len(str)
	for _, d := range str {
		if d < '0' || d > '9' {
			return false
		}
	}
	return true
}

func GetMetricsPath(route string) string {
	route = GetOptimisticBucketPath(route, "")
	var path = ""
	parts := strings.Split(route, "/")

	if strings.HasPrefix(route, "/invite/!") {
		return "/invite/!"
	}

	for _, part := range parts {
		if part == "" { continue }
		if IsNumericInput(part) {
			path += "/!"
		} else {
			path += "/" + part
		}
	}

	return path
}

func GetOptimisticBucketPath(url string, method string) string {
	var bucket = "/"
	cleanUrl := strings.SplitN(url, "?", 1)[0]
	if strings.HasPrefix(cleanUrl, "/api/v") {
		cleanUrl = strings.ReplaceAll(cleanUrl, "/api/v", "")
		l := len(cleanUrl)
		i := strings.Index(cleanUrl, "/")
		cleanUrl = cleanUrl[i+1:l]
	}
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
	case MajorInvites:
		bucket += MajorInvites + "/!"
		currMajor = MajorInvites
	case MajorGuilds:
		/* TODO: Figure out why this makes the bot unresponsive
		if numParts == 3 && parts[2] == "channels" {
			return "/guilds/!/channels"
		}
		*/
		fallthrough
	case MajorWebhooks:
		fallthrough
	default:
		bucket += parts[0] + "/" + parts[1]
		currMajor = parts[0]
	}

	if numParts == 2 {
		return bucket
	}

	// At this point, the major + id part is already accounted for
	// In this loop, we only need to strip all remaining snowflakes, emoji names and webhook tokens(optional)
	for idx, part := range parts[2:] {
		if IsSnowflake(part) {
			// Custom rule for messages older than 14d
			if currMajor == MajorChannels && parts[idx - 1] == "messages" && method == "DELETE" {
				createdAt, _ := GetSnowflakeCreatedAt(part)
				if createdAt.Before(time.Now().Add(-1 * 14 * 24 * time.Hour)) {
					bucket += "/!14dmsg"
				}
				continue
			}
			bucket += "/!"
		} else {
			if currMajor == MajorChannels && part == "reactions" {
				// reaction put/delete fall under a different bucket from other reaction endpoints
				if method == "PUT" || method == "DELETE" {
					bucket += "/reactions/!modify"
					break
				}
				//All other reaction endpoints falls under the same bucket, so it's irrelevant if the user
				//is passing userid, emoji, etc.
				bucket += "/reactions/!/!"
				//Reactions can only be followed by emoji/userid combo, since we don't care, break
				break
			}

			// Strip webhook tokens and interaction tokens
			if (currMajor == MajorWebhooks || currMajor == MajorInteractions) && len(part) >= 64 {
				bucket += "/!"
				continue
			}
			bucket += "/" + part
		}
	}

	return bucket
}
