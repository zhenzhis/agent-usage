package notifications

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zhenzhis/agent-ledger/internal/config"
	"github.com/zhenzhis/agent-ledger/internal/storage"
)

// WebhookPayload is the redacted summary sent to an explicitly configured webhook.
type WebhookPayload struct {
	Product     string                      `json:"product"`
	Kind        string                      `json:"kind"`
	GeneratedAt string                      `json:"generated_at"`
	Summary     WebhookSummary              `json:"summary"`
	Events      []storage.WorkloadFeedEvent `json:"events"`
}

// WebhookSummary contains aggregate counts without local paths or prompt content.
type WebhookSummary struct {
	Total      int            `json:"total"`
	ByPhase    map[string]int `json:"by_phase"`
	BySeverity map[string]int `json:"by_severity"`
}

// DeliveryResult describes one attempted notification delivery.
type DeliveryResult struct {
	Enabled    bool   `json:"enabled"`
	DryRun     bool   `json:"dry_run"`
	EventCount int    `json:"event_count"`
	StatusCode int    `json:"status_code,omitempty"`
	Message    string `json:"message"`
}

// BuildWebhookPayload creates a privacy-safe notification payload from a workload event feed.
func BuildWebhookPayload(feed *storage.WorkloadEventFeed, maxEvents int) WebhookPayload {
	redacted := RedactWorkloadEventFeed(feed, maxEvents)
	summary := WebhookSummary{
		Total:      len(redacted),
		ByPhase:    map[string]int{},
		BySeverity: map[string]int{},
	}
	for _, event := range redacted {
		summary.ByPhase[event.Phase]++
		summary.BySeverity[event.Severity]++
	}
	return WebhookPayload{
		Product:     "Agent Ledger",
		Kind:        "workload_event_summary",
		GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Summary:     summary,
		Events:      redacted,
	}
}

// RedactWorkloadEventFeed returns a bounded copy suitable for external summaries.
func RedactWorkloadEventFeed(feed *storage.WorkloadEventFeed, maxEvents int) []storage.WorkloadFeedEvent {
	if feed == nil || len(feed.Rows) == 0 {
		return []storage.WorkloadFeedEvent{}
	}
	if maxEvents <= 0 || maxEvents > 100 {
		maxEvents = 20
	}
	limit := maxEvents
	if len(feed.Rows) < limit {
		limit = len(feed.Rows)
	}
	out := make([]storage.WorkloadFeedEvent, 0, limit)
	for i := 0; i < limit; i++ {
		event := feed.Rows[i]
		event.EventID = shortHash(event.EventID)
		event.WorkloadID = shortHash(event.WorkloadID)
		event.Goal = "<redacted>"
		event.Project = "<redacted>"
		event.Repo = "<redacted>"
		event.GitBranch = "<redacted>"
		event.Team = "<redacted>"
		out = append(out, event)
	}
	return out
}

// SendWebhook sends a redacted notification payload, or returns an explicit disabled/dry-run result.
func SendWebhook(ctx context.Context, cfg config.WebhookConfig, feed *storage.WorkloadEventFeed, dryRun bool) (*DeliveryResult, error) {
	if cfg.MaxEvents <= 0 || cfg.MaxEvents > 100 {
		cfg.MaxEvents = 20
	}
	payload := BuildWebhookPayload(feed, cfg.MaxEvents)
	result := &DeliveryResult{
		Enabled:    cfg.Enabled,
		DryRun:     dryRun,
		EventCount: len(payload.Events),
	}
	if dryRun {
		result.Message = "dry run payload built; webhook not sent"
		return result, nil
	}
	if !cfg.Enabled {
		result.Message = "webhook disabled"
		return result, fmt.Errorf("webhook disabled")
	}
	if strings.TrimSpace(cfg.URL) == "" {
		result.Message = "webhook url is required when webhooks are enabled"
		return result, fmt.Errorf("webhook url is required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return result, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(body))
	if err != nil {
		result.Message = "invalid webhook url"
		return result, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "agent-ledger-webhook")
	client := &http.Client{Timeout: cfg.Timeout}
	resp, err := client.Do(req)
	if err != nil {
		result.Message = err.Error()
		return result, err
	}
	defer resp.Body.Close()
	result.StatusCode = resp.StatusCode
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.Message = fmt.Sprintf("webhook returned status %d", resp.StatusCode)
		return result, errors.New(result.Message)
	}
	result.Message = "webhook sent"
	return result, nil
}

func shortHash(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}
