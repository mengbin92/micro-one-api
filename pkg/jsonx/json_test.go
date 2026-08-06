package jsonx

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Consistency with encoding/json
// ---------------------------------------------------------------------------

type sampleNested struct {
	Z int     `json:"z"`
	A string  `json:"a"`
	B float64 `json:"b"`
}

type sampleStruct struct {
	Name    string       `json:"name"`
	HTML    string       `json:"html"`
	Count   int          `json:"count"`
	Nested  sampleNested `json:"nested"`
	Raw     RawMessage   `json:"raw,omitempty"`
	Ignored string       `json:"-"`
	NoTag   string
}

func TestMarshalMatchesEncodingJSON(t *testing.T) {
	v := sampleStruct{
		Name:    "kimi & <glm>",
		HTML:    "<script>alert(1)</script> & \"quoted\"",
		Count:   42,
		Nested:  sampleNested{Z: 3, A: "x", B: 1.5},
		Raw:     RawMessage(`{"keep":true}`),
		Ignored: "should not appear",
		NoTag:   "no-tag-appears",
	}

	got, err := Marshal(v)
	if err != nil {
		t.Fatalf("jsonx.Marshal: %v", err)
	}
	want, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encoding/json.Marshal: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("marshal mismatch:\njsonx: %s\nstd:   %s", got, want)
	}

	// HTML escaping must match std (ConfigStd default true).
	if !bytes.Contains(got, []byte(`\u003c`)) {
		t.Errorf("expected HTML escaping (\\u003c) in output, got: %s", got)
	}
}

func TestMarshalMapKeyOrderMatchesEncodingJSON(t *testing.T) {
	m := map[string]interface{}{
		"zebra": 1,
		"alpha": 2,
		"mango": 3,
		"beta":  4,
	}
	got, err := Marshal(m)
	if err != nil {
		t.Fatalf("jsonx.Marshal: %v", err)
	}
	want, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("encoding/json.Marshal: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("map key order mismatch:\njsonx: %s\nstd:   %s", got, want)
	}
}

func TestMarshalIndentMatchesEncodingJSON(t *testing.T) {
	v := map[string]int{"b": 2, "a": 1}
	got, err := MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("jsonx.MarshalIndent: %v", err)
	}
	want, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("encoding/json.MarshalIndent: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("marshal indent mismatch:\njsonx: %s\nstd:   %s", got, want)
	}
}

func TestMarshalToString(t *testing.T) {
	s, err := MarshalToString(map[string]int{"a": 1})
	if err != nil {
		t.Fatalf("MarshalToString: %v", err)
	}
	if s != `{"a":1}` {
		t.Fatalf("MarshalToString = %q, want %q", s, `{"a":1}`)
	}
}

func TestUnmarshalMatchesEncodingJSON(t *testing.T) {
	data := []byte(`{"name":"kimi & <glm>","count":42,"nested":{"z":3,"a":"x","b":1.5},"raw":{"keep":true}}`)

	var got sampleStruct
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("jsonx.Unmarshal: %v", err)
	}
	var want sampleStruct
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatalf("encoding/json.Unmarshal: %v", err)
	}

	if got.Name != want.Name || got.Count != want.Count ||
		got.Nested != want.Nested || !bytes.Equal(got.Raw, want.Raw) {
		t.Fatalf("unmarshal mismatch:\njsonx: %+v\nstd:   %+v", got, want)
	}
}

func TestUnmarshalString(t *testing.T) {
	var got map[string]int
	if err := UnmarshalFromString(`{"a":1,"b":2}`, &got); err != nil {
		t.Fatalf("UnmarshalFromString: %v", err)
	}
	if got["a"] != 1 || got["b"] != 2 {
		t.Fatalf("UnmarshalFromString = %+v", got)
	}
}

func TestValid(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{`{"a":1}`, true},
		{`{"a":}`, false},
		{``, false},
		{`[]`, true},
		{`[1,2`, false},
	}
	for _, c := range cases {
		if got := Valid([]byte(c.in)); got != c.want {
			t.Errorf("Valid(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestNumberTypeAlias(t *testing.T) {
	// jsonx.Number must be the same type as encoding/json.Number so type
	// switches in existing code keep working.
	var n Number = json.Number("3.14")
	var _ json.Number = Number(n)
	if string(n) != "3.14" {
		t.Fatalf("Number alias broken: %q", string(n))
	}
}

func TestRawMessageRoundTrip(t *testing.T) {
	raw := RawMessage(`{"x":1}`)
	out, err := Marshal(struct {
		Data RawMessage `json:"data"`
	}{Data: raw})
	if err != nil {
		t.Fatalf("marshal raw: %v", err)
	}
	if string(out) != `{"data":{"x":1}}` {
		t.Fatalf("raw round-trip = %s", out)
	}

	var back struct {
		Data RawMessage `json:"data"`
	}
	if err := Unmarshal(out, &back); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	if !bytes.Equal(back.Data, raw) {
		t.Fatalf("raw unmarshal = %s, want %s", back.Data, raw)
	}
}

// customMarshaler exercises the json.Marshaler interface path.
type customMarshaler struct{ N int }

func (c customMarshaler) MarshalJSON() ([]byte, error) {
	return []byte(`{"custom":` + string(rune('0'+c.N)) + `}`), nil
}

func TestCustomMarshalerMatchesEncodingJSON(t *testing.T) {
	v := map[string]customMarshaler{"k": {N: 7}}
	got, err := Marshal(v)
	if err != nil {
		t.Fatalf("jsonx.Marshal: %v", err)
	}
	want, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encoding/json.Marshal: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("marshaler mismatch:\njsonx: %s\nstd:   %s", got, want)
	}
}

// customUnmarshaler exercises the json.Unmarshaler interface path.
type customUnmarshaler struct{ V int }

func (c *customUnmarshaler) UnmarshalJSON(data []byte) error {
	var m map[string]int
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	c.V = m["custom"]
	return nil
}

func TestCustomUnmarshalerMatchesEncodingJSON(t *testing.T) {
	data := []byte(`{"custom":9}`)
	var got customUnmarshaler
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("jsonx.Unmarshal: %v", err)
	}
	var want customUnmarshaler
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatalf("encoding/json.Unmarshal: %v", err)
	}
	if got.V != want.V {
		t.Fatalf("unmarshaler mismatch: jsonx=%d std=%d", got.V, want.V)
	}
}

func TestEncoderDecoderMatchEncodingJSON(t *testing.T) {
	var gotBuf bytes.Buffer
	enc := NewEncoder(&gotBuf)
	if err := enc.Encode(map[string]int{"a": 1}); err != nil {
		t.Fatalf("jsonx encoder: %v", err)
	}

	var wantBuf bytes.Buffer
	if err := json.NewEncoder(&wantBuf).Encode(map[string]int{"a": 1}); err != nil {
		t.Fatalf("std encoder: %v", err)
	}
	if !bytes.Equal(gotBuf.Bytes(), wantBuf.Bytes()) {
		t.Fatalf("encoder mismatch:\njsonx: %s\nstd:   %s", gotBuf.Bytes(), wantBuf.Bytes())
	}

	var gotM map[string]int
	if err := NewDecoder(strings.NewReader(`{"b":2}`)).Decode(&gotM); err != nil {
		t.Fatalf("jsonx decoder: %v", err)
	}
	var wantM map[string]int
	if err := json.NewDecoder(strings.NewReader(`{"b":2}`)).Decode(&wantM); err != nil {
		t.Fatalf("std decoder: %v", err)
	}
	if gotM["b"] != wantM["b"] {
		t.Fatalf("decoder mismatch: %+v vs %+v", gotM, wantM)
	}
}

func TestUnmarshalErrorBehavior(t *testing.T) {
	var v map[string]int
	err := Unmarshal([]byte(`{"a":`), &v)
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
	// Invalid JSON must error (no panic, no silent success).
	var s sampleStruct
	if err := Unmarshal([]byte(`{"count":"notanumber"}`), &s); err == nil {
		t.Fatal("expected type error for string->int, got nil")
	}
}

func TestGetPathLookup(t *testing.T) {
	data := []byte(`{"response":{"id":"r_123"},"response_id":"r_456"}`)

	node, err := Get(data, "response", "id")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !node.Exists() {
		t.Fatal("expected node to exist")
	}
	rid, _ := node.String()
	if rid != "r_123" {
		t.Fatalf("Get(response.id) = %q, want r_123", rid)
	}

	node2, _ := Get(data, "missing")
	if node2.Exists() {
		t.Fatal("expected missing path to not exist")
	}
}

// ---------------------------------------------------------------------------
// Float edge cases: sonic vs std
// ---------------------------------------------------------------------------

func TestFloatRoundTrip(t *testing.T) {
	values := []float64{0.1, 1e-7, 1.5, -3.25, 1e21, math.MaxFloat64}
	for _, f := range values {
		got, err := Marshal(f)
		if err != nil {
			t.Fatalf("marshal %v: %v", f, err)
		}
		want, err := json.Marshal(f)
		if err != nil {
			t.Fatalf("std marshal %v: %v", f, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("float marshal mismatch for %v:\njsonx: %s\nstd:   %s", f, got, want)
		}
	}
}

func TestNumberLargeIntPrecision(t *testing.T) {
	// Large int64 must round-trip exactly through interface{} decode.
	data := []byte(`{"id":9223372036854775807}`)
	var got map[string]interface{}
	if err := Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	// Default (no UseNumber) decodes to float64, losing precision beyond 2^53 —
	// this must match std behavior exactly.
	var want map[string]interface{}
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatalf("std Unmarshal: %v", err)
	}
	if got["id"] != want["id"] {
		t.Fatalf("int precision mismatch: jsonx=%v std=%v", got["id"], want["id"])
	}
}

// ---------------------------------------------------------------------------
// Benchmarks (performance justification for the sonic migration)
// ---------------------------------------------------------------------------

var benchPayload = []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hello world"}],"max_tokens":128,"stream":false,"temperature":0.7}`)

type benchRequest struct {
	Model       string                 `json:"model"`
	Messages    []benchMessage         `json:"messages"`
	MaxTokens   int                    `json:"max_tokens"`
	Stream      bool                   `json:"stream"`
	Temperature float64                `json:"temperature"`
	Extra       map[string]interface{} `json:"-"`
}

type benchMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func BenchmarkUnmarshalJSONX(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var req benchRequest
		if err := Unmarshal(benchPayload, &req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkUnmarshalStd(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var req benchRequest
		if err := json.Unmarshal(benchPayload, &req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshalJSONX(b *testing.B) {
	req := benchRequest{Model: "gpt-4o", Messages: []benchMessage{{Role: "user", Content: "hello"}}, MaxTokens: 128}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Marshal(req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshalStd(b *testing.B) {
	req := benchRequest{Model: "gpt-4o", Messages: []benchMessage{{Role: "user", Content: "hello"}}, MaxTokens: 128}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := json.Marshal(req); err != nil {
			b.Fatal(err)
		}
	}
}
