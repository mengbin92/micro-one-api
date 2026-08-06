package server

import (
	"io"

	"micro-one-api/pkg/jsonx"
)

func decodeJSON(r io.Reader, v interface{}) error {
	limitedReader := io.LimitReader(r, 10*1024*1024) // 10MB limit
	data, err := io.ReadAll(limitedReader)
	if err != nil {
		return err
	}
	return jsonx.Unmarshal(data, v)
}

func encodeJSON(w io.Writer, v interface{}) error {
	data, err := jsonx.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
