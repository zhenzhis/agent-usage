package pricing

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhenzhis/agent-ledger/internal/config"
	"github.com/zhenzhis/agent-ledger/internal/storage"
)

func TestCalcCost_Basic(t *testing.T) {
	// prices: [input, output, cache_read, cache_creation]
	prices := [4]float64{0.003, 0.015, 0.001, 0.004}

	cost := CalcCost(1000, 500, 0, 0, prices)
	// 1000 * 0.003 + 500 * 0.015 = 3.0 + 7.5 = 10.5
	if cost != 10.5 {
		t.Errorf("expected 10.5, got %f", cost)
	}
}

func TestCalcCost_WithCache(t *testing.T) {
	prices := [4]float64{0.003, 0.015, 0.001, 0.004}

	// input=500 (non-cached), output=500, cacheCreation=200, cacheRead=300
	// cost = 500*0.003 + 200*0.004 + 300*0.001 + 500*0.015
	//      = 1.5 + 0.8 + 0.3 + 7.5 = 10.1
	cost := CalcCost(500, 500, 200, 300, prices)
	expected := 10.1
	if diff := cost - expected; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("expected %f, got %f", expected, cost)
	}
}

func TestCalcCost_ZeroNonCachedInput(t *testing.T) {
	prices := [4]float64{0.003, 0.015, 0.001, 0.004}

	// All input is cached, non-cached input = 0
	cost := CalcCost(0, 500, 200, 300, prices)
	// cost = 0 + 200*0.004 + 300*0.001 + 500*0.015 = 0.8 + 0.3 + 7.5 = 8.6
	expected := 8.6
	if diff := cost - expected; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("expected %f, got %f", expected, cost)
	}
}

func TestCalcCost_ZeroTokens(t *testing.T) {
	prices := [4]float64{0.003, 0.015, 0.001, 0.004}
	cost := CalcCost(0, 0, 0, 0, prices)
	if cost != 0 {
		t.Errorf("expected 0, got %f", cost)
	}
}

func TestCalcCost_ZeroPrices(t *testing.T) {
	prices := [4]float64{0, 0, 0, 0}
	cost := CalcCost(1000, 500, 200, 300, prices)
	if cost != 0 {
		t.Errorf("expected 0, got %f", cost)
	}
}

func TestSyncWithConfigAppliesOfficialAndOverrideWhenLiteLLMFails(t *testing.T) {
	oldFetch := fetchPricingBytes
	fetchPricingBytes = func(string) ([]byte, string, error) {
		return nil, "", errors.New("offline fallback")
	}
	defer func() { fetchPricingBytes = oldFetch }()

	db, err := storage.Open(filepath.Join(t.TempDir(), "agent-ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := SyncWithConfig(db, config.PricingConfig{Overrides: []config.PriceRule{{
		Model: "gpt-5", Source: "contract", InputCostPerToken: 0.000001, OutputCostPerToken: 0.000002,
	}}}); err != nil {
		t.Fatalf("SyncWithConfig should preserve official/local rules on fallback failure: %v", err)
	}
	rows, err := db.GetPricingAudit(100)
	if err != nil {
		t.Fatal(err)
	}
	var gpt5 storage.PricingAuditRow
	for _, row := range rows {
		if row.Model == "gpt-5" {
			gpt5 = row
			break
		}
	}
	if gpt5.PricingSource != "contract" || gpt5.Priority != 1 || gpt5.Confidence != "override" {
		t.Fatalf("local override did not win over official/fallback rows: %+v", gpt5)
	}
	summary, err := db.GetPricingRuleSummary()
	if err != nil {
		t.Fatal(err)
	}
	if summary.OverrideRules < 1 || summary.OfficialRules < 1 || summary.TotalRules < summary.OverrideRules+summary.OfficialRules {
		t.Fatalf("unexpected pricing summary: %+v", summary)
	}
	sources, err := db.GetPricingSources(24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	byName := map[string]storage.PricingSourceStatus{}
	for _, source := range sources {
		status[source.Name] = source.Status
		byName[source.Name] = source
	}
	if status["litellm"] != "error" || status["openai-official"] != "seeded" || status["anthropic-official"] != "seeded" {
		t.Fatalf("unexpected source statuses: %+v", sources)
	}
	if status["contract"] != "configured" || byName["contract"].Kind != "override" || byName["contract"].Priority != 1 || byName["contract"].ModelCount != 1 {
		t.Fatalf("local override source was not recorded: %+v", sources)
	}
	for _, name := range []string{"openai-official", "anthropic-official", "contract"} {
		if !strings.HasPrefix(byName[name].SHA256, "sha256:") {
			t.Fatalf("pricing source %s missing stable sha256 hash: %+v", name, byName[name])
		}
	}
}
