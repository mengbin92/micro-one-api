package filtering

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestEqualities(t *testing.T) {
	f, err := Equalities(`key = "a_b% AND \\\"x" AND status = "enabled"`, "key", "status")
	require.NoError(t, err)
	require.Equal(t, "enabled", f["status"])
	for _, s := range []string{`key = "a" OR key = "b"`, `key = "a" AND`, `key = "a" AND key = "b"`, `missing = "x"`, `key = 123`, `key = "x"; DROP TABLE routing_groups`, `key = "\q"`} {
		_, err := Equalities(s, "key", "status")
		require.Error(t, err, s)
	}
}
