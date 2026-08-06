// Package jsonx provides JSON utilities using sonic for high performance.
// It is a drop-in replacement for the standard library encoding/json,
// backed by sonic.ConfigStd for full compatibility with encoding/json semantics.
package jsonx

import (
	"io"

	stdjson "encoding/json"

	"github.com/bytedance/sonic"
)

var api = sonic.ConfigStd

// Number represents a JSON number literal, kept identical to encoding/json.Number
// so type switches on interface values decoded with UseNumber keep working.
type Number = stdjson.Number

// RawMessage is a raw encoded JSON value, identical to encoding/json.RawMessage.
type RawMessage = stdjson.RawMessage

// Marshaler is the interface implemented by types that can marshal themselves into valid JSON.
type Marshaler = stdjson.Marshaler

// Unmarshaler is the interface implemented by types that can unmarshal a JSON description of themselves.
type Unmarshaler = stdjson.Unmarshaler

// Marshal serializes a value to JSON using sonic.
func Marshal(v any) ([]byte, error) {
	return api.Marshal(v)
}

// MarshalIndent is like Marshal but applies Indent to format the output.
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	return api.MarshalIndent(v, prefix, indent)
}

// MarshalToString returns the JSON encoding string of v.
func MarshalToString(v any) (string, error) {
	return api.MarshalToString(v)
}

// Unmarshal deserializes JSON data into a value using sonic.
func Unmarshal(data []byte, v any) error {
	return api.Unmarshal(data, v)
}

// UnmarshalFromString parses the JSON-encoded string and stores the result in the value pointed to by v.
func UnmarshalFromString(str string, v any) error {
	return api.UnmarshalFromString(str, v)
}

// Valid validates the JSON-encoded bytes and reports if it is valid.
func Valid(data []byte) bool {
	return api.Valid(data)
}

// NewEncoder creates an Encoder writing to w.
func NewEncoder(w io.Writer) sonic.Encoder {
	return api.NewEncoder(w)
}

// NewDecoder creates a Decoder reading from r.
func NewDecoder(r io.Reader) sonic.Decoder {
	return api.NewDecoder(r)
}
