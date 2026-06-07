package storage

import (
	"testing"
	"time"
)

func TestGetFleetAttributionDetectsSubAgentsAndParallelRuns(t *testing.T) {
	db := tempDB(t)
	ts := time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)
	seedFleetRun(t, db, "wl1", "Build agent infra", "alpha", "parent", "", ts, ts.Add(30*time.Minute), 100, 50, 1.5)
	seedFleetRun(t, db, "wl1", "Build agent infra", "alpha", "child", "parent", ts.Add(5*time.Minute), ts.Add(20*time.Minute), 200, 50, 2.5)
	seedFleetRun(t, db, "wl1", "Build agent infra", "alpha", "parallel", "", ts.Add(10*time.Minute), ts.Add(25*time.Minute), 300, 50, 3.5)

	report, err := db.GetFleetAttribution(ts.Add(-time.Hour), ts.Add(time.Hour), "codex", "", "alpha", 20)
	if err != nil {
		t.Fatalf("GetFleetAttribution: %v", err)
	}
	if report.Runs != 3 || report.SubAgentRuns != 1 || report.MaxConcurrent != 3 {
		t.Fatalf("unexpected fleet totals: %+v", report)
	}
	if report.ModelCalls != 3 || report.Tokens != 750 || !near(report.CostUSD, 7.5, 0.000001) {
		t.Fatalf("unexpected usage totals: %+v", report)
	}
	byRun := map[string]FleetAttributionRow{}
	for _, row := range report.Rows {
		byRun[row.RunID] = row
	}
	if byRun["child"].Attribution != "sub-agent" || byRun["child"].ConcurrentRuns != 3 {
		t.Fatalf("child attribution wrong: %+v", byRun["child"])
	}
	if byRun["parent"].ChildRuns != 1 || byRun["parent"].Attribution != "parent-agent" {
		t.Fatalf("parent attribution wrong: %+v", byRun["parent"])
	}
	if byRun["parallel"].Attribution != "parallel-run" {
		t.Fatalf("parallel attribution wrong: %+v", byRun["parallel"])
	}
}

func seedFleetRun(t *testing.T, db *DB, workloadID, goal, project, runID, parentRunID string, start, end time.Time, input, output int64, cost float64) {
	t.Helper()
	if _, err := db.db.Exec(`INSERT OR IGNORE INTO workloads(workload_id,goal,status,source,project,repo,git_branch,team,budget_usd,outcome,confidence,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, workloadID, goal, "active", "codex", project, "repo", "main", "research", 0, "", 1, start, end); err != nil {
		t.Fatalf("insert workload: %v", err)
	}
	if _, err := db.db.Exec(`INSERT INTO agent_runs(run_id,workload_id,parent_run_id,source,agent_name,status,started_at,ended_at,duration_ms,confidence)
		VALUES(?,?,?,?,?,?,?,?,?,?)`, runID, workloadID, parentRunID, "codex", "codex", "completed", start, end, int64(end.Sub(start)/time.Millisecond), 1); err != nil {
		t.Fatalf("insert run: %v", err)
	}
	if _, err := db.db.Exec(`INSERT INTO model_calls(call_id,workload_id,run_id,source,session_id,provider,model,input_tokens,output_tokens,cost_usd,timestamp,confidence)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, "call-"+runID, workloadID, runID, "codex", "session-"+runID, "openai", "gpt-5", input, output, cost, start.Add(time.Minute), 1); err != nil {
		t.Fatalf("insert model call: %v", err)
	}
}
