package gemini

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"

	"micro-one-api/pkg/jsonx"
)

const MaxEventBytes = 4 << 20

// Stream validates complete SSE events before exposing them. Its final EOF is
// proof of framing, terminal state AND usage. No synthetic success is emitted.
// It is pull-driven: cancellation/Close never leaves a converter goroutine.
type Stream struct {
	scanner  *bufio.Scanner
	data     []byte
	output   *bytes.Reader
	terminal bool
	usage    *Usage
	done     bool
	tail     []byte
}

func NewStream(src io.Reader) *Stream {
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 64*1024), MaxEventBytes)
	return &Stream{scanner: scanner}
}

func (s *Stream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if s.output != nil && s.output.Len() > 0 {
			return s.output.Read(p)
		}
		if s.done {
			return 0, io.EOF
		}
		if !s.scanner.Scan() {
			s.done = true
			if s.scanner.Err() != nil || len(s.data) > 0 || !s.terminal || s.usage == nil {
				return 0, fmt.Errorf("Gemini stream ended without complete terminal usage")
			}
			s.output = bytes.NewReader(s.tail)
			if s.output.Len() > 0 {
				return s.output.Read(p)
			}
			return 0, io.EOF
		}
		line := s.scanner.Text()
		if line == "" {
			if len(s.data) == 0 {
				continue
			}
			frame, err := ParseResponse(s.data)
			if err != nil {
				s.done = true
				return 0, err
			}
			if s.terminal {
				// Only usage-only frames may follow the final candidate.
				if bytes.Contains(s.data, []byte(`"candidates"`)) {
					s.done = true
					return 0, fmt.Errorf("Gemini content after terminal")
				}
			}
			s.terminal = s.terminal || frame.Terminal
			if frame.Usage != nil {
				copy := *frame.Usage
				s.usage = &copy
			}
			// Collapse multiline JSON into one data line so all downstream
			// observers see precisely the same validated frame.
			compact, err := jsonx.Marshal(jsonx.RawMessage(s.data))
			if err != nil {
				s.done = true
				return 0, err
			}
			event := append(append([]byte("data: "), compact...), '\n', '\n')
			if s.terminal {
				if len(s.tail)+len(event) > MaxEventBytes {
					s.done = true
					return 0, fmt.Errorf("Gemini terminal event limit")
				}
				s.tail = append(s.tail, event...)
			} else {
				s.output = bytes.NewReader(event)
			}
			s.data = nil
			continue
		}
		if data, ok := strings.CutPrefix(line, "data:"); ok {
			data = strings.TrimPrefix(data, " ")
			if len(s.data)+len(data)+1 > MaxEventBytes {
				s.done = true
				return 0, fmt.Errorf("Gemini SSE event too large")
			}
			if len(s.data) > 0 {
				s.data = append(s.data, '\n')
			}
			s.data = append(s.data, data...)
		} else if !strings.HasPrefix(line, ":") && !strings.HasPrefix(line, "event:") && !strings.HasPrefix(line, "id:") && !strings.HasPrefix(line, "retry:") {
			s.done = true
			return 0, fmt.Errorf("invalid Gemini SSE framing")
		}
	}
}
