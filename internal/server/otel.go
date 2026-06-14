package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/zhenzhis/agent-ledger/internal/integrations"
)

type otlpBackpressureReport struct {
	Status         string `json:"status"`
	BodyBytes      int    `json:"body_bytes"`
	MaxBodyBytes   int64  `json:"max_body_bytes"`
	SpansSeen      int    `json:"spans_seen"`
	MaxSpans       int    `json:"max_spans"`
	EventsProduced int    `json:"events_produced"`
	ContentType    string `json:"content_type,omitempty"`
	Error          string `json:"error,omitempty"`
}

func (m otlpBackpressureReport) applyHeaders(w http.ResponseWriter) {
	status := strings.TrimSpace(m.Status)
	if status == "" {
		status = "unknown"
	}
	w.Header().Set("X-Agent-Ledger-OTLP-Backpressure", status)
	w.Header().Set("X-Agent-Ledger-OTLP-Body-Bytes", fmt.Sprint(m.BodyBytes))
	w.Header().Set("X-Agent-Ledger-OTLP-Max-Body-Bytes", fmt.Sprint(m.MaxBodyBytes))
	w.Header().Set("X-Agent-Ledger-OTLP-Spans", fmt.Sprint(m.SpansSeen))
	w.Header().Set("X-Agent-Ledger-OTLP-Max-Spans", fmt.Sprint(m.MaxSpans))
	w.Header().Set("X-Agent-Ledger-OTLP-Events", fmt.Sprint(m.EventsProduced))
}

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
	maxSpans := s.options.Integrations.OTLPReceiver.MaxSpans
	if maxSpans <= 0 {
		maxSpans = 1000
	}
	raw := bytes.Buffer{}
	if _, err := raw.ReadFrom(http.MaxBytesReader(w, r.Body, maxBody)); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			report := otlpBackpressureReport{
				Status:       "body_limit_exceeded",
				BodyBytes:    raw.Len(),
				MaxBodyBytes: maxBytesErr.Limit,
				MaxSpans:     maxSpans,
				ContentType:  contentType,
				Error:        err.Error(),
			}
			s.writeOTLPBackpressureError(w, r, http.StatusRequestEntityTooLarge, err, report)
			return
		}
		report := otlpBackpressureReport{
			Status:       "body_read_error",
			BodyBytes:    raw.Len(),
			MaxBodyBytes: maxBody,
			MaxSpans:     maxSpans,
			ContentType:  contentType,
			Error:        err.Error(),
		}
		s.writeOTLPBackpressureError(w, r, http.StatusBadRequest, err, report)
		return
	}
	report := otlpBackpressureReport{
		Status:       "accepted",
		BodyBytes:    raw.Len(),
		MaxBodyBytes: maxBody,
		MaxSpans:     maxSpans,
		ContentType:  contentType,
	}
	var spans []integrations.OTelSpan
	var err error
	if isOTLPProtobufContentType(contentType) {
		spans, err = integrations.DecodeOTelProtoTraceSpans(raw.Bytes())
	} else {
		spans, err = integrations.DecodeOTelGenAISpans(raw.Bytes())
	}
	if err != nil {
		report.Status = "decode_error"
		report.Error = err.Error()
		s.writeOTLPBackpressureError(w, r, http.StatusBadRequest, err, report)
		return
	}
	report.SpansSeen = len(spans)
	if maxSpans > 0 && len(spans) > maxSpans {
		err := fmt.Errorf("OTLP span batch has %d spans and exceeds receiver limit %d", len(spans), maxSpans)
		report.Status = "span_limit_exceeded"
		report.Error = err.Error()
		s.writeOTLPBackpressureError(w, r, http.StatusRequestEntityTooLarge, err, report)
		return
	}
	result, err := s.ingestOTelSpanRows(spans, maxSpans, false, "otlp.receiver.ingest", r)
	if err != nil {
		report.Status = "ingest_error"
		report.Error = err.Error()
		s.writeOTLPBackpressureError(w, r, http.StatusBadRequest, err, report)
		return
	}
	if events, ok := result["events"].(int); ok {
		report.EventsProduced = events
	}
	report.applyHeaders(w)
	result["backpressure"] = report
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

func (s *Server) writeOTLPBackpressureError(w http.ResponseWriter, r *http.Request, status int, err error, report otlpBackpressureReport) {
	report.applyHeaders(w)
	s.appendAuditLog("local", s.roleFor(r), "otlp.receiver.backpressure", report.Status, map[string]string{
		"body_bytes":     fmt.Sprint(report.BodyBytes),
		"max_body_bytes": fmt.Sprint(report.MaxBodyBytes),
		"spans_seen":     fmt.Sprint(report.SpansSeen),
		"max_spans":      fmt.Sprint(report.MaxSpans),
		"content_type":   report.ContentType,
		"error":          err.Error(),
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error":        err.Error(),
		"backpressure": report,
	})
}

func isOTLPProtobufContentType(contentType string) bool {
	return strings.Contains(contentType, "application/x-protobuf") || strings.Contains(contentType, "application/protobuf")
}
