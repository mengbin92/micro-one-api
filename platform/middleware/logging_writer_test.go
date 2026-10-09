package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoggingWriterPreservesFlush(t *testing.T) {
	recorder := httptest.NewRecorder()
	w := &responseWriter{recorder, http.StatusOK}
	flusher, ok := any(w).(http.Flusher)
	require.True(t, ok)
	flusher.Flush()
	require.True(t, recorder.Flushed)
}
