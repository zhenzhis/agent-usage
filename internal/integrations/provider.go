package integrations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zhenzhis/agent-ledger/internal/storage"
)

// ProviderCall is a metadata-only provider response usage envelope.
type ProviderCall struct {
	ID        string                 `json:"id"`
	Provider  string                 `json:"provider"`
	Model     string                 `json:"model"`
	Project   string                 `json:"project,omitempty"`
	SessionID string                 `json:"session_id,omitempty"`
	Timestamp time.Time              `json:"timestamp,omitempty"`
	Usage     ProviderUsage          `json:"usage"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// ProviderUsage uses Agent Ledger non-overlapping token semantics.
type ProviderUsage struct {
	InputTokens              int     `json:"input_tokens"`
	CacheReadInputTokens     int     `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int     `json:"cache_creation_input_tokens"`
	OutputTokens             int     `json:"output_tokens"`
	ReasoningOutputTokens    int     `json:"reasoning_output_tokens"`
	CostUSD                  float64 `json:"cost_usd,omitempty"`
}

// DecodeProviderCalls decodes OpenAI-style, Anthropic-style, LiteLLM-style, or generic usage response envelopes.
func DecodeProviderCalls(raw []byte) ([]ProviderCall, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty provider input")
	}
	if trimmed[0] == '[' {
		var entries []json.RawMessage
		if err := json.Unmarshal(trimmed, &entries); err != nil {
			return nil, err
		}
		return decodeProviderEntries(entries)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return nil, err
	}
	inheritedMetadata := providerEnvelopeMetadataFromRaw(trimmed)
	for _, key := range []string{"responses", "calls", "items"} {
		if rawEntries, ok := obj[key]; ok {
			var entries []json.RawMessage
			if err := json.Unmarshal(rawEntries, &entries); err != nil {
				return nil, err
			}
			return decodeProviderEntries(entries, inheritedMetadata)
		}
	}
	call, err := decodeProviderEntry(trimmed)
	if err != nil {
		return nil, err
	}
	return []ProviderCall{call}, nil
}

// ConvertProviderCalls projects provider usage envelopes into canonical model.call and context.ref events.
func ConvertProviderCalls(calls []ProviderCall) ([]storage.CanonicalEvent, error) {
	events := []storage.CanonicalEvent{}
	for _, call := range calls {
		if call.Model == "" {
			return nil, fmt.Errorf("provider call model is required")
		}
		callID := firstNonEmpty(call.ID, deterministicLedgerID("call", "provider-call:"+call.Provider+":"+call.Model+":"+call.Timestamp.String()))
		source := firstNonEmpty(metadataString(call.Metadata, "agent_ledger.source", "source"), "provider")
		goal := firstNonEmpty(metadataString(call.Metadata, "agent_ledger.goal", "goal"), "Provider model call "+callID)
		workloadID := metadataString(call.Metadata, "agent_ledger.workload_id", "workload_id")
		if workloadID == "" && goal != "" {
			workloadID = deterministicLedgerID("wl", "provider-workload:"+goal+":"+firstNonEmpty(call.SessionID, callID))
		}
		runID := metadataString(call.Metadata, "agent_ledger.agent_run_id", "agent_run_id")
		timestamp := firstTime(call.Timestamp, time.Now().UTC())
		sourceVersion := firstNonEmpty(metadataString(call.Metadata, "agent_ledger.source_version", "provider_version", "provider_usage_schema"), call.Provider)
		parserVersion := firstNonEmpty(metadataString(call.Metadata, "agent_ledger.parser_version"), "agent-ledger-provider-usage@v1")
		rawRef := firstNonEmpty(metadataString(call.Metadata, "agent_ledger.raw_ref", "raw_ref"), "provider:"+firstNonEmpty(call.Provider, "unknown")+":"+callID)
		matchType := firstNonEmpty(metadataString(call.Metadata, "agent_ledger.match_type", "match_type"), "source_reported")
		reconciliationHash := providerReconciliationHash(call)
		payload := map[string]interface{}{
			"goal":                         goal,
			"call_id":                      callID,
			"provider":                     call.Provider,
			"model":                        call.Model,
			"model_alias":                  metadataString(call.Metadata, "model_alias", "agent_ledger.model_alias"),
			"input_tokens":                 call.Usage.InputTokens,
			"cache_read_input_tokens":      call.Usage.CacheReadInputTokens,
			"cache_creation_input_tokens":  call.Usage.CacheCreationInputTokens,
			"output_tokens":                call.Usage.OutputTokens,
			"reasoning_output_tokens":      call.Usage.ReasoningOutputTokens,
			"cost_usd":                     call.Usage.CostUSD,
			"latency_ms":                   metadataInt(call.Metadata, "latency_ms", "agent_ledger.latency_ms"),
			"pricing_source":               firstNonEmpty(metadataString(call.Metadata, "pricing_source"), "provider-reported"),
			"pricing_confidence":           firstNonEmpty(metadataString(call.Metadata, "pricing_confidence"), "provider-usage"),
			"finish_reason":                metadataString(call.Metadata, "finish_reason"),
			"provider_request_id":          metadataString(call.Metadata, "provider_request_id", "request_id"),
			"provider_response_id":         firstNonEmpty(call.ID, metadataString(call.Metadata, "provider_response_id", "response_id")),
			"provider_status_code":         metadataInt(call.Metadata, "provider_status_code", "status_code", "statusCode"),
			"provider_endpoint":            metadataString(call.Metadata, "provider_endpoint", "endpoint_path", "endpoint"),
			"provider_stream":              metadataBool(call.Metadata, "provider_stream", "stream"),
			"reconciliation_ref_hash":      reconciliationHash,
			"reconciliation_window_start":  metadataString(call.Metadata, "reconciliation_window_start", "billing_window_start", "window_start"),
			"reconciliation_window_end":    metadataString(call.Metadata, "reconciliation_window_end", "billing_window_end", "window_end"),
			"provider_usage_schema":        metadataString(call.Metadata, "provider_usage_schema"),
			"agent_ledger_provider_mapper": "v1",
		}
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		modelEvent := storage.CanonicalEvent{
			EventID:       "provider:" + callID,
			Source:        source,
			EventType:     "model.call",
			SchemaVersion: "v1",
			SourceVersion: sourceVersion,
			ParserVersion: parserVersion,
			SourceEventID: callID,
			RawRef:        rawRef,
			MatchType:     matchType,
			WorkloadID:    workloadID,
			AgentRunID:    runID,
			SessionID:     call.SessionID,
			Model:         call.Model,
			Project:       call.Project,
			GitBranch:     metadataString(call.Metadata, "agent_ledger.git_branch", "git_branch"),
			Timestamp:     timestamp,
			Payload:       payloadJSON,
			Confidence:    providerConfidence(call),
		}
		events = append(events, modelEvent)
		contextJSON, err := json.Marshal(map[string]interface{}{
			"context_ref_id": "providerctx:" + callID,
			"ref_type":       "provider_response",
			"ref_hash":       hashRef("provider:" + callID),
			"label":          firstNonEmpty(call.Provider, "provider") + " response " + callID,
			"privacy_label":  "local",
			"goal":           goal,
		})
		if err != nil {
			return nil, err
		}
		events = append(events, storage.CanonicalEvent{
			EventID:       "providerctx:" + callID,
			Source:        source,
			EventType:     "context.ref",
			SchemaVersion: modelEvent.SchemaVersion,
			SourceVersion: modelEvent.SourceVersion,
			ParserVersion: modelEvent.ParserVersion,
			SourceEventID: "providerctx:" + callID,
			RawRef:        modelEvent.RawRef + ":context",
			MatchType:     "reconstructed",
			WorkloadID:    workloadID,
			AgentRunID:    runID,
			SessionID:     call.SessionID,
			Model:         call.Model,
			Project:       call.Project,
			GitBranch:     modelEvent.GitBranch,
			Timestamp:     timestamp,
			Payload:       contextJSON,
			Confidence:    modelEvent.Confidence,
		})
		if reconciliationHash != "" {
			reconciliationJSON, err := json.Marshal(map[string]interface{}{
				"context_ref_id": "providerrecon:" + callID,
				"ref_type":       "provider_reconciliation",
				"ref_hash":       reconciliationHash,
				"label":          firstNonEmpty(call.Provider, "provider") + " billing evidence",
				"privacy_label":  "local",
				"goal":           goal,
			})
			if err != nil {
				return nil, err
			}
			events = append(events, storage.CanonicalEvent{
				EventID:       "providerrecon:" + callID,
				Source:        source,
				EventType:     "context.ref",
				SchemaVersion: modelEvent.SchemaVersion,
				SourceVersion: modelEvent.SourceVersion,
				ParserVersion: modelEvent.ParserVersion,
				SourceEventID: "providerrecon:" + callID,
				RawRef:        modelEvent.RawRef + ":reconciliation",
				MatchType:     "reconstructed",
				WorkloadID:    workloadID,
				AgentRunID:    runID,
				SessionID:     call.SessionID,
				Model:         call.Model,
				Project:       call.Project,
				GitBranch:     modelEvent.GitBranch,
				Timestamp:     timestamp,
				Payload:       reconciliationJSON,
				Confidence:    modelEvent.Confidence,
			})
		}
	}
	return events, nil
}

func decodeProviderEntries(entries []json.RawMessage, inheritedMetadata ...map[string]interface{}) ([]ProviderCall, error) {
	out := make([]ProviderCall, 0, len(entries))
	for _, entry := range entries {
		call, err := decodeProviderEntry(entry)
		if err != nil {
			return nil, err
		}
		if len(inheritedMetadata) > 0 {
			call.Metadata = mergeProviderMetadata(inheritedMetadata[0], call.Metadata)
			if call.Project == "" {
				call.Project = firstNonEmpty(metadataString(call.Metadata, "agent_ledger.project", "project"), call.Project)
			}
			if call.SessionID == "" {
				call.SessionID = firstNonEmpty(metadataString(call.Metadata, "agent_ledger.session_id", "session_id"), call.SessionID)
			}
		}
		out = append(out, call)
	}
	return out, nil
}

func decodeProviderEntry(raw json.RawMessage) (ProviderCall, error) {
	var obj map[string]interface{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ProviderCall{}, err
	}
	metadata := providerEnvelopeMetadata(obj)
	request, _ := objectValue(obj["request"])
	response, _ := objectValue(obj["response"])
	provider := firstNonEmpty(
		firstNonNilString(obj, "provider", "system", "gen_ai.provider.name", "provider_name", "providerName"),
		stringFromObject(response, "provider", "system", "gen_ai.provider.name", "provider_name", "providerName"),
		stringFromObject(request, "provider", "system", "gen_ai.provider.name", "provider_name", "providerName"),
		metadataString(metadata, "provider", "provider_name", "providerName"),
	)
	usage, schema := providerUsage(obj)
	if schema == "" {
		return ProviderCall{}, fmt.Errorf("provider usage object is required")
	}
	if metadataString(metadata, "provider_usage_schema") == "" {
		metadata["provider_usage_schema"] = schema
	}
	if finish := firstNonEmpty(firstNonNilString(obj, "finish_reason", "stop_reason"), stringFromObject(response, "finish_reason", "stop_reason")); finish != "" {
		metadata["finish_reason"] = finish
	}
	return ProviderCall{
		ID: firstNonEmpty(
			firstNonNilString(obj, "id", "response_id", "completion_id", "request_id", "run_id"),
			stringFromObject(response, "id", "response_id", "completion_id", "request_id", "run_id"),
			stringFromObject(request, "id", "request_id", "run_id"),
			metadataString(metadata, "provider_response_id", "response_id", "provider_request_id", "request_id"),
		),
		Provider:  provider,
		Model:     firstNonEmpty(firstNonNilString(obj, "model", "model_id", "modelID", "model_name", "modelName"), stringFromObject(response, "model", "model_id", "modelID", "model_name", "modelName"), stringFromObject(request, "model", "model_id", "modelID", "model_name", "modelName"), metadataString(metadata, "model", "model_id", "modelID", "model_name", "modelName")),
		Project:   firstNonEmpty(metadataString(metadata, "agent_ledger.project", "project"), firstNonNilString(obj, "project")),
		SessionID: firstNonEmpty(metadataString(metadata, "agent_ledger.session_id", "session_id"), firstNonNilString(obj, "session_id")),
		Timestamp: firstTime(providerTimestamp(obj), providerTimestamp(response), providerTimestamp(request), metadataTimestamp(metadata)),
		Usage:     usage,
		Metadata:  metadata,
	}, nil
}

func providerEnvelopeMetadataFromRaw(raw []byte) map[string]interface{} {
	var obj map[string]interface{}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return map[string]interface{}{}
	}
	return providerEnvelopeMetadata(obj)
}

func providerEnvelopeMetadata(obj map[string]interface{}) map[string]interface{} {
	metadata := map[string]interface{}{}
	mergeProviderMetadataInto(metadata, objectOrEmpty(obj["metadata"]), false)
	mergeSafeProviderMetadataInto(metadata, obj)
	for _, key := range []string{"request_metadata", "requestMetadata", "response_metadata", "responseMetadata", "billing", "reconciliation"} {
		mergeSafeProviderMetadataInto(metadata, objectOrEmpty(obj[key]))
	}
	if request, ok := objectValue(obj["request"]); ok {
		mergeSafeProviderMetadataInto(metadata, request)
		mergeSafeProviderMetadataInto(metadata, objectOrEmpty(request["metadata"]))
		if id := firstNonNilString(request, "id", "request_id", "requestId", "run_id"); id != "" {
			metadata["provider_request_id"] = id
		}
	}
	if response, ok := objectValue(obj["response"]); ok {
		mergeSafeProviderMetadataInto(metadata, response)
		mergeSafeProviderMetadataInto(metadata, objectOrEmpty(response["metadata"]))
		if id := firstNonNilString(response, "id", "response_id", "responseId", "completion_id"); id != "" {
			metadata["provider_response_id"] = id
		}
	}
	return metadata
}

func mergeProviderMetadata(base, override map[string]interface{}) map[string]interface{} {
	merged := map[string]interface{}{}
	mergeProviderMetadataInto(merged, base, false)
	mergeProviderMetadataInto(merged, override, false)
	return merged
}

func mergeProviderMetadataInto(dst, src map[string]interface{}, onlyIfAbsent bool) {
	for key, value := range src {
		if value == nil {
			continue
		}
		if onlyIfAbsent {
			if _, exists := dst[key]; exists {
				continue
			}
		}
		dst[key] = value
	}
}

func mergeSafeProviderMetadataInto(dst map[string]interface{}, src map[string]interface{}) {
	for _, key := range providerSafeMetadataKeys() {
		if value, ok := src[key]; ok && value != nil {
			normalized := providerSafeMetadataValue(key, value)
			if normalized != "" {
				dst[key] = normalized
			}
		}
	}
	if id := firstNonNilString(src, "request_id", "requestId"); id != "" {
		dst["provider_request_id"] = id
	}
	if id := firstNonNilString(src, "response_id", "responseId", "completion_id", "id"); id != "" {
		dst["provider_response_id"] = id
	}
	if endpoint := firstNonNilString(src, "endpoint", "endpoint_path", "path", "url"); endpoint != "" {
		dst["provider_endpoint"] = sanitizeProviderEndpoint(endpoint)
	}
	if status := intFromMap(src, "status_code", "statusCode", "http_status", "httpStatus"); status > 0 {
		dst["provider_status_code"] = status
	}
	if latency := intFromMap(src, "latency_ms", "latencyMs", "duration_ms", "durationMs"); latency > 0 {
		dst["latency_ms"] = latency
	}
	if stream, ok := boolFromMap(src, "stream", "streaming"); ok {
		dst["provider_stream"] = stream
	}
}

func providerSafeMetadataKeys() []string {
	return []string{
		"agent_ledger.source", "agent_ledger.goal", "agent_ledger.project", "agent_ledger.workload_id", "agent_ledger.agent_run_id", "agent_ledger.session_id", "agent_ledger.git_branch", "agent_ledger.source_version", "agent_ledger.parser_version", "agent_ledger.raw_ref", "agent_ledger.match_type", "agent_ledger.model_alias", "agent_ledger.latency_ms",
		"source", "goal", "project", "workload_id", "agent_run_id", "run_id", "session_id", "git_branch", "branch", "source_version", "provider_version", "parser_version", "raw_ref", "match_type", "model_alias", "pricing_source", "pricing_confidence", "finish_reason",
		"provider", "provider_name", "providerName", "model", "model_id", "modelID", "model_name", "modelName", "provider_usage_schema",
		"reconciliation_ref_hash", "provider_statement_hash", "statement_hash", "payload_sha256", "invoice_hash", "reconciliation_ref", "statement_id", "invoice_id", "provider_bill_ref", "reconciliation_window_start", "reconciliation_window_end", "billing_window_start", "billing_window_end", "window_start", "window_end", "provider_account_hash", "organization_hash",
	}
}

func providerSafeMetadataValue(key string, value interface{}) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" || text == "<nil>" {
		return ""
	}
	switch key {
	case "endpoint", "endpoint_path", "path", "url":
		return sanitizeProviderEndpoint(text)
	case "reconciliation_ref_hash", "provider_statement_hash", "statement_hash", "payload_sha256", "invoice_hash", "provider_account_hash", "organization_hash":
		return providerHashValue(text)
	default:
		return text
	}
}

func objectOrEmpty(value interface{}) map[string]interface{} {
	if obj, ok := objectValue(value); ok {
		return obj
	}
	return map[string]interface{}{}
}

func sanitizeProviderEndpoint(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if parsed, err := url.Parse(raw); err == nil && parsed.Path != "" {
		return parsed.Path
	}
	if idx := strings.Index(raw, "?"); idx >= 0 {
		raw = raw[:idx]
	}
	if idx := strings.Index(raw, "#"); idx >= 0 {
		raw = raw[:idx]
	}
	if raw == "" || strings.Contains(raw, " ") {
		return ""
	}
	return raw
}

func providerUsage(obj map[string]interface{}) (ProviderUsage, string) {
	usage, schemaHint, ok := providerUsageObject(obj)
	if !ok {
		return ProviderUsage{}, ""
	}
	inputTotal := intFromMap(usage,
		"input_tokens",
		"prompt_tokens",
		"promptTokenCount",
		"inputTokenCount",
		"totalInputTokens",
		"total_input_tokens",
	)
	cacheRead := nestedInt(usage, "input_tokens_details", "cached_tokens") +
		nestedInt(usage, "prompt_tokens_details", "cached_tokens") +
		intFromMap(usage,
			"cache_read_input_tokens",
			"cache_read_tokens",
			"cacheReadInputTokens",
			"cacheReadTokens",
			"cachedContentTokenCount",
			"cached_content_token_count",
		)
	cacheWrite := intFromMap(usage,
		"cache_creation_input_tokens",
		"cache_write_input_tokens",
		"cache_write_tokens",
		"cacheCreationInputTokens",
		"cacheWriteInputTokens",
		"cacheWriteTokens",
	)
	output := intFromMap(usage,
		"output_tokens",
		"completion_tokens",
		"completionTokenCount",
		"candidatesTokenCount",
		"outputTokenCount",
		"totalOutputTokens",
		"total_output_tokens",
	)
	if output == 0 {
		totalTokens := intFromMap(usage, "total_tokens", "totalTokens", "totalTokenCount")
		if totalTokens > inputTotal {
			output = totalTokens - inputTotal
		}
	}
	reasoning := nestedInt(usage, "output_tokens_details", "reasoning_tokens") +
		nestedInt(usage, "completion_tokens_details", "reasoning_tokens") +
		intFromMap(usage,
			"reasoning_output_tokens",
			"reasoningOutputTokens",
			"reasoningTokenCount",
			"thoughtsTokenCount",
		)
	nonCachedInput := inputTotal - cacheRead - cacheWrite
	if nonCachedInput < 0 {
		nonCachedInput = 0
	}
	schema := firstNonEmpty(schemaHint, "generic")
	if hasAnyKey(usage, "promptTokenCount", "inputTokenCount", "totalInputTokens", "usageMetadata", "usage_metadata") {
		schema = "usage-metadata"
	}
	if _, ok := usage["prompt_tokens"]; ok {
		schema = "openai-chat-completions"
	}
	if _, ok := usage["input_tokens"]; ok {
		schema = "openai-responses-or-anthropic"
	}
	if _, ok := usage["cache_creation_input_tokens"]; ok {
		schema = "anthropic"
	}
	return ProviderUsage{
		InputTokens:              nonCachedInput,
		CacheReadInputTokens:     cacheRead,
		CacheCreationInputTokens: cacheWrite,
		OutputTokens:             output,
		ReasoningOutputTokens:    reasoning,
		CostUSD:                  floatFromMap(usage, "cost_usd", "costUSD", "costUsd", "total_cost", "totalCost", "cost"),
	}, schema
}

func providerUsageObject(obj map[string]interface{}) (map[string]interface{}, string, bool) {
	for _, candidate := range []struct {
		key    string
		schema string
	}{
		{"usage", "generic"},
		{"usage_metadata", "usage-metadata"},
		{"usageMetadata", "usage-metadata"},
	} {
		if usage, ok := objectValue(obj[candidate.key]); ok {
			return usage, candidate.schema, true
		}
	}
	if response, ok := objectValue(obj["response"]); ok {
		return providerUsageObject(response)
	}
	return nil, "", false
}

func hasAnyKey(obj map[string]interface{}, keys ...string) bool {
	for _, key := range keys {
		if _, ok := obj[key]; ok {
			return true
		}
	}
	return false
}

func providerTimestamp(obj map[string]interface{}) time.Time {
	for _, key := range []string{"created_at", "created", "timestamp"} {
		value, ok := obj[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			if parsed, err := time.Parse(time.RFC3339Nano, typed); err == nil {
				return parsed.UTC()
			}
			if seconds, err := strconv.ParseInt(typed, 10, 64); err == nil {
				return time.Unix(seconds, 0).UTC()
			}
		case float64:
			return time.Unix(int64(typed), 0).UTC()
		}
	}
	return time.Time{}
}

func firstNonNilString(obj map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := obj[key]; ok && value != nil {
			text := fmt.Sprint(value)
			if text != "" && text != "<nil>" {
				return text
			}
		}
	}
	return ""
}

func intFromMap(obj map[string]interface{}, keys ...string) int {
	for _, key := range keys {
		if value, ok := obj[key]; ok {
			return intValue(value)
		}
	}
	return 0
}

func floatFromMap(obj map[string]interface{}, keys ...string) float64 {
	for _, key := range keys {
		if value, ok := obj[key]; ok {
			switch typed := value.(type) {
			case float64:
				return typed
			case string:
				parsed, _ := strconv.ParseFloat(typed, 64)
				return parsed
			}
		}
	}
	return 0
}

func nestedInt(obj map[string]interface{}, key, child string) int {
	if value, ok := obj[key]; ok {
		if nested, ok := value.(map[string]interface{}); ok {
			return intFromMap(nested, child)
		}
	}
	return 0
}

func intValue(value interface{}) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		parsed, _ := strconv.Atoi(typed)
		return parsed
	default:
		return 0
	}
}

func metadataInt(metadata map[string]interface{}, keys ...string) int {
	for _, key := range keys {
		if value, ok := metadata[key]; ok {
			return intValue(value)
		}
	}
	return 0
}

func metadataBool(metadata map[string]interface{}, keys ...string) bool {
	value, ok := boolFromMap(metadata, keys...)
	return ok && value
}

func metadataTimestamp(metadata map[string]interface{}) time.Time {
	for _, key := range []string{"timestamp", "created_at", "created", "request_timestamp", "response_timestamp"} {
		value, ok := metadata[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case time.Time:
			return typed.UTC()
		case string:
			if parsed, err := time.Parse(time.RFC3339Nano, typed); err == nil {
				return parsed.UTC()
			}
			if seconds, err := strconv.ParseInt(typed, 10, 64); err == nil {
				return time.Unix(seconds, 0).UTC()
			}
		case float64:
			return time.Unix(int64(typed), 0).UTC()
		case int:
			return time.Unix(int64(typed), 0).UTC()
		case int64:
			return time.Unix(typed, 0).UTC()
		}
	}
	return time.Time{}
}

func boolFromMap(obj map[string]interface{}, keys ...string) (bool, bool) {
	for _, key := range keys {
		value, ok := obj[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case bool:
			return typed, true
		case string:
			switch strings.ToLower(strings.TrimSpace(typed)) {
			case "1", "true", "yes":
				return true, true
			case "0", "false", "no":
				return false, true
			}
		case float64:
			return typed != 0, true
		case int:
			return typed != 0, true
		}
	}
	return false, false
}

func providerReconciliationHash(call ProviderCall) string {
	if hash := metadataString(call.Metadata, "reconciliation_ref_hash", "provider_statement_hash", "statement_hash", "payload_sha256", "invoice_hash"); hash != "" {
		return providerHashValue(hash)
	}
	ref := metadataString(call.Metadata, "reconciliation_ref", "statement_id", "invoice_id", "provider_bill_ref")
	if ref == "" {
		return ""
	}
	return hashRef(firstNonEmpty(call.Provider, "provider") + ":" + ref)
}

func providerHashValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(value), "sha256:") {
		return value
	}
	if len(value) == 64 && isLowerHex(value) {
		return "sha256:" + value
	}
	return hashRef(value)
}

func isLowerHex(value string) bool {
	for _, ch := range value {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
			return false
		}
	}
	return true
}

func providerConfidence(call ProviderCall) float64 {
	if call.Model != "" && (call.Usage.InputTokens+call.Usage.CacheReadInputTokens+call.Usage.CacheCreationInputTokens+call.Usage.OutputTokens) > 0 {
		return 0.9
	}
	return 0.65
}
