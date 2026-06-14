package server

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/zhenzhis/agent-ledger/internal/integrations"
)

func (s *Server) handleProviderCalls(w http.ResponseWriter, r *http.Request) {
	if !requireHTTPMethod(w, r, http.MethodPost) {
		return
	}
	if !s.requireLocalOrAuth(w, r) || !s.requireRole(w, r, "operator") {
		return
	}
	raw := bytes.Buffer{}
	if _, err := raw.ReadFrom(http.MaxBytesReader(w, r.Body, 4<<20)); err != nil {
		badRequest(w, err)
		return
	}
	calls, err := integrations.DecodeProviderCalls(raw.Bytes())
	if err != nil {
		badRequest(w, err)
		return
	}
	events, err := integrations.ConvertProviderCalls(calls)
	if err != nil {
		badRequest(w, err)
		return
	}
	if len(events) == 0 {
		badRequest(w, fmt.Errorf("no provider usage calls found"))
		return
	}
	results := make([]interface{}, 0, len(events))
	for _, event := range events {
		result, err := s.db.IngestCanonicalEvent(event)
		if err != nil {
			badRequest(w, err)
			return
		}
		results = append(results, result)
	}
	budgetAdvisories, budgetErr := s.providerBudgetAdvisories(r, calls)
	budgetWarning := ""
	if budgetErr != nil {
		budgetWarning = budgetErr.Error()
		log.Printf("provider budget evaluation failed: %v", budgetErr)
		s.appendAuditLog("local", s.roleFor(r), "provider.calls.budget.evaluation_failed", "provider-calls", map[string]string{"error": budgetWarning})
	}
	if worst := worstBudgetStatus(budgetAdvisories); worst != nil {
		w.Header().Set("X-Agent-Ledger-Budget-Severity", worst.Severity)
		w.Header().Set("X-Agent-Ledger-Budget-Rule", worst.Name)
		w.Header().Set("X-Agent-Ledger-Budget-Ratio", fmt.Sprintf("%.4f", worst.Ratio))
	}
	reconciliationHooks := providerReconciliationHookCount(calls)
	if reconciliationHooks > 0 {
		s.appendAuditLog("local", s.roleFor(r), "provider.calls.reconciliation_hook", fmt.Sprintf("%d", reconciliationHooks), map[string]string{"hooks": fmt.Sprint(reconciliationHooks)})
	}
	s.appendAuditLog("local", s.roleFor(r), "provider.calls.ingest", fmt.Sprintf("%d", len(results)), map[string]string{"calls": fmt.Sprint(len(calls)), "events": fmt.Sprint(len(results)), "budget_advisories": fmt.Sprint(len(budgetAdvisories)), "reconciliation_hooks": fmt.Sprint(reconciliationHooks)})
	writeJSON(w, map[string]interface{}{"ok": true, "calls": len(calls), "events": len(events), "results": results, "budget_advisories": budgetAdvisories, "budget_warning": budgetWarning, "reconciliation_hooks": reconciliationHooks})
}

func (s *Server) providerBudgetAdvisories(r *http.Request, calls []integrations.ProviderCall) ([]BudgetStatus, error) {
	statuses, err := s.evaluateBudgets(time.Now())
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := []BudgetStatus{}
	for _, call := range calls {
		source := providerCallSource(call)
		for _, status := range statuses {
			if budgetSeverityRank(status.Severity) == 0 || !usageBudgetRelevant(status, source, call.Model, call.Project) {
				continue
			}
			key := strings.Join([]string{status.Name, status.PeriodKey, status.Scope, status.Match, status.Metric}, "|")
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, status)
			s.appendAuditLog("local", s.roleFor(r), "provider.calls.budget."+status.Severity, status.Name, map[string]string{
				"source":     source,
				"model":      call.Model,
				"project":    call.Project,
				"period":     status.Period,
				"scope":      status.Scope,
				"match":      status.Match,
				"metric":     status.Metric,
				"ratio":      fmt.Sprintf("%.4f", status.Ratio),
				"period_key": status.PeriodKey,
			})
		}
	}
	return out, nil
}

func providerCallSource(call integrations.ProviderCall) string {
	return firstNonEmpty(gatewayMetadataString(call.Metadata, "agent_ledger.source", "source"), "provider")
}

func worstBudgetStatus(rows []BudgetStatus) *BudgetStatus {
	if len(rows) == 0 {
		return nil
	}
	worst := rows[0]
	for _, row := range rows[1:] {
		if budgetSeverityRank(row.Severity) > budgetSeverityRank(worst.Severity) ||
			(budgetSeverityRank(row.Severity) == budgetSeverityRank(worst.Severity) && row.Ratio > worst.Ratio) {
			worst = row
		}
	}
	return &worst
}

func providerReconciliationHookCount(calls []integrations.ProviderCall) int {
	count := 0
	for _, call := range calls {
		if gatewayMetadataString(call.Metadata,
			"reconciliation_ref_hash",
			"provider_statement_hash",
			"statement_hash",
			"payload_sha256",
			"invoice_hash",
			"reconciliation_ref",
			"statement_id",
			"invoice_id",
			"provider_bill_ref",
		) != "" {
			count++
		}
	}
	return count
}
