// Package jsonx provides JSON utilities using sonic for high performance.
// It is a drop-in replacement for the standard library encoding/json,
// backed by sonic.ConfigStd for full compatibility with encoding/json semantics.
//
// NOTE (version boundary): sonic's compat.go build tags make it fall back to the
// standard library encoding/json when built with go1.27+, on non-amd64/arm64
// architectures, or on arm64 with go < 1.20. Under that fallback every function
// here behaves identically (ConfigStd is encoding/json-compatible) but the
// performance benefit disappears — EXCEPT Get, which is sonic-specific (see its
// doc comment). Keep go.mod <= go1.26, or re-benchmark before upgrading past
// that boundary.
package jsonx

import (
	"io"

	stdjson "encoding/json"

	"github.com/bytedance/sonic"
	sonicast "github.com/bytedance/sonic/ast"
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

// Get searches the given JSON data for the specified path and returns the
// resulting node.
//
// Unlike the rest of this package, Get is NOT encoding/json-compatible: the
// sonic.API interface (ConfigStd) does not expose a path-lookup method, so Get
// calls the package-level sonic.Get, which uses sonic.ConfigDefault and returns
// a sonic/ast.Node. Under the go1.27+/non-amd64-arm64 compat fallback, ast.Node
// still exists but is a pure-Go implementation that does NOT guarantee the same
// accept/reject behavior as ConfigStd decoding. Callers therefore MUST NOT treat
// Get as part of the encoding/json-parity contract — use it only for hot-path
// field extraction (model, response.id, etc.) where the input is already known
// to be well-formed, and fall back gracefully when the node is absent or the
// conversion errors.
func Get(data []byte, path ...interface{}) (sonicast.Node, error) {
	return sonic.Get(data, path...)
}
