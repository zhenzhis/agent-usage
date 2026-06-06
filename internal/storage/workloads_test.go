package storage

import (
	"testing"
	"time"
)

func TestBackfillWorkloadsFromUsageIsIdempotent(t *testing.T) {
	db := tempDB(t)
	ts := time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)
	if err := db.UpsertSession(&SessionRecord{
		Source:    "codex",
		SessionID: "sess-1",
		Project:   "repo-a",
		CWD:       "/home/user/repo-a",
		GitBranch: "main",
		StartTime: ts,
		Prompts:   2,
	}); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := db.InsertUsage(&UsageRecord{
			Source:                   "codex",
			SessionID:                "sess-1",
			Model:                    "gpt-5",
			InputTokens:              int64(100 + i),
			OutputTokens:             50,
			CacheReadInputTokens:     20,
			CacheCreationInputTokens: 10,
			CostUSD:                  0.25,
			Timestamp:                ts.Add(time.Duration(i) * time.Minute),
			Project:                  "repo-a",
			GitBranch:                "main",
			PricingSource:            "test",
			PricingConfidence:        "official",
		}); err != nil {
			t.Fatalf("InsertUsage: %v", err)
		}
	}

	from := ts.Add(-time.Hour)
	to := ts.Add(time.Hour)
	for i := 0; i < 2; i++ {
		if err := db.BackfillWorkloadsFromUsage(from, to); err != nil {
			t.Fatalf("BackfillWorkloadsFromUsage pass %d: %v", i, err)
		}
	}

	page, err := db.GetWorkloadsPage(from, to, "", "", "", "", "", 10, 0)
	if err != nil {
		t.Fatalf("GetWorkloadsPage: %v", err)
	}
	if page.Total != 1 || len(page.Rows) != 1 {
		t.Fatalf("expected one workload, total=%d rows=%d", page.Total, len(page.Rows))
	}
	row := page.Rows[0]
	if row.ModelCalls != 2 {
		t.Fatalf("expected 2 model calls, got %d", row.ModelCalls)
	}
	if row.Runs != 1 || row.Sessions != 1 {
		t.Fatalf("expected one run/session, got runs=%d sessions=%d", row.Runs, row.Sessions)
	}
	wantTokens := int64((100 + 50 + 20 + 10) + (101 + 50 + 20 + 10))
	if row.Tokens != wantTokens {
		t.Fatalf("tokens mismatch: got %d want %d", row.Tokens, wantTokens)
	}
	if row.CostUSD != 0.5 {
		t.Fatalf("cost mismatch: got %f", row.CostUSD)
	}
}

func TestManualWorkloadAndRunDetail(t *testing.T) {
	db := tempDB(t)
	id, err := db.CreateWorkload("ship agent telemetry", "codex", "repo-a", "repo-a", "main", "alice", "research", 12.5)
	if err != nil {
		t.Fatalf("CreateWorkload: %v", err)
	}
	runID, err := db.StartAgentRun(id, "codex", "codex", "codex exec", "/home/user/repo-a")
	if err != nil {
		t.Fatalf("StartAgentRun: %v", err)
	}
	if err := db.FinishAgentRun(runID, "completed", 0, "", 1234); err != nil {
		t.Fatalf("FinishAgentRun: %v", err)
	}
	if err := db.CloseWorkload(id, "completed", "tests-passed"); err != nil {
		t.Fatalf("CloseWorkload: %v", err)
	}

	detail, err := db.GetWorkloadDetail(id)
	if err != nil {
		t.Fatalf("GetWorkloadDetail: %v", err)
	}
	if detail.Summary.WorkloadID != id {
		t.Fatalf("id mismatch: got %s want %s", detail.Summary.WorkloadID, id)
	}
	if detail.Summary.Status != "completed" || detail.Summary.Outcome != "tests-passed" {
		t.Fatalf("bad status/outcome: %s/%s", detail.Summary.Status, detail.Summary.Outcome)
	}
	if len(detail.Runs) != 1 || detail.Runs[0].RunID != runID {
		t.Fatalf("run detail missing: %+v", detail.Runs)
	}
}
