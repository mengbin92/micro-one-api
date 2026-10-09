package xhttp

import (
	"fmt"
	"io"
)

const MaxExternalResponseBody = 1 << 20

// ReadBody rejects oversized responses instead of parsing a truncated document.
func ReadBody(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("external response exceeds %d bytes", max)
	}
	return b, nil
}
