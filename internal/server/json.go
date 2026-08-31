package server

import (
	"errors"
	"io"
	"net/http"

	"micro-one-api/pkg/jsonx"
)

func decodeJSON(r io.Reader, v any) error {
	const maxSize = jsonRequestBodyLimit
	limitedReader := io.LimitReader(r, maxSize+1)
	data, err := io.ReadAll(limitedReader)
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return errRequestBodyTooLarge
		}
		return err
	}
	if int64(len(data)) > maxSize {
		return errRequestBodyTooLarge
	}
	return jsonx.Unmarshal(data, v)
}

func encodeJSON(w io.Writer, v any) error {
	data, err := jsonx.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
