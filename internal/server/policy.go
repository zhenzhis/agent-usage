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
