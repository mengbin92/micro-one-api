package pagination

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestQueryBoundPagination(t *testing.T) {
	token := Token(50, "query")
	offset, err := Offset(token, "query")
	require.NoError(t, err)
	require.Equal(t, 50, offset)
	_, err = Offset(token, "other")
	require.Error(t, err)
	_, err = Offset(Token(-1, "query"), "query")
	require.Error(t, err)
	_, err = Offset("not a token", "query")
	require.Error(t, err)
	_, err = Offset(Token(int(^uint(0)>>1), "query"), "query")
	require.Error(t, err)
}
