package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zhenzhis/agent-ledger/internal/config"
	"github.com/zhenzhis/agent-ledger/internal/notifications"
	"github.com/zhenzhis/agent-ledger/internal/storage"
)

func TestWebhookNotificationEndpointSendsRedactedPayload(t *testing.T) {
	db := testServerDB(t)
	now := time.Now().UTC()
	workloadID := createStaleWebhookWorkload(t, db, now)
	var payload notifications.WebhookPayload
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("unexpected webhook request: %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode webhook payload: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer receiver.Close()

	srv := New(db, "", Options{Webhooks: config.WebhookConfig{Enabled: true, URL: receiver.URL, Timeout: time.Second, MaxEvents: 10}})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/notifications/webhook?from="+now.AddDate(0, 0, -1).Format("2006-01-02")+"&to="+now.Format("2006-01-02")+"&severity=warning&max_age=10m", nil)
	rr := httptest.NewRecorder()
	srv.handleWebhookNotification(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("notify status=%d body=%s", rr.Code, rr.Body.String())
	}
	if payload.Summary.Total != 1 || len(payload.Events) != 1 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	event := payload.Events[0]
	if event.WorkloadID == workloadID || event.Goal != "<redacted>" || event.Project != "<redacted>" || event.Repo != "<redacted>" || event.GitBranch != "<redacted>" || event.Team != "<redacted>" {
		t.Fatalf("webhook payload leaked sensitive data: %+v", event)
	}
	audit, err := db.GetAuditLog(10)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	rawAudit, _ := json.Marshal(audit)
	if !strings.Contains(string(rawAudit), "notification.webhook") || strings.Contains(string(rawAudit), receiver.URL) || strings.Contains(string(rawAudit), "private-project") {
		t.Fatalf("unexpected audit log: %s", string(rawAudit))
	}
}

func TestWebhookNotificationEndpointFailsWhenDisabled(t *testing.T) {
	db := testServerDB(t)
	now := time.Now().UTC()
	createStaleWebhookWorkload(t, db, now)
	srv := New(db, "", Options{Webhooks: config.WebhookConfig{Enabled: false}})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/notifications/webhook?from="+now.AddDate(0, 0, -1).Format("2006-01-02")+"&to="+now.Format("2006-01-02")+"&severity=warning", nil)
	rr := httptest.NewRecorder()
	srv.handleWebhookNotification(rr, req)
	if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), "webhook disabled") {
		t.Fatalf("expected disabled failure, status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func createStaleWebhookWorkload(t *testing.T, db *storage.DB, now time.Time) string {
	t.Helper()
	workloadID, err := db.CreateWorkload("private webhook goal", "codex", "private-project", "zhenzhis/private-project", "feature/private", "", "research", 0)
	if err != nil {
		t.Fatalf("CreateWorkload: %v", err)
	}
	runID, err := db.StartAgentRun(workloadID, "codex", "codex", "codex", "C:/private/workspace")
	if err != nil {
		t.Fatalf("StartAgentRun: %v", err)
	}
	if _, err := db.RecordAgentRunHeartbeat("evt-webhook-stale", runID, "working", "testing", "private message", 0.6, nil, now.Add(-20*time.Minute), 1); err != nil {
		t.Fatalf("RecordAgentRunHeartbeat: %v", err)
	}
	return workloadID
}
