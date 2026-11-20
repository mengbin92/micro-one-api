package server

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"micro-one-api/domain/routing"
	"micro-one-api/pkg/jsonx"
)

const (
	stepAudioASRPath    = "/audio/asr/sse"
	stepAudioSpeechPath = "/audio/speech"
)

type stepAudioInput struct {
	bound       routing.CostBound
	body        []byte
	streamEvent string
}

func parseStepAudioInput(path, model string, body []byte) (*stepAudioInput, error) {
	if path == stepAudioASRPath {
		return parseStepAudioASR(body)
	}
	if path != stepAudioSpeechPath || model != routing.StepAudioTTSModel {
		return nil, nil
	}
	var payload map[string]jsonx.RawMessage
	var input, streamFormat string
	if jsonx.Unmarshal(body, &payload) != nil || jsonx.Unmarshal(payload["input"], &input) != nil || strings.TrimSpace(input) == "" || !utf8.ValidString(input) || utf8.RuneCountInString(input) > 1000 {
		return nil, fmt.Errorf("a nonempty input of up to 1000 characters is required")
	}
	for key := range payload {
		if (strings.EqualFold(key, "input") && key != "input") || (strings.EqualFold(key, "model") && key != "model") || (strings.EqualFold(key, "stream_format") && key != "stream_format") {
			return nil, fmt.Errorf("use canonical audio request fields")
		}
	}
	if raw := payload["stream_format"]; raw != nil && (jsonx.Unmarshal(raw, &streamFormat) != nil || (streamFormat != "" && streamFormat != "audio" && streamFormat != "sse")) {
		return nil, fmt.Errorf("invalid speech stream format")
	}
	clean, err := jsonx.Marshal(payload)
	if err != nil {
		return nil, err
	}
	result := &stepAudioInput{bound: routing.CostBound{Protocol: routing.AudioInputProtocol(model), UpstreamModel: model, AudioUnits: stepAudioSpeechUnits(input)}, body: clean}
	if streamFormat == "sse" {
		result.streamEvent = "speech.audio.done"
	}
	return result, nil
}

func stepAudioSpeechUnits(input string) int64 {
	units := int64(0)
	for _, char := range input {
		units++
		if !unicode.IsPunct(char) && (char < 'A' || char > 'Z') && (char < 'a' || char > 'z') {
			units++
		}
	}
	return units
}

type stepAudioASRFormat struct {
	Type    string `json:"type"`
	Codec   string `json:"codec,omitempty"`
	Rate    int64  `json:"rate,omitempty"`
	Bits    int64  `json:"bits,omitempty"`
	Channel int64  `json:"channel,omitempty"`
}

type stepAudioASRRequest struct {
	Audio struct {
		Data  string `json:"data"`
		Input struct {
			Transcription struct {
				Model           string   `json:"model"`
				Language        string   `json:"language,omitempty"`
				Hotwords        []string `json:"hotwords,omitempty"`
				EnableITN       *bool    `json:"enable_itn,omitempty"`
				EnableTimestamp *bool    `json:"enable_timestamp,omitempty"`
			} `json:"transcription"`
			Format stepAudioASRFormat `json:"format"`
		} `json:"input"`
	} `json:"audio"`
}

func parseStepAudioASR(body []byte) (*stepAudioInput, error) {
	var req stepAudioASRRequest
	if jsonx.Unmarshal(body, &req) != nil || req.Audio.Input.Transcription.Model != "stepaudio-2.5-asr" {
		return nil, fmt.Errorf("stepaudio-2.5-asr is required in audio.input.transcription.model")
	}
	audio, err := base64.StdEncoding.Strict().DecodeString(req.Audio.Data)
	if err != nil || len(audio) == 0 {
		return nil, fmt.Errorf("valid base64 audio is required")
	}
	f := req.Audio.Input.Format
	size := int64(len(audio))
	switch f.Type {
	case "pcm":
		if f.Codec != "" && f.Codec != "pcm_s16le" {
			return nil, fmt.Errorf("unsupported PCM audio codec")
		}
	case "wav":
		wav, n, ok := stepAudioWAV(audio)
		if !ok || (f.Rate != 0 && f.Rate != wav.Rate) || (f.Channel != 0 && f.Channel != wav.Channel) || (f.Bits != 0 && f.Bits != wav.Bits) || f.Codec != "" {
			return nil, fmt.Errorf("invalid PCM WAV audio")
		}
		f, size = wav, n
	default:
		return nil, fmt.Errorf("ASR requires PCM16 or PCM WAV audio")
	}
	if f.Rate < 8000 || f.Rate > 48000 || f.Bits != 16 || f.Channel < 1 || f.Channel > 2 || size == 0 || size%(f.Channel*2) != 0 {
		return nil, fmt.Errorf("invalid PCM audio geometry")
	}
	frames := size / (f.Channel * 2)
	duration := (frames*1_000_000 + f.Rate - 1) / f.Rate
	model := req.Audio.Input.Transcription.Model
	bound := routing.CostBound{Protocol: routing.AudioInputProtocol(model), UpstreamModel: model, AudioUnits: duration}
	if !bound.Valid() {
		return nil, fmt.Errorf("audio duration out of range")
	}
	clean, err := jsonx.Marshal(req)
	if err != nil {
		return nil, err
	}
	return &stepAudioInput{bound: bound, body: clean, streamEvent: "transcript.text.done"}, nil
}

func stepAudioWAV(data []byte) (stepAudioASRFormat, int64, bool) {
	var format stepAudioASRFormat
	var size int64
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" || int64(binary.LittleEndian.Uint32(data[4:8]))+8 != int64(len(data)) {
		return format, 0, false
	}
	for offset := int64(12); offset < int64(len(data)); {
		if int64(len(data))-offset < 8 {
			return format, 0, false
		}
		id := string(data[offset : offset+4])
		length := int64(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		start := offset + 8
		offset = start + length + (length % 2)
		if offset > int64(len(data)) {
			return format, 0, false
		}
		buf := data[start : start+length]
		switch id {
		case "fmt ":
			if format.Type != "" || len(buf) < 16 || binary.LittleEndian.Uint16(buf[:2]) != 1 {
				return format, 0, false
			}
			format = stepAudioASRFormat{Type: "wav", Channel: int64(binary.LittleEndian.Uint16(buf[2:4])), Rate: int64(binary.LittleEndian.Uint32(buf[4:8])), Bits: int64(binary.LittleEndian.Uint16(buf[14:16]))}
			block := int64(binary.LittleEndian.Uint16(buf[12:14]))
			if block != format.Channel*2 || int64(binary.LittleEndian.Uint32(buf[8:12])) != format.Rate*block {
				return format, 0, false
			}
		case "data":
			if size != 0 || length == 0 || format.Type == "" {
				return format, 0, false
			}
			size = length
		}
	}
	return format, size, format.Type != "" && size != 0
}
