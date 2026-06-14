package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zhenzhis/agent-ledger/internal/config"
	"github.com/zhenzhis/agent-ledger/internal/storage"
)

func TestAuditLogAPIFiltersAndRedacts(t *testing.T) {
	db := testServerDB(t)
	if err := db.AppendAuditLog("local", "operator", "pricing.sync", "openai", map[string]string{"project": "private"}); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendAuditLog("local", "viewer", "export", "sessions", map[string]string{"format": "csv"}); err != nil {
		t.Fatal(err)
	}
	srv := New(db, "", Options{Privacy: config.PrivacyConfig{ScreenshotMode: true}})
	req := httptest.NewRequest(http.MethodGet, "/api/audit-log?action=pricing&role=operator&target=open&privacy=1", nil)
	rr := httptest.NewRecorder()
	srv.handleAuditLog(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var rows []storage.AuditEvent
	if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Action != "pricing.sync" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	if rows[0].Target == "openai" || rows[0].Params != "<redacted>" {
		t.Fatalf("audit privacy redaction failed: %+v", rows[0])
	}
}
