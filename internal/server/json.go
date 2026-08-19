package server

import (
	"errors"
	"io"
	"net/http"

	"micro-one-api/pkg/jsonx"
)

func decodeJSON(r io.Reader, v interface{}) error {
	const maxSize = jsonRequestBodyLimit
	limitedReader := io.LimitReader(r, maxSize+1)
	data, err := io.ReadAll(limitedReader)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return errRequestBodyTooLarge
		}
		return err
	}
	if int64(len(data)) > maxSize {
		return errRequestBodyTooLarge
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
