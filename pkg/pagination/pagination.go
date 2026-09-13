package pagination

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"micro-one-api/pkg/jsonx"
)

type Cursor struct {
	Offset int    `json:"offset"`
	Query  string `json:"query"`
}

func Offset(token, query string) (int, error) {
	if token == "" {
		return 0, nil
	}
	if len(token) > 512 {
		return 0, fmt.Errorf("invalid page token")
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, fmt.Errorf("invalid page token")
	}
	var c Cursor
	if jsonx.Unmarshal(data, &c) != nil || c.Offset < 0 || c.Offset > int(^uint(0)>>1)-201 || c.Query != fingerprint(query) {
		return 0, fmt.Errorf("page token does not match query")
	}
	return c.Offset, nil
}
func Token(offset int, query string) string {
	data, _ := jsonx.Marshal(Cursor{Offset: offset, Query: fingerprint(query)})
	return base64.RawURLEncoding.EncodeToString(data)
}
func fingerprint(query string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(query))) }
