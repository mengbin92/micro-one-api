package biz

import "testing"

func TestRelayModelName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "glm-5.3", want: "glm-5.3"},
		{input: "glm-5.3[1m]", want: "glm-5.3"},
		{input: " GLM-5.3[1M] ", want: "GLM-5.3"},
		{input: "model[1m]-preview", want: "model[1m]-preview"},
	}
	for _, tt := range tests {
		if got := RelayModelName(tt.input); got != tt.want {
			t.Errorf("RelayModelName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
