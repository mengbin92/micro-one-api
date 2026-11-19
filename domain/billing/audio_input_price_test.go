package billing

import (
	"testing"

	"github.com/stretchr/testify/require"

	"micro-one-api/domain/routing"
)

func TestAudioInputPriceUsesMeasuredUnitsAndExactExchange(t *testing.T) {
	prices, err := ParseAudioInputPrices(`{
		"stepaudio-2.5-asr":{"yuan":"0.45","yuan_per_usd":"6.8"},
		"stepaudio-2.5-tts":{"yuan":"580","yuan_per_usd":"6.8"}
	}`)
	require.NoError(t, err)

	asr := routing.CostBound{Protocol: "stepaudio_asr_sse", UpstreamModel: "stepaudio-2.5-asr", AudioUnits: 3_600_000_000}
	cost, err := prices[asr.UpstreamModel].Quota(asr, 1, 10_000)
	require.NoError(t, err)
	require.EqualValues(t, 662, cost)

	tts := routing.CostBound{Protocol: "stepaudio_tts", UpstreamModel: "stepaudio-2.5-tts", AudioUnits: 2_000}
	cost, err = prices[tts.UpstreamModel].Quota(tts, 1, 10_000)
	require.NoError(t, err)
	require.EqualValues(t, 853, cost)

	asr.Protocol = tts.Protocol
	_, err = prices[asr.UpstreamModel].Quota(asr, 1, 10_000)
	require.Error(t, err)
	_, err = ParseAudioInputPrices(`{"stepaudio-2.5-asr":{"yuan":"0.45","yuan_per_usd":"0"}}`)
	require.Error(t, err)
}

func TestAudioInputPriceRejectsBlankButAllowsExplicitEmptyMap(t *testing.T) {
	for _, raw := range []string{"", " \t\n"} {
		_, err := ParseAudioInputPrices(raw)
		require.Error(t, err)
	}
	prices, err := ParseAudioInputPrices(`{}`)
	require.NoError(t, err)
	require.NotNil(t, prices)
	require.Empty(t, prices)
}
