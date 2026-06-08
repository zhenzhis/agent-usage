package storage

import (
	"testing"
	"time"
)

func TestPolicyEnforcementReportSummarizesEvidence(t *testing.T) {
	db := tempDB(t)
	workloadID, err := db.CreateWorkload("policy evidence", "codex", "agent-ledger", "zhenzhis/agent-ledger", "main", "", "infra", 0)
	if err != nil {
		t.Fatalf("CreateWorkload: %v", err)
	}
	if _, err := db.RecordPolicyDecision(workloadID, "", "deny-model", "block", "model blocked", "operator"); err != nil {
		t.Fatalf("RecordPolicyDecision block: %v", err)
	}
	if _, err := db.RecordPolicyDecision(workloadID, "", "review-export", "require_approval", "export needs approval", "operator"); err != nil {
		t.Fatalf("RecordPolicyDecision approval: %v", err)
	}
	if _, err := db.CreateApprovalRequest(ApprovalRequest{
		WorkloadID:             workloadID,
		Source:                 "codex",
		Project:                "agent-ledger",
		Action:                 "export",
		Target:                 "sessions",
		ActorRole:              "operator",
		Status:                 "pending",
		Reason:                 "export needs approval",
		RequiredApprovals:      2,
		ApproverHint:           "alice,bob",
		EscalationTarget:       "desk-lead",
		EscalationAfterSeconds: 600,
		DueAt:                  time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano),
	}); err != nil {
		t.Fatalf("CreateApprovalRequest: %v", err)
	}
	if err := db.AppendAuditLog("local", "operator", "policy.evaluate", "sessions", map[string]string{"action": "export"}); err != nil {
		t.Fatalf("AppendAuditLog: %v", err)
	}
	report, err := db.GetPolicyEnforcementReport(50)
	if err != nil {
		t.Fatalf("GetPolicyEnforcementReport: %v", err)
	}
	if report.Summary.Decisions != 2 || report.Summary.Blocks != 1 || report.Summary.ApprovalsRequired != 1 || report.Summary.PendingApprovals != 1 || report.Summary.OverdueApprovals != 1 || report.Summary.PolicyAuditEvents != 1 {
		t.Fatalf("unexpected summary: %+v", report.Summary)
	}
	if len(report.Decisions) != 2 || len(report.ApprovalRequests) != 1 || len(report.AuditEvents) != 1 {
		t.Fatalf("unexpected rows: %+v", report)
	}
}

func TestApprovalRouteSummaryGroupsPendingRequests(t *testing.T) {
	db := tempDB(t)
	now := time.Now().UTC()
	for _, req := range []ApprovalRequest{
		{
			Action: "model.call", Target: "openai-chat-completions", Source: "gateway", Model: "gpt-5.5", Project: "agent-ledger",
			Status: "pending", RequiredApprovals: 2, ApproverHint: "desk-lead,risk", EscalationTarget: "research-head",
			DueAt: now.Add(30 * time.Minute).Format(time.RFC3339Nano),
		},
		{
			Action: "export", Target: "sessions", Source: "codex", Model: "gpt-5.5", Project: "agent-ledger",
			Status: "pending", ApproverHint: "desk-lead,risk", EscalationTarget: "research-head",
			DueAt: now.Add(-time.Minute).Format(time.RFC3339Nano),
		},
		{
			Action: "export", Target: "sessions", Source: "codex", Model: "gpt-4.1", Project: "private-project",
			Status: "pending",
		},
	} {
		if _, err := db.CreateApprovalRequest(req); err != nil {
			t.Fatalf("CreateApprovalRequest: %v", err)
		}
	}
	report, err := db.GetApprovalRouteSummary(10, time.Hour)
	if err != nil {
		t.Fatalf("GetApprovalRouteSummary: %v", err)
	}
	if report.Summary.Pending != 3 || report.Summary.Overdue != 1 || report.Summary.DueSoon != 1 || report.Summary.Unassigned != 1 || len(report.Routes) != 2 {
		t.Fatalf("unexpected route summary: %+v", report)
	}
	routed := report.Routes[0]
	if routed.Approver != "desk-lead,risk" || routed.EscalationTarget != "research-head" || routed.Pending != 2 || routed.Overdue != 1 || routed.DueSoon != 1 {
		t.Fatalf("unexpected routed approval rollup: %+v", routed)
	}
	if len(routed.Sources) != 2 || len(routed.Models) != 1 || len(routed.Projects) != 1 || len(routed.Actions) != 2 {
		t.Fatalf("route dimensions should be deduped: %+v", routed)
	}
}
