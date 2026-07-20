package httpapi

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// withCSRFGuard rejects state-changing cross-origin requests. The security
// model of the unauthenticated localhost API is exactly this guard plus the
// loopback-default bind: browsers cannot forge loopback requests because the
// Host header must be loopback (DNS-rebind defense) and any present
// Origin/Referer must be loopback too.
//
// The DNS-rebind Host-header check applies to EVERY method, including reads: a
// rebind attacker who points a hostname at the loopback API sends a non-loopback
// Host, and reads leak privacy-sensitive state (torrent list, publisher
// identity, published keywords). Exempting GET/HEAD/OPTIONS from the Host check
// left every read endpoint open. The Origin/Referer cross-origin CSRF check
// stays write-only (reads carry no state-change/CSRF risk). This matches the
// documented localhost-only model — writes already required a loopback Host.
func withCSRFGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHostHeader(r.Host) {
			http.Error(w, "forbidden: non-loopback Host", http.StatusForbidden)
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
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

// isLoopbackOrigin reports whether an Origin/Referer value points at a
// loopback host. Unparseable values and host-less values (e.g. "null") fail
// closed.
func isLoopbackOrigin(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	return isLoopbackHostHeader(u.Host)
}

// isLoopbackHostHeader reports whether a Host-header-shaped string names a
// loopback endpoint: any 127.0.0.0/8 address, ::1, or the literal
// "localhost" (case-insensitive). No other DNS name is ever trusted — even
// one that resolves to 127.0.0.1 — because attacker-controlled DNS is exactly
// the rebinding attack this defends against.
func isLoopbackHostHeader(host string) bool {
	if host == "" {
		return false
	}
	h := host
	if hp, _, err := net.SplitHostPort(host); err == nil {
		h = hp
	}
	h = strings.TrimPrefix(h, "[")
	h = strings.TrimSuffix(h, "]")
	if strings.EqualFold(h, "localhost") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
