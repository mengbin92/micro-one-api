package adaptor

import (
	"net/http"
	"strings"
)

// baseAdaptor provides shared HTTP/header helpers used by all concrete
// adaptors. It intentionally has no state: each adaptor embeds it for its
// methods and keeps its own provider reference.
type baseAdaptor struct{}

// copyForwardHeaders copies non-hop-by-hop, non-authorization headers from src
// to dst. It mirrors provider.copyForwardHeaders so adaptors do not need to
// depend on the provider package's unexported helper.
func (baseAdaptor) copyForwardHeaders(dst, src http.Header) {
	for key, values := range src {
		if isHopByHopHeader(key) || strings.EqualFold(key, "Authorization") {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func isHopByHopHeader(key string) bool {
	switch strings.ToLower(key) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

// truncateBody returns a size-capped copy of body for inclusion in error
// messages. Upstream error bodies may leak internal account identifiers (the
// upstream's own view of the subscription, request ids, etc.), so callers
// should never forward them verbatim to the client. We cap at 512 bytes and
// mark truncation.
func truncateBody(body []byte) string {
	const max = 512
	if len(body) <= max {
		return string(body)
	}
	return string(body[:max]) + "...(truncated)"
}
