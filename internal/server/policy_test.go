package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/zhenzhis/agent-ledger/internal/config"
	"github.com/zhenzhis/agent-ledger/internal/storage"
)

func TestExportPolicyBlock(t *testing.T) {
	db := testServerDB(t)
	srv := New(db, "", Options{Policies: config.PolicyConfig{
		Enabled: true,
		Rules: []config.PolicyRule{{
			Name: "block-export", Scope: "action", Match: "export", Action: "block", Message: "exports disabled",
		}},
	}})
	req := httptest.NewRequest(http.MethodGet, "/api/export?type=sessions&format=csv&from=2026-06-07&to=2026-06-07", nil)
	rr := httptest.NewRecorder()
	srv.handleExport(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestExportPolicyWarnRecordsAudit(t *testing.T) {
	db := testServerDB(t)
	srv := New(db, "", Options{Policies: config.PolicyConfig{
		Enabled: true,
		Rules: []config.PolicyRule{{
			Name: "warn-export", Scope: "action", Match: "export", Action: "warn", Message: "audit export",
		}},
	}})
	req := httptest.NewRequest(http.MethodGet, "/api/export?type=sessions&format=csv&from=2026-06-07&to=2026-06-07", nil)
	rr := httptest.NewRecorder()
	srv.handleExport(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	events, err := db.GetAuditLog(10)
	if err != nil {
		t.Fatalf("GetAuditLog: %v", err)
	}
	seenPolicy, seenExport := false, false
	for _, event := range events {
		if event.Action == "policy.evaluate" {
			seenPolicy = true
		}
		if event.Action == "export" && event.Target == "sessions" {
			seenExport = true
		}
	}
	if !seenPolicy || !seenExport {
		t.Fatalf("expected policy and export audit rows, got %+v", events)
	}
}

func TestExportPolicyRequireApprovalCreatesRequestAndApprovedRequestAllows(t *testing.T) {
	db := testServerDB(t)
	srv := New(db, "", Options{Policies: config.PolicyConfig{
		Enabled: true,
		Rules: []config.PolicyRule{{
			Name: "approve-export", Scope: "action", Match: "export", Action: "require_approval", Message: "review export",
		}},
	}})
	req := httptest.NewRequest(http.MethodGet, "/api/export?type=sessions&format=csv&from=2026-06-07&to=2026-06-07", nil)
	rr := httptest.NewRecorder()
	srv.handleExport(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rr.Code, rr.Body.String())
	}
	rows, err := db.ListApprovalRequests("pending", 10)
	if err != nil {
		t.Fatalf("approvals: %v", err)
	}
	if len(rows) != 1 || rows[0].Action != "export" || rows[0].Target != "sessions" {
		t.Fatalf("unexpected approvals: %+v", rows)
	}
	body, _ := json.Marshal(map[string]string{"request_id": rows[0].RequestID, "status": "approved", "note": "ok"})
	resolveReq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/policy/approvals", bytes.NewReader(body))
	resolveRR := httptest.NewRecorder()
	srv.handlePolicyApprovals(resolveRR, resolveReq)
	if resolveRR.Code != http.StatusOK {
		t.Fatalf("resolve approval status=%d body=%s", resolveRR.Code, resolveRR.Body.String())
	}
	retry := httptest.NewRequest(http.MethodGet, "/api/export?type=sessions&format=csv&from=2026-06-07&to=2026-06-07&approval_id="+rows[0].RequestID, nil)
	retryRR := httptest.NewRecorder()
	srv.handleExport(retryRR, retry)
	if retryRR.Code != http.StatusOK {
		t.Fatalf("approved retry status=%d body=%s", retryRR.Code, retryRR.Body.String())
	}
}

func testServerDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
