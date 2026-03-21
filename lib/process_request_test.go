package lib

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func ensureTestLogger() {
	if logger == nil {
		SetLogger(logrus.New())
	}
}

func TestDoDiscordReqForwardsRequestBody(t *testing.T) {
	ensureTestLogger()

	expectedBody := "payload"
	header := make(http.Header)
	header.Set("Content-Type", "application/json")

	var capturedBody string
	var capturedContentType string

	originalClient := client
	client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		data, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		capturedBody = string(data)
		capturedContentType = req.Header.Get("Content-Type")
		_ = req.Body.Close()

		return &http.Response{
			StatusCode: http.StatusNoContent,
			Status:     "204 No Content",
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBuffer(nil)),
			Request:    req,
		}, nil
	})}
	t.Cleanup(func() { client = originalClient })

	resp, err := doDiscordReq(context.Background(), "/api/v10/webhooks/123", http.MethodPost, io.NopCloser(bytes.NewBufferString(expectedBody)), header, "")
	if err != nil {
		t.Fatalf("doDiscordReq returned error: %v", err)
	}
	defer resp.Body.Close()

	if capturedBody != expectedBody {
		t.Fatalf("expected upstream to receive body %q, got %q", expectedBody, capturedBody)
	}
	if capturedContentType != "application/json" {
		t.Fatalf("expected Content-Type header to be forwarded, got %q", capturedContentType)
	}
}

func TestProcessRequestPreservesResponseBody(t *testing.T) {
	ensureTestLogger()

	const (
		expectedRequestBody = "request body"
		upstreamBody        = "{\"message\":\"Unknown Webhook\",\"code\":10015}"
	)

	var capturedBody string

	originalClient := client
	client = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		data, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		capturedBody = string(data)
		_ = req.Body.Close()

		return &http.Response{
			StatusCode: http.StatusNotFound,
			Status:     "404 Not Found",
			Header: http.Header{
				"Content-Type":      {"application/json"},
				"X-Ratelimit-Scope": {"user"},
			},
			Body:    io.NopCloser(bytes.NewBufferString(upstreamBody)),
			Request: req,
		}, nil
	})}
	t.Cleanup(func() { client = originalClient })

	originalTimeout := contextTimeout
	contextTimeout = time.Second
	t.Cleanup(func() { contextTimeout = originalTimeout })

	req := httptest.NewRequest(http.MethodPost, "http://localhost/api/v10/webhooks/123", bytes.NewBufferString(expectedRequestBody))
	recorder := httptest.NewRecorder()
	writer := http.ResponseWriter(recorder)

	item := &QueueItem{
		Req: req,
		Res: &writer,
	}

	resp, err := ProcessRequest(context.Background(), item)
	if err != nil {
		t.Fatalf("ProcessRequest returned error: %v", err)
	}
	defer resp.Body.Close()

	if capturedBody != expectedRequestBody {
		t.Fatalf("expected upstream to receive body %q, got %q", expectedRequestBody, capturedBody)
	}
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("unexpected status written to client: got %d want %d", recorder.Code, http.StatusNotFound)
	}
	if recorder.Body.String() != upstreamBody {
		t.Fatalf("unexpected body written to client: got %q want %q", recorder.Body.String(), upstreamBody)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}
	if string(bodyBytes) != upstreamBody {
		t.Fatalf("expected preserved response body %q, got %q", upstreamBody, string(bodyBytes))
	}
}
