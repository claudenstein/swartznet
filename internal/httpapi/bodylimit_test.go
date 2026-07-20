package httpapi

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWithMaxBodyBytes(t *testing.T) {
	var readErr error
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
	})
	h := withMaxBodyBytes(next, 8)

	t.Run("under limit", func(t *testing.T) {
		readErr = nil
		req := httptest.NewRequest("POST", "http://localhost/x", bytes.NewReader([]byte("tiny")))
		h.ServeHTTP(httptest.NewRecorder(), req)
		if readErr != nil {
			t.Fatalf("small body errored: %v", readErr)
		}
	})

	t.Run("over limit", func(t *testing.T) {
		readErr = nil
		req := httptest.NewRequest("POST", "http://localhost/x", bytes.NewReader(bytes.Repeat([]byte("x"), 64)))
		h.ServeHTTP(httptest.NewRecorder(), req)
		var mbe *http.MaxBytesError
		if !errors.As(readErr, &mbe) {
			t.Fatalf("read error = %T %v, want *http.MaxBytesError", readErr, readErr)
		}
	})
}
