package integrations

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zhenzhis/agent-ledger/internal/storage"
)

func TestConvertA2ATaskSnapshotToCanonicalEvents(t *testing.T) {
	raw := []byte(`{
		"id":"task-123",
		"contextId":"ctx-abc",
		"kind":"task",
		"status":{"state":"completed","timestamp":"2026-06-07T11:00:00Z","message":{"role":"agent","parts":[{"kind":"text","text":"must not persist"}]}},
		"history":[{"role":"user","parts":[{"kind":"text","text":"secret request"}]}],
		"artifacts":[{
			"artifactId":"artifact-1",
			"name":"summary",
			"description":"short report",
			"parts":[{"kind":"text","text":"artifact body must not persist"},{"kind":"data","data":{"x":1}}],
			"metadata":{"sha256":"abc123"}
		}],
		"metadata":{
			"agent_ledger.goal":"delegate research",
			"agent_ledger.project":"agent-ledger",
			"agent_ledger.agent_name":"remote-researcher"
		}
	}`)
	tasks, err := DecodeA2ATasks(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	events, err := ConvertA2ATasks(tasks)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	for _, want := range []string{"workload.started", "agent.run.started", "context.ref", "artifact.created", "agent.run.finished", "workload.closed", "evaluation.recorded"} {
		if findCanonical(events, want).EventType == "" {
			t.Fatalf("%s missing in %#v", want, events)
		}
	}
	workloadID := findCanonical(events, "workload.started").WorkloadID
	if workloadID == "" {
		t.Fatal("missing workload id")
	}
	for _, event := range events {
		if event.WorkloadID != workloadID {
			t.Fatalf("events must share workload: %s != %s for %s", event.WorkloadID, workloadID, event.EventType)
		}
		if string(event.Payload) == "" {
			t.Fatalf("missing payload for %s", event.EventType)
		}
		if event.SchemaVersion != "v1" || event.SourceVersion == "" || event.ParserVersion != "agent-ledger-a2a@v1" || event.RawRef == "" || event.MatchType == "" {
			t.Fatalf("A2A provenance missing for %s: %#v", event.EventType, event)
		}
		if containsAny(string(event.Payload), "must not persist", "secret request", "artifact body") {
			t.Fatalf("sensitive A2A content leaked in %s payload: %s", event.EventType, string(event.Payload))
		}
	}
	artifact := findCanonical(events, "artifact.created")
	var payload map[string]interface{}
	if err := json.Unmarshal(artifact.Payload, &payload); err != nil {
		t.Fatalf("artifact payload: %v", err)
	}
	metadata := payload["metadata"].(map[string]interface{})
	if metadata["part_count"].(float64) != 2 {
		t.Fatalf("part count missing: %#v", metadata)
	}
}

func TestDecodeA2AJSONRPCResultAndEvent(t *testing.T) {
	raw := []byte(`[
		{"jsonrpc":"2.0","id":"1","result":{"task":{"id":"task-rpc","contextId":"ctx-rpc","status":{"state":"working"},"metadata":{"agent_ledger.goal":"rpc task"}}}},
		{"taskId":"task-event","contextId":"ctx-event","status":{"state":"failed","timestamp":"2026-06-07T11:05:00Z"},"metadata":{"agent_ledger.goal":"event task"}}
	]`)
	tasks, err := DecodeA2ATasks(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("tasks=%d", len(tasks))
	}
	events, err := ConvertA2ATasks(tasks)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if findEventByID(events, "a2a:workload:task-rpc").EventType != "workload.started" {
		t.Fatalf("rpc task missing")
	}
	if findEventByID(events, "a2a:workload-closed:task-event:failed").EventType != "workload.closed" {
		t.Fatalf("failed task close missing")
	}
}

func findCanonical(events []storage.CanonicalEvent, eventType string) storage.CanonicalEvent {
	for _, event := range events {
		if event.EventType == eventType {
			return event
		}
	}
	return storage.CanonicalEvent{}
}

func findEventByID(events []storage.CanonicalEvent, eventID string) storage.CanonicalEvent {
	for _, event := range events {
		if event.EventID == eventID {
			return event
		}
	}
	return storage.CanonicalEvent{}
}

func containsAny(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}
