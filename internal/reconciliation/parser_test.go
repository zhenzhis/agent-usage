package reconciliation

import "testing"

func TestParseProviderStatementJSONEnvelope(t *testing.T) {
	raw := []byte(`{
		"data":[
			{"provider":"openai","created_at":"2026-06-06T10:00:00Z","cost_usd":1.25},
			{"provider":"openai","created_at":"2026-06-06T11:00:00Z","usage":{"total_cost_usd":"$2.75"}}
		]
	}`)
	summary, err := ParseProviderStatement(raw, "auto", "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if summary.Provider != "openai" || summary.Format != "json" || summary.RowsSeen != 2 || summary.CostRows != 2 {
		t.Fatalf("unexpected summary metadata: %+v", summary)
	}
	if summary.ProviderCostUSD != 4.0 {
		t.Fatalf("cost=%f", summary.ProviderCostUSD)
	}
	if summary.PayloadSHA256 == "" || summary.WindowStart.IsZero() || summary.WindowEnd.IsZero() {
		t.Fatalf("missing integrity/window fields: %+v", summary)
	}
}

func TestParseProviderStatementCSVSkipsNonUSD(t *testing.T) {
	raw := []byte("provider,date,currency,amount_usd\nanthropic,2026-06-06,USD,$3.50\nanthropic,2026-06-07,EUR,9.00\n")
	summary, err := ParseProviderStatement(raw, "csv", "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if summary.Provider != "anthropic" || summary.RowsSeen != 2 || summary.CostRows != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if summary.ProviderCostUSD != 3.5 {
		t.Fatalf("cost=%f", summary.ProviderCostUSD)
	}
	if len(summary.Warnings) == 0 {
		t.Fatalf("expected non-USD warning: %+v", summary)
	}
}
