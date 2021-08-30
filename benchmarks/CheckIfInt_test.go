package benchmarks

import (
	"regexp"
	"strconv"
	"testing"
	"unicode"
)

var url = `121124124124124124`

func BenchmarkAtoi(b *testing.B) {
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		_, err := strconv.Atoi(url)
		if err != nil {
		}
	}
}

func BenchmarkIsDigit(b *testing.B) {
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		for _, d := range url {
			if !unicode.IsDigit(d) {
			}
		}
	}
}

func BenchmarkManual(b *testing.B) {
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		for _, d := range url {
			if d < '0' || d > '9' {
			}
		}
	}
}

var r = regexp.MustCompile(`^[0-9]+$`)
func BenchmarkRegexInt(b *testing.B) {
	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		r.MatchString(url)
	}
}