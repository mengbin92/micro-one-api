package jsonx

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUniqueJSONRejectsProjectionAmbiguityAndComplexity(t *testing.T) {
	for _, raw := range []string{`{"a":0,"a":1}`, `{"a":{"x":0,"x":0}}`, `{} {}`, `{"a":[`, strings.Repeat("[", 34) + "0" + strings.Repeat("]", 34)} {
		require.Error(t, ValidateUnique([]byte(raw)))
	}
	for _, raw := range []string{`{"zero":0,"null":null}`, `[{},[1],true]`, `"value"`} {
		require.NoError(t, ValidateUnique([]byte(raw)))
	}
}
