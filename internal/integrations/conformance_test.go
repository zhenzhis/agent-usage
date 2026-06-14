package integrations

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunAdapterConformanceAutoDetectsProviderUsage(t *testing.T) {
	report, err := RunAdapterConformance("auto", []byte(`{
		"id":"resp_conf_1",
		"provider":"openai",
		"model":"gpt-5.5",
		"input":[{"role":"user","content":"must not persist"}],
		"usage":{"input_tokens":100,"input_tokens_details":{"cached_tokens":25},"output_tokens":40},
		"metadata":{"agent_ledger.goal":"conformance provider","agent_ledger.project":"agent-ledger"}
	}`))
	if err != nil {
		t.Fatalf("conformance: %v", err)
	}
	if !report.OK || report.Status != "pass" || report.InputKind != "provider" || report.DecodedEvents != 2 || report.FailedEvents != 0 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if report.SchemaVersion != "v1" || report.SchemaHash == "" {
		t.Fatalf("missing schema identity: %#v", report)
	}
	for _, result := range report.Results {
		if result.EventID == "" || result.PayloadHash == "" || result.EventType == "" {
			t.Fatalf("result missing validation identity: %#v", result)
		}
	}
}

func TestRunAdapterConformanceReportsCanonicalPrivacyFailure(t *testing.T) {
	report, err := RunAdapterConformance("canonical", []byte(`{
		"source":"test-adapter",
		"event_type":"workload.started",
		"payload":{"goal":"safe","messages":[{"content":"must fail"}]}
	}`))
	if err != nil {
		t.Fatalf("conformance should return a report for validation failures: %v", err)
	}
	if report.OK || report.Status != "fail" || report.FailedEvents != 1 {
		t.Fatalf("expected failed report: %#v", report)
	}
	if len(report.Results) != 1 || report.Results[0].Error == "" {
		t.Fatalf("expected result error: %#v", report.Results)
	}
}

func TestRunAdapterConformanceAutoDetectsProviderStreamUsage(t *testing.T) {
	report, err := RunAdapterConformance("auto", []byte(`event: message_start
data: {"type":"message_start","message":{"id":"msg_stream_conf_1","model":"claude-opus-4-7","usage":{"input_tokens":100,"cache_read_input_tokens":10,"cache_creation_input_tokens":5,"output_tokens":1}}}

event: content_block_delta
data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"must not persist"}}

event: message_delta
data: {"type":"message_delta","usage":{"output_tokens":25}}

event: message_stop
data: {"type":"message_stop"}
`))
	if err != nil {
		t.Fatalf("conformance: %v", err)
	}
	if !report.OK || report.Status != "pass" || report.InputKind != "provider-stream" || report.DecodedEvents != 2 || report.FailedEvents != 0 {
		t.Fatalf("unexpected provider-stream report: %#v", report)
	}
}

func TestRunAdapterConformanceCanonicalWarnings(t *testing.T) {
	report, err := RunAdapterConformance("canonical", []byte(`{
		"source":"test-adapter",
		"event_type":"workload.started",
		"payload":{"goal":"missing provenance"}
	}`))
	if err != nil {
		t.Fatalf("conformance: %v", err)
	}
	if !report.OK || report.Status != "pass_with_warnings" || report.WarningEvents != 1 || len(report.Recommendations) == 0 {
		t.Fatalf("expected provenance warning report: %#v", report)
	}
	strict, err := RunAdapterConformanceWithOptions(AdapterConformanceOptions{Kind: "canonical", Strict: true}, []byte(`{
		"source":"test-adapter",
		"event_type":"workload.started",
		"payload":{"goal":"missing provenance"}
	}`))
	if err != nil {
		t.Fatalf("strict conformance: %v", err)
	}
	if strict.OK || strict.Status != "fail" || strict.WarningEvents != 1 || strict.FailedEvents != 0 {
		t.Fatalf("expected strict warning failure: %#v", strict)
	}
}

func TestAdapterFixtureFilesPassStrictConformance(t *testing.T) {
	matrix := AdapterConformanceMatrixSpec()
	profileIDs := map[string]bool{}
	profileFixtures := map[string]map[string]bool{}
	for _, profile := range ProviderProfiles().Profiles {
		profileIDs[profile.ID] = true
		profileFixtures[profile.ID] = map[string]bool{}
		for _, path := range profile.ConformanceFixtures {
			profileFixtures[profile.ID][path] = true
		}
	}

	seenPaths := map[string]bool{}
	checked := 0
	for _, kind := range matrix.Kinds {
		kind := kind
		if kind.ConformanceKind == "" {
			t.Fatalf("matrix kind missing conformance kind: %+v", kind)
		}
		for _, fixture := range kind.Fixtures {
			fixture := fixture
			checked++
			t.Run(fixture.Path, func(t *testing.T) {
				if seenPaths[fixture.Path] {
					t.Fatalf("duplicate fixture path in conformance matrix: %s", fixture.Path)
				}
				seenPaths[fixture.Path] = true
				expectedCommand := "agent-ledger adapter conformance --kind " + kind.ConformanceKind + " --strict --file " + fixture.Path
				if fixture.Command != expectedCommand {
					t.Fatalf("fixture command drifted: got %q want %q", fixture.Command, expectedCommand)
				}
				for _, profileID := range fixture.ProviderProfileIDs {
					if !profileIDs[profileID] {
						t.Fatalf("fixture references unknown provider profile %q", profileID)
					}
					if !profileFixtures[profileID][fixture.Path] {
						t.Fatalf("provider profile %q does not reciprocally list fixture %s", profileID, fixture.Path)
					}
				}
				raw, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(fixture.Path)))
				if err != nil {
					t.Fatalf("read fixture: %v", err)
				}
				report, err := RunAdapterConformanceWithOptions(AdapterConformanceOptions{Kind: kind.ConformanceKind, Strict: true}, raw)
				if err != nil {
					t.Fatalf("conformance: %v", err)
				}
				if report.InputKind != kind.ConformanceKind || !report.OK || report.Status != "pass" || report.FailedEvents != 0 || report.WarningEvents != 0 {
					t.Fatalf("fixture failed strict conformance: %#v", report)
				}
				reportTypes := map[string]bool{}
				for _, result := range report.Results {
					reportTypes[result.EventType] = true
				}
				for _, expectedType := range fixture.ExpectedEventTypes {
					if !reportTypes[expectedType] {
						t.Fatalf("fixture did not produce expected event type %q: report=%#v", expectedType, report)
					}
				}
			})
		}
	}
	if checked != matrix.Summary.Fixtures || checked != matrix.Summary.StrictFixtures {
		t.Fatalf("matrix summary drifted from fixtures: checked=%d summary=%+v", checked, matrix.Summary)
	}
}
