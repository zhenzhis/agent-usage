package server

import (
	"bytes"
	"fmt"
	"net/http"

	"github.com/zhenzhis/agent-ledger/internal/integrations"
)

func (s *Server) handleA2ATasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
	tasks, err := integrations.DecodeA2ATasks(raw.Bytes())
	if err != nil {
		badRequest(w, err)
		return
	}
	events, err := integrations.ConvertA2ATasks(tasks)
	if err != nil {
		badRequest(w, err)
		return
	}
	if len(events) == 0 {
		badRequest(w, fmt.Errorf("no A2A task events found"))
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
	_ = s.db.AppendAuditLog("local", s.roleFor(r), "a2a.task.ingest", fmt.Sprintf("%d", len(results)), map[string]string{"tasks": fmt.Sprint(len(tasks)), "events": fmt.Sprint(len(results))})
	writeJSON(w, map[string]interface{}{"ok": true, "tasks": len(tasks), "events": len(events), "results": results})
}
