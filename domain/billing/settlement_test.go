package billing

import (
	"github.com/stretchr/testify/require"
	"micro-one-api/pkg/jsonx"
	"testing"
)

func TestSettlementPayloadCanonicalDigest(t *testing.T) {
	payload := SettlementPayload{ReservationID: "res", ActualTokens: 1, Success: true, Usage: jsonx.RawMessage(`{"PromptTokens":9007199254740993,"Envelope":null}`)}
	original, err := payload.Digest()
	require.NoError(t, err)
	payload.Usage = jsonx.RawMessage(`{ "Envelope": null, "PromptTokens":9007199254740993 }`)
	replay, err := payload.Digest()
	require.NoError(t, err)
	require.Equal(t, original, replay)
	payload.Usage = jsonx.RawMessage(`{"PromptTokens":9007199254740992,"Envelope":null}`)
	changed, err := payload.Digest()
	require.NoError(t, err)
	require.NotEqual(t, original, changed, "hash must preserve integer precision")
}
func TestSettlementPayloadRejectsInvalidUsageObject(t *testing.T) {
	for _, value := range []string{"null", "[]", "{} {}", "{bad"} {
		_, err := (SettlementPayload{Usage: jsonx.RawMessage(value)}).Digest()
		require.Error(t, err)
	}
}
