package collector

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zhenzhis/agent-ledger/internal/storage"
)

// CodexCollector scans Codex CLI session JSONL files and extracts usage records.
type CodexCollector struct {
	db    *storage.DB
	paths []string
}

// NewCodexCollector creates a CodexCollector that scans the given base paths.
func NewCodexCollector(db *storage.DB, paths []string) *CodexCollector {
	return &CodexCollector{db: db, paths: paths}
}

type codexEntry struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexSessionMeta struct {
	ID         string `json:"id"`
	CWD        string `json:"cwd"`
	CLIVersion string `json:"cli_version"`
}

type codexTurnContext struct {
	Model string `json:"model"`
}

type codexEventPayload struct {
	Type   string          `json:"type"`
	TurnID string          `json:"turn_id"`
	Info   *codexTokenInfo `json:"info"`
}

type codexTokenInfo struct {
	LastTokenUsage *codexTokenUsage `json:"last_token_usage"`
}

type codexTokenUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
}

type codexResponseItem struct {
	Role string `json:"role"`
	Type string `json:"type"`
}

type codexSQLiteThread struct {
	ID           string
	CWD          string
	GitBranch    string
	CLIVersion   string
	Model        string
	TokensUsed   int64
	HasUserEvent bool
	CreatedAtMS  int64
	UpdatedAtMS  int64
}

// Scan walks all configured paths and processes new JSONL data from Codex CLI sessions.
func (c *CodexCollector) Scan() error {
	seenStateDBs := map[string]struct{}{}
	for _, basePath := range c.paths {
		info, err := os.Stat(basePath)
		if err != nil {
			log.Printf("codex: cannot read %s: %v", basePath, err)
			continue
		}
		if !info.IsDir() && strings.EqualFold(filepath.Ext(basePath), ".sqlite") {
			c.scanStateDB(basePath, seenStateDBs)
			continue
		}
		err = filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				return nil
			}
			switch strings.ToLower(filepath.Ext(path)) {
			case ".jsonl":
				if err := c.processFile(path); err != nil {
					log.Printf("codex: error processing %s: %v", path, err)
				}
			case ".sqlite":
				c.scanStateDB(path, seenStateDBs)
			}
			return nil
		})
		if err != nil {
			log.Printf("codex: cannot walk %s: %v", basePath, err)
		}
		for _, candidate := range codexStateDBCandidates(basePath) {
			c.scanStateDB(candidate, seenStateDBs)
		}
	}
	return nil
}

func (c *CodexCollector) scanStateDB(path string, seen map[string]struct{}) {
	clean := filepath.Clean(path)
	if _, ok := seen[clean]; ok {
		return
	}
	seen[clean] = struct{}{}
	if err := c.processStateSQLite(clean); err != nil {
		log.Printf("codex: error processing sqlite state %s: %v", clean, err)
	}
}

func codexStateDBCandidates(basePath string) []string {
	var roots []string
	if info, err := os.Stat(basePath); err == nil && info.IsDir() {
		roots = append(roots, basePath)
		if strings.EqualFold(filepath.Base(basePath), "sessions") {
			roots = append(roots, filepath.Dir(basePath))
		}
	}
	var out []string
	seen := map[string]struct{}{}
	for _, root := range roots {
		matches, _ := filepath.Glob(filepath.Join(root, "state_*.sqlite"))
		for _, match := range matches {
			clean := filepath.Clean(match)
			if _, ok := seen[clean]; ok {
				continue
			}
			seen[clean] = struct{}{}
			out = append(out, clean)
		}
	}
	sort.Strings(out)
	return out
}

func (c *CodexCollector) processFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	_, lastOffset, scanCtx, err := c.db.GetFileState(path)
	if err != nil {
		return err
	}
	if info.Size() <= lastOffset {
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if lastOffset > 0 {
		if _, err := f.Seek(lastOffset, io.SeekStart); err != nil {
			return err
		}
	}

	var sessionID, cwd, version, model string
	if lastOffset > 0 && scanCtx != nil {
		sessionID = scanCtx.SessionID
		cwd = scanCtx.CWD
		version = scanCtx.Version
		model = scanCtx.Model
	}
	var records []*storage.UsageRecord
	var promptEvents []*storage.PromptEvent
	var prompts int
	var firstTime time.Time

	scanner := newJSONLScanner(f)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry codexEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}

		ts, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
		if firstTime.IsZero() && !ts.IsZero() {
			firstTime = ts
		}

		switch entry.Type {
		case "session_meta":
			var meta codexSessionMeta
			if err := json.Unmarshal(entry.Payload, &meta); err != nil {
				continue
			}
			sessionID = meta.ID
			cwd = meta.CWD
			version = meta.CLIVersion

		case "turn_context":
			var tc codexTurnContext
			if err := json.Unmarshal(entry.Payload, &tc); err != nil {
				continue
			}
			if tc.Model != "" {
				model = tc.Model
			}

		case "response_item":
			var ri codexResponseItem
			if err := json.Unmarshal(entry.Payload, &ri); err != nil {
				continue
			}
			if ri.Role == "user" && ri.Type != "function_call_output" {
				prompts++
				promptEvents = append(promptEvents, &storage.PromptEvent{
					Source: "codex", SessionID: sessionID, Model: model, Project: filepath.Base(cwd), Timestamp: ts,
				})
			}

		case "event_msg":
			var ep codexEventPayload
			if err := json.Unmarshal(entry.Payload, &ep); err != nil {
				continue
			}
			if ep.Type == "token_count" && ep.Info != nil && ep.Info.LastTokenUsage != nil {
				u := ep.Info.LastTokenUsage
				rec := &storage.UsageRecord{
					Source:                "codex",
					SessionID:             sessionID,
					Model:                 model,
					InputTokens:           u.InputTokens - u.CachedInputTokens,
					OutputTokens:          u.OutputTokens,
					CacheReadInputTokens:  u.CachedInputTokens,
					ReasoningOutputTokens: u.ReasoningOutputTokens,
					Timestamp:             ts,
					Project:               filepath.Base(cwd),
				}
				records = append(records, rec)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan codex jsonl %s: %w", path, err)
	}

	if sessionID == "" {
		base := filepath.Base(path)
		sessionID = base[:len(base)-len(filepath.Ext(base))]
	}

	for _, r := range records {
		if r.SessionID == "" {
			r.SessionID = sessionID
		}
	}
	for _, e := range promptEvents {
		if e.SessionID == "" {
			e.SessionID = sessionID
		}
	}

	if len(records) > 0 {
		if err := c.db.InsertUsageBatch(records); err != nil {
			return fmt.Errorf("insert codex usage: %w", err)
		}
	}

	if len(promptEvents) > 0 {
		if err := c.db.InsertPromptBatch(promptEvents); err != nil {
			return fmt.Errorf("insert codex prompts: %w", err)
		}
	}

	if prompts > 0 || len(records) > 0 {
		sess := &storage.SessionRecord{
			Source:    "codex",
			SessionID: sessionID,
			CWD:       cwd,
			Version:   version,
			StartTime: firstTime,
			Prompts:   prompts,
		}
		if err := c.db.UpsertSession(sess); err != nil {
			return fmt.Errorf("upsert codex session: %w", err)
		}
	}

	return c.db.SetFileState(path, info.Size(), info.Size(), &storage.FileScanContext{
		SessionID: sessionID,
		CWD:       cwd,
		Version:   version,
		Model:     model,
	})
}

func (c *CodexCollector) processStateSQLite(dbPath string) error {
	info, err := os.Stat(dbPath)
	if err != nil {
		return err
	}
	_, lastWatermark, scanCtx, err := c.db.GetFileState(dbPath)
	if err != nil {
		return err
	}
	threadTokens := map[string]int64{}
	if scanCtx != nil && scanCtx.ThreadTokens != nil {
		for id, tokens := range scanCtx.ThreadTokens {
			threadTokens[id] = tokens
		}
	}

	src, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=busy_timeout(3000)")
	if err != nil {
		return err
	}
	defer src.Close()

	var tableName string
	if err := src.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='threads'`).Scan(&tableName); err == sql.ErrNoRows {
		return nil
	} else if err != nil {
		return err
	}

	rows, err := src.Query(`
		SELECT id,
			COALESCE(cwd,''),
			COALESCE(git_branch,''),
			COALESCE(cli_version,''),
			COALESCE(model,''),
			COALESCE(tokens_used,0),
			COALESCE(has_user_event,0),
			CASE WHEN COALESCE(created_at_ms,0) > 0 THEN created_at_ms ELSE COALESCE(created_at,0) * 1000 END AS created_ms,
			CASE WHEN COALESCE(updated_at_ms,0) > 0 THEN updated_at_ms ELSE COALESCE(updated_at,0) * 1000 END AS updated_ms
		FROM threads
		WHERE (CASE WHEN COALESCE(updated_at_ms,0) > 0 THEN updated_at_ms ELSE COALESCE(updated_at,0) * 1000 END) > ?
		ORDER BY updated_ms`, lastWatermark)
	if err != nil {
		return fmt.Errorf("query codex threads: %w", err)
	}
	defer rows.Close()

	var records []*storage.UsageRecord
	var maxWatermark int64 = lastWatermark
	for rows.Next() {
		var row codexSQLiteThread
		var hasUserEvent int
		if err := rows.Scan(&row.ID, &row.CWD, &row.GitBranch, &row.CLIVersion, &row.Model, &row.TokensUsed, &hasUserEvent, &row.CreatedAtMS, &row.UpdatedAtMS); err != nil {
			return err
		}
		if row.ID == "" {
			continue
		}
		row.HasUserEvent = hasUserEvent != 0
		if row.UpdatedAtMS > maxWatermark {
			maxWatermark = row.UpdatedAtMS
		}
		project := ""
		if row.CWD != "" {
			project = filepath.Base(row.CWD)
		}
		prompts := 0
		if row.HasUserEvent {
			prompts = 1
		}
		if err := c.db.UpsertSession(&storage.SessionRecord{
			Source:    "codex",
			SessionID: row.ID,
			Project:   project,
			CWD:       row.CWD,
			Version:   row.CLIVersion,
			GitBranch: row.GitBranch,
			StartTime: msToTime(row.CreatedAtMS),
			Prompts:   prompts,
		}); err != nil {
			return fmt.Errorf("upsert codex sqlite session: %w", err)
		}

		previousTokens := threadTokens[row.ID]
		if previousTokens < 0 {
			previousTokens = 0
		}
		threadTokens[row.ID] = row.TokensUsed
		if row.TokensUsed <= previousTokens {
			continue
		}
		hasPreciseUsage, err := c.db.HasNonEstimatedUsage("codex", row.ID)
		if err != nil {
			return err
		}
		if hasPreciseUsage {
			continue
		}
		model := row.Model
		if strings.TrimSpace(model) == "" {
			model = "unknown"
		}
		records = append(records, &storage.UsageRecord{
			Source:            "codex",
			SessionID:         row.ID,
			Model:             model,
			InputTokens:       row.TokensUsed - previousTokens,
			Timestamp:         msToTime(row.UpdatedAtMS),
			Project:           project,
			GitBranch:         row.GitBranch,
			PricingConfidence: "estimated-aggregate",
			PricingNote:       "codex sqlite thread tokens_used aggregate; input/output/cache split and cost are unavailable",
		})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(records) > 0 {
		if err := c.db.InsertUsageBatch(records); err != nil {
			return fmt.Errorf("insert codex sqlite usage: %w", err)
		}
	}
	return c.db.SetFileState(dbPath, info.Size(), maxWatermark, &storage.FileScanContext{
		ThreadTokens: threadTokens,
	})
}

func msToTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}
