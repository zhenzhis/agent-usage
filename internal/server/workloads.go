package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/zhenzhis/agent-ledger/internal/config"
	"github.com/zhenzhis/agent-ledger/internal/storage"
)

func (s *Server) handleWorkloads(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleWorkloadsList(w, r)
	case http.MethodPost:
		s.handleWorkloadCreate(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleWorkloadsList(w http.ResponseWriter, r *http.Request) {
	from, to, _, err := s.parseTimeRange(r)
	if err != nil {
		badRequest(w, err)
		return
	}
	limit, offset := parseLimitOffset(r)
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		parsed, err := strconv.Atoi(cursor)
		if err != nil || parsed < 0 {
			badRequest(w, fmt.Errorf("invalid cursor %q", cursor))
			return
		}
		offset = parsed
	}
	page, err := s.db.GetWorkloadsPage(from, to,
		r.URL.Query().Get("source"),
		r.URL.Query().Get("model"),
		r.URL.Query().Get("project"),
		r.URL.Query().Get("status"),
		r.URL.Query().Get("q"),
		limit, offset)
	if err != nil {
		serverError(w, err)
		return
	}
	applyWorkloadPagePrivacy(page, s.privacyFor(r))
	writeJSON(w, page)
}

func (s *Server) handleWorkloadCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireLocalOrAuth(w, r) || !s.requireRole(w, r, "operator") {
		return
	}
	var payload struct {
		Goal      string  `json:"goal"`
		Source    string  `json:"source"`
		Project   string  `json:"project"`
		Repo      string  `json:"repo"`
		GitBranch string  `json:"git_branch"`
		Owner     string  `json:"owner"`
		Team      string  `json:"team"`
		BudgetUSD float64 `json:"budget_usd"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&payload); err != nil {
		badRequest(w, err)
		return
	}
	id, err := s.db.CreateWorkload(payload.Goal, payload.Source, payload.Project, payload.Repo, payload.GitBranch, payload.Owner, payload.Team, payload.BudgetUSD)
	if err != nil {
		badRequest(w, err)
		return
	}
	_ = s.db.AppendAuditLog("local", s.roleFor(r), "workload.create", id, map[string]string{"source": payload.Source, "project": payload.Project})
	writeJSON(w, map[string]interface{}{"ok": true, "workload_id": id})
}

func (s *Server) handleWorkloadClose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireLocalOrAuth(w, r) || !s.requireRole(w, r, "operator") {
		return
	}
	var payload struct {
		WorkloadID string `json:"workload_id"`
		Status     string `json:"status"`
		Outcome    string `json:"outcome"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&payload); err != nil {
		badRequest(w, err)
		return
	}
	if payload.WorkloadID == "" {
		payload.WorkloadID = r.URL.Query().Get("workload_id")
	}
	if err := s.db.CloseWorkload(payload.WorkloadID, payload.Status, payload.Outcome); err != nil {
		badRequest(w, err)
		return
	}
	_ = s.db.AppendAuditLog("local", s.roleFor(r), "workload.close", payload.WorkloadID, map[string]string{"status": payload.Status})
	writeJSON(w, map[string]interface{}{"ok": true, "workload_id": payload.WorkloadID, "status": payload.Status})
}

func (s *Server) handleWorkloadDetail(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("workload_id")
	if id == "" {
		badRequest(w, fmt.Errorf("workload_id required"))
		return
	}
	detail, err := s.db.GetWorkloadDetail(id)
	if err != nil {
		badRequest(w, err)
		return
	}
	applyWorkloadDetailPrivacy(detail, s.privacyFor(r))
	writeJSON(w, detail)
}

func (s *Server) handleWorkloadGraph(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("workload_id")
	if id == "" {
		badRequest(w, fmt.Errorf("workload_id required"))
		return
	}
	graph, err := s.db.GetWorkloadGraph(id)
	if err != nil {
		badRequest(w, err)
		return
	}
	writeJSON(w, graph)
}

func (s *Server) handleModelRegistry(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.GetModelRegistry(s.options.Pricing.StaleAfter, parseLimit(r, 1000))
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, rows)
}

func (s *Server) handlePolicyDecisions(w http.ResponseWriter, r *http.Request) {
	if !s.requireRole(w, r, "viewer") {
		return
	}
	rows, err := s.db.GetPolicyDecisions(r.URL.Query().Get("workload_id"), parseLimit(r, 200))
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, rows)
}

func applyWorkloadPagePrivacy(page *storage.WorkloadPage, privacy config.PrivacyConfig) {
	if page == nil {
		return
	}
	for i := range page.Rows {
		applyWorkloadPrivacy(&page.Rows[i], privacy)
	}
}

func applyWorkloadDetailPrivacy(detail *storage.WorkloadDetail, privacy config.PrivacyConfig) {
	if detail == nil {
		return
	}
	applyWorkloadPrivacy(&detail.Summary, privacy)
	for i := range detail.Runs {
		if privacy.RedactPaths || privacy.ScreenshotMode {
			detail.Runs[i].CWD = "<redacted>"
			detail.Runs[i].Command = "<redacted>"
		}
	}
	for i := range detail.ModelCalls {
		if privacy.HashSessionIDs || privacy.ScreenshotMode {
			detail.ModelCalls[i].SessionID = hashValue(detail.ModelCalls[i].SessionID)
		}
	}
	for i := range detail.Sessions {
		applySessionPrivacy(&detail.Sessions[i], privacy)
	}
}

func applyWorkloadPrivacy(row *storage.WorkloadSummary, privacy config.PrivacyConfig) {
	if privacy.HideProjectNames || privacy.ScreenshotMode {
		row.Project = "<redacted>"
		row.Repo = "<redacted>"
		row.GitBranch = "<redacted>"
		row.Owner = "<redacted>"
		row.Team = "<redacted>"
	}
}
