package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadPolicyApprovalRouting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte(`
policies:
  enabled: true
  rules:
    - name: expensive-model-review
      scope: model
      match: gpt-5.5
      action: require_approval
      message: Review expensive model usage
      required_approvals: 2
      approvers: ["desk-lead", "risk"]
      escalate_after: 30m
      escalate_to: ["research-head"]
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Policies.Enabled || len(cfg.Policies.Rules) != 1 {
		t.Fatalf("policy config not loaded: %+v", cfg.Policies)
	}
	rule := cfg.Policies.Rules[0]
	if rule.RequiredApprovals != 2 || rule.EscalateAfter != 30*time.Minute {
		t.Fatalf("approval routing numeric fields not parsed: %+v", rule)
	}
	if len(rule.Approvers) != 2 || rule.Approvers[0] != "desk-lead" || rule.Approvers[1] != "risk" {
		t.Fatalf("approvers not parsed: %+v", rule.Approvers)
	}
	if len(rule.EscalateTo) != 1 || rule.EscalateTo[0] != "research-head" {
		t.Fatalf("escalation targets not parsed: %+v", rule.EscalateTo)
	}
}
