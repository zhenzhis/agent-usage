package collector

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCodexCollector_Scan(t *testing.T) {
	db := tempDB(t)

	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "2025", "01", "01")
	os.MkdirAll(sessionDir, 0o755)

	ts := time.Now().UTC().Format(time.RFC3339Nano)
	jsonl := `{"timestamp":"` + ts + `","type":"session_meta","payload":{"id":"codex-sess-1","cwd":"/home/user","cli_version":"0.1.0"}}
{"timestamp":"` + ts + `","type":"turn_context","payload":{"model":"o4-mini"}}
{"timestamp":"` + ts + `","type":"response_item","payload":{"role":"user"}}
{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"token_count","turn_id":"t1","info":{"last_token_usage":{"input_tokens":300,"cached_input_tokens":50,"output_tokens":150,"reasoning_output_tokens":20}}}}
`
	if err := os.WriteFile(filepath.Join(sessionDir, "session.jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cx := NewCodexCollector(db, []string{dir})
	if err := cx.Scan(); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	from := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	stats, err := db.GetDashboardStats(from, to, "", "")
	if err != nil {
		t.Fatalf("GetDashboardStats: %v", err)
	}
	if stats.TotalTokens != 450 {
		t.Errorf("expected 450 tokens, got %d", stats.TotalTokens)
	}
}

func TestCodexCollector_IncrementalScanPreservesSessionContext(t *testing.T) {
	db := tempDB(t)

	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "2025", "01", "01")
	os.MkdirAll(sessionDir, 0o755)

	ts1 := time.Now().UTC().Format(time.RFC3339Nano)
	fpath := filepath.Join(sessionDir, "session.jsonl")
	initial := `{"timestamp":"` + ts1 + `","type":"session_meta","payload":{"id":"codex-sess-1","cwd":"/home/user","cli_version":"0.1.0"}}
{"timestamp":"` + ts1 + `","type":"turn_context","payload":{"model":"gpt-5.4"}}
{"timestamp":"` + ts1 + `","type":"event_msg","payload":{"type":"token_count","turn_id":"t1","info":{"last_token_usage":{"input_tokens":300,"cached_input_tokens":50,"output_tokens":150,"reasoning_output_tokens":20}}}}
`
	if err := os.WriteFile(fpath, []byte(initial), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cx := NewCodexCollector(db, []string{dir})
	if err := cx.Scan(); err != nil {
		t.Fatalf("Scan 1: %v", err)
	}

	ts2 := time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano)
	incremental := `{"timestamp":"` + ts2 + `","type":"event_msg","payload":{"type":"token_count","turn_id":"t2","info":{"last_token_usage":{"input_tokens":400,"cached_input_tokens":100,"output_tokens":200,"reasoning_output_tokens":30}}}}
`
	f, err := os.OpenFile(fpath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if _, err := f.WriteString(incremental); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := cx.Scan(); err != nil {
		t.Fatalf("Scan 2: %v", err)
	}

	from := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)

	sessions, err := db.GetSessions(from, to, "codex", "")
	if err != nil {
		t.Fatalf("GetSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session after incremental scan, got %d", len(sessions))
	}
	if sessions[0].SessionID != "codex-sess-1" {
		t.Fatalf("expected preserved session_id codex-sess-1, got %s", sessions[0].SessionID)
	}

	details, err := db.GetSessionDetail("codex-sess-1")
	if err != nil {
		t.Fatalf("GetSessionDetail: %v", err)
	}
	if len(details) != 1 {
		t.Fatalf("expected 1 model detail row, got %d", len(details))
	}
	if details[0].Model != "gpt-5.4" {
		t.Fatalf("expected preserved model gpt-5.4, got %q", details[0].Model)
	}
	if details[0].Calls != 2 {
		t.Fatalf("expected 2 calls after incremental scan, got %d", details[0].Calls)
	}
}

func TestCodexCollector_StateSQLiteThreadsFallback(t *testing.T) {
	db := tempDB(t)
	root := t.TempDir()
	sessionsDir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, "state_5.sqlite")
	createCodexStateSQLite(t, statePath)
	upsertCodexThread(t, statePath, "thread-sqlite", "/work/agent-ledger", "main", "gpt-5-codex", 1000, 1781450000000, 1781450300000)

	cx := NewCodexCollector(db, []string{sessionsDir})
	if err := cx.Scan(); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	from := time.UnixMilli(1781450000000).Add(-time.Minute)
	to := time.UnixMilli(1781450300000).Add(time.Minute)
	stats, err := db.GetDashboardStats(from, to, "codex", "")
	if err != nil {
		t.Fatalf("GetDashboardStats: %v", err)
	}
	if stats.TotalTokens != 1000 || stats.TotalCalls != 1 || stats.TotalSessions != 1 {
		t.Fatalf("unexpected sqlite fallback stats: %+v", stats)
	}
	sessions, err := db.GetSessions(from, to, "codex", "")
	if err != nil {
		t.Fatalf("GetSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != "thread-sqlite" || sessions[0].Project != "agent-ledger" || sessions[0].GitBranch != "main" {
		t.Fatalf("unexpected sqlite fallback session: %+v", sessions)
	}
	details, err := db.GetSessionDetailScoped("codex", "thread-sqlite")
	if err != nil {
		t.Fatalf("GetSessionDetailScoped: %v", err)
	}
	if len(details) != 1 || details[0].Model != "gpt-5-codex" || details[0].InputTokens != 1000 || details[0].CostUSD != 0 {
		t.Fatalf("unexpected sqlite fallback detail: %+v", details)
	}
}

func TestCodexCollector_StateSQLiteThreadsFallbackUsesTokenDelta(t *testing.T) {
	db := tempDB(t)
	root := t.TempDir()
	sessionsDir := filepath.Join(root, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, "state_5.sqlite")
	createCodexStateSQLite(t, statePath)
	upsertCodexThread(t, statePath, "thread-delta", "/work/agent-ledger", "feature/codex", "gpt-5-codex", 400, 1781450000000, 1781450100000)

	cx := NewCodexCollector(db, []string{sessionsDir})
	if err := cx.Scan(); err != nil {
		t.Fatalf("Scan 1: %v", err)
	}
	upsertCodexThread(t, statePath, "thread-delta", "/work/agent-ledger", "feature/codex", "gpt-5-codex", 900, 1781450000000, 1781450200000)
	if err := cx.Scan(); err != nil {
		t.Fatalf("Scan 2: %v", err)
	}

	from := time.UnixMilli(1781450000000).Add(-time.Minute)
	to := time.UnixMilli(1781450200000).Add(time.Minute)
	stats, err := db.GetDashboardStats(from, to, "codex", "")
	if err != nil {
		t.Fatalf("GetDashboardStats: %v", err)
	}
	if stats.TotalTokens != 900 || stats.TotalCalls != 2 {
		t.Fatalf("expected delta tokens 900 over two snapshots, got stats %+v", stats)
	}
	details, err := db.GetSessionDetailScoped("codex", "thread-delta")
	if err != nil {
		t.Fatalf("GetSessionDetailScoped: %v", err)
	}
	if len(details) != 1 || details[0].InputTokens != 900 || details[0].Calls != 2 {
		t.Fatalf("unexpected delta detail: %+v", details)
	}
}

func TestCodexCollector_StateSQLiteDoesNotDuplicateJSONLUsage(t *testing.T) {
	db := tempDB(t)
	root := t.TempDir()
	sessionsDir := filepath.Join(root, "sessions")
	dayDir := filepath.Join(sessionsDir, "2026", "06", "14")
	if err := os.MkdirAll(dayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ts := time.UnixMilli(1781450000000).UTC().Format(time.RFC3339Nano)
	jsonl := `{"timestamp":"` + ts + `","type":"session_meta","payload":{"id":"thread-exact","cwd":"/work/agent-ledger","cli_version":"1.2.3"}}
{"timestamp":"` + ts + `","type":"turn_context","payload":{"model":"gpt-5-codex"}}
{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"token_count","turn_id":"t1","info":{"last_token_usage":{"input_tokens":300,"cached_input_tokens":50,"output_tokens":150,"reasoning_output_tokens":20}}}}
`
	if err := os.WriteFile(filepath.Join(dayDir, "thread-exact.jsonl"), []byte(jsonl), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	statePath := filepath.Join(root, "state_5.sqlite")
	createCodexStateSQLite(t, statePath)
	upsertCodexThread(t, statePath, "thread-exact", "/work/agent-ledger", "main", "gpt-5-codex", 1000, 1781450000000, 1781450300000)

	cx := NewCodexCollector(db, []string{sessionsDir})
	if err := cx.Scan(); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	from := time.UnixMilli(1781450000000).Add(-time.Minute)
	to := time.UnixMilli(1781450300000).Add(time.Minute)
	stats, err := db.GetDashboardStats(from, to, "codex", "")
	if err != nil {
		t.Fatalf("GetDashboardStats: %v", err)
	}
	if stats.TotalTokens != 450 || stats.TotalCalls != 1 {
		t.Fatalf("sqlite fallback duplicated exact JSONL usage: %+v", stats)
	}
}

func createCodexStateSQLite(t *testing.T, path string) {
	t.Helper()
	src, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer src.Close()
	if _, err := src.Exec(`
		CREATE TABLE threads (
			id TEXT PRIMARY KEY,
			rollout_path TEXT,
			created_at INTEGER,
			updated_at INTEGER,
			source TEXT,
			model_provider TEXT,
			cwd TEXT,
			title TEXT,
			sandbox_policy TEXT,
			approval_mode TEXT,
			tokens_used INTEGER,
			has_user_event INTEGER,
			archived INTEGER,
			archived_at INTEGER,
			git_sha TEXT,
			git_branch TEXT,
			git_origin_url TEXT,
			cli_version TEXT,
			first_user_message TEXT,
			agent_nickname TEXT,
			agent_role TEXT,
			memory_mode TEXT,
			model TEXT,
			reasoning_effort TEXT,
			agent_path TEXT,
			created_at_ms INTEGER,
			updated_at_ms INTEGER,
			thread_source TEXT,
			preview TEXT
		)`); err != nil {
		t.Fatalf("create threads: %v", err)
	}
}

func upsertCodexThread(t *testing.T, path, id, cwd, branch, model string, tokens, createdMS, updatedMS int64) {
	t.Helper()
	src, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer src.Close()
	if _, err := src.Exec(`
		INSERT INTO threads(id,cwd,git_branch,cli_version,model,tokens_used,has_user_event,created_at,updated_at,created_at_ms,updated_at_ms)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			cwd=excluded.cwd,
			git_branch=excluded.git_branch,
			cli_version=excluded.cli_version,
			model=excluded.model,
			tokens_used=excluded.tokens_used,
			has_user_event=excluded.has_user_event,
			updated_at=excluded.updated_at,
			updated_at_ms=excluded.updated_at_ms`,
		id, cwd, branch, "1.2.3", model, tokens, 1, createdMS/1000, updatedMS/1000, createdMS, updatedMS); err != nil {
		t.Fatalf("upsert thread: %v", err)
	}
}
