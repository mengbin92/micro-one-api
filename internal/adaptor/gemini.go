package adaptor

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"micro-one-api/domain/upstream/provider"
	"micro-one-api/internal/apicompat"
	"micro-one-api/pkg/jsonx"
)

// GeminiAdaptor bridges supported client protocols to Gemini's native
// generateContent wire format. Keeping this conversion here prevents the
// /v1/messages handler from falling back to the legacy provider path.
type GeminiAdaptor struct {
	baseAdaptor
	provider provider.Provider
	models   []string
}

var geminiModels = []string{
	"gemini-1.5-pro", "gemini-1.5-flash", "gemini-2.0-flash",
}

type geminiRequest struct {
	Contents          []geminiContent         `json:"contents"`
	SystemInstruction *geminiContent          `json:"systemInstruction,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
	Tools             []geminiTool            `json:"tools,omitempty"`
	ToolConfig        *geminiToolConfig       `json:"toolConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	InlineData       *geminiBlob             `json:"inlineData,omitempty"`
	FileData         *geminiFileData         `json:"fileData,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiBlob struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiFileData struct {
	MIMEType string `json:"mimeType,omitempty"`
	FileURI  string `json:"fileUri"`
}

type geminiFunctionCall struct {
	ID   string           `json:"id,omitempty"`
	Name string           `json:"name"`
	Args jsonx.RawMessage `json:"args,omitempty"`
}

type geminiFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type geminiGenerationConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	TopP            *float64 `json:"topP,omitempty"`
	MaxOutputTokens *int     `json:"maxOutputTokens,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations"`
}

type geminiFunctionDeclaration struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Parameters  jsonx.RawMessage `json:"parameters,omitempty"`
}

type geminiToolConfig struct {
	FunctionCallingConfig geminiFunctionCallingConfig `json:"functionCallingConfig"`
}

type geminiFunctionCallingConfig struct {
	Mode                 string   `json:"mode"`
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

type geminiResponse struct {
	Candidates    []geminiCandidate   `json:"candidates"`
	UsageMetadata geminiUsageMetadata `json:"usageMetadata"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type geminiUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
}

// NewGeminiAdaptor builds an adaptor for a Gemini API-key channel.
func NewGeminiAdaptor(p provider.Provider, models []string) *GeminiAdaptor {
	if len(models) == 0 {
		models = geminiModels
	}
	return &GeminiAdaptor{provider: p, models: models}
}

func (a *GeminiAdaptor) Init(_ *RelayContext) {}

func (a *GeminiAdaptor) Name() string        { return "gemini" }
func (a *GeminiAdaptor) ModelList() []string { return a.models }

func (a *GeminiAdaptor) ConvertRequest(rc *RelayContext, inbound Format, body []byte) (Format, []byte, error) {
	if inbound == FormatGemini {
		return FormatGemini, body, nil
	}
	_, chatBody, err := convertRequestToChat(rc, inbound, body)
	if err != nil {
		return "", nil, err
	}
	var chatRequest apicompat.ChatCompletionsRequest
	if err := jsonx.Unmarshal(chatBody, &chatRequest); err != nil {
		return "", nil, fmt.Errorf("gemini adaptor: parse chat request: %w", err)
	}
	if rc != nil {
		rc.IsStream = chatRequest.Stream
	}
	request, err := chatRequestToGemini(&chatRequest)
	if err != nil {
		return "", nil, err
	}
	out, err := jsonx.Marshal(request)
	if err != nil {
		return "", nil, fmt.Errorf("gemini adaptor: marshal request: %w", err)
	}
	return FormatGemini, out, nil
}

func (a *GeminiAdaptor) GetUpstreamURL(ctx *RelayContext) (string, error) {
	base := baseURLFromContext(ctx)
	if base == "" {
		base = "https://generativelanguage.googleapis.com"
	}
	model := ""
	if ctx != nil {
		model = ctx.ResolvedModel
	}
	if model == "" {
		return "", fmt.Errorf("gemini adaptor: resolved model is required")
	}
	method := "generateContent"
	if ctx != nil && ctx.IsStream {
		method = "streamGenerateContent"
		return fmt.Sprintf("%s/v1beta/models/%s:%s?alt=sse", strings.TrimRight(base, "/"), model, method), nil
	}
	return fmt.Sprintf("%s/v1beta/models/%s:%s", strings.TrimRight(base, "/"), model, method), nil
}

func (a *GeminiAdaptor) BuildUpstreamRequest(ctx context.Context, rc *RelayContext, _ Format, body []byte) (*http.Request, error) {
	url, err := a.GetUpstreamURL(rc)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytesReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := apiKeyFromContext(rc); key != "" {
		req.Header.Set("x-goog-api-key", key)
	}
	return req, nil
}

func (a *GeminiAdaptor) ConvertResponse(rc *RelayContext, _ Format, resp *http.Response) (Format, []byte, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, provider.MaxUpstreamResponseBody))
	if err != nil {
		return "", nil, fmt.Errorf("read upstream response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, &provider.UpstreamHTTPError{StatusCode: resp.StatusCode, Body: body}
	}
	if rc == nil || rc.InboundFormat == FormatGemini {
		return FormatGemini, body, nil
	}
	var response geminiResponse
	if err := jsonx.Unmarshal(body, &response); err != nil {
		return "", nil, fmt.Errorf("gemini adaptor: parse response: %w", err)
	}
	chatResponse, err := geminiResponseToChat(&response, rc.ClientModel)
	if err != nil {
		return "", nil, err
	}
	chatBody, err := jsonx.Marshal(chatResponse)
	if err != nil {
		return "", nil, fmt.Errorf("gemini adaptor: marshal chat response: %w", err)
	}
	chatHTTPResponse := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(chatBody)))}
	return convertChatResponse(rc, chatHTTPResponse)
}

func (a *GeminiAdaptor) ConvertStreamResponse(rc *RelayContext, _ Format, resp *http.Response) (Format, io.Reader, error) {
	if rc == nil || rc.InboundFormat == FormatGemini {
		return FormatGemini, resp.Body, nil
	}
	chatReader, chatWriter := io.Pipe()
	go pumpGeminiToChat(resp.Body, chatWriter, rc.ClientModel)
	chatResponse := &http.Response{StatusCode: http.StatusOK, Body: chatReader}
	return convertChatStream(rc, chatResponse)
}

func chatRequestToGemini(request *apicompat.ChatCompletionsRequest) (*geminiRequest, error) {
	out := &geminiRequest{}
	callNames := make(map[string]string)
	for _, message := range request.Messages {
		parts, err := geminiPartsFromChatMessage(message, callNames)
		if err != nil {
			return nil, err
		}
		if len(parts) == 0 {
			continue
		}
		if message.Role == "system" || message.Role == "developer" {
			if out.SystemInstruction == nil {
				out.SystemInstruction = &geminiContent{}
			}
			out.SystemInstruction.Parts = append(out.SystemInstruction.Parts, parts...)
			continue
		}
		role := "user"
		if message.Role == "assistant" {
			role = "model"
		}
		out.Contents = append(out.Contents, geminiContent{Role: role, Parts: parts})
	}

	maxTokens := request.MaxTokens
	if maxTokens == nil {
		maxTokens = request.MaxCompletionTokens
	}
	stopSequences, err := chatStopSequences(request.Stop)
	if err != nil {
		return nil, fmt.Errorf("gemini adaptor: parse stop: %w", err)
	}
	if request.Temperature != nil || request.TopP != nil || maxTokens != nil || len(stopSequences) > 0 {
		out.GenerationConfig = &geminiGenerationConfig{
			Temperature: request.Temperature, TopP: request.TopP,
			MaxOutputTokens: maxTokens, StopSequences: stopSequences,
		}
	}
	if len(request.Tools) > 0 {
		declarations := make([]geminiFunctionDeclaration, 0, len(request.Tools))
		for _, tool := range request.Tools {
			if tool.Function == nil {
				continue
			}
			declarations = append(declarations, geminiFunctionDeclaration{
				Name: tool.Function.Name, Description: tool.Function.Description, Parameters: tool.Function.Parameters,
			})
		}
		if len(declarations) > 0 {
			out.Tools = []geminiTool{{FunctionDeclarations: declarations}}
		}
	}
	if len(request.ToolChoice) > 0 {
		config, err := chatToolChoiceToGemini(request.ToolChoice)
		if err != nil {
			return nil, fmt.Errorf("gemini adaptor: parse tool_choice: %w", err)
		}
		out.ToolConfig = config
	}
	return out, nil
}

func geminiPartsFromChatMessage(message apicompat.ChatMessage, callNames map[string]string) ([]geminiPart, error) {
	var parts []geminiPart
	if len(message.Content) > 0 && string(message.Content) != "null" {
		var text string
		if err := jsonx.Unmarshal(message.Content, &text); err == nil {
			if text != "" {
				parts = append(parts, geminiPart{Text: text})
			}
		} else {
			var contentParts []apicompat.ChatContentPart
			if err := jsonx.Unmarshal(message.Content, &contentParts); err != nil {
				return nil, fmt.Errorf("gemini adaptor: parse message content: %w", err)
			}
			for _, contentPart := range contentParts {
				switch contentPart.Type {
				case "text":
					parts = append(parts, geminiPart{Text: contentPart.Text})
				case "image_url":
					if contentPart.ImageURL == nil || contentPart.ImageURL.URL == "" {
						continue
					}
					parts = append(parts, geminiImagePart(contentPart.ImageURL.URL))
				}
			}
		}
	}
	for _, toolCall := range message.ToolCalls {
		callNames[toolCall.ID] = toolCall.Function.Name
		arguments := jsonx.RawMessage(toolCall.Function.Arguments)
		if len(arguments) == 0 {
			arguments = jsonx.RawMessage(`{}`)
		}
		if !jsonx.Valid(arguments) {
			return nil, fmt.Errorf("gemini adaptor: tool %q has invalid JSON arguments", toolCall.Function.Name)
		}
		parts = append(parts, geminiPart{FunctionCall: &geminiFunctionCall{Name: toolCall.Function.Name, Args: arguments}})
	}
	if message.Role == "tool" {
		name := callNames[message.ToolCallID]
		if name == "" {
			return nil, fmt.Errorf("gemini adaptor: tool result %q has no matching tool call", message.ToolCallID)
		}
		text := ""
		_ = jsonx.Unmarshal(message.Content, &text)
		parts = []geminiPart{{FunctionResponse: &geminiFunctionResponse{Name: name, Response: map[string]any{"result": text}}}}
	}
	return parts, nil
}

func geminiImagePart(uri string) geminiPart {
	if after, ok := strings.CutPrefix(uri, "data:"); ok {
		header, data, ok := strings.Cut(after, ",")
		if ok && strings.HasSuffix(header, ";base64") {
			return geminiPart{InlineData: &geminiBlob{MIMEType: strings.TrimSuffix(header, ";base64"), Data: data}}
		}
	}
	return geminiPart{FileData: &geminiFileData{FileURI: uri}}
}

func chatStopSequences(raw jsonx.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var sequences []string
	if err := jsonx.Unmarshal(raw, &sequences); err == nil {
		return sequences, nil
	}
	var sequence string
	if err := jsonx.Unmarshal(raw, &sequence); err != nil {
		return nil, err
	}
	return []string{sequence}, nil
}

func chatToolChoiceToGemini(raw jsonx.RawMessage) (*geminiToolConfig, error) {
	var simple string
	if err := jsonx.Unmarshal(raw, &simple); err == nil {
		mode := map[string]string{"auto": "AUTO", "required": "ANY", "none": "NONE"}[simple]
		if mode == "" {
			return nil, fmt.Errorf("unsupported tool choice %q", simple)
		}
		return &geminiToolConfig{FunctionCallingConfig: geminiFunctionCallingConfig{Mode: mode}}, nil
	}
	var named struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := jsonx.Unmarshal(raw, &named); err != nil {
		return nil, err
	}
	if named.Function.Name == "" {
		return nil, fmt.Errorf("named tool choice is missing function.name")
	}
	return &geminiToolConfig{FunctionCallingConfig: geminiFunctionCallingConfig{Mode: "ANY", AllowedFunctionNames: []string{named.Function.Name}}}, nil
}

func geminiResponseToChat(response *geminiResponse, model string) (*apicompat.ChatCompletionsResponse, error) {
	if len(response.Candidates) == 0 {
		return nil, fmt.Errorf("gemini adaptor: response contains no candidates")
	}
	message := apicompat.ChatMessage{Role: "assistant"}
	finishReason := "stop"
	candidate := response.Candidates[0]
	var text strings.Builder
	for index, part := range candidate.Content.Parts {
		text.WriteString(part.Text)
		if part.FunctionCall != nil {
			callID := part.FunctionCall.ID
			if callID == "" {
				callID = fmt.Sprintf("call_gemini_%d", index)
			}
			message.ToolCalls = append(message.ToolCalls, apicompat.ChatToolCall{
				ID: callID, Type: "function",
				Function: apicompat.ChatFunctionCall{Name: part.FunctionCall.Name, Arguments: geminiArguments(part.FunctionCall.Args)},
			})
		}
	}
	if text.Len() > 0 {
		message.Content, _ = jsonx.Marshal(text.String())
	}
	var err error
	finishReason, err = geminiFinishReasonToChat(candidate.FinishReason, len(message.ToolCalls) > 0)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	return &apicompat.ChatCompletionsResponse{
		ID: fmt.Sprintf("gemini-%d", now.UnixMilli()), Object: "chat.completion", Created: now.Unix(), Model: model,
		Choices: []apicompat.ChatChoice{{Index: 0, Message: message, FinishReason: finishReason}},
		Usage:   geminiUsageToChat(response.UsageMetadata),
	}, nil
}

func geminiFinishReasonToChat(reason string, hasToolCalls bool) (string, error) {
	switch reason {
	case "", "FINISH_REASON_UNSPECIFIED", "STOP":
		if hasToolCalls {
			return "tool_calls", nil
		}
		return "stop", nil
	case "MAX_TOKENS":
		return "length", nil
	default:
		return "", fmt.Errorf("gemini adaptor: generation stopped with %s", reason)
	}
}

func geminiArguments(arguments jsonx.RawMessage) string {
	if len(arguments) == 0 {
		return "{}"
	}
	return string(arguments)
}

func geminiUsageToChat(usage geminiUsageMetadata) *apicompat.ChatUsage {
	if usage.PromptTokenCount == 0 && usage.CandidatesTokenCount == 0 && usage.TotalTokenCount == 0 && usage.CachedContentTokenCount == 0 {
		return nil
	}
	return &apicompat.ChatUsage{
		PromptTokens: usage.PromptTokenCount, CompletionTokens: usage.CandidatesTokenCount, TotalTokens: usage.TotalTokenCount,
		PromptTokensDetails: &apicompat.ChatTokenDetails{CachedTokens: usage.CachedContentTokenCount},
	}
}

func pumpGeminiToChat(src io.Reader, writer *io.PipeWriter, model string) {
	defer writer.Close()
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	created := time.Now().Unix()
	id := fmt.Sprintf("gemini-%d", time.Now().UnixMilli())
	roleSent := false
	nextToolIndex := 0
	sawCandidate := false
	for scanner.Scan() {
		data, ok := sseData(scanner.Text())
		if !ok {
			continue
		}
		var response geminiResponse
		if err := jsonx.UnmarshalFromString(data, &response); err != nil {
			writeChatStreamError(writer)
			return
		}
		var choices []apicompat.ChatChunkChoice
		if len(response.Candidates) > 0 {
			sawCandidate = true
			candidate := response.Candidates[0]
			delta := apicompat.ChatDelta{}
			if !roleSent {
				delta.Role = "assistant"
				roleSent = true
			}
			var text strings.Builder
			for _, part := range candidate.Content.Parts {
				text.WriteString(part.Text)
				if part.FunctionCall != nil {
					toolIndex := nextToolIndex
					nextToolIndex++
					callID := part.FunctionCall.ID
					if callID == "" {
						callID = fmt.Sprintf("call_gemini_%d", toolIndex)
					}
					delta.ToolCalls = append(delta.ToolCalls, apicompat.ChatToolCall{
						Index: &toolIndex, ID: callID, Type: "function",
						Function: apicompat.ChatFunctionCall{Name: part.FunctionCall.Name, Arguments: geminiArguments(part.FunctionCall.Args)},
					})
				}
			}
			if text.Len() > 0 {
				value := text.String()
				delta.Content = &value
			}
			var finish *string
			if candidate.FinishReason != "" && candidate.FinishReason != "FINISH_REASON_UNSPECIFIED" {
				value, err := geminiFinishReasonToChat(candidate.FinishReason, len(delta.ToolCalls) > 0)
				if err != nil {
					writeChatStreamError(writer)
					return
				}
				finish = &value
			}
			choices = []apicompat.ChatChunkChoice{{Index: 0, Delta: delta, FinishReason: finish}}
		}
		chunk := apicompat.ChatCompletionsChunk{
			ID: id, Object: "chat.completion.chunk", Created: created, Model: model, Choices: choices,
			Usage: geminiUsageToChat(response.UsageMetadata),
		}
		sse, err := apicompat.ChatChunkToSSE(chunk)
		if err != nil {
			continue
		}
		if _, err := io.WriteString(writer, sse); err != nil {
			return
		}
	}
	if scanner.Err() != nil {
		writeChatStreamError(writer)
		return
	}
	if !sawCandidate {
		writeChatStreamError(writer)
		return
	}
	_, _ = io.WriteString(writer, "data: [DONE]\n\n")
}
