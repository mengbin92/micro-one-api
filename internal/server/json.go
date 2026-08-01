package server

import (
	"io"

	"github.com/bytedance/sonic"
)

func decodeJSON(r io.Reader, v interface{}) error {
	limitedReader := io.LimitReader(r, 10*1024*1024) // 10MB limit
	data, err := io.ReadAll(limitedReader)
	if err != nil {
		return err
	}
	return sonic.Unmarshal(data, v)
}

func encodeJSON(w io.Writer, v interface{}) error {
	data, err := sonic.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
