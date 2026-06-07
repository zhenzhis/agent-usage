package integrations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zhenzhis/agent-ledger/internal/storage"
)

// A2ATask is a privacy-safe subset of an Agent2Agent Task snapshot.
type A2ATask struct {
	ID         string                 `json:"id"`
	ContextID  string                 `json:"context_id"`
	Status     A2AStatus              `json:"status"`
	Artifacts  []A2AArtifact          `json:"artifacts,omitempty"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
	Kind       string                 `json:"kind,omitempty"`
	SourceType string                 `json:"source_type"`
}

// A2AStatus describes task lifecycle metadata without retaining message content.
type A2AStatus struct {
	State     string    `json:"state"`
	Timestamp time.Time `json:"timestamp,omitempty"`
}

// A2AArtifact describes artifact metadata without retaining artifact parts.
type A2AArtifact struct {
	ID          string                 `json:"artifact_id"`
	Name        string                 `json:"name,omitempty"`
	Description string                 `json:"description,omitempty"`
	PartCount   int                    `json:"part_count"`
	PartKinds   []string               `json:"part_kinds,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// DecodeA2ATasks decodes common A2A JSON shapes: Task, JSON-RPC result, task event, arrays, or {tasks/events:[...]}.
func DecodeA2ATasks(raw []byte) ([]A2ATask, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty A2A input")
	}
	if trimmed[0] == '[' {
		var entries []json.RawMessage
		if err := json.Unmarshal(trimmed, &entries); err != nil {
			return nil, err
		}
		return decodeA2AEntries(entries)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return nil, err
	}
	for _, key := range []string{"tasks", "events"} {
		if rawEntries, ok := obj[key]; ok {
			var entries []json.RawMessage
			if err := json.Unmarshal(rawEntries, &entries); err != nil {
				return nil, err
			}
			return decodeA2AEntries(entries)
		}
	}
	task, err := decodeA2AEntry(trimmed)
	if err != nil {
		return nil, err
	}
	return []A2ATask{task}, nil
}

// ConvertA2ATasks projects A2A task snapshots into metadata-only canonical events.
func ConvertA2ATasks(tasks []A2ATask) ([]storage.CanonicalEvent, error) {
	events := []storage.CanonicalEvent{}
	for _, task := range tasks {
		if task.ID == "" {
			return nil, fmt.Errorf("A2A task id is required")
		}
		metadata := task.Metadata
		workloadID := metadataString(metadata, "agent_ledger.workload_id", "workload_id")
		if workloadID == "" {
			workloadID = deterministicLedgerID("wl", "a2a-task:"+task.ID)
		}
		runID := metadataString(metadata, "agent_ledger.agent_run_id", "agent_run_id")
		if runID == "" {
			runID = deterministicLedgerID("run", "a2a-run:"+task.ID)
		}
		source := firstNonEmpty(metadataString(metadata, "agent_ledger.source", "source"), "a2a")
		project := metadataString(metadata, "agent_ledger.project", "project")
		branch := metadataString(metadata, "agent_ledger.git_branch", "git_branch")
		goal := firstNonEmpty(metadataString(metadata, "agent_ledger.goal", "goal", "title"), "A2A task "+task.ID)
		timestamp := firstTime(task.Status.Timestamp, time.Now().UTC())
		state := normalizeA2AState(task.Status.State)
		sourceVersion := firstNonEmpty(metadataString(metadata, "agent_ledger.source_version", "a2a.version", "protocol_version"), task.SourceType, "a2a-json")
		parserVersion := firstNonEmpty(metadataString(metadata, "agent_ledger.parser_version"), "agent-ledger-a2a@v1")
		rawRef := firstNonEmpty(metadataString(metadata, "agent_ledger.raw_ref", "raw_ref"), "a2a:task:"+task.ID)
		events = append(events, storage.CanonicalEvent{
			EventID:       "a2a:workload:" + task.ID,
			Source:        source,
			EventType:     "workload.started",
			SchemaVersion: "v1",
			SourceVersion: sourceVersion,
			ParserVersion: parserVersion,
			SourceEventID: task.ID,
			RawRef:        rawRef,
			MatchType:     "source_reported",
			WorkloadID:    workloadID,
			Project:       project,
			GitBranch:     branch,
			Timestamp:     timestamp,
			Payload: mustJSON(map[string]interface{}{
				"goal":       goal,
				"project":    project,
				"git_branch": branch,
				"repo":       metadataString(metadata, "agent_ledger.repo", "repo"),
				"owner":      metadataString(metadata, "agent_ledger.owner", "owner"),
				"team":       metadataString(metadata, "agent_ledger.team", "team"),
			}),
			Confidence: 0.9,
		})
		events = append(events, storage.CanonicalEvent{
			EventID:       "a2a:run:" + task.ID,
			Source:        source,
			EventType:     "agent.run.started",
			SchemaVersion: "v1",
			SourceVersion: sourceVersion,
			ParserVersion: parserVersion,
			SourceEventID: task.ID,
			RawRef:        rawRef + ":run",
			MatchType:     "source_reported",
			WorkloadID:    workloadID,
			AgentRunID:    runID,
			Project:       project,
			GitBranch:     branch,
			Timestamp:     timestamp,
			Payload: mustJSON(map[string]interface{}{
				"run_id":        runID,
				"agent_name":    firstNonEmpty(metadataString(metadata, "agent_ledger.agent_name", "agent_name"), "a2a-remote-agent"),
				"agent_version": metadataString(metadata, "agent_ledger.agent_version", "agent_version"),
				"status":        runningStatusForA2A(state),
				"parent_run_id": metadataString(metadata, "agent_ledger.parent_run_id", "parent_run_id"),
				"goal":          goal,
			}),
			Confidence: 0.85,
		})
		events = append(events, storage.CanonicalEvent{
			EventID:       "a2a:context:" + task.ID,
			Source:        source,
			EventType:     "context.ref",
			SchemaVersion: "v1",
			SourceVersion: sourceVersion,
			ParserVersion: parserVersion,
			SourceEventID: "a2a:context:" + task.ID,
			RawRef:        rawRef + ":context",
			MatchType:     "reconstructed",
			WorkloadID:    workloadID,
			AgentRunID:    runID,
			Project:       project,
			GitBranch:     branch,
			Timestamp:     timestamp,
			Payload: mustJSON(map[string]interface{}{
				"context_ref_id": "a2actx:" + firstNonEmpty(task.ContextID, task.ID),
				"ref_type":       "a2a_task",
				"ref_hash":       hashRef("a2a:" + task.ID + ":" + task.ContextID),
				"label":          "A2A task " + task.ID,
				"privacy_label":  "local",
				"goal":           goal,
			}),
			Confidence: 0.85,
		})
		for _, artifact := range task.Artifacts {
			artifactID := firstNonEmpty(artifact.ID, deterministicLedgerID("art", "a2a-artifact:"+task.ID+":"+artifact.Name))
			events = append(events, storage.CanonicalEvent{
				EventID:       "a2a:artifact:" + task.ID + ":" + artifactID,
				Source:        source,
				EventType:     "artifact.created",
				SchemaVersion: "v1",
				SourceVersion: sourceVersion,
				ParserVersion: parserVersion,
				SourceEventID: artifactID,
				RawRef:        rawRef + ":artifact:" + artifactID,
				MatchType:     "source_reported",
				WorkloadID:    workloadID,
				AgentRunID:    runID,
				Project:       project,
				GitBranch:     branch,
				Timestamp:     timestamp,
				Payload: mustJSON(map[string]interface{}{
					"artifact_id":   artifactID,
					"artifact_type": "a2a_artifact",
					"label":         firstNonEmpty(artifact.Name, artifact.Description, artifactID),
					"sha256":        metadataString(artifact.Metadata, "sha256", "hash"),
					"metadata": map[string]interface{}{
						"description": artifact.Description,
						"part_count":  artifact.PartCount,
						"part_kinds":  artifact.PartKinds,
					},
				}),
				Confidence: 0.8,
			})
		}
		if terminalA2AState(state) {
			status := workloadStatusForA2A(state)
			events = append(events, storage.CanonicalEvent{
				EventID:       "a2a:run-finished:" + task.ID + ":" + state,
				Source:        source,
				EventType:     "agent.run.finished",
				SchemaVersion: "v1",
				SourceVersion: sourceVersion,
				ParserVersion: parserVersion,
				SourceEventID: task.ID,
				RawRef:        rawRef + ":status:" + state,
				MatchType:     "source_reported",
				WorkloadID:    workloadID,
				AgentRunID:    runID,
				Project:       project,
				GitBranch:     branch,
				Timestamp:     timestamp,
				Payload: mustJSON(map[string]interface{}{
					"run_id": runID,
					"status": status,
					"error":  errorForA2A(state),
				}),
				Confidence: 0.8,
			})
			events = append(events, storage.CanonicalEvent{
				EventID:       "a2a:workload-closed:" + task.ID + ":" + state,
				Source:        source,
				EventType:     "workload.closed",
				SchemaVersion: "v1",
				SourceVersion: sourceVersion,
				ParserVersion: parserVersion,
				SourceEventID: task.ID,
				RawRef:        rawRef + ":status:" + state,
				MatchType:     "source_reported",
				WorkloadID:    workloadID,
				Project:       project,
				GitBranch:     branch,
				Timestamp:     timestamp,
				Payload: mustJSON(map[string]interface{}{
					"status":  status,
					"outcome": "A2A task " + state,
				}),
				Confidence: 0.8,
			})
			events = append(events, storage.CanonicalEvent{
				EventID:       "a2a:evaluation:" + task.ID + ":" + state,
				Source:        source,
				EventType:     "evaluation.recorded",
				SchemaVersion: "v1",
				SourceVersion: sourceVersion,
				ParserVersion: parserVersion,
				SourceEventID: task.ID,
				RawRef:        rawRef + ":evaluation:" + state,
				MatchType:     "reconstructed",
				WorkloadID:    workloadID,
				AgentRunID:    runID,
				Project:       project,
				GitBranch:     branch,
				Timestamp:     timestamp,
				Payload: mustJSON(map[string]interface{}{
					"evaluation_id": "a2aeval:" + task.ID,
					"evaluator":     "a2a-status",
					"status":        evaluationStatusForA2A(state),
					"signal":        "a2a_task_state",
					"score":         scoreForA2A(state),
					"notes":         "metadata-only A2A task lifecycle signal",
				}),
				Confidence: 0.75,
			})
		}
	}
	return events, nil
}

func decodeA2AEntries(entries []json.RawMessage) ([]A2ATask, error) {
	out := make([]A2ATask, 0, len(entries))
	for _, entry := range entries {
		task, err := decodeA2AEntry(entry)
		if err != nil {
			return nil, err
		}
		out = append(out, task)
	}
	return out, nil
}

func decodeA2AEntry(raw json.RawMessage) (A2ATask, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return A2ATask{}, err
	}
	if result, ok := obj["result"]; ok {
		return decodeA2AResult(result)
	}
	if task, ok := obj["task"]; ok {
		return decodeA2AEntry(task)
	}
	if _, ok := obj["status"]; ok {
		return decodeA2ATaskObject(obj, "task")
	}
	if _, ok := obj["taskId"]; ok {
		return decodeA2AEventObject(obj)
	}
	if _, ok := obj["task_id"]; ok {
		return decodeA2AEventObject(obj)
	}
	return A2ATask{}, fmt.Errorf("unsupported A2A JSON shape")
}

func decodeA2AResult(raw json.RawMessage) (A2ATask, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return A2ATask{}, err
	}
	if task, ok := obj["task"]; ok {
		return decodeA2AEntry(task)
	}
	if _, ok := obj["status"]; ok {
		return decodeA2ATaskObject(obj, "jsonrpc_result")
	}
	return A2ATask{}, fmt.Errorf("A2A result does not contain a task")
}

func decodeA2ATaskObject(obj map[string]json.RawMessage, sourceType string) (A2ATask, error) {
	metadata, err := decodeMetadata(obj["metadata"])
	if err != nil {
		return A2ATask{}, err
	}
	status, err := decodeA2AStatus(obj["status"])
	if err != nil {
		return A2ATask{}, err
	}
	artifacts, err := decodeA2AArtifacts(obj["artifacts"])
	if err != nil {
		return A2ATask{}, err
	}
	return A2ATask{
		ID:         textField(obj, "id", "taskId", "task_id"),
		ContextID:  textField(obj, "contextId", "context_id"),
		Status:     status,
		Artifacts:  artifacts,
		Metadata:   metadata,
		Kind:       textField(obj, "kind"),
		SourceType: sourceType,
	}, nil
}

func decodeA2AEventObject(obj map[string]json.RawMessage) (A2ATask, error) {
	status, err := decodeA2AStatus(obj["status"])
	if err != nil {
		return A2ATask{}, err
	}
	metadata, err := decodeMetadata(obj["metadata"])
	if err != nil {
		return A2ATask{}, err
	}
	artifacts := []A2AArtifact{}
	if artifactRaw, ok := obj["artifact"]; ok {
		artifact, err := decodeA2AArtifact(artifactRaw)
		if err != nil {
			return A2ATask{}, err
		}
		artifacts = append(artifacts, artifact)
	}
	return A2ATask{
		ID:         textField(obj, "taskId", "task_id", "id"),
		ContextID:  textField(obj, "contextId", "context_id"),
		Status:     status,
		Artifacts:  artifacts,
		Metadata:   metadata,
		SourceType: "event",
	}, nil
}

func decodeA2AStatus(raw json.RawMessage) (A2AStatus, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return A2AStatus{State: "unknown"}, nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return A2AStatus{}, err
	}
	return A2AStatus{
		State:     textField(obj, "state"),
		Timestamp: timeField(obj, "timestamp"),
	}, nil
}

func decodeA2AArtifacts(raw json.RawMessage) ([]A2AArtifact, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return nil, nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	out := make([]A2AArtifact, 0, len(entries))
	for _, entry := range entries {
		artifact, err := decodeA2AArtifact(entry)
		if err != nil {
			return nil, err
		}
		out = append(out, artifact)
	}
	return out, nil
}

func decodeA2AArtifact(raw json.RawMessage) (A2AArtifact, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return A2AArtifact{}, err
	}
	metadata, err := decodeMetadata(obj["metadata"])
	if err != nil {
		return A2AArtifact{}, err
	}
	partKinds := []string{}
	if partsRaw, ok := obj["parts"]; ok {
		var parts []map[string]interface{}
		if err := json.Unmarshal(partsRaw, &parts); err == nil {
			for _, part := range parts {
				partKinds = append(partKinds, firstNonEmpty(fmt.Sprint(part["kind"]), fmt.Sprint(part["type"])))
			}
		}
	}
	return A2AArtifact{
		ID:          textField(obj, "artifactId", "artifact_id", "id"),
		Name:        textField(obj, "name"),
		Description: textField(obj, "description"),
		PartCount:   len(partKinds),
		PartKinds:   partKinds,
		Metadata:    metadata,
	}, nil
}

func decodeMetadata(raw json.RawMessage) (map[string]interface{}, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return map[string]interface{}{}, nil
	}
	var metadata map[string]interface{}
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return nil, err
	}
	return metadata, nil
}

func metadataString(metadata map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := metadata[key]; ok && value != nil {
			text := strings.TrimSpace(fmt.Sprint(value))
			if text != "" {
				return text
			}
		}
	}
	return ""
}

func normalizeA2AState(state string) string {
	normalized := strings.ToLower(strings.TrimSpace(state))
	if normalized == "" {
		return "unknown"
	}
	switch normalized {
	case "cancelled":
		return "canceled"
	default:
		return normalized
	}
}

func terminalA2AState(state string) bool {
	switch normalizeA2AState(state) {
	case "completed", "failed", "canceled", "rejected":
		return true
	default:
		return false
	}
}

func runningStatusForA2A(state string) string {
	if terminalA2AState(state) {
		return "completed"
	}
	return "running"
}

func workloadStatusForA2A(state string) string {
	switch normalizeA2AState(state) {
	case "completed":
		return "completed"
	case "failed", "rejected":
		return "failed"
	case "canceled":
		return "abandoned"
	default:
		return "partial"
	}
}

func evaluationStatusForA2A(state string) string {
	if normalizeA2AState(state) == "completed" {
		return "pass"
	}
	return "fail"
}

func scoreForA2A(state string) float64 {
	if normalizeA2AState(state) == "completed" {
		return 1
	}
	return 0
}

func errorForA2A(state string) string {
	switch normalizeA2AState(state) {
	case "failed", "rejected", "canceled":
		return "a2a task " + normalizeA2AState(state)
	default:
		return ""
	}
}

func mustJSON(v interface{}) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}
