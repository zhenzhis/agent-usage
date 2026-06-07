package policy

import (
	"strings"

	"github.com/zhenzhis/agent-ledger/internal/config"
)

// Request describes a local policy evaluation request.
type Request struct {
	WorkloadID string `json:"workload_id"`
	RunID      string `json:"run_id"`
	Source     string `json:"source"`
	Model      string `json:"model"`
	Project    string `json:"project"`
	Action     string `json:"action"`
	Role       string `json:"role"`
}

// Decision describes one matched policy rule.
type Decision struct {
	DecisionID string `json:"decision_id,omitempty"`
	Rule       string `json:"rule"`
	Scope      string `json:"scope"`
	Match      string `json:"match"`
	Action     string `json:"action"`
	Message    string `json:"message"`
}

// Result describes the effective local policy action.
type Result struct {
	Enabled       bool       `json:"enabled"`
	Action        string     `json:"action"`
	Decisions     []Decision `json:"decisions"`
	Webhooks      string     `json:"webhooks"`
	PrivacyExport bool       `json:"privacy_export"`
}

// Evaluate applies advisory local policy rules. It does not perform side effects.
func Evaluate(cfg config.PolicyConfig, req Request) Result {
	if !cfg.Enabled {
		return Result{
			Enabled:       false,
			Action:        "allow",
			Decisions:     []Decision{},
			Webhooks:      "disabled-by-default",
			PrivacyExport: cfg.RequirePrivacyExport,
		}
	}
	result := Result{
		Enabled:       true,
		Action:        "allow",
		Decisions:     []Decision{},
		Webhooks:      "disabled-by-default",
		PrivacyExport: cfg.RequirePrivacyExport,
	}
	for _, rule := range cfg.Rules {
		if !Matches(rule, req) {
			continue
		}
		action := NormalizeAction(rule.Action)
		if Rank(action) > Rank(result.Action) {
			result.Action = action
		}
		result.Decisions = append(result.Decisions, Decision{
			Rule:    rule.Name,
			Scope:   NormalizeScope(rule.Scope),
			Match:   rule.Match,
			Action:  action,
			Message: rule.Message,
		})
	}
	return result
}

// Matches reports whether a policy rule applies to a request.
func Matches(rule config.PolicyRule, req Request) bool {
	scope := NormalizeScope(rule.Scope)
	match := strings.ToLower(strings.TrimSpace(rule.Match))
	if match == "" || match == "*" {
		return true
	}
	switch scope {
	case "source":
		return strings.EqualFold(req.Source, rule.Match)
	case "model":
		return strings.EqualFold(req.Model, rule.Match)
	case "project":
		return strings.Contains(strings.ToLower(req.Project), match)
	case "action":
		return strings.EqualFold(req.Action, rule.Match)
	case "role":
		return strings.EqualFold(req.Role, rule.Match)
	case "global":
		return true
	default:
		return false
	}
}

// NormalizeAction converts policy aliases into stable action names.
func NormalizeAction(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "block", "deny", "denied":
		return "block"
	case "approval", "require_approval", "approve", "review":
		return "require_approval"
	case "warn", "warning":
		return "warn"
	default:
		return "allow"
	}
}

// NormalizeScope converts scope aliases into stable scope names.
func NormalizeScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "source", "model", "project", "action", "role":
		return strings.ToLower(strings.TrimSpace(scope))
	case "", "global", "*":
		return "global"
	default:
		return strings.ToLower(strings.TrimSpace(scope))
	}
}

// Rank returns action severity for effective action selection.
func Rank(action string) int {
	switch NormalizeAction(action) {
	case "block":
		return 3
	case "require_approval":
		return 2
	case "warn":
		return 1
	default:
		return 0
	}
}
