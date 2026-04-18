package httpapi

import "net/http"

// maxRequestBody caps the size of any single request body the
// daemon will accept. A POST /search with a well-formed body is
// well under 4 KiB; 1 MiB gives >200× headroom for
// edge cases like a hand-edited request carrying many infohashes,
// without letting a local misbehaving client (or a fetch held
// open through the browser dev tools) stream a multi-gigabyte
// JSON blob into our json.Decoder.
const maxRequestBody = 1 << 20

// withMaxBodyBytes wraps next so every incoming request has its
// Body swapped for an http.MaxBytesReader that returns an error
// (and closes the underlying connection) once more than
// maxRequestBody bytes have been read. All handlers decode JSON
// via encoding/json, which surfaces the MaxBytesReader error as
// a decode failure — identical to how they already handle
// genuinely malformed bodies.
func withMaxBodyBytes(next http.Handler, limit int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
		}
		next.ServeHTTP(w, r)
	})
}
