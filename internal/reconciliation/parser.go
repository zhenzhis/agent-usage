package reconciliation

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ImportSummary is a privacy-safe provider billing summary derived from a
// local CSV/JSON statement. It deliberately stores no prompt or response text.
type ImportSummary struct {
	Provider        string    `json:"provider"`
	Format          string    `json:"format"`
	Currency        string    `json:"currency"`
	ProviderCostUSD float64   `json:"provider_cost_usd"`
	RowsSeen        int       `json:"rows_seen"`
	CostRows        int       `json:"cost_rows"`
	PayloadSHA256   string    `json:"payload_sha256"`
	WindowStart     time.Time `json:"window_start,omitempty"`
	WindowEnd       time.Time `json:"window_end,omitempty"`
	Warnings        []string  `json:"warnings,omitempty"`
}

var moneyCleaner = regexp.MustCompile(`[^0-9+\-.]`)

// ParseProviderStatement parses common provider billing exports without
// requiring provider credentials or uploading data.
func ParseProviderStatement(raw []byte, format, provider string) (*ImportSummary, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, fmt.Errorf("provider statement is empty")
	}
	sum := sha256.Sum256(raw)
	summary := &ImportSummary{
		Provider:      strings.TrimSpace(provider),
		Format:        normalizeFormat(format, raw),
		Currency:      "USD",
		PayloadSHA256: hex.EncodeToString(sum[:]),
	}
	switch summary.Format {
	case "json":
		if err := parseJSONStatement(raw, summary); err != nil {
			return nil, err
		}
	case "csv":
		if err := parseCSVStatement(raw, summary); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported provider statement format %q", summary.Format)
	}
	if summary.Provider == "" {
		summary.Provider = "provider"
	}
	if summary.RowsSeen == 0 {
		return nil, fmt.Errorf("provider statement contains no billable rows")
	}
	if summary.CostRows == 0 {
		summary.Warnings = append(summary.Warnings, "no USD cost column was recognized")
	}
	return summary, nil
}

// WarningsJSON returns an empty string when there are no warnings, otherwise a
// compact JSON array suitable for persistence in audit tables.
func WarningsJSON(warnings []string) string {
	if len(warnings) == 0 {
		return ""
	}
	raw, _ := json.Marshal(warnings)
	return string(raw)
}

func normalizeFormat(format string, raw []byte) string {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" || format == "auto" {
		if len(raw) > 0 && (raw[0] == '{' || raw[0] == '[') {
			return "json"
		}
		return "csv"
	}
	return format
}

func parseJSONStatement(raw []byte, summary *ImportSummary) error {
	var decoded interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	rows := collectJSONRows(decoded)
	for _, row := range rows {
		consumeMapRow(row, summary)
	}
	return nil
}

func collectJSONRows(value interface{}) []map[string]interface{} {
	var out []map[string]interface{}
	var walk func(interface{})
	walk = func(v interface{}) {
		switch typed := v.(type) {
		case []interface{}:
			for _, item := range typed {
				walk(item)
			}
		case map[string]interface{}:
			if rowLooksBillable(typed) {
				out = append(out, typed)
				return
			}
			for _, key := range []string{"data", "rows", "items", "line_items", "usage", "costs", "invoices", "records"} {
				if child, ok := typed[key]; ok {
					walk(child)
				}
			}
		}
	}
	walk(value)
	return out
}

func rowLooksBillable(row map[string]interface{}) bool {
	if _, ok := firstValue(row, providerKeys...); ok {
		return true
	}
	if _, ok := firstValue(row, timeKeys...); ok {
		return true
	}
	if _, ok := firstValue(row, costKeys...); ok {
		return true
	}
	if nested, ok := row["usage"].(map[string]interface{}); ok {
		if _, ok := firstValue(nested, costKeys...); ok {
			return true
		}
	}
	if nested, ok := row["billing"].(map[string]interface{}); ok {
		if _, ok := firstValue(nested, costKeys...); ok {
			return true
		}
	}
	return false
}

func parseCSVStatement(raw []byte, summary *ImportSummary) error {
	reader := csv.NewReader(bytes.NewReader(raw))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	header, err := reader.Read()
	if err != nil {
		return err
	}
	index := map[string]int{}
	for i, h := range header {
		index[normalizeKey(h)] = i
	}
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		row := map[string]interface{}{}
		for key, i := range index {
			if i < len(record) {
				row[key] = record[i]
			}
		}
		consumeMapRow(row, summary)
	}
	return nil
}

func consumeMapRow(row map[string]interface{}, summary *ImportSummary) {
	normalized := normalizeMap(row)
	summary.RowsSeen++
	if summary.Provider == "" {
		if provider, ok := firstString(normalized, providerKeys...); ok {
			summary.Provider = provider
		}
	}
	currency := "USD"
	if rawCurrency, ok := firstString(normalized, "currency", "billing_currency"); ok {
		currency = strings.ToUpper(strings.TrimSpace(rawCurrency))
	}
	if currency != "" && currency != "USD" {
		summary.Warnings = appendOnce(summary.Warnings, "non-USD rows were ignored; configure provider currency conversion externally")
		return
	}
	if cost, ok := extractCost(normalized); ok {
		summary.ProviderCostUSD += cost
		summary.CostRows++
	}
	if ts, ok := firstTime(normalized, timeKeys...); ok {
		if summary.WindowStart.IsZero() || ts.Before(summary.WindowStart) {
			summary.WindowStart = ts
		}
		if summary.WindowEnd.IsZero() || ts.After(summary.WindowEnd) {
			summary.WindowEnd = ts
		}
	}
}

func normalizeMap(row map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{}
	for key, value := range row {
		out[normalizeKey(key)] = value
		if nested, ok := value.(map[string]interface{}); ok {
			for nestedKey, nestedValue := range normalizeMap(nested) {
				out[nestedKey] = nestedValue
			}
		}
	}
	return out
}

func normalizeKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, " ", "_")
	key = strings.ReplaceAll(key, "-", "_")
	key = strings.ReplaceAll(key, ".", "_")
	key = strings.Trim(key, "_")
	return key
}

var providerKeys = []string{"provider", "vendor", "source", "system", "organization"}
var timeKeys = []string{"created_at", "created", "timestamp", "date", "start_time", "end_time", "period_start", "period_end"}
var costKeys = []string{
	"cost_usd", "total_cost_usd", "amount_usd", "usage_cost_usd", "price_usd",
	"billed_amount_usd", "total_cost", "cost", "amount", "price",
}

func extractCost(row map[string]interface{}) (float64, bool) {
	value, ok := firstValue(row, costKeys...)
	if !ok {
		return 0, false
	}
	return parseMoney(value)
}

func firstValue(row map[string]interface{}, keys ...string) (interface{}, bool) {
	for _, key := range keys {
		if value, ok := row[key]; ok && value != nil {
			return value, true
		}
	}
	return nil, false
}

func firstString(row map[string]interface{}, keys ...string) (string, bool) {
	value, ok := firstValue(row, keys...)
	if !ok {
		return "", false
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	return text, text != "" && text != "<nil>"
}

func firstTime(row map[string]interface{}, keys ...string) (time.Time, bool) {
	value, ok := firstValue(row, keys...)
	if !ok {
		return time.Time{}, false
	}
	switch typed := value.(type) {
	case float64:
		return time.Unix(int64(typed), 0).UTC(), true
	case int64:
		return time.Unix(typed, 0).UTC(), true
	case int:
		return time.Unix(int64(typed), 0).UTC(), true
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" || text == "<nil>" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed.UTC(), true
		}
	}
	if seconds, err := strconv.ParseInt(text, 10, 64); err == nil {
		return time.Unix(seconds, 0).UTC(), true
	}
	return time.Time{}, false
}

func parseMoney(value interface{}) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case string:
		cleaned := moneyCleaner.ReplaceAllString(strings.TrimSpace(typed), "")
		if cleaned == "" || cleaned == "." || cleaned == "-" {
			return 0, false
		}
		parsed, err := strconv.ParseFloat(cleaned, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func appendOnce(items []string, item string) []string {
	for _, existing := range items {
		if existing == item {
			return items
		}
	}
	return append(items, item)
}
