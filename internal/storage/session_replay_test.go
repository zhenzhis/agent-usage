package storage

import (
	"encoding/json"
	"testing"
	"time"
)

func TestGetSessionReplay(t *testing.T) {
	db := tempDB(t)
	ts := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	if err := db.InsertUsageBatch([]*UsageRecord{
		{Source: "codex", SessionID: "s1", Model: "m1", Timestamp: ts, InputTokens: 100, OutputTokens: 50, CostUSD: 1.5, PricingSource: "test", PricingModel: "m1", PricingConfidence: "override"},
		{Source: "codex", SessionID: "s1", Model: "m1", Timestamp: ts.Add(time.Minute), InputTokens: 20, OutputTokens: 10, CacheReadInputTokens: 70, CacheCreationInputTokens: 5, CostUSD: 0.5, PricingSource: "test", PricingModel: "m1", PricingConfidence: "override"},
	}); err != nil {
		t.Fatalf("InsertUsageBatch: %v", err)
	}
	report, err := db.GetSessionReplay("codex", "s1", 100)
	if err != nil {
		t.Fatalf("GetSessionReplay: %v", err)
	}
	if report.Source != "codex" || report.SessionID != "s1" {
		t.Fatalf("unexpected identity: %+v", report)
	}
	if report.Calls != 2 || len(report.Points) != 2 {
		t.Fatalf("calls=%d points=%d", report.Calls, len(report.Points))
	}
	if report.TotalTokens != 255 || report.PeakTokensPerCall != 150 {
		t.Fatalf("tokens=%d peak=%d", report.TotalTokens, report.PeakTokensPerCall)
	}
	if !near(report.TotalCostUSD, 2.0, 0.000001) {
		t.Fatalf("cost=%f", report.TotalCostUSD)
	}
	if report.Points[1].CumulativeTokens != 255 || !near(report.Points[1].CumulativeCostUSD, 2.0, 0.000001) {
		t.Fatalf("bad cumulative point: %+v", report.Points[1])
	}
	if report.Points[1].PricingConfidence != "override" {
		t.Fatalf("pricing metadata not preserved: %+v", report.Points[1])
	}
}

func TestGetSessionReplayRequiresSourceForAmbiguousSession(t *testing.T) {
	db := tempDB(t)
	ts := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	if err := db.InsertUsageBatch([]*UsageRecord{
		{Source: "codex", SessionID: "same", Model: "m", Timestamp: ts, CostUSD: 1},
		{Source: "opencode", SessionID: "same", Model: "m", Timestamp: ts, CostUSD: 1},
	}); err != nil {
		t.Fatalf("InsertUsageBatch: %v", err)
	}
	if _, err := db.GetSessionReplay("", "same", 100); err == nil {
		t.Fatalf("expected ambiguous source error")
	}
	if report, err := db.GetSessionReplay("codex", "same", 100); err != nil || report.Calls != 1 {
		t.Fatalf("scoped replay failed: report=%+v err=%v", report, err)
	}
}

func TestGetSessionReplayTruncates(t *testing.T) {
	db := tempDB(t)
	ts := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	if err := db.InsertUsageBatch([]*UsageRecord{
		{Source: "codex", SessionID: "s1", Model: "m", Timestamp: ts, CostUSD: 1},
		{Source: "codex", SessionID: "s1", Model: "m", Timestamp: ts.Add(time.Second), CostUSD: 1},
	}); err != nil {
		t.Fatalf("InsertUsageBatch: %v", err)
	}
	report, err := db.GetSessionReplay("codex", "s1", 1)
	if err != nil {
		t.Fatalf("GetSessionReplay: %v", err)
	}
	if !report.Truncated || len(report.Points) != 1 {
		t.Fatalf("expected truncated one-point report: %+v", report)
	}
}

func TestGetSessionReplayFallsBackToCanonicalModelCalls(t *testing.T) {
	db := tempDB(t)
	ts := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	payload, _ := json.Marshal(map[string]interface{}{
		"goal":               "replay canonical call",
		"call_id":            "call-1",
		"input_tokens":       10,
		"output_tokens":      20,
		"cost_usd":           0.25,
		"pricing_source":     "gateway",
		"pricing_confidence": "source-reported",
	})
	_, err := db.IngestCanonicalEvent(CanonicalEvent{
		EventID:   "evt-replay-call",
		Source:    "gateway",
		EventType: "model.call",
		SessionID: "canon-s1",
		Model:     "gpt-test",
		Timestamp: ts,
		Payload:   payload,
	})
	if err != nil {
		t.Fatalf("IngestCanonicalEvent: %v", err)
	}
	report, err := db.GetSessionReplay("gateway", "canon-s1", 100)
	if err != nil {
		t.Fatalf("GetSessionReplay canonical: %v", err)
	}
	if report.Calls != 1 || report.TotalTokens != 30 || !near(report.TotalCostUSD, 0.25, 0.000001) {
		t.Fatalf("unexpected canonical replay: %+v", report)
	}
	if report.Points[0].PricingSource != "gateway" || report.Points[0].PricingConfidence != "source-reported" {
		t.Fatalf("canonical pricing metadata missing: %+v", report.Points[0])
	}
}
