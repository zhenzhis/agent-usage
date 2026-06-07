package storage

import (
	"strings"
	"time"
)

// PolicyEnforcementSummary counts local policy enforcement evidence.
type PolicyEnforcementSummary struct {
	Decisions         int `json:"decisions"`
	Blocks            int `json:"blocks"`
	Warnings          int `json:"warnings"`
	ApprovalsRequired int `json:"approvals_required"`
	ApprovalRequests  int `json:"approval_requests"`
	PendingApprovals  int `json:"pending_approvals"`
	ApprovedApprovals int `json:"approved_approvals"`
	RejectedApprovals int `json:"rejected_approvals"`
	ApprovalVotes     int `json:"approval_votes"`
	RejectionVotes    int `json:"rejection_votes"`
	PolicyAuditEvents int `json:"policy_audit_events"`
}

// PolicyEnforcementReport is a local evidence bundle for policy governance.
type PolicyEnforcementReport struct {
	GeneratedAt      string                   `json:"generated_at"`
	Summary          PolicyEnforcementSummary `json:"summary"`
	Decisions        []PolicyDecisionRow      `json:"decisions"`
	ApprovalRequests []ApprovalRequest        `json:"approval_requests"`
	AuditEvents      []AuditEvent             `json:"audit_events"`
}

// GetPolicyEnforcementReport returns recent policy decisions, approvals, and audit evidence.
func (d *DB) GetPolicyEnforcementReport(limit int) (*PolicyEnforcementReport, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	decisions, err := d.GetPolicyDecisions("", limit)
	if err != nil {
		return nil, err
	}
	approvals, err := d.ListApprovalRequests("all", limit)
	if err != nil {
		return nil, err
	}
	audit, err := d.QueryAuditLog(AuditLogFilter{Action: "policy", Limit: limit})
	if err != nil {
		return nil, err
	}
	report := &PolicyEnforcementReport{
		GeneratedAt:      time.Now().UTC().Format(time.RFC3339Nano),
		Decisions:        decisions,
		ApprovalRequests: approvals,
		AuditEvents:      audit,
	}
	for _, decision := range decisions {
		report.Summary.Decisions++
		switch strings.ToLower(strings.TrimSpace(decision.Action)) {
		case "block":
			report.Summary.Blocks++
		case "warn":
			report.Summary.Warnings++
		case "require_approval":
			report.Summary.ApprovalsRequired++
		}
	}
	for _, approval := range approvals {
		report.Summary.ApprovalRequests++
		report.Summary.ApprovalVotes += approval.ApprovalVotes
		report.Summary.RejectionVotes += approval.RejectionVotes
		switch strings.ToLower(strings.TrimSpace(approval.Status)) {
		case "approved":
			report.Summary.ApprovedApprovals++
		case "rejected":
			report.Summary.RejectedApprovals++
		default:
			report.Summary.PendingApprovals++
		}
	}
	report.Summary.PolicyAuditEvents = len(audit)
	return report, nil
}
