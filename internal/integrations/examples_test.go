package integrations

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOTelCollectorExampleIsLocalAndPrivacyPreserving(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "examples", "otel-collector", "config.yaml"))
	if err != nil {
		t.Fatalf("read collector example: %v", err)
	}
	text := string(raw)
	for _, forbidden := range []string{"sk-", "xoxb-", "api_key", "https://"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("collector example contains forbidden secret or remote marker %q", forbidden)
		}
	}
	var cfg map[string]interface{}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse collector example yaml: %v", err)
	}
	exporters := nestedMap(t, cfg, "exporters")
	agentLedger := nestedMap(t, exporters, "otlphttp/agentledger")
	if got := yamlStringValue(agentLedger["endpoint"]); got != "http://127.0.0.1:9800" {
		t.Fatalf("collector exporter endpoint=%q, want local Agent Ledger endpoint", got)
	}
	headers := nestedMap(t, agentLedger, "headers")
	if got := yamlStringValue(headers["Authorization"]); got != "${env:AGENT_LEDGER_AUTH_HEADER}" {
		t.Fatalf("collector Authorization header must use env indirection, got %q", got)
	}
	processors := nestedMap(t, cfg, "processors")
	privacy := nestedMap(t, processors, "attributes/privacy")
	actions, ok := privacy["actions"].([]interface{})
	if !ok || len(actions) < 4 {
		t.Fatalf("collector privacy processor missing delete actions: %#v", privacy["actions"])
	}
	deletes := map[string]bool{}
	for _, action := range actions {
		item, ok := action.(map[string]interface{})
		if !ok {
			t.Fatalf("invalid privacy action: %#v", action)
		}
		if yamlStringValue(item["action"]) == "delete" {
			deletes[yamlStringValue(item["key"])] = true
		}
	}
	for _, key := range []string{"gen_ai.input.messages", "gen_ai.output.messages", "llm.prompts", "llm.completions"} {
		if !deletes[key] {
			t.Fatalf("collector privacy processor does not delete %q: %#v", key, deletes)
		}
	}
}

func nestedMap(t *testing.T, parent map[string]interface{}, key string) map[string]interface{} {
	t.Helper()
	value, ok := parent[key]
	if !ok {
		t.Fatalf("missing YAML key %q in %#v", key, parent)
	}
	child, ok := value.(map[string]interface{})
	if !ok {
		t.Fatalf("YAML key %q is %T, want map", key, value)
	}
	return child
}

func yamlStringValue(value interface{}) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
