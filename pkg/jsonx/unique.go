package jsonx

import (
	"bytes"
	"fmt"
	"io"
)

// ValidateUnique rejects duplicate object keys before typed/map projection can
// discard evidence. Depth and token budgets bound untrusted document work.
func ValidateUnique(raw []byte) error {
	if len(raw) > 4<<20 {
		return fmt.Errorf("JSON size limit exceeded")
	}
	d := NewTokenDecoder(bytes.NewReader(raw))
	d.UseNumber()
	budget := 200000
	var value func(int) error
	value = func(depth int) error {
		budget--
		if depth > 32 || budget < 0 {
			return fmt.Errorf("JSON complexity limit exceeded")
		}
		t, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := t.(Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				s, ok := key.(string)
				if !ok || seen[s] {
					return fmt.Errorf("duplicate JSON object key")
				}
				seen[s] = true
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter")
		}
		end, err := d.Token()
		if err != nil {
			return err
		}
		if delim == '{' && end != Delim('}') || delim == '[' && end != Delim(']') {
			return fmt.Errorf("unclosed JSON container")
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON content")
	}
	return nil
}
