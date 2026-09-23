package adaptor

import (
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/domain/upstream/provider"
)

func TestResponsesConversionStateCapabilities(t *testing.T) {
	for _, tc := range []struct{ body, field string }{
		{`{"input":"hi"}`, ""},
		{`{"store":false,"background":false,"previous_response_id":null,"conversation":null}`, ""},
		{`{"previous_response_id":"resp_1"}`, "previous_response_id"},
		{`{"conversation":"conv_1"}`, "conversation"},
		{`{"conversation":{"id":"conv_1"}}`, "conversation"},
		{`{"store":true}`, "store"},
		{`{"background":true}`, "background"},
	} {
		t.Run(tc.body, func(t *testing.T) {
			err := ValidateResponsesConversion([]byte(tc.body))
			if tc.field == "" {
				require.NoError(t, err)
				return
			}
			var capability *provider.CapabilityError
			require.ErrorAs(t, err, &capability)
			require.Contains(t, capability.Feature, tc.field)
		})
	}
}

func TestResponsesConversionOtherUpstreams(t *testing.T) {
	for _, ad := range []Adaptor{NewAzureAdaptor(nil, nil, ""), NewGeminiAdaptor(nil, nil)} {
		t.Run(ad.Name(), func(t *testing.T) {
			rc := &RelayContext{InboundFormat: FormatOpenAIResponses, ResolvedModel: "mapped-model"}
			format, body, err := ad.ConvertRequest(rc, FormatOpenAIResponses, []byte(`{"model":"m","input":"ping","stream":true}`))
			require.NoError(t, err)
			require.NotEqual(t, FormatOpenAIResponses, format)
			require.Contains(t, string(body), "ping")
			_, _, err = ad.ConvertRequest(rc, FormatOpenAIResponses, []byte(`{"model":"m","input":"ping","background":true}`))
			var capability *provider.CapabilityError
			require.ErrorAs(t, err, &capability)
		})
	}
}
