package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zhenzhis/agent-ledger/internal/config"
)

func TestProviderCallsWrapperBudgetAdvisoryAndReconciliationHook(t *testing.T) {
	db := testServerDB(t)
	srv := New(db, "", Options{
		Budgets: config.BudgetConfig{Enabled: true, Rules: []config.BudgetRule{{
			Name: "provider-model-cap", Period: "day", Scope: "model", Match: "gpt-5.5", Metric: "cost_usd", Limit: 1.00, WarnRatio: 0.50,
		}}},
	})
	body := `{
		"provider":"openai",
		"request":{
			"id":"req_provider_budget",
			"model":"gpt-5.5",
			"messages":[{"role":"user","content":"secret provider prompt must not persist"}],
			"metadata":{"agent_ledger.project":"provider-budget-project","agent_ledger.session_id":"provider-budget-session"}
		},
		"request_metadata":{"endpoint":"https://api.openai.com/v1/responses?token=secret","agent_ledger.goal":"provider budget wrapper"},
		"response":{
			"id":"resp_provider_budget",
			"model":"gpt-5.5",
			"output":[{"content":[{"text":"secret provider output must not persist"}]}],
			"usage":{"input_tokens":100,"output_tokens":20,"cost_usd":0.75}
		},
		"response_metadata":{"status_code":200,"latency_ms":123},
		"reconciliation":{"statement_id":"secret-provider-statement"}
	}`
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/provider/calls", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.handleProviderCalls(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-Agent-Ledger-Budget-Severity") != "warning" || rr.Header().Get("X-Agent-Ledger-Budget-Rule") != "provider-model-cap" {
		t.Fatalf("budget headers missing: %+v", rr.Header())
	}
	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response["ok"] != true || response["reconciliation_hooks"].(float64) != 1 {
		t.Fatalf("unexpected provider response: %#v", response)
	}
	advisories, _ := response["budget_advisories"].([]interface{})
	if len(advisories) != 1 {
		t.Fatalf("budget advisories=%d body=%s", len(advisories), rr.Body.String())
	}
	audit, err := db.GetAuditLog(50)
	if err != nil {
		t.Fatalf("GetAuditLog: %v", err)
	}
	rawAudit, _ := json.Marshal(audit)
	for _, forbidden := range []string{"secret provider prompt", "secret provider output", "secret-provider-statement", "token=secret"} {
		if strings.Contains(string(rawAudit), forbidden) || strings.Contains(rr.Body.String(), forbidden) {
			t.Fatalf("provider ingest leaked %q\nresponse=%s\naudit=%s", forbidden, rr.Body.String(), string(rawAudit))
		}
	}
	foundBudget := false
	foundReconciliation := false
	for _, row := range audit {
		if row.Action == "provider.calls.budget.warning" && row.Target == "provider-model-cap" {
			foundBudget = true
		}
		if row.Action == "provider.calls.reconciliation_hook" {
			foundReconciliation = true
		}
	}
	if !foundBudget || !foundReconciliation {
		t.Fatalf("missing provider budget/reconciliation audit events: %+v", audit)
	}
}
