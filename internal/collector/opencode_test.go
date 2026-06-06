package collector

import (
	"database/sql"
	"encoding/json"
	"math"
	"path/filepath"
	"testing"

	"github.com/briqt/agent-usage/internal/storage"
)

func TestOpenCodeCollectorUsesSourceCost(t *testing.T) {
	dir := t.TempDir()
	appDB, err := storage.Open(filepath.Join(dir, "agent-usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer appDB.Close()

	sourcePath := filepath.Join(dir, "opencode.db")
	src, err := sql.Open("sqlite", sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()

	_, err = src.Exec(`
		CREATE TABLE session (id TEXT PRIMARY KEY, directory TEXT);
		CREATE TABLE message (data TEXT, session_id TEXT, time_created INTEGER);
		INSERT INTO session (id, directory) VALUES ('sess-1', '/work/project');
	`)
	if err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(map[string]interface{}{
		"role":       "assistant",
		"modelID":    "custom-opencode-model",
		"providerID": "custom",
		"cost":       0.1234,
		"tokens": map[string]interface{}{
			"input":  100,
			"output": 50,
			"cache": map[string]interface{}{
				"read":  20,
				"write": 5,
			},
		},
		"time": map[string]interface{}{
			"created": int64(1780736000000),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.Exec(`INSERT INTO message (data, session_id, time_created) VALUES (?, 'sess-1', 1780736000000)`, string(data)); err != nil {
		t.Fatal(err)
	}

	c := NewOpenCodeCollector(appDB, []string{sourcePath})
	if err := c.Scan(); err != nil {
		t.Fatal(err)
	}

	details, err := appDB.GetSessionDetail("sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 1 {
		t.Fatalf("expected one session detail row, got %d", len(details))
	}
	if math.Abs(details[0].CostUSD-0.1234) > 0.0000001 {
		t.Fatalf("expected source cost 0.1234, got %f", details[0].CostUSD)
	}
}
