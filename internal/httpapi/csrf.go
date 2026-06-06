package httpapi

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// The HTTP API binds loopback by default and carries no
// authentication: it trusts the OS to keep off-host clients out.
// A browser, however, can be steered by any page the user happens
// to be visiting into issuing cross-origin requests at
// http://localhost:7654 — POST is a CORS "simple" method, so no
// preflight stands in the way. Without a guard, a hostile page can
// pause/resume/remove torrents, flip capabilities, change rate
// limits, or mutate reputation/Bloom state behind the user's back,
// and a DNS-rebinding attack can defeat a naive same-origin
// assumption by pointing an attacker-controlled hostname at
// 127.0.0.1.
//
// withCSRFGuard closes that surface for every state-mutating
// request (anything other than GET/HEAD/OPTIONS):
//
//   - If an Origin or Referer header is present, its host:port must
//     resolve to a loopback address. Same-origin browser requests
//     from the embedded web UI always carry a loopback Origin, so
//     they pass; a cross-origin page carries its own Origin and is
//     rejected.
//   - The request's Host header must itself be a loopback host
//     (127.0.0.0/8, ::1, or the literal "localhost"). This rejects
//     DNS-rebinding, where the browser sends Host: evil.example for
//     a name that now points at 127.0.0.1.
//
// Non-browser clients (CLI, curl) send neither Origin nor Referer
// and connect with a loopback Host, so they are unaffected. The
// guard fails closed: an unparseable or non-loopback Host or Origin
// yields 403.
func withCSRFGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			// Read-only methods (and CORS preflight) do not
			// change state, so they are exempt.
			next.ServeHTTP(w, r)
			return
		}

		// Host must be loopback — blocks DNS rebinding where the
		// resolved name now maps to 127.0.0.1 but the browser
		// still sends the attacker's hostname.
		if !isLoopbackHostHeader(r.Host) {
			http.Error(w, "forbidden: non-loopback Host", http.StatusForbidden)
			return
		}

		// If the browser declared an origin, it must be loopback.
		// curl/CLI clients send neither header and are allowed.
		if origin := r.Header.Get("Origin"); origin != "" {
			if !isLoopbackOrigin(origin) {
				http.Error(w, "forbidden: cross-origin request", http.StatusForbidden)
				return
			}
		} else if referer := r.Header.Get("Referer"); referer != "" {
			if !isLoopbackOrigin(referer) {
				http.Error(w, "forbidden: cross-origin Referer", http.StatusForbidden)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// isLoopbackOrigin reports whether the host component of an Origin
// or Referer URL is a loopback address. A missing/unparseable URL,
// or one with a non-loopback host, returns false (fail closed).
func isLoopbackOrigin(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	return isLoopbackHostHeader(u.Host)
}

// isLoopbackHostHeader reports whether a Host header value (which
// may carry a port, e.g. "127.0.0.1:7654" or "[::1]:7654") names a
// loopback host. The literal "localhost" is treated as loopback;
// any other DNS name is not (it could resolve off-host or be a
// rebinding target).
func isLoopbackHostHeader(host string) bool {
	if host == "" {
		return false
	}
	h := host
	if hostOnly, _, err := net.SplitHostPort(host); err == nil {
		h = hostOnly
	}
	h = strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")
	if strings.EqualFold(h, "localhost") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
