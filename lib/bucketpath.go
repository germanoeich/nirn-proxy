package lib

import (
	"encoding/base64"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MajorChannels     = "channels"
	MajorGuilds       = "guilds"
	MajorWebhooks     = "webhooks"
	MajorInvites      = "invites"
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
	for _, d := range str {
		if d < '0' || d > '9' {
			return false
		}
	}
	return true
}

func GetMetricsPath(route string) string {
	route, _ = GetOptimisticBucketPath(route, "")
	var path = ""
	parts := strings.Split(route, "/")

	if strings.HasPrefix(route, "/invite/!") {
		return "/invite/!"
	}

	for _, part := range parts {
		if part == "" {
			continue
		}
		if IsNumericInput(part) {
			path += "/!"
		} else {
			path += "/" + part
		}
	}

	if !utf8.ValidString(path) {
		logger.Warn("Non utf-8 path detected, Prometheus only supports utf-8, invalid runes will be replaced with @ in metrics. Path: " + path)
		path = strings.ToValidUTF8(path, "@")
	}

	return path
}

func majorParamHash(major string, parts ...string) uint64 {
	hashStr := major

	for _, part := range parts {
		hashStr += ":" + part
	}

	return HashCRC64(hashStr)
}

func tokenInfo(token string) string {
	// aW50ZXJhY3Rpb246 is base64 for "interaction:"
	if !strings.HasPrefix(token, "aW50ZXJhY3Rpb246") {
		return "/!"
	}

	// fix padding
	if i := len(token) % 4; i != 0 {
		token += strings.Repeat("=", 4-i)
	}

	decodedPart, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return "/unknown"
	}

	interactionId := strings.Split(string(decodedPart), ":")[1]
	return "/" + interactionId
}

func GetOptimisticBucketPath(url string, method string) (string, uint64) {
	bucket := strings.Builder{}
	bucket.WriteByte('/')
	cleanUrl := strings.SplitN(url, "?", 1)[0]
	if strings.HasPrefix(cleanUrl, "/api/v") {
		cleanUrl = strings.ReplaceAll(cleanUrl, "/api/v", "")
		l := len(cleanUrl)
		i := strings.Index(cleanUrl, "/")
		cleanUrl = cleanUrl[i+1 : l]
	} else {
		// Handle unversioned endpoints
		cleanUrl = strings.ReplaceAll(cleanUrl, "/api/", "")
	}

	parts := strings.Split(cleanUrl, "/")
	numParts := len(parts)

	if numParts <= 1 {
		return cleanUrl, HashCRC64(cleanUrl)
	}

	currMajor := parts[0]
	var majorParamsHash uint64
	// ! stands for any replaceable id
	switch parts[0] {
	case MajorInvites:
		bucket.WriteString(MajorInvites)
		bucket.WriteString("/!")

		currMajor = MajorInvites
		majorParamsHash = majorParamHash(MajorInvites)
		parts = parts[2:]
	case MajorWebhooks:
		bucket.WriteString(MajorWebhooks)
		bucket.WriteByte('/')
		bucket.WriteString(parts[1])

		currMajor = MajorWebhooks
		// Webhook tokens are optional, and they fall under different top level resources
		if numParts > 2 && len(parts[2]) >= 64 {
			// webhook_id + token
			bucket.WriteString(tokenInfo(parts[2]))
			majorParamsHash = majorParamHash(MajorWebhooks, parts[1], parts[2])
			parts = parts[3:]
		} else {
			// just webhook_id
			majorParamsHash = majorParamHash(MajorWebhooks, parts[1])
			parts = parts[2:]
		}
	case MajorInteractions:
		if numParts == 4 && parts[3] == "callback" {
			// Hash 0 is a special case for "no ratelimits"
			return "/" + MajorInteractions + "/!/!/callback", 0
		}
		fallthrough
	default:
		bucket.WriteString(parts[0])
		bucket.WriteByte('/')
		bucket.WriteString(parts[1])
		currMajor = parts[0]
		majorParamsHash = majorParamHash(currMajor, parts[1])
		parts = parts[2:]
	}

	if numParts == 2 {
		return bucket.String(), majorParamsHash
	}

	// At this point, the major + id part is already accounted for (and trimmed out of 'parts')
	// In this loop, we only need to strip all remaining snowflakes, emoji names and webhook tokens(optional)
	for idx, part := range parts {
		if IsSnowflake(part) {
			//Custom rule for message DELETES older than 14d and message PATCHES older than 1h
			if currMajor == MajorChannels && idx == len(parts)-1 && parts[idx-1] == "messages" {
				createdAt, _ := GetSnowflakeCreatedAt(part)
				diff := time.Since(createdAt)

				if method == "DELETE" && diff >= 14*24*time.Hour {
					bucket.WriteString("/!14dmsg")
					continue
				} else if method == "PATCH" && diff >= 1*time.Hour {
					bucket.WriteString("/!1hmsg")
					continue
				}
			}

			bucket.WriteString("/!")
			continue
		}

		if currMajor == MajorChannels && part == "reactions" {
			// reaction put/delete fall under a different bucket from other reaction endpoints
			if method == "PUT" || method == "DELETE" {
				bucket.WriteString("/reactions/!modify")
				break
			}
			//All other reaction endpoints falls under the same bucket, so it's irrelevant if the user
			//is passing userid, emoji, etc.
			bucket.WriteString("/reactions/!/!")
			//Reactions can only be followed by emoji/userid combo, since we don't care, break
			break
		}

		// Strip webhook tokens and interaction tokens
		if (currMajor == MajorWebhooks || currMajor == MajorInteractions) && len(part) >= 64 {
			bucket.WriteString("/!")
			continue
		}

		bucket.WriteByte('/')
		bucket.WriteString(part)
	}

	return bucket.String(), majorParamsHash
}
