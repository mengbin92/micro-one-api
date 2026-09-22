package server

import (
	relaybiz "micro-one-api/internal/biz"
	"net/http"
	"strconv"
)

// Only an actually selected, authorized source is exposed. Names, keys and
// upstream URLs remain private. The root request id is set at route entry.
func setErrorSource(w http.ResponseWriter, channelID, accountID int64) {
	if accountID > 0 {
		w.Header().Set("X-Source-Kind", "subscription")
		w.Header().Set("X-Source-ID", strconv.FormatInt(accountID, 10))
	} else if channelID > 0 {
		w.Header().Set("X-Source-Kind", "channel")
		w.Header().Set("X-Source-ID", strconv.FormatInt(channelID, 10))
	}
}
func setErrorChannel(w http.ResponseWriter, ch *relaybiz.Channel) {
	if ch != nil {
		setErrorSource(w, ch.ID, ch.SubscriptionAccountID)
	}
}
func errorIdentity(w http.ResponseWriter, payload map[string]any) map[string]any {
	if id := w.Header().Get("X-Request-ID"); id != "" {
		payload["request_id"] = id
	}
	if id := w.Header().Get("X-Source-ID"); id != "" {
		payload["source_id"] = id
		payload["source_kind"] = w.Header().Get("X-Source-Kind")
	}
	return payload
}
