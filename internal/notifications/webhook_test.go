package notifications

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zhenzhis/agent-ledger/internal/config"
	"github.com/zhenzhis/agent-ledger/internal/storage"
)

func TestWebhookDisabledFailsExplicitly(t *testing.T) {
	feed := sampleFeed()
	result, err := SendWebhook(context.Background(), config.WebhookConfig{Enabled: false}, feed, false)
	if err == nil {
		t.Fatal("expected disabled webhook to fail explicitly")
	}
	if result == nil || result.Enabled || result.Message != "webhook disabled" {
		t.Fatalf("unexpected result: %+v err=%v", result, err)
	}
}

func TestWebhookDryRunBuildsRedactedPayload(t *testing.T) {
	feed := sampleFeed()
	result, err := SendWebhook(context.Background(), config.WebhookConfig{Enabled: false, MaxEvents: 10}, feed, true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if result == nil || !result.DryRun || result.EventCount != 1 {
		t.Fatalf("unexpected dry-run result: %+v", result)
	}
	payload := BuildWebhookPayload(feed, 10)
	if payload.Events[0].Goal != "<redacted>" || payload.Events[0].Project != "<redacted>" || payload.Events[0].Repo != "<redacted>" || payload.Events[0].GitBranch != "<redacted>" || payload.Events[0].Team != "<redacted>" {
		t.Fatalf("payload was not redacted: %+v", payload.Events[0])
	}
	if payload.Events[0].WorkloadID == "wl-private" || payload.Events[0].EventID == "evt-private" {
		t.Fatalf("ids were not hashed: %+v", payload.Events[0])
	}
	if payload.Summary.BySeverity["warning"] != 1 || payload.Summary.ByPhase["stale"] != 1 {
		t.Fatalf("unexpected summary: %+v", payload.Summary)
	}
}

func TestWebhookSendsOnlyRedactedSummary(t *testing.T) {
	var payload WebhookPayload
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer ts.Close()

	result, err := SendWebhook(context.Background(), config.WebhookConfig{Enabled: true, URL: ts.URL, Timeout: time.Second, MaxEvents: 10}, sampleFeed(), false)
	if err != nil {
		t.Fatalf("send webhook: %v", err)
	}
	if result.StatusCode != http.StatusAccepted || result.EventCount != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if payload.Events[0].Goal != "<redacted>" || payload.Events[0].Project != "<redacted>" || payload.Events[0].WorkloadID == "wl-private" {
		t.Fatalf("sensitive data leaked: %+v", payload.Events[0])
	}
}

func sampleFeed() *storage.WorkloadEventFeed {
	return &storage.WorkloadEventFeed{Rows: []storage.WorkloadFeedEvent{{
		EventID:    "evt-private",
		WorkloadID: "wl-private",
		Goal:       "private goal",
		Source:     "codex",
		Project:    "private-project",
		Repo:       "zhenzhis/private-project",
		GitBranch:  "feature/private",
		Team:       "research",
		Phase:      "stale",
		Severity:   "warning",
		Message:    "workload has stale active agent runs",
		NextAction: "inspect stale agent run heartbeat",
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		Stale:      true,
	}}}
}
