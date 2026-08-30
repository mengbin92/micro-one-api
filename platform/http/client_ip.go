package xhttp

import (
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
)

const trustedProxyCIDRsEnv = "TRUSTED_PROXY_CIDRS"

// TrustedProxyCIDRsFromEnv parses TRUSTED_PROXY_CIDRS. Invalid entries are
// ignored, which fails closed by treating those networks as untrusted.
func TrustedProxyCIDRsFromEnv(envNames ...string) []netip.Prefix {
	envName := trustedProxyCIDRsEnv
	if len(envNames) > 0 && envNames[0] != "" {
		envName = envNames[0]
	}
	var prefixes []netip.Prefix
	for _, raw := range strings.Split(os.Getenv(envName), ",") {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err == nil {
			prefixes = append(prefixes, prefix.Masked())
		}
	}
	return prefixes
}

// ClientIP returns the direct peer unless that peer is explicitly trusted.
// For trusted proxies it walks X-Forwarded-For from right to left and returns
// the first untrusted hop, preventing callers from choosing the leftmost IP.
func ClientIP(r *http.Request, trusted []netip.Prefix) string {
	if r == nil {
		return ""
	}
	peer, ok := parseRemoteIP(r.RemoteAddr)
	if !ok {
		return r.RemoteAddr
	}
	if !ipInPrefixes(peer, trusted) {
		return peer.String()
	}

	forwarded := strings.Join(r.Header.Values("X-Forwarded-For"), ",")
	if forwarded != "" {
		hops := strings.Split(forwarded, ",")
		current := peer
		for i := len(hops) - 1; i >= 0; i-- {
			hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
			if err != nil {
				return current.String()
			}
			current = hop.Unmap()
			if !ipInPrefixes(current, trusted) {
				return current.String()
			}
		}
		return current.String()
	}

	if realIP, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); err == nil {
		return realIP.Unmap().String()
	}
	return peer.String()
}

func parseRemoteIP(remoteAddr string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		host = strings.TrimSpace(remoteAddr)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return ip.Unmap(), true
}

func ipInPrefixes(ip netip.Addr, prefixes []netip.Prefix) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}
