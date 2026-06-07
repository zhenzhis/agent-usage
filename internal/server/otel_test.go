package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zhenzhis/agent-ledger/internal/config"
)

func TestOTLPReceiverDisabledByDefault(t *testing.T) {
	db := testServerDB(t)
	srv := New(db, "", Options{})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/v1/traces", strings.NewReader(otlpPayload("span-1")))
	rr := httptest.NewRecorder()
	srv.handleOTLPTraces(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected disabled receiver 404, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestOTLPReceiverIngestsJSONSpans(t *testing.T) {
	db := testServerDB(t)
	srv := New(db, "", Options{Integrations: config.IntegrationsConfig{
		OTLPReceiver: config.OTLPReceiverConfig{Enabled: true, MaxBodyBytes: 1 << 20, MaxSpans: 10},
	}})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/v1/traces", strings.NewReader(otlpPayload("span-1")))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.handleOTLPTraces(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if int(body["spans"].(float64)) != 1 || int(body["events"].(float64)) != 2 {
		t.Fatalf("unexpected receiver body: %#v", body)
	}
	events, err := db.GetAuditLog(10)
	if err != nil {
		t.Fatalf("GetAuditLog: %v", err)
	}
	for _, event := range events {
		if event.Action == "otlp.receiver.ingest" && event.Target == "2" {
			return
		}
	}
	t.Fatalf("missing otlp audit event: %+v", events)
}

func TestOTLPReceiverRejectsOversizedSpanBatch(t *testing.T) {
	db := testServerDB(t)
	srv := New(db, "", Options{Integrations: config.IntegrationsConfig{
		OTLPReceiver: config.OTLPReceiverConfig{Enabled: true, MaxBodyBytes: 1 << 20, MaxSpans: 1},
	}})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/otlp/v1/traces", strings.NewReader(otlpPayload("span-1", "span-2")))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.handleOTLPTraces(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestOTLPReceiverRejectsProtobuf(t *testing.T) {
	db := testServerDB(t)
	srv := New(db, "", Options{Integrations: config.IntegrationsConfig{
		OTLPReceiver: config.OTLPReceiverConfig{Enabled: true, MaxBodyBytes: 1 << 20, MaxSpans: 10},
	}})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/v1/traces", strings.NewReader("not-json"))
	req.Header.Set("Content-Type", "application/x-protobuf")
	rr := httptest.NewRecorder()
	srv.handleOTLPTraces(rr, req)
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func otlpPayload(spanIDs ...string) string {
	spans := make([]string, 0, len(spanIDs))
	for _, spanID := range spanIDs {
		spans = append(spans, `{
			"traceId":"trace-otlp",
			"spanId":"`+spanID+`",
			"name":"genai.chat",
			"startTimeUnixNano":"1780836000000000000",
			"endTimeUnixNano":"1780836001000000000",
			"attributes":[
				{"key":"gen_ai.request.model","value":{"stringValue":"gpt-5.5"}},
				{"key":"gen_ai.provider.name","value":{"stringValue":"openai"}},
				{"key":"gen_ai.usage.input_tokens","value":{"intValue":"10"}},
				{"key":"gen_ai.usage.output_tokens","value":{"intValue":"5"}},
				{"key":"agent_ledger.goal","value":{"stringValue":"receiver smoke"}},
				{"key":"agent_ledger.source","value":{"stringValue":"otlp-test"}}
			]
		}`)
	}
	return `{
		"resourceSpans":[{
			"resource":{"attributes":[{"key":"service.namespace","value":{"stringValue":"quant"}}]},
			"scopeSpans":[{"scope":{"name":"agent-ledger-test"},"spans":[` + strings.Join(spans, ",") + `]}]
		}]
	}`
}
