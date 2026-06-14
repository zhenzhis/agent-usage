package integrations

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProviderProfilesCoverFutureProviderEcosystem(t *testing.T) {
	catalog := ProviderProfiles()
	if catalog.Contract != "agent-ledger.provider-profile-catalog" || !catalog.LocalFirst || catalog.Summary.Profiles < 6 {
		t.Fatalf("unexpected provider profile catalog: %+v", catalog)
	}
	for _, id := range []string{"openai-official", "anthropic-official", "openrouter-relay", "litellm-proxy", "google-gemini", "ollama-local", "vllm-local"} {
		if !providerProfileExists(catalog, id) {
			t.Fatalf("missing provider profile %q: %+v", id, catalog.Profiles)
		}
	}
	if catalog.Summary.OpenAICompatible < 4 || catalog.Summary.UsageMetadataProfiles < 3 || catalog.Summary.LocalRuntimeProfiles < 2 || catalog.Summary.EdgeRuntimeProfiles < 1 {
		t.Fatalf("provider profile coverage too narrow: %+v", catalog.Summary)
	}
	raw, _ := json.Marshal(catalog)
	for _, forbidden := range []string{"sk-", "api_key=", "bearer ", "prompt text", "response text", "C:/Users/", "BEGIN PRIVATE KEY"} {
		if strings.Contains(strings.ToLower(string(raw)), strings.ToLower(forbidden)) {
			t.Fatalf("provider profile catalog leaked forbidden marker %q: %s", forbidden, string(raw))
		}
	}
	if ProviderProfilesFingerprint() == "" || !strings.HasPrefix(ProviderProfilesFingerprint(), "sha256:") {
		t.Fatalf("missing provider profile fingerprint")
	}
}

func providerProfileExists(catalog ProviderProfileCatalog, id string) bool {
	for _, profile := range catalog.Profiles {
		if profile.ID == id {
			return true
		}
	}
	return false
}
