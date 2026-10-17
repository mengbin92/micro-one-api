package requesttrace

import (
	"bytes"
	"micro-one-api/pkg/jsonx"
	"strings"
)

// ContentProbe retains at most one bounded SSE frame, never a full response.
// Role-only frames, pings and response.created do not count as content.
type ContentProbe struct {
	buffer    []byte
	discard   bool
	Kind      string
	Terminal  bool
	Failed    bool
	Truncated bool
	outcome   string
}

func (p *ContentProbe) TerminalOutcome() string {
	if p.outcome != "" {
		return p.outcome
	}
	return "success"
}

func (p *ContentProbe) Feed(data []byte) {
	for len(data) > 0 {
		index := bytes.IndexByte(data, '\n')
		if index < 0 {
			p.append(data)
			return
		}
		p.append(data[:index])
		if !p.discard {
			p.Frame(bytes.TrimSpace(p.buffer))
		}
		p.buffer = nil
		p.discard = false
		data = data[index+1:]
	}
}

func (p *ContentProbe) append(data []byte) {
	if p.discard {
		return
	}
	if len(p.buffer)+len(data) > 64<<10 {
		p.buffer = nil
		p.discard = true
		p.Truncated = true
		return
	}
	p.buffer = append(p.buffer, data...)
}

func (p *ContentProbe) Frame(data []byte) {
	if len(data) > 64<<10 {
		p.Truncated = true
		return
	}
	if bytes.HasPrefix(data, []byte("data:")) {
		data = bytes.TrimSpace(bytes.TrimPrefix(data, []byte("data:")))
	}
	if bytes.Equal(data, []byte("[DONE]")) {
		p.Terminal = true
		return
	}
	type function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}
	var event struct {
		ContentBlock struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"content_block"`
		Item struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"item"`
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text     string           `json:"text"`
					Thought  bool             `json:"thought"`
					Function jsonx.RawMessage `json:"functionCall"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		Type     string           `json:"type"`
		Delta    jsonx.RawMessage `json:"delta"`
		Error    jsonx.RawMessage `json:"error"`
		Response struct {
			Status string           `json:"status"`
			Error  jsonx.RawMessage `json:"error"`
		} `json:"response"`
		Choices []struct {
			Text  string `json:"text"`
			Delta struct {
				Content      string `json:"content"`
				Reasoning    string `json:"reasoning_content"`
				ReasoningAlt string `json:"reasoning"`
				Tools        []struct {
					Function function `json:"function"`
				} `json:"tool_calls"`
				Function function `json:"function_call"`
			} `json:"delta"`
		} `json:"choices"`
	}
	if jsonx.Unmarshal(data, &event) != nil {
		return
	}
	switch event.Type {
	case "response.completed", "response.done", "message_stop":
		p.Terminal = true
	case "response.failed", "error":
		p.Terminal = true
		p.Failed = true
		p.outcome = "failed"
	case "response.incomplete":
		p.Terminal, p.Failed, p.outcome = true, true, "incomplete"
	case "response.cancelled", "response.canceled":
		p.Terminal, p.Failed, p.outcome = true, true, "cancelled"
	}
	switch event.Response.Status {
	case "failed", "incomplete":
		p.Terminal, p.Failed, p.outcome = true, true, event.Response.Status
	case "cancelled", "canceled":
		p.Terminal, p.Failed, p.outcome = true, true, "cancelled"
	}
	if (len(event.Error) > 0 && string(event.Error) != "null") || (len(event.Response.Error) > 0 && string(event.Response.Error) != "null") {
		p.Terminal = true
		p.Failed = true
		if p.outcome == "" {
			p.outcome = "failed"
		}
	}
	if p.Kind != "" {
		return
	}
	for _, choice := range event.Choices {
		d := choice.Delta
		switch {
		case d.Content != "" || choice.Text != "":
			p.Kind = "text"
		case d.Reasoning != "" || d.ReasoningAlt != "":
			p.Kind = "reasoning"
		case d.Function.Name != "" || d.Function.Arguments != "":
			p.Kind = "tool_call"
		}
		for _, tool := range d.Tools {
			if tool.Function.Name != "" || tool.Function.Arguments != "" {
				p.Kind = "tool_call"
			}
		}
	}
	if p.Kind != "" {
		return
	}
	if (event.Type == "content_block_start" && event.ContentBlock.Type == "tool_use" && event.ContentBlock.Name != "") || (event.Type == "response.output_item.added" && event.Item.Type == "function_call" && event.Item.Name != "") {
		p.Kind = "tool_call"
	}
	for _, candidate := range event.Candidates {
		for _, part := range candidate.Content.Parts {
			if part.Text != "" {
				if part.Thought {
					p.Kind = "reasoning"
				} else {
					p.Kind = "text"
				}
			}
			if len(part.Function) > 2 && string(part.Function) != "null" {
				p.Kind = "tool_call"
			}
		}
	}
	var delta string
	if jsonx.Unmarshal(event.Delta, &delta) == nil && delta != "" {
		switch {
		case strings.Contains(event.Type, "output_text.delta"):
			p.Kind = "text"
		case strings.Contains(event.Type, "reasoning") && strings.HasSuffix(event.Type, ".delta"):
			p.Kind = "reasoning"
		case event.Type == "response.function_call_arguments.delta":
			p.Kind = "tool_call"
		}
	}
	var block struct {
		Text     string `json:"text"`
		Thinking string `json:"thinking"`
		JSON     string `json:"partial_json"`
	}
	if event.Type == "content_block_delta" && jsonx.Unmarshal(event.Delta, &block) == nil {
		switch {
		case block.Text != "":
			p.Kind = "text"
		case block.Thinking != "":
			p.Kind = "reasoning"
		case block.JSON != "":
			p.Kind = "tool_call"
		}
	}
}
