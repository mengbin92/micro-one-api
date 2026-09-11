package server

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestEmbeddingCostBound(t *testing.T) {
	bound := embeddingCostBound("/embeddings", 1, "text-embedding-3-small", []byte(`{"input":["中文","hello"],"model":"alias"}`))
	require.True(t, bound.Valid())
	require.EqualValues(t, len("中文")+len("hello")+32, bound.InputTokens)
	for _, body := range []string{`{"input":[1,2]}`, `{"input":"text","tools":[]}`, `{"input":[]}`, `{"input":""}`, `{"input":null}`, `{`} {
		require.False(t, embeddingCostBound("/embeddings", 1, "text-embedding-3-small", []byte(body)).Valid(), body)
	}
	require.False(t, embeddingCostBound("/chat/completions", 1, "text-embedding-3-small", []byte(`{"input":"text"}`)).Valid())
	require.False(t, embeddingCostBound("/embeddings", 1, "unknown", []byte(`{"input":"text"}`)).Valid())
	require.False(t, embeddingCostBound("/embeddings", 3, "text-embedding-3-small", []byte(`{"input":"text"}`)).Valid())
}
