package lib

import (
	"io"
	"net/http"
	"strings"
)

func copyHeader(dst, src http.Header) {
	dst["Date"] = nil
	dst["Content-Type"] = nil
	for k, vv := range src {
		for _, v := range vv {
			if k != "Content-Length" {
				dst[strings.ToLower(k)] = []string{v}
			}
		}
	}
}

func CopyResponseToResponseWriter(resp *http.Response, respWriter *http.ResponseWriter) error {
	writer := *respWriter
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writer.WriteHeader(500)
		_, _ = writer.Write([]byte(err.Error()))
		return err
	}

	copyHeader(writer.Header(), resp.Header)
	writer.WriteHeader(resp.StatusCode)

	_, err = writer.Write(body)
	if err != nil {
		return err
	}
	return nil
}
