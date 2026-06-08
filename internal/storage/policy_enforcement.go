package storage

import (
	"sort"
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
	OverdueApprovals  int `json:"overdue_approvals"`
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

// ApprovalRouteSummary groups pending approval requests by local routing metadata.
type ApprovalRouteSummary struct {
	GeneratedAt string                    `json:"generated_at"`
	DueWithin   string                    `json:"due_within"`
	Summary     ApprovalRouteSummaryStats `json:"summary"`
	Routes      []ApprovalRouteRow        `json:"routes"`
}

// ApprovalRouteSummaryStats summarizes the approval routing queue.
type ApprovalRouteSummaryStats struct {
	Routes     int `json:"routes"`
	Pending    int `json:"pending"`
	Overdue    int `json:"overdue"`
	DueSoon    int `json:"due_soon"`
	Unassigned int `json:"unassigned"`
}

// ApprovalRouteRow is a privacy-redactable routing row for notification adapters.
type ApprovalRouteRow struct {
	RouteKey             string   `json:"route_key"`
	Approver             string   `json:"approver"`
	EscalationTarget     string   `json:"escalation_target"`
	Pending              int      `json:"pending"`
	Overdue              int      `json:"overdue"`
	DueSoon              int      `json:"due_soon"`
	ApprovalVotes        int      `json:"approval_votes"`
	RejectionVotes       int      `json:"rejection_votes"`
	MaxRequiredApprovals int      `json:"max_required_approvals"`
	DueNext              string   `json:"due_next"`
	Sources              []string `json:"sources"`
	Models               []string `json:"models"`
	Projects             []string `json:"projects"`
	Actions              []string `json:"actions"`
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
		if approval.Overdue {
			report.Summary.OverdueApprovals++
		}
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

// GetApprovalRouteSummary returns pending approval route rollups for local notification adapters.
func (d *DB) GetApprovalRouteSummary(limit int, dueWithin time.Duration) (*ApprovalRouteSummary, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	if dueWithin <= 0 {
		dueWithin = 24 * time.Hour
	}
	approvals, err := d.ListApprovalRequests("pending", 1000)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	cutoff := now.Add(dueWithin)
	routes := map[string]*ApprovalRouteRow{}
	seenSources := map[string]map[string]bool{}
	seenModels := map[string]map[string]bool{}
	seenProjects := map[string]map[string]bool{}
	seenActions := map[string]map[string]bool{}
	report := &ApprovalRouteSummary{
		GeneratedAt: now.Format(time.RFC3339Nano),
		DueWithin:   dueWithin.String(),
	}
	for _, approval := range approvals {
		key := approvalRouteKey(approval)
		row := routes[key]
		if row == nil {
			row = &ApprovalRouteRow{
				RouteKey:         key,
				Approver:         strings.TrimSpace(approval.ApproverHint),
				EscalationTarget: strings.TrimSpace(approval.EscalationTarget),
			}
			routes[key] = row
			seenSources[key] = map[string]bool{}
			seenModels[key] = map[string]bool{}
			seenProjects[key] = map[string]bool{}
			seenActions[key] = map[string]bool{}
		}
		row.Pending++
		row.ApprovalVotes += approval.ApprovalVotes
		row.RejectionVotes += approval.RejectionVotes
		if approval.RequiredApprovals > row.MaxRequiredApprovals {
			row.MaxRequiredApprovals = approval.RequiredApprovals
		}
		if approval.Overdue {
			row.Overdue++
		}
		if due, ok := approvalDueAtTime(approval.DueAt); ok {
			if due.After(now) && !due.After(cutoff) {
				row.DueSoon++
			}
			if row.DueNext == "" || due.Before(mustParseApprovalDue(row.DueNext)) {
				row.DueNext = due.Format(time.RFC3339Nano)
			}
		}
		addApprovalRouteValue(&row.Sources, seenSources[key], approval.Source)
		addApprovalRouteValue(&row.Models, seenModels[key], approval.Model)
		addApprovalRouteValue(&row.Projects, seenProjects[key], approval.Project)
		addApprovalRouteValue(&row.Actions, seenActions[key], approval.Action)
		report.Summary.Pending++
		if approval.Overdue {
			report.Summary.Overdue++
		}
	}
	out := make([]ApprovalRouteRow, 0, len(routes))
	for _, row := range routes {
		sort.Strings(row.Sources)
		sort.Strings(row.Models)
		sort.Strings(row.Projects)
		sort.Strings(row.Actions)
		if strings.TrimSpace(row.Approver) == "" && strings.TrimSpace(row.EscalationTarget) == "" {
			report.Summary.Unassigned++
		}
		report.Summary.DueSoon += row.DueSoon
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Overdue != out[j].Overdue {
			return out[i].Overdue > out[j].Overdue
		}
		if out[i].DueNext != out[j].DueNext {
			if out[i].DueNext == "" {
				return false
			}
			if out[j].DueNext == "" {
				return true
			}
			return out[i].DueNext < out[j].DueNext
		}
		if out[i].Pending != out[j].Pending {
			return out[i].Pending > out[j].Pending
		}
		return out[i].RouteKey < out[j].RouteKey
	})
	report.Summary.Routes = len(out)
	if len(out) > limit {
		out = out[:limit]
	}
	report.Routes = out
	return report, nil
}

func approvalRouteKey(approval ApprovalRequest) string {
	approver := strings.TrimSpace(approval.ApproverHint)
	escalation := strings.TrimSpace(approval.EscalationTarget)
	if approver == "" && escalation == "" {
		return "unassigned"
	}
	return strings.ToLower(approver + " -> " + escalation)
}

func addApprovalRouteValue(out *[]string, seen map[string]bool, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	key := strings.ToLower(value)
	if seen[key] {
		return
	}
	seen[key] = true
	*out = append(*out, value)
}

func approvalDueAtTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t, err = time.Parse(time.RFC3339, value)
	}
	return t, err == nil
}

func mustParseApprovalDue(value string) time.Time {
	t, ok := approvalDueAtTime(value)
	if !ok {
		return time.Time{}
	}
	return t
}
