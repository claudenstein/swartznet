package httpapi

import "net/http"

// maxRequestBody caps every request body at 1 MiB. Overflow surfaces to
// handlers as a read/decode failure (→ 400), never as unbounded memory use.
const maxRequestBody = 1 << 20

// withMaxBodyBytes swaps each request's Body for a MaxBytesReader. It never
// reads the body itself, so it composes as the outermost wrapper without
// changing rejection behavior of inner middleware.
func withMaxBodyBytes(next http.Handler, limit int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}
