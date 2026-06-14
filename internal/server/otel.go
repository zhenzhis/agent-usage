package server

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"

	"github.com/zhenzhis/agent-ledger/internal/integrations"
)

func (s *Server) handleOTelGenAI(w http.ResponseWriter, r *http.Request) {
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
	result, err := s.ingestOTelSpans(raw.Bytes(), 0, true, "otel.genai.ingest", r)
	if err != nil {
		badRequest(w, err)
		return
	}
	writeJSON(w, result)
}

func (s *Server) handleOTLPTraces(w http.ResponseWriter, r *http.Request) {
	if !requireHTTPMethod(w, r, http.MethodPost) {
		return
	}
	if !s.options.Integrations.OTLPReceiver.Enabled {
		http.Error(w, "OTLP receiver is disabled; enable integrations.otlp_receiver.enabled", http.StatusNotFound)
		return
	}
	if !s.requireLocalOrAuth(w, r) || !s.requireRole(w, r, "operator") {
		return
	}
	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	maxBody := s.options.Integrations.OTLPReceiver.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = 4 << 20
	}
	raw := bytes.Buffer{}
	if _, err := raw.ReadFrom(http.MaxBytesReader(w, r.Body, maxBody)); err != nil {
		badRequest(w, err)
		return
	}
	maxSpans := s.options.Integrations.OTLPReceiver.MaxSpans
	if maxSpans <= 0 {
		maxSpans = 1000
	}
	var result map[string]interface{}
	var err error
	if isOTLPProtobufContentType(contentType) {
		result, err = s.ingestOTelProtoSpans(raw.Bytes(), maxSpans, "otlp.receiver.ingest", r)
	} else {
		result, err = s.ingestOTelSpans(raw.Bytes(), maxSpans, false, "otlp.receiver.ingest", r)
	}
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "exceeds receiver limit") {
			status = http.StatusRequestEntityTooLarge
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"error":%q}`+"\n", err.Error())))
		return
	}
	writeJSON(w, result)
}

func (s *Server) ingestOTelSpans(raw []byte, maxSpans int, requireGenAI bool, auditAction string, r *http.Request) (map[string]interface{}, error) {
	spans, err := integrations.DecodeOTelGenAISpans(raw)
	if err != nil {
		return nil, err
	}
	return s.ingestOTelSpanRows(spans, maxSpans, requireGenAI, auditAction, r)
}

func (s *Server) ingestOTelProtoSpans(raw []byte, maxSpans int, auditAction string, r *http.Request) (map[string]interface{}, error) {
	spans, err := integrations.DecodeOTelProtoTraceSpans(raw)
	if err != nil {
		return nil, err
	}
	return s.ingestOTelSpanRows(spans, maxSpans, false, auditAction, r)
}

func (s *Server) ingestOTelSpanRows(spans []integrations.OTelSpan, maxSpans int, requireGenAI bool, auditAction string, r *http.Request) (map[string]interface{}, error) {
	if maxSpans > 0 && len(spans) > maxSpans {
		return nil, fmt.Errorf("OTLP span batch has %d spans and exceeds receiver limit %d", len(spans), maxSpans)
	}
	events, err := integrations.ConvertOTelGenAISpans(spans)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 && requireGenAI {
		return nil, fmt.Errorf("no GenAI spans found")
	}
	results := make([]interface{}, 0, len(events))
	for _, event := range events {
		result, err := s.db.IngestCanonicalEvent(event)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	_ = s.db.AppendAuditLog("local", s.roleFor(r), auditAction, fmt.Sprintf("%d", len(results)), map[string]string{"spans": fmt.Sprint(len(spans)), "events": fmt.Sprint(len(results))})
	out := map[string]interface{}{"ok": true, "spans": len(spans), "events": len(events), "results": results}
	if len(events) == 0 {
		out["warning"] = "no GenAI spans found; batch accepted without ledger events"
	}
	return out, nil
}

func isOTLPProtobufContentType(contentType string) bool {
	return strings.Contains(contentType, "application/x-protobuf") || strings.Contains(contentType, "application/protobuf")
}
