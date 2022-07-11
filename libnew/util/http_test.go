package util

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCopyResponseToResponseWriter(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "test")
		w.Header().Set("Some-Header", "some-value")
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(433)
		w.Write([]byte("Hello, world!"))
	}))
	defer s.Close()

	resp, _ := http.Get(s.URL)

	respWriter := httptest.NewRecorder()
	err := CopyResponseToResponseWriter(resp, respWriter)

	if err != nil {
		t.Error(err)
	}
	if respWriter.Code != 433 {
		t.Errorf("Expected 433, got %d", respWriter.Code)
	}

	result := respWriter.Result()
	buf := new(bytes.Buffer)
	buf.ReadFrom(result.Body)
	body := buf.String()
	if body != "Hello, world!" {
		t.Errorf("Expected 'Hello, world!', got '%s'", respWriter.Body.String())
	}

	// To match discord, we write all headers in lower-case, but .Get converts the key to canonical form (And go expects the map to be canonicalized), so we
	// have to directly access the map in order to check the value. Annoying, but gets the job done.
	if result.Header["x-test"][0] != "test" {
		t.Errorf("Expected 'test', got '%s'", result.Header["x-test"][0])
	}
	if result.Header["some-header"][0] != "some-value" {
		t.Errorf("Expected 'some-value', got '%s'", result.Header["some-header"][0])
	}

	if result.Header["content-type"][0] != "text/plain" {
		t.Errorf("Expected 'text/plain', got '%s'", result.Header["content-type"][0])
	}

	// Regression test, letting Go handle this header when Write is called is easier than dedupping it (and more accurate too)
	if len(result.Header.Values("Content-Length")) != 0 || len(result.Header["content-length"]) != 0 {
		t.Error("Expected no Content-Length header")
	}
}
