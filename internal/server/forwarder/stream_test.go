package forwarder

import (
	"context"
	"sync"
	"testing"
	"time"
)

type endlessReadCloser struct {
	closed chan struct{}
	once   sync.Once
}

func (r *endlessReadCloser) Read(p []byte) (int, error) {
	p[0] = 'x'
	return 1, nil
}

func (r *endlessReadCloser) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}

func TestReadChunksStopsWhenConsumerAbandonsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	body := &endlessReadCloser{closed: make(chan struct{})}
	_ = readChunks(ctx, body)
	cancel()

	select {
	case <-body.closed:
	case <-time.After(time.Second):
		t.Fatal("readChunks did not close the upstream body after cancellation")
	}
}
