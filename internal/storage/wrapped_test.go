package storage

import (
	"strings"
	"testing"
	"time"
)

func TestGetAgentWrapped(t *testing.T) {
	db := tempDB(t)
	ts := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	if err := db.InsertUsageBatch([]*UsageRecord{
		{Source: "codex", SessionID: "s1", Model: "gpt-5", Project: "alpha", Timestamp: ts, InputTokens: 100, CacheReadInputTokens: 100, OutputTokens: 50, CostUSD: 2},
		{Source: "codex", SessionID: "s1", Model: "gpt-5", Project: "alpha", Timestamp: ts.Add(time.Minute), InputTokens: 200, CacheReadInputTokens: 200, OutputTokens: 100, CostUSD: 3},
		{Source: "opencode", SessionID: "s2", Model: "glm", Project: "beta", Timestamp: ts.Add(24 * time.Hour), InputTokens: 1000, OutputTokens: 100, CostUSD: 1},
	}); err != nil {
		t.Fatalf("InsertUsageBatch: %v", err)
	}
	if err := db.InsertPromptBatch([]*PromptEvent{
		{Source: "codex", SessionID: "s1", Model: "gpt-5", Project: "alpha", Timestamp: ts},
		{Source: "opencode", SessionID: "s2", Model: "glm", Project: "beta", Timestamp: ts.Add(24 * time.Hour)},
	}); err != nil {
		t.Fatalf("InsertPromptBatch: %v", err)
	}
	report, err := db.GetAgentWrapped(ts.Add(-time.Hour), ts.Add(48*time.Hour), "monthly", "", "", "")
	if err != nil {
		t.Fatalf("GetAgentWrapped: %v", err)
	}
	if report.Stats.TotalCalls != 3 || report.Stats.TotalPrompts != 2 || report.Stats.TotalTokens != 1850 {
		t.Fatalf("unexpected stats: %+v", report.Stats)
	}
	if report.TopModel.Model != "gpt-5" || !near(report.TopModel.Cost, 5, 0.000001) {
		t.Fatalf("unexpected top model: %+v", report.TopModel)
	}
	if report.TopProject.Project != "alpha" || report.TopProject.Sessions != 1 || !near(report.TopProject.CostUSD, 5, 0.000001) {
		t.Fatalf("unexpected top project: %+v", report.TopProject)
	}
	if report.MostActiveDay.Date != "2026-06-02" || report.MostActiveDay.Tokens != 1100 {
		t.Fatalf("unexpected active day: %+v", report.MostActiveDay)
	}
	if report.BestCacheDay.Date != "2026-06-01" || !near(report.BestCacheDay.CacheHitRate, 0.5, 0.000001) {
		t.Fatalf("unexpected cache day: %+v", report.BestCacheDay)
	}
	if report.MostExpensiveSession.SessionID != "s1" || !near(report.MostExpensiveSession.CostUSD, 5, 0.000001) {
		t.Fatalf("unexpected expensive session: %+v", report.MostExpensiveSession)
	}
	md := FormatWrappedMarkdown(report)
	if !strings.Contains(md, "Agent Ledger Wrapped") || !strings.Contains(md, "top model") {
		t.Fatalf("unexpected markdown: %s", md)
	}
}

func TestGetAgentWrappedEmpty(t *testing.T) {
	db := tempDB(t)
	ts := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	report, err := db.GetAgentWrapped(ts, ts.Add(time.Hour), "month", "", "", "")
	if err != nil {
		t.Fatalf("GetAgentWrapped empty: %v", err)
	}
	if report.Stats.TotalCalls != 0 || len(report.Issues) == 0 {
		t.Fatalf("expected explicit empty issue: %+v", report)
	}
}
