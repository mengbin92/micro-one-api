package provider

import (
	"net/http"
	"reflect"
	"testing"
)

func TestAnthropicForwardPreservesFeatureHeaders(t *testing.T) {
	for _, version := range []string{"", "2023-01-01"} {
		source := http.Header{}
		source.Set("Authorization", "Bearer gateway-key")
		source.Set("x-api-key", "gateway-key")
		source.Set("anthropic-version", version)
		source.Add("anthropic-beta", "opaque-beta-one")
		source.Add("anthropic-beta", "opaque-beta-two")
		source.Set("Cookie", "gateway_session=secret")
		source.Set("Accept-Encoding", "gzip, br")
		source.Set("Connection", "keep-alive, x-private-token")
		source.Set("X-Private-Token", "gateway-only")
		destination := http.Header{}
		p := &AnthropicProvider{apiKey: "upstream-key"}
		p.anthropicSetForwardHeaders(destination, source)
		wantVersion := version
		if wantVersion == "" {
			wantVersion = "2023-06-01"
		}
		if destination.Get("anthropic-version") != wantVersion || !reflect.DeepEqual(destination.Values("anthropic-beta"), source.Values("anthropic-beta")) {
			t.Fatalf("feature headers changed: %v", destination)
		}
		if destination.Get("Authorization") != "" || destination.Get("x-api-key") != "upstream-key" {
			t.Fatal("gateway credentials leaked upstream")
		}
		for _, key := range []string{"Cookie", "Accept-Encoding", "Connection", "X-Private-Token"} {
			if destination.Get(key) != "" {
				t.Errorf("%s must not be forwarded", key)
			}
		}
	}
}
