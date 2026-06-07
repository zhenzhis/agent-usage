package policy

import (
	"testing"

	"github.com/zhenzhis/agent-ledger/internal/config"
)

func TestEvaluateDisabledAllowsWithoutDecisions(t *testing.T) {
	result := Evaluate(config.PolicyConfig{
		Enabled: false,
		Rules: []config.PolicyRule{{
			Name: "disabled-block", Scope: "model", Match: "gpt-5.5", Action: "block",
		}},
	}, Request{Model: "gpt-5.5"})
	if result.Enabled || result.Action != "allow" || len(result.Decisions) != 0 {
		t.Fatalf("unexpected disabled result: %#v", result)
	}
}

func TestEvaluateChoosesHighestSeverity(t *testing.T) {
	result := Evaluate(config.PolicyConfig{
		Enabled: true,
		Rules: []config.PolicyRule{
			{Name: "warn-codex", Scope: "source", Match: "codex", Action: "warn"},
			{Name: "approval-model", Scope: "model", Match: "gpt-5.5", Action: "approval"},
			{Name: "block-project", Scope: "project", Match: "secret", Action: "deny"},
		},
	}, Request{Source: "codex", Model: "gpt-5.5", Project: "secret-alpha"})
	if result.Action != "block" || len(result.Decisions) != 3 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestEvaluateRoleAndActionScopes(t *testing.T) {
	result := Evaluate(config.PolicyConfig{
		Enabled: true,
		Rules: []config.PolicyRule{
			{Name: "viewer-export", Scope: "role", Match: "viewer", Action: "block"},
			{Name: "scan-warning", Scope: "action", Match: "scan", Action: "warn"},
		},
	}, Request{Role: "viewer", Action: "scan"})
	if result.Action != "block" || len(result.Decisions) != 2 {
		t.Fatalf("unexpected scoped result: %#v", result)
	}
}

func TestUnknownScopeDoesNotMatch(t *testing.T) {
	result := Evaluate(config.PolicyConfig{
		Enabled: true,
		Rules: []config.PolicyRule{{
			Name: "bad-scope", Scope: "unknown", Match: "codex", Action: "block",
		}},
	}, Request{Source: "codex"})
	if result.Action != "allow" || len(result.Decisions) != 0 {
		t.Fatalf("unknown scope matched: %#v", result)
	}
}
