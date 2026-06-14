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
	agentLedger := nestedMap(t, exporters, "otlp_http/agentledger")
	if got := yamlStringValue(agentLedger["endpoint"]); got != "http://127.0.0.1:9800" {
		t.Fatalf("collector exporter endpoint=%q, want local Agent Ledger endpoint", got)
	}
	if got := yamlStringValue(agentLedger["compression"]); got != "gzip" {
		t.Fatalf("collector exporter compression=%q, want gzip", got)
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

func TestOTelCollectorComposeSmokeIsLocalAndWired(t *testing.T) {
	root := filepath.Join("..", "..", "examples", "otel-collector")
	for _, name := range []string{"docker-compose.smoke.yml", "config.compose.yaml", "agent-ledger.smoke.yaml"} {
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		text := string(raw)
		for _, forbidden := range []string{"sk-", "xoxb-", "api_key", "https://", "/sessions/claude", "/sessions/codex", "/sessions/opencode"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s contains forbidden secret, remote marker, or session mount %q", name, forbidden)
			}
		}
	}

	composeRaw, err := os.ReadFile(filepath.Join(root, "docker-compose.smoke.yml"))
	if err != nil {
		t.Fatalf("read compose smoke: %v", err)
	}
	var compose map[string]interface{}
	if err := yaml.Unmarshal(composeRaw, &compose); err != nil {
		t.Fatalf("parse compose smoke yaml: %v", err)
	}
	services := nestedMap(t, compose, "services")
	agentLedger := nestedMap(t, services, "agent-ledger")
	if !stringListHas(yamlStringList(agentLedger["ports"]), "127.0.0.1:19800:9800") {
		t.Fatalf("compose smoke must publish Agent Ledger on loopback only: %#v", agentLedger["ports"])
	}
	agentVolumes := yamlStringList(agentLedger["volumes"])
	if !stringListHas(agentVolumes, "./agent-ledger.smoke.yaml:/etc/agent-ledger/config.yaml:ro") || stringListContains(agentVolumes, "/sessions/") {
		t.Fatalf("compose smoke must mount only smoke config/data, got %#v", agentVolumes)
	}
	collector := nestedMap(t, services, "otel-collector")
	if got := yamlStringValue(collector["image"]); got != "${OTELCOL_IMAGE:-otel/opentelemetry-collector-contrib:0.154.0}" {
		t.Fatalf("collector image=%q, want pinned default with override", got)
	}
	if got := yamlStringValue(collector["network_mode"]); got != "service:agent-ledger" {
		t.Fatalf("collector must share Agent Ledger network namespace, got %q", got)
	}
	if ports := yamlStringList(collector["ports"]); len(ports) != 0 {
		t.Fatalf("collector should not publish host ports in compose smoke: %#v", ports)
	}
	if got := yamlStringValue(nestedMap(t, services, "smoke")["network_mode"]); got != "service:agent-ledger" {
		t.Fatalf("smoke sender must share Agent Ledger network namespace, got %q", got)
	}
	smoke := nestedMap(t, services, "smoke")
	command := strings.Join(yamlStringList(smoke["command"]), "\n")
	for _, want := range []string{"/fixtures/otlp-resource-spans.json", "http://127.0.0.1:13133/", "http://127.0.0.1:4318/v1/traces", "http://127.0.0.1:9800/api/model-calls", "otlp-fixture", "gpt-5.5"} {
		if !strings.Contains(command, want) {
			t.Fatalf("compose smoke command missing %q: %s", want, command)
		}
	}

	collectorRaw, err := os.ReadFile(filepath.Join(root, "config.compose.yaml"))
	if err != nil {
		t.Fatalf("read collector compose config: %v", err)
	}
	var collectorCfg map[string]interface{}
	if err := yaml.Unmarshal(collectorRaw, &collectorCfg); err != nil {
		t.Fatalf("parse collector compose config yaml: %v", err)
	}
	exporters := nestedMap(t, collectorCfg, "exporters")
	composeExporter := nestedMap(t, exporters, "otlp_http/agentledger")
	if got := yamlStringValue(composeExporter["endpoint"]); got != "http://127.0.0.1:9800" {
		t.Fatalf("compose collector endpoint=%q, want shared-network localhost Agent Ledger endpoint", got)
	}
	if got := yamlStringValue(composeExporter["compression"]); got != "gzip" {
		t.Fatalf("compose collector compression=%q, want gzip", got)
	}
	if yamlBoolValue(nestedMap(t, composeExporter, "sending_queue")["enabled"]) || yamlBoolValue(nestedMap(t, composeExporter, "retry_on_failure")["enabled"]) {
		t.Fatalf("compose smoke exporter should fail fast without queue/retry: %#v", composeExporter)
	}
	extensions := nestedMap(t, collectorCfg, "extensions")
	health := nestedMap(t, extensions, "health_check")
	if got := yamlStringValue(health["endpoint"]); got != "0.0.0.0:13133" {
		t.Fatalf("compose collector health endpoint=%q", got)
	}
	assertPrivacyDeletes(t, collectorCfg)

	ledgerRaw, err := os.ReadFile(filepath.Join(root, "agent-ledger.smoke.yaml"))
	if err != nil {
		t.Fatalf("read Agent Ledger smoke config: %v", err)
	}
	var ledgerCfg map[string]interface{}
	if err := yaml.Unmarshal(ledgerRaw, &ledgerCfg); err != nil {
		t.Fatalf("parse Agent Ledger smoke config yaml: %v", err)
	}
	integrations := nestedMap(t, ledgerCfg, "integrations")
	otlp := nestedMap(t, integrations, "otlp_receiver")
	if !yamlBoolValue(otlp["enabled"]) || yamlBoolValue(otlp["grpc_enabled"]) {
		t.Fatalf("smoke config should enable only OTLP HTTP receiver: %#v", otlp)
	}
	collectors := nestedMap(t, ledgerCfg, "collectors")
	for _, name := range []string{"claude", "codex", "openclaw", "opencode", "kiro", "pi"} {
		source := nestedMap(t, collectors, name)
		if yamlBoolValue(source["enabled"]) {
			t.Fatalf("smoke config should not scan local session collectors: %s=%#v", name, source)
		}
	}
}

func assertPrivacyDeletes(t *testing.T, cfg map[string]interface{}) {
	t.Helper()
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

func yamlStringList(value interface{}) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []interface{}:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, yamlStringValue(item))
		}
		return out
	default:
		if text := yamlStringValue(value); text != "" {
			return []string{text}
		}
		return nil
	}
}

func yamlBoolValue(value interface{}) bool {
	got, _ := value.(bool)
	return got
}

func stringListHas(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func stringListContains(values []string, needle string) bool {
	for _, value := range values {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
