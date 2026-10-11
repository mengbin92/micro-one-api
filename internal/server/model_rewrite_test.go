package server

import (
	"bytes"
	"testing"

	"micro-one-api/pkg/jsonx"
)

func TestModelRewritePreservesRequestNumbers(t *testing.T) {
	for name, rewrite := range map[string]func([]byte) []byte{
		"raw HTTP":     func(body []byte) []byte { return rewriteRawModel(body, "upstream") },
		"adaptor HTTP": func(body []byte) []byte { return ensureRawModel(body, "upstream") },
		"orchestrator": func(body []byte) []byte { return rewriteRequestModel(body, "upstream") },
		"WebSocket":    func(body []byte) []byte { return rewriteOpenAIWSModel(body, "public", "upstream") },
	} {
		t.Run(name, func(t *testing.T) {
			for _, input := range []string{"null", "[]", `"text"`, "invalid JSON"} {
				body := []byte(input)
				if got := rewrite(body); !bytes.Equal(got, body) {
					t.Fatalf("non-object input %q changed to %q", input, got)
				}
			}
			body := []byte(`{"model":"public","seed":9007199254740993,"input":{"id":9223372036854775807},"temperature":0.1234567890123456789}`)
			var got map[string]jsonx.RawMessage
			if err := jsonx.Unmarshal(rewrite(body), &got); err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]string{
				"model": `"upstream"`, "seed": `9007199254740993`,
				"input": `{"id":9223372036854775807}`, "temperature": `0.1234567890123456789`,
			} {
				if string(got[key]) != want {
					t.Errorf("%s = %s, want %s", key, got[key], want)
				}
			}
		})
	}
}
