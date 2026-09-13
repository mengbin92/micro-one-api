package ordering

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestOrdering(t *testing.T) {
	fields, err := Parse("key desc,id asc", "key", "id")
	require.NoError(t, err)
	require.Equal(t, []Field{{Name: "key", Desc: true}, {Name: "id"}}, fields)
	for _, s := range []string{"key DESC; DROP TABLE users", "secret asc", "key invalid", "key,key", "key,,id"} {
		_, err := Parse(s, "key", "id")
		require.Error(t, err, s)
	}
}
