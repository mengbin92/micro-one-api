package gemini

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/pkg/jsonx"
)

const validResponse = `{"responseId":"g-1","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"pong"},{"functionCall":{"id":"f1","name":"lookup","args":{"q":"x"}},"thoughtSignature":"opaque+/="}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"cachedContentTokenCount":3,"candidatesTokenCount":4,"thoughtsTokenCount":2,"totalTokenCount":16}}`

func TestPrepareRequestCapabilities(t *testing.T) {
	for _, body := range []string{
		`{"contents":[{"parts":[{"text":"ping"}]}]}`,
		`{"system_instruction":{"parts":[{"text":"system"}]},"contents":[{"role":"model","parts":[{"function_call":{"id":"f1","name":"lookup","args":{}},"thought_signature":"opaque+/="}]},{"role":"user","parts":[{"function_response":{"id":"f1","name":"lookup","response":{"value":1}}}]}],"tools":[{"function_declarations":[{"name":"lookup","parameters":{"type":"OBJECT","properties":{"inlineData":{"type":"STRING"}}}}]}],"generation_config":{"max_output_tokens":32}}`,
	} {
		prepared, limit, err := PrepareRequest([]byte(body))
		require.NoError(t, err)
		require.Positive(t, limit)
		require.True(t, jsonx.Valid(prepared))
		require.Contains(t, string(prepared), "maxOutputTokens")
	}
	for _, body := range []string{
		`null`, `{}`, `{"contents":[]}`, `{"contents":[],"contents":[{"parts":[{"text":"p"}]}]}`,
		`{"contents":[{"parts":[{"inlineData":{"mimeType":"image/png","data":"AA"}}]}]}`,
		`{"contents":[{"parts":[{"file_data":{"file_uri":"https://example.com"}}]}]}`,
		`{"contents":[{"parts":[{"text":"p"}]}],"cachedContent":"cachedContents/secret"}`,
		`{"contents":[{"parts":[{"text":"p"}]}],"tools":[{"googleSearch":{}}]}`,
		`{"contents":[{"parts":[{"text":"p"}]}],"generationConfig":{"responseModalities":["AUDIO"]}}`,
		`{"contents":[{"parts":[{"text":"p"}]}],"generationConfig":{"candidateCount":2}}`,
		`{"contents":[{"parts":[{"text":"p"}]}],"generationConfig":{"maxOutputTokens":-1}}`,
		`{"contents":[{"parts":[{"text":"p"}]}],"generationConfig":{"maxOutputTokens":4,"max_output_tokens":5}}`,
		`{"contents":[{"parts":[{"text":"p","functionCall":{"name":"x"}}]}]}`,
		`{"contents":[{"parts":[{"text":"p"}]}],"serviceTier":"priority"}`,
	} {
		t.Run(body, func(t *testing.T) { _, _, err := PrepareRequest([]byte(body)); require.Error(t, err) })
	}
}

func TestParseResponseMetering(t *testing.T) {
	r, err := ParseResponse([]byte(validResponse))
	require.NoError(t, err)
	require.True(t, r.Terminal)
	require.Equal(t, Usage{Prompt: 10, Cached: 3, Candidates: 4, Thoughts: 2, Total: 16}, *r.Usage)
	for _, body := range []string{
		strings.Replace(validResponse, `"cachedContentTokenCount":3`, `"cachedContentTokenCount":11`, 1),
		strings.Replace(validResponse, `"totalTokenCount":16`, `"totalTokenCount":15`, 1),
		strings.Replace(validResponse, `"thoughtsTokenCount":2`, `"thoughtsTokenCount":-2`, 1),
		strings.Replace(validResponse, `"thoughtsTokenCount":2`, `"thoughtsTokenCount":2.5`, 1),
		strings.Replace(validResponse, `"thoughtsTokenCount":2`, `"toolUsePromptTokenCount":1,"thoughtsTokenCount":2`, 1),
		strings.Replace(validResponse, `"text":"pong"`, `"inlineData":{"mimeType":"audio/wav","data":"AA"}`, 1),
		`{"usageMetadata":{"promptTokenCount":2,"totalTokenCount":2,"promptTokensDetails":[{"modality":"IMAGE","tokenCount":2}]}}`,
	} {
		_, err := ParseResponse([]byte(body))
		require.Error(t, err, body)
	}
}

func TestStreamFramingAndCompletion(t *testing.T) {
	partial := `{"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]}}]}`
	for _, tc := range []struct {
		name, wire string
		valid      bool
	}{
		{"valid", "data: " + validResponse + "\n\n", true},
		{"crlf_multiline", "data: " + strings.Replace(validResponse, `,"usageMetadata"`, ",\r\ndata: \"usageMetadata\"", 1) + "\r\n\r\n", true},
		{"usage_after_finish", "data: " + strings.Split(validResponse, `,"usageMetadata"`)[0] + "}\n\ndata: {\"usageMetadata\":{\"promptTokenCount\":2,\"totalTokenCount\":2}}\n\n", true},
		{"no_terminal", "data: " + partial + "\n\n", false},
		{"no_usage", "data: " + strings.Split(validResponse, `,"usageMetadata"`)[0] + "}\n\n", false},
		{"no_event_boundary", "data: " + validResponse + "\n", false},
		{"bad_json_after_finish", "data: " + validResponse + "\n\ndata: {bad}\n\n", false},
		{"content_after_finish", "data: " + validResponse + "\n\ndata: " + partial + "\n\n", false},
		{"done_not_gemini", "data: [DONE]\n\n", false},
		{"oversize", "data: " + strings.Repeat("x", MaxEventBytes) + "\n\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := io.ReadAll(NewStream(strings.NewReader(tc.wire)))
			if tc.valid {
				require.NoError(t, err)
				require.Contains(t, string(out), "usageMetadata")
			} else {
				require.Error(t, err)
				require.NotContains(t, string(out), "finishReason")
			}
		})
	}
}
