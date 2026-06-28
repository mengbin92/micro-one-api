// Package jsonx provides JSON utilities using sonic for high performance.
package jsonx

import (
	"github.com/bytedance/sonic"
)

// Marshal serializes a value to JSON using sonic.
func Marshal(v any) ([]byte, error) {
	return sonic.Marshal(v)
}

// Unmarshal deserializes JSON data into a value using sonic.
func Unmarshal(data []byte, v any) error {
	return sonic.Unmarshal(data, v)
}
