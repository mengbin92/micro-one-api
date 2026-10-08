package routingtest

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/stretchr/testify/require"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/pkg/jsonx"
)

type longSample struct {
	Executor, Source, Model, ContentSHA256, PayloadSHA256 string
	TTFTMS, TotalMS                                       float64
	ServerElapsedMS, PromptTokens, OutputTokens, Cached   int64
}

type longBucket struct {
	Protocol, Context, Verdict                                     string
	Chunks, Pairs                                                  int
	Legacy, Orchestrator                                           []longSample
	LegacyP50MS, LegacyP95MS, OrchestratorP50MS, OrchestratorP95MS float64
}

func percentile(samples []longSample, p float64) float64 {
	values := make([]float64, len(samples))
	for i, sample := range samples {
		values[i] = sample.TotalMS
	}
	sort.Float64s(values)
	return values[int(math.Ceil(float64(len(values))*p))-1]
}

func (s *suite) longStreamPairs() {
	pairs, err := strconv.Atoi(os.Getenv("LONG_STREAM_PAIRS"))
	require.NoError(s.t, err)
	require.Positive(s.t, pairs)
	// Keep billing identical across the whole cohort; the legacy fixture's
	// small subscription budget otherwise switches to split settlement mid-run.
	s.revoke(s.state.LegacySubscription)
	legacy := s.api("POST", "/api/token", s.state.Session, object{"name": "long-stream-legacy", "unlimited_quota": true}, "")["key"].(string)
	report := struct {
		Verdict                string
		CadenceMS, WarmupPairs int
		Buckets                []*longBucket
	}{Verdict: "FAIL", CadenceMS: 1, WarmupPairs: 3}
	writeReport := func() {
		raw, err := jsonx.MarshalIndent(report, "", "  ")
		require.NoError(s.t, err)
		require.NoError(s.t, os.WriteFile(filepath.Join(filepath.Dir(os.Getenv("ROUTING_STATE")), "long-stream.json"), raw, 0600))
	}
	defer writeReport()
	for _, protocol := range []string{"chat", "messages", "responses"} {
		for _, chunks := range []int{256, 1024} {
			name := fmt.Sprintf("long-%d-%s", chunks, protocol)
			ctx, conn := s.conn("CHANNEL_GRPC_ENDPOINT")
			client := channelv1.NewChannelServiceClient(conn)
			m, err := client.CreateModel(ctx, &channelv1.CreateModelRequest{ModelId: name, DisplayName: name, Provider: "openai", ModelType: "chat", IsPublic: true, PricingInput: 1, PricingOutput: 1})
			require.NoError(s.t, err)
			require.True(s.t, m.Success)
			kind := int32(1)
			if protocol == "messages" {
				kind = 2
			}
			ch, err := client.CreateChannel(ctx, &channelv1.CreateChannelRequest{Name: name, Type: kind, BaseUrl: "http://mock-upstream:9999", Key: "sk-local-fixture", Models: name, Group: "default", Priority: 20, Weight: 1})
			require.NoError(s.t, err)
			require.True(s.t, ch.Success)
			require.Eventually(s.t, func() bool {
				return s.scalar("SELECT COUNT(*) FROM model_channel_mapping WHERE channel_id=? AND model_id=?", ch.ChannelId, m.ModelPk) == 1
			}, 5*time.Second, 20*time.Millisecond)
			for _, size := range []string{"short", "long"} {
				bucket := &longBucket{Protocol: protocol, Context: size, Chunks: chunks, Verdict: "FAIL"}
				report.Buckets = append(report.Buckets, bucket)
				input := "isolated stream"
				if size == "long" {
					input = strings.Repeat("context ", 4096)
				}
				body := object{"model": name, "stream": true, "max_tokens": 2048, "messages": []any{object{"role": "user", "content": input}}}
				endpoint := "/v1/chat/completions"
				if protocol == "messages" {
					endpoint = "/v1/messages"
				}
				if protocol == "responses" {
					endpoint = "/v1/responses"
					delete(body, "messages")
					body["input"] = input
					delete(body, "max_tokens")
					body["max_output_tokens"] = 2048
				}
				for i := 0; i < pairs+report.WarmupPairs; i++ {
					order := []string{"legacy", "orchestrator"}
					if i%2 == 1 {
						order[0], order[1] = order[1], order[0]
					}
					results := map[string]longSample{}
					for _, executor := range order {
						token := legacy
						if executor == "orchestrator" {
							token = s.state.Reliability
						}
						results[executor] = s.measureLongStream(endpoint, protocol, executor, token, body, chunks)
					}
					a, b := results["legacy"], results["orchestrator"]
					require.Equal(s.t, a.Source, b.Source)
					require.Equal(s.t, a.Model, b.Model)
					require.Equal(s.t, a.PromptTokens, b.PromptTokens)
					require.Equal(s.t, a.OutputTokens, b.OutputTokens)
					require.Equal(s.t, a.Cached, b.Cached)
					require.Equal(s.t, a.ContentSHA256, b.ContentSHA256)
					require.Equal(s.t, a.PayloadSHA256, b.PayloadSHA256)
					if i >= report.WarmupPairs {
						bucket.Legacy = append(bucket.Legacy, a)
						bucket.Orchestrator = append(bucket.Orchestrator, b)
						bucket.Pairs++
					}
				}
				bucket.LegacyP50MS = percentile(bucket.Legacy, .5)
				bucket.LegacyP95MS = percentile(bucket.Legacy, .95)
				bucket.OrchestratorP50MS = percentile(bucket.Orchestrator, .5)
				bucket.OrchestratorP95MS = percentile(bucket.Orchestrator, .95)
				if pairs >= 50 {
					require.LessOrEqual(s.t, bucket.OrchestratorP95MS/bucket.LegacyP95MS, 1.2, "%s %d %s P95 regression", protocol, chunks, size)
				}
				bucket.Verdict = "PASS"
				if pairs < 50 {
					bucket.Verdict = "INSUFFICIENT"
				}
				s.t.Logf("long-stream protocol=%s chunks=%d context=%s pairs=%d legacy_p95_ms=%.2f orchestrator_p95_ms=%.2f verdict=%s", protocol, chunks, size, pairs, bucket.LegacyP95MS, bucket.OrchestratorP95MS, bucket.Verdict)
				writeReport()
			}
		}
	}
	report.Verdict = "PASS"
	if pairs < 50 {
		report.Verdict = "INSUFFICIENT"
	}
}

func (s *suite) executionCount(endpoint, executor string) int64 {
	status, raw, err := send("GET", s.relay+"/metrics", "", nil, "")
	require.NoError(s.t, err)
	require.Equal(s.t, 200, status)
	var total int64
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "micro_one_api_relay_executor_requests_total{") && strings.Contains(line, `endpoint="`+endpoint+`"`) && strings.Contains(line, `execution_path="`+executor+`"`) && strings.Contains(line, `result="success"`) {
			value, err := strconv.ParseFloat(strings.Fields(line)[1], 64)
			require.NoError(s.t, err)
			total += int64(value)
		}
	}
	return total
}

func (s *suite) measureLongStream(endpoint, protocol, executor, token string, body object, chunks int) longSample {
	before := s.scalar("SELECT COALESCE(MAX(id),0) FROM billing_reservations")
	metric := s.executionCount(endpoint, executor)
	raw, err := jsonx.Marshal(body)
	require.NoError(s.t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, "POST", s.relay+endpoint, bytes.NewReader(raw))
	require.NoError(s.t, err)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	started := time.Now()
	response, err := http.DefaultClient.Do(r)
	require.NoError(s.t, err)
	defer response.Body.Close()
	require.Equal(s.t, 200, response.StatusCode)
	sample := longSample{Executor: executor, PayloadSHA256: fmt.Sprintf("%x", sha256.Sum256(raw))}
	var text strings.Builder
	terminal, usage := false, false
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			terminal = true
			continue
		}
		var event object
		require.NoError(s.t, jsonx.Unmarshal([]byte(data), &event))
		var delta string
		switch protocol {
		case "chat":
			if choices, ok := event["choices"].([]any); ok && len(choices) > 0 {
				choice := choices[0].(map[string]any)
				if d, ok := choice["delta"].(map[string]any); ok {
					delta, _ = d["content"].(string)
				}
			}
			if u, ok := event["usage"].(map[string]any); ok {
				usage = num(u["prompt_tokens"]) == 100 && num(u["completion_tokens"]) == 20
			}
		case "messages":
			if event["type"] == "content_block_delta" {
				d := event["delta"].(map[string]any)
				delta, _ = d["text"].(string)
			}
			if event["type"] == "message_delta" {
				u := event["usage"].(map[string]any)
				usage = num(u["output_tokens"]) == 20
			}
			if event["type"] == "message_stop" {
				terminal = true
			}
		case "responses":
			if event["type"] == "response.output_text.delta" {
				delta, _ = event["delta"].(string)
			}
			if event["type"] == "response.completed" {
				terminal = true
				completed := event["response"].(map[string]any)
				u := completed["usage"].(map[string]any)
				usage = num(u["input_tokens"]) == 100 && num(u["output_tokens"]) == 20
			}
		}
		if delta != "" && text.Len() == 0 {
			sample.TTFTMS = float64(time.Since(started).Microseconds()) / 1000
		}
		text.WriteString(delta)
	}
	sample.TotalMS = float64(time.Since(started).Microseconds()) / 1000
	require.NoError(s.t, scanner.Err())
	require.True(s.t, terminal)
	require.True(s.t, usage)
	require.Equal(s.t, strings.Repeat("x", chunks), text.String(), "complete content")
	sample.ContentSHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(text.String())))
	require.Eventually(s.t, func() bool {
		return s.scalar("SELECT COUNT(*) FROM billing_reservations WHERE id>? AND status='committed'", before) == 1
	}, 15*time.Second, 20*time.Millisecond)
	require.EqualValues(s.t, 1, s.scalar("SELECT COUNT(*) FROM billing_reservations WHERE id>?", before))
	require.EqualValues(s.t, 1, s.scalar("SELECT COUNT(DISTINCT l.ledger_dedupe_key) FROM billing_ledgers l JOIN billing_reservations r ON l.reference_id=r.reservation_id WHERE r.id>? AND l.type='consume'", before))
	require.EqualValues(s.t, 1, s.scalar("SELECT COUNT(*) FROM billing_ledgers l JOIN billing_reservations r ON l.reference_id=r.reservation_id WHERE r.id>? AND l.type='consume'", before))
	require.NoError(s.t, s.db.QueryRow("SELECT l.source_kind,l.upstream_model_id,l.elapsed_time,l.prompt_tokens,l.completion_tokens,COALESCE(l.cache_read_tokens,0) FROM billing_ledgers l JOIN billing_reservations r ON l.reference_id=r.reservation_id WHERE r.id>? AND l.type='consume'", before).Scan(&sample.Source, &sample.Model, &sample.ServerElapsedMS, &sample.PromptTokens, &sample.OutputTokens, &sample.Cached))
	require.Equal(s.t, "channel", sample.Source)
	require.Equal(s.t, body["model"], sample.Model)
	require.EqualValues(s.t, 100, sample.PromptTokens)
	require.EqualValues(s.t, 20, sample.OutputTokens)
	require.Zero(s.t, sample.Cached)
	require.Eventually(s.t, func() bool { return s.executionCount(endpoint, executor) == metric+1 }, 5*time.Second, 20*time.Millisecond, "real executor metric")
	return sample
}
