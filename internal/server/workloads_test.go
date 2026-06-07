package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zhenzhis/agent-ledger/internal/storage"
)

func TestAgentRunHeartbeatAPI(t *testing.T) {
	db := testServerDB(t)
	workloadID, err := db.CreateWorkload("async api workload", "codex", "agent-ledger", "agent-ledger", "main", "", "", 0)
	if err != nil {
		t.Fatalf("CreateWorkload: %v", err)
	}
	runID, err := db.StartAgentRun(workloadID, "codex", "codex", "codex", "C:/work/agent-ledger")
	if err != nil {
		t.Fatalf("StartAgentRun: %v", err)
	}
	body, _ := json.Marshal(map[string]interface{}{
		"run_id":   runID,
		"status":   "working",
		"phase":    "testing",
		"progress": 0.75,
		"message":  "running tests",
		"metrics":  map[string]interface{}{"tests_seen": 12},
	})
	srv := New(db, "", Options{})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/agent-runs/heartbeat", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	srv.handleAgentRunHeartbeat(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("heartbeat status=%d body=%s", rr.Code, rr.Body.String())
	}
	var response struct {
		OK        bool                     `json:"ok"`
		Heartbeat storage.AgentRunEventRow `json:"heartbeat"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.OK || response.Heartbeat.RunID != runID || response.Heartbeat.Progress != 0.75 {
		t.Fatalf("unexpected heartbeat response: %+v", response)
	}
	detail, err := db.GetWorkloadDetail(workloadID)
	if err != nil {
		t.Fatalf("GetWorkloadDetail: %v", err)
	}
	if len(detail.Runs) != 1 || detail.Runs[0].HeartbeatCount != 1 || detail.Runs[0].Phase != "testing" {
		t.Fatalf("run snapshot not updated: %+v", detail.Runs)
	}
	audit, err := db.GetAuditLog(10)
	if err != nil {
		t.Fatalf("GetAuditLog: %v", err)
	}
	rawAudit, _ := json.Marshal(audit)
	if !strings.Contains(string(rawAudit), "agent_run.heartbeat") || strings.Contains(string(rawAudit), "running tests") {
		t.Fatalf("unexpected audit log: %s", string(rawAudit))
	}
}
