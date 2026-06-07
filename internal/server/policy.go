package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	ledgerpolicy "github.com/zhenzhis/agent-ledger/internal/policy"
)

func (s *Server) handlePolicyEvaluate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireLocalOrAuth(w, r) || !s.requireRole(w, r, "operator") {
		return
	}
	var payload struct {
		ledgerpolicy.Request
		Record *bool `json:"record"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&payload); err != nil {
		badRequest(w, err)
		return
	}
	result := ledgerpolicy.Evaluate(s.options.Policies, payload.Request)
	shouldRecord := payload.WorkloadID != "" && result.Enabled && len(result.Decisions) > 0
	if payload.Record != nil {
		shouldRecord = *payload.Record && result.Enabled && len(result.Decisions) > 0
	}
	if shouldRecord && payload.WorkloadID == "" {
		badRequest(w, fmt.Errorf("record=true requires workload_id"))
		return
	}
	if shouldRecord {
		for i := range result.Decisions {
			id, err := s.db.RecordPolicyDecision(payload.WorkloadID, payload.RunID, result.Decisions[i].Rule, result.Decisions[i].Action, result.Decisions[i].Message, s.roleFor(r))
			if err != nil {
				serverError(w, err)
				return
			}
			result.Decisions[i].DecisionID = id
		}
	}
	_ = s.db.AppendAuditLog("local", s.roleFor(r), "policy.evaluate", result.Action, map[string]string{
		"source": payload.Source,
		"model":  payload.Model,
		"action": payload.Action,
		"record": fmt.Sprint(shouldRecord),
	})
	writeJSON(w, result)
}

func (s *Server) evaluateOperationPolicy(w http.ResponseWriter, r *http.Request, action, source, model, project, target string) bool {
	result := ledgerpolicy.Evaluate(s.options.Policies, ledgerpolicy.Request{
		Source:  source,
		Model:   model,
		Project: project,
		Action:  action,
		Role:    s.roleFor(r),
	})
	if result.Enabled && len(result.Decisions) > 0 {
		raw, _ := json.Marshal(result.Decisions)
		_ = s.db.AppendAuditLog("local", s.roleFor(r), "policy.evaluate", target, map[string]string{
			"action":           action,
			"effective_action": result.Action,
			"source":           source,
			"model":            model,
			"project":          project,
			"decisions":        string(raw),
		})
	}
	switch ledgerpolicy.NormalizeAction(result.Action) {
	case "block":
		http.Error(w, "blocked by policy", http.StatusForbidden)
		return false
	case "require_approval":
		http.Error(w, "operation requires approval by policy", http.StatusForbidden)
		return false
	default:
		return true
	}
}
