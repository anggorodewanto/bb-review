package server

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
)

// minGzipSize is the smallest body worth compressing: tiny responses grow when
// gzipped and the SPA never notices the difference.
const minGzipSize = 1 << 10

// writeMaybeGzipped writes a buffered body, gzip-encoding it when the client
// accepts gzip and the body is large enough. Callers set their own headers first.
func writeMaybeGzipped(w http.ResponseWriter, r *http.Request, body []byte) {
	w.Header().Set("Vary", "Accept-Encoding")
	if len(body) <= minGzipSize || !acceptsGzip(r) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return
	}

	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	_, writeErr := writer.Write(body)
	closeErr := writer.Close()
	if writeErr != nil || closeErr != nil {
		// Compression failed: fall back to the plain body, no Content-Encoding.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return
	}

	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

// acceptsGzip reports whether the request advertises gzip and does not forbid it
// with "gzip;q=0".
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		fields := strings.Split(part, ";")
		if !strings.EqualFold(strings.TrimSpace(fields[0]), "gzip") {
			continue
		}
		for _, param := range fields[1:] {
			if strings.EqualFold(strings.TrimSpace(param), "q=0") {
				return false
			}
		}
		return true
	}
	return false
}
