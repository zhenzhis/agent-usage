package server

import (
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

func testServerDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
