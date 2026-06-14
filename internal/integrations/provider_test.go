package integrations

import (
	"encoding/json"
	"testing"

	"github.com/zhenzhis/agent-ledger/internal/storage"
)

func TestConvertOpenAIResponsesUsage(t *testing.T) {
	raw := []byte(`{
		"id":"resp_123",
		"provider":"openai",
		"model":"gpt-5.5",
		"created":1780830000,
		"input":[{"role":"user","content":"must not persist"}],
		"output":[{"content":[{"text":"must not persist either"}]}],
		"usage":{
			"input_tokens":100,
			"input_tokens_details":{"cached_tokens":30},
			"output_tokens":50,
			"output_tokens_details":{"reasoning_tokens":10}
		},
		"metadata":{"agent_ledger.goal":"provider smoke","agent_ledger.project":"agent-ledger"}
	}`)
	calls, err := DecodeProviderCalls(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	events, err := ConvertProviderCalls(calls)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	modelEvent := findCanonical(events, "model.call")
	contextEvent := findCanonical(events, "context.ref")
	if modelEvent.WorkloadID == "" || contextEvent.WorkloadID != modelEvent.WorkloadID {
		t.Fatalf("expected shared workload ids: model=%s context=%s", modelEvent.WorkloadID, contextEvent.WorkloadID)
	}
	if modelEvent.ParserVersion == "" || modelEvent.RawRef == "" || modelEvent.MatchType != "source_reported" {
		t.Fatalf("provider provenance missing: %#v", modelEvent)
	}
	if contextEvent.ParserVersion != modelEvent.ParserVersion || contextEvent.MatchType != "reconstructed" {
		t.Fatalf("context provenance missing: %#v", contextEvent)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(modelEvent.Payload, &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload["input_tokens"].(float64) != 70 || payload["cache_read_input_tokens"].(float64) != 30 || payload["output_tokens"].(float64) != 50 || payload["reasoning_output_tokens"].(float64) != 10 {
		t.Fatalf("unexpected usage payload: %#v", payload)
	}
	if containsAny(string(modelEvent.Payload), "must not persist") {
		t.Fatalf("provider content leaked: %s", string(modelEvent.Payload))
	}
}

func TestConvertProviderRequestResponseMetadataWrapper(t *testing.T) {
	raw := []byte(`{
		"provider":"openai",
		"request":{
			"id":"req_123",
			"model":"gpt-5.5",
			"messages":[{"role":"user","content":"secret wrapper prompt must not persist"}],
			"metadata":{"agent_ledger.project":"wrapped-project","agent_ledger.session_id":"wrapped-session"}
		},
		"request_metadata":{
			"endpoint":"https://api.openai.com/v1/responses?api_key=secret",
			"stream":true,
			"agent_ledger.goal":"wrapped provider call"
		},
		"response":{
			"id":"resp_123",
			"model":"gpt-5.5",
			"output":[{"content":[{"text":"secret wrapper output must not persist"}]}],
			"usage":{"input_tokens":120,"input_tokens_details":{"cached_tokens":40},"output_tokens":30}
		},
		"response_metadata":{"status_code":200,"latency_ms":345},
		"reconciliation":{"statement_id":"provider-statement-secret","billing_window_start":"2026-06-01","billing_window_end":"2026-06-30"}
	}`)
	calls, err := DecodeProviderCalls(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls=%d", len(calls))
	}
	call := calls[0]
	if call.ID != "resp_123" || call.Model != "gpt-5.5" || call.Project != "wrapped-project" || call.SessionID != "wrapped-session" {
		t.Fatalf("unexpected wrapped call identity: %#v", call)
	}
	events, err := ConvertProviderCalls(calls)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("events=%d want model call, response context, reconciliation context", len(events))
	}
	modelEvent := findCanonical(events, "model.call")
	reconciliationEvent := storage.CanonicalEvent{}
	for _, event := range events {
		if event.EventType == "context.ref" && event.EventID == "providerrecon:resp_123" {
			reconciliationEvent = event
		}
	}
	if reconciliationEvent.EventID == "" {
		t.Fatalf("missing provider reconciliation context: %+v", events)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(modelEvent.Payload, &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload["input_tokens"].(float64) != 80 || payload["cache_read_input_tokens"].(float64) != 40 || payload["output_tokens"].(float64) != 30 {
		t.Fatalf("unexpected wrapper usage payload: %#v", payload)
	}
	if payload["provider_endpoint"] != "/v1/responses" || payload["provider_status_code"].(float64) != 200 || payload["provider_stream"] != true {
		t.Fatalf("wrapper metadata missing or unsafe: %#v", payload)
	}
	if payload["provider_request_id"] != "req_123" || payload["provider_response_id"] != "resp_123" {
		t.Fatalf("provider request/response ids missing: %#v", payload)
	}
	reconHash, _ := payload["reconciliation_ref_hash"].(string)
	if reconHash == "" || reconHash == "provider-statement-secret" {
		t.Fatalf("reconciliation hash missing or raw ref leaked: %#v", payload)
	}
	serialized, _ := json.Marshal(events)
	if containsAny(string(serialized), "secret wrapper prompt", "secret wrapper output", "provider-statement-secret", "api_key=secret") {
		t.Fatalf("wrapped provider content leaked: %s", string(serialized))
	}
}

func TestConvertProviderArrayChatAndAnthropicUsage(t *testing.T) {
	raw := []byte(`[
		{
			"id":"chatcmpl_1",
			"provider":"openai",
			"model":"gpt-4.1-mini",
			"usage":{"prompt_tokens":20,"prompt_tokens_details":{"cached_tokens":5},"completion_tokens":7},
			"metadata":{"agent_ledger.goal":"chat usage"}
		},
		{
			"id":"msg_1",
			"system":"anthropic",
			"model":"claude-opus-4-7",
			"usage":{"input_tokens":40,"cache_creation_input_tokens":10,"cache_read_input_tokens":8,"output_tokens":12},
			"metadata":{"agent_ledger.goal":"anthropic usage"}
		}
	]`)
	calls, err := DecodeProviderCalls(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	events, err := ConvertProviderCalls(calls)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("events=%d", len(events))
	}
	first := events[0]
	var payload map[string]interface{}
	if err := json.Unmarshal(first.Payload, &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload["provider_usage_schema"] != "openai-chat-completions" || payload["input_tokens"].(float64) != 15 {
		t.Fatalf("unexpected chat payload: %#v", payload)
	}
	secondModel := events[2]
	if err := json.Unmarshal(secondModel.Payload, &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload["provider_usage_schema"] != "anthropic" || payload["provider"] != "anthropic" || payload["input_tokens"].(float64) != 22 {
		t.Fatalf("unexpected anthropic payload: %#v", payload)
	}
}

func TestConvertProviderUsageMetadataEnvelope(t *testing.T) {
	raw := []byte(`{
		"id":"relay_call_1",
		"providerName":"relay-compatible",
		"modelName":"gemini-2.5-pro",
		"usage_metadata":{"promptTokenCount":80,"cachedContentTokenCount":20,"candidatesTokenCount":12,"thoughtsTokenCount":3},
		"metadata":{"agent_ledger.goal":"relay usage metadata"}
	}`)
	calls, err := DecodeProviderCalls(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	events, err := ConvertProviderCalls(calls)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(findCanonical(events, "model.call").Payload, &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload["provider"] != "relay-compatible" || payload["model"] != "gemini-2.5-pro" || payload["provider_usage_schema"] != "usage-metadata" {
		t.Fatalf("unexpected usage metadata identity: %#v", payload)
	}
	if payload["input_tokens"].(float64) != 60 || payload["cache_read_input_tokens"].(float64) != 20 || payload["output_tokens"].(float64) != 12 || payload["reasoning_output_tokens"].(float64) != 3 {
		t.Fatalf("unexpected usage metadata payload: %#v", payload)
	}
}

func TestDecodeGenericUsageMetadataStream(t *testing.T) {
	raw := []byte(`event: model
data: {"id":"relay_stream_fixture_001","provider":"relay-compatible","model":"gemini-2.5-pro"}

event: usage
data: {"id":"relay_stream_fixture_001","provider":"relay-compatible","model":"gemini-2.5-pro","usageMetadata":{"promptTokenCount":120,"cachedContentTokenCount":30,"candidatesTokenCount":42,"thoughtsTokenCount":6}}

data: [DONE]
`)
	calls, err := DecodeProviderStream(raw)
	if err != nil {
		t.Fatalf("decode stream: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("calls=%d", len(calls))
	}
	call := calls[0]
	if call.Provider != "relay-compatible" || call.Model != "gemini-2.5-pro" {
		t.Fatalf("unexpected stream identity: %#v", call)
	}
	if call.Usage.InputTokens != 90 || call.Usage.CacheReadInputTokens != 30 || call.Usage.OutputTokens != 42 || call.Usage.ReasoningOutputTokens != 6 {
		t.Fatalf("unexpected stream usage: %#v", call.Usage)
	}
	events, err := ConvertProviderCalls(calls)
	if err != nil {
		t.Fatalf("convert stream: %v", err)
	}
	modelEvent := findCanonical(events, "model.call")
	var payload map[string]interface{}
	if err := json.Unmarshal(modelEvent.Payload, &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload["provider_usage_schema"] != "usage-metadata" || payload["input_tokens"].(float64) != 90 || payload["cache_read_input_tokens"].(float64) != 30 {
		t.Fatalf("unexpected usage metadata payload: %#v", payload)
	}
	if containsAny(string(modelEvent.Payload), "delta", "content", "prompt") {
		t.Fatalf("provider stream content leaked: %s", string(modelEvent.Payload))
	}
}
