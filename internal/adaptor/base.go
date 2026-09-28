package adaptor

import (
	"net/http"

	"micro-one-api/domain/upstream/provider"
)

// baseAdaptor provides shared HTTP/header helpers used by all concrete
// adaptors. It intentionally has no state: each adaptor embeds it for its
// methods and keeps its own provider reference.
type baseAdaptor struct{}

// copyForwardHeaders shares the provider's credential, hop-by-hop and
// compression filtering for adaptor-built requests.
func (baseAdaptor) copyForwardHeaders(dst, src http.Header) {
	provider.CopyForwardHeaders(dst, src)
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
