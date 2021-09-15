package benchmarks

import (
	"github.com/germanoeich/nirn-proxy/lib"
	"regexp"
	"strings"
	"testing"
)

/*

First regex: \/([a-z-]+)\/(?:[0-9]{17,19})
match /a-z/0-9/
replace channels/id, guilds/id, webhooks/id with <major>/<id> or with the match

Second regex: \/reactions\/[^/]+
Matches /reactions/* where * is the emoji id. Replaces it with /reactions/:id

Third regex: \/reactions\/:id\/[^/]+
Matches /reactions/:id/* where * is the user id or @me. Replaces it with /reactions/:id/:userID

Fourth regex: ^\/webhooks\/(\d+)\/[A-Za-z0-9-_]{64,}
Matches /webhooks/id/token, replaces with /webhooks/$1/:token where $1 is the id

 */
var majorsRegex = regexp.MustCompile(`/(guilds|channels|webhooks)/[0-9]{17,19}`)
func GetBucketPath(url string) string {
	var bucket string
	bucket += majorsRegex.ReplaceAllString(url, `/$1/:id`)
	return bucket
}

func BenchmarkRegex(b *testing.B) {
	b.ReportAllocs()
	url := `/guilds/121124124124124124/`
	for n := 0; n < b.N; n++ {
		GetBucketPath(url)
	}
}

// ====================================================


func BenchmarkOptimistic(b *testing.B) {
	b.ReportAllocs()
	url := `guilds/121124124124124124/pins/121124124124124124`
	for n := 0; n < b.N; n++ {
		lib.GetOptimisticBucketPath(url, "GET")
	}
}

// ====================================================

var prefixUrl = `/api/v9/guilds/121124124124124124/pins/121124124124124124`

func BenchmarkPrefixStripReplaceAllNaive(b *testing.B) {
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		strings.ReplaceAll(prefixUrl, "/api/v10/", "")
		strings.ReplaceAll(prefixUrl, "/api/v9/", "")
		strings.ReplaceAll(prefixUrl, "/api/v8/", "")
		strings.ReplaceAll(prefixUrl, "/api/v7/", "")
		strings.ReplaceAll(prefixUrl, "/api/v6/", "")
	}
}

func BenchmarkPrefixStripReplaceAllSmart(b *testing.B) {
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		var clean string
		if strings.HasPrefix(prefixUrl, "/api/v") {
			clean = strings.ReplaceAll(prefixUrl, "/api/v", "")
			l := len(clean)
			i := strings.Index(clean, "/")
			clean = clean[i:l]
		}
	}
}