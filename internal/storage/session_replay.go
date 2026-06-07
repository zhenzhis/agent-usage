package storage

import (
	"fmt"
)

// SessionReplayPoint is one model-call point in a session cost replay.
type SessionReplayPoint struct {
	Timestamp             string  `json:"timestamp"`
	Source                string  `json:"source"`
	SessionID             string  `json:"session_id"`
	Model                 string  `json:"model"`
	InputTokens           int64   `json:"input_tokens"`
	OutputTokens          int64   `json:"output_tokens"`
	CacheRead             int64   `json:"cache_read"`
	CacheCreate           int64   `json:"cache_create"`
	ReasoningOutputTokens int64   `json:"reasoning_output_tokens"`
	Tokens                int64   `json:"tokens"`
	CostUSD               float64 `json:"cost_usd"`
	CumulativeTokens      int64   `json:"cumulative_tokens"`
	CumulativeCostUSD     float64 `json:"cumulative_cost_usd"`
	CumulativeCalls       int     `json:"cumulative_calls"`
	PricingSource         string  `json:"pricing_source"`
	PricingModel          string  `json:"pricing_model"`
	PricingConfidence     string  `json:"pricing_confidence"`
}

// SessionReplayReport describes how tokens and cost accumulated inside one
// source-scoped session.
type SessionReplayReport struct {
	Source            string               `json:"source"`
	SessionID         string               `json:"session_id"`
	StartTime         string               `json:"start_time"`
	EndTime           string               `json:"end_time"`
	Calls             int                  `json:"calls"`
	TotalTokens       int64                `json:"total_tokens"`
	TotalCostUSD      float64              `json:"total_cost_usd"`
	PeakTokensPerCall int64                `json:"peak_tokens_per_call"`
	Truncated         bool                 `json:"truncated"`
	Points            []SessionReplayPoint `json:"points"`
}

// GetSessionReplay returns a bounded, chronological token/cost replay for a
// session. When source is omitted, the session id must exist in only one source.
func (d *DB) GetSessionReplay(source, sessionID string, limit int) (*SessionReplayReport, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session_id required")
	}
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	if source == "" {
		var sources int
		if err := d.db.QueryRow(`SELECT COUNT(*) FROM (
			SELECT source FROM usage_records WHERE session_id=?
			UNION
			SELECT source FROM model_calls WHERE session_id=?
		)`, sessionID, sessionID).Scan(&sources); err != nil {
			return nil, err
		}
		if sources > 1 {
			return nil, fmt.Errorf("session_id %q exists in multiple sources; include source", sessionID)
		}
	}
	report, err := d.getUsageSessionReplay(source, sessionID, limit)
	if err != nil {
		return nil, err
	}
	if len(report.Points) > 0 || report.Truncated {
		return report, nil
	}
	return d.getModelCallSessionReplay(source, sessionID, limit)
}

func (d *DB) getUsageSessionReplay(source, sessionID string, limit int) (*SessionReplayReport, error) {
	sf, sa := sourceFilter(source)
	args := append([]interface{}{sessionID}, sa...)
	args = append(args, limit+1)
	rows, err := d.db.Query(`SELECT source,session_id,model,COALESCE(timestamp,''),
		COALESCE(input_tokens,0),COALESCE(output_tokens,0),
		COALESCE(cache_read_input_tokens,0),COALESCE(cache_creation_input_tokens,0),
		COALESCE(reasoning_output_tokens,0),COALESCE(cost_usd,0),
		COALESCE(pricing_source,''),COALESCE(pricing_model,''),COALESCE(pricing_confidence,'')
		FROM usage_records WHERE session_id=?`+sf+`
		ORDER BY timestamp,id LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSessionReplayRows(rows, source, sessionID, limit)
}

func (d *DB) getModelCallSessionReplay(source, sessionID string, limit int) (*SessionReplayReport, error) {
	sf, sa := sourceFilter(source)
	args := append([]interface{}{sessionID}, sa...)
	args = append(args, limit+1)
	rows, err := d.db.Query(`SELECT source,session_id,model,COALESCE(timestamp,''),
		COALESCE(input_tokens,0),COALESCE(output_tokens,0),
		COALESCE(cache_read_input_tokens,0),COALESCE(cache_creation_input_tokens,0),
		COALESCE(reasoning_output_tokens,0),COALESCE(cost_usd,0),
		COALESCE(pricing_source,''),COALESCE(model_alias,''),COALESCE(pricing_confidence,'')
		FROM model_calls WHERE session_id=?`+sf+`
		ORDER BY timestamp,call_id LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSessionReplayRows(rows, source, sessionID, limit)
}

type sessionReplayScanner interface {
	Next() bool
	Scan(dest ...interface{}) error
	Err() error
}

func scanSessionReplayRows(rows sessionReplayScanner, source, sessionID string, limit int) (*SessionReplayReport, error) {
	report := &SessionReplayReport{Source: source, SessionID: sessionID, Points: []SessionReplayPoint{}}
	for rows.Next() {
		var p SessionReplayPoint
		if err := rows.Scan(&p.Source, &p.SessionID, &p.Model, &p.Timestamp, &p.InputTokens, &p.OutputTokens,
			&p.CacheRead, &p.CacheCreate, &p.ReasoningOutputTokens, &p.CostUSD, &p.PricingSource, &p.PricingModel, &p.PricingConfidence); err != nil {
			return nil, err
		}
		if len(report.Points) >= limit {
			report.Truncated = true
			continue
		}
		p.Tokens = p.InputTokens + p.OutputTokens + p.CacheRead + p.CacheCreate
		report.Calls++
		report.TotalTokens += p.Tokens
		report.TotalCostUSD += p.CostUSD
		if p.Tokens > report.PeakTokensPerCall {
			report.PeakTokensPerCall = p.Tokens
		}
		p.CumulativeCalls = report.Calls
		p.CumulativeTokens = report.TotalTokens
		p.CumulativeCostUSD = report.TotalCostUSD
		if report.Source == "" {
			report.Source = p.Source
		}
		if report.StartTime == "" {
			report.StartTime = p.Timestamp
		}
		report.EndTime = p.Timestamp
		report.Points = append(report.Points, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return report, nil
}
