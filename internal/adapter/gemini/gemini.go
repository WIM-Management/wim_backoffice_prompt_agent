package gemini

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/model"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/state"
)

// Adapter implements model.Adapter for Google Gemini CLI (~/.gemini).
type Adapter struct {
	home string

	mu        sync.Mutex
	winnerMap map[string]string // sessionId -> winning file path (lazy, built once per instance)
}

func New(home string) *Adapter { return &Adapter{home: home} }
func (a *Adapter) Name() string { return "GEMINI" }

// SessionPaths returns the union of all Gemini session file globs.
// Formats supported:
//
//	A. Monolithic JSON:   <home>/.gemini/tmp/*/chats/session-*.json
//	B. Journal JSONL:     <home>/.gemini/tmp/*/chats/session-*.jsonl
//	C. Nested (uuid):     <home>/.gemini/tmp/*/chats/*/*.jsonl
//	D. Nested JSON:       <home>/.gemini/tmp/*/chats/*/*.json
func (a *Adapter) SessionPaths() ([]string, error) {
	base := filepath.Join(a.home, ".gemini", "tmp")
	globs := []string{
		filepath.Join(base, "*", "chats", "session-*.json"),
		filepath.Join(base, "*", "chats", "session-*.jsonl"),
		filepath.Join(base, "*", "chats", "*", "*.jsonl"),
		filepath.Join(base, "*", "chats", "*", "*.json"),
	}

	seen := make(map[string]struct{})
	var result []string
	for _, g := range globs {
		matches, err := filepath.Glob(g)
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			abs, err := filepath.Abs(m)
			if err != nil {
				abs = m
			}
			if _, ok := seen[abs]; !ok {
				seen[abs] = struct{}{}
				result = append(result, abs)
			}
		}
	}
	return result, nil
}

// pathPriority returns the priority rank of a file path for cross-format dedup.
// Lower number = higher priority (wins over higher number).
//
//	0 = nested (chats/<uuid>/)
//	1 = monolithic .json  (chats/session-*.json)
//	2 = journal .jsonl    (chats/session-*.jsonl)
func pathPriority(p string) int {
	dir := filepath.Dir(p)
	base := filepath.Base(p)
	parentBase := filepath.Base(dir)

	// Nested: parent dir is neither "chats" itself, and the segment before the
	// filename is not prefixed with "session-" (it's a uuid/short-id dir).
	// Pattern: .../chats/<something>/<file> where <something> != "session-*"
	// We detect "nested" when the immediate parent is NOT "chats".
	if parentBase != "chats" {
		return 0 // nested — highest priority
	}
	// Direct children of "chats/"
	if strings.HasSuffix(base, ".json") {
		return 1 // monolithic
	}
	return 2 // journal .jsonl
}

// monolithicSession is the top-level structure of a monolithic .json file.
type monolithicSession struct {
	SessionID string             `json:"sessionId"`
	Messages  []monolithicMsg    `json:"messages"`
}

type monolithicMsg struct {
	ID        int64           `json:"id"`
	Timestamp int64           `json:"timestamp"` // milliseconds epoch
	Type      string          `json:"type"`      // "user", "gemini", "info"
	Content   json.RawMessage `json:"content"`   // string OR [{text:...}]
	Model     string          `json:"model,omitempty"`
}

// journalHeader is line 1 of a .jsonl file.
type journalHeader struct {
	SessionID string `json:"sessionId"`
}

// journalLine covers both message lines and $set lines.
type journalLine struct {
	Set       *json.RawMessage `json:"$set,omitempty"`
	ID        int64            `json:"id,omitempty"`
	Timestamp int64            `json:"timestamp,omitempty"`
	Type      string           `json:"type,omitempty"`
	Content   json.RawMessage  `json:"content,omitempty"`
	Model     string           `json:"model,omitempty"`
}

// sessionIDFromFile reads just enough of a file to extract its sessionId.
func sessionIDFromFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	if strings.HasSuffix(path, ".json") {
		// Monolithic: decode full JSON to get sessionId field.
		var s monolithicSession
		if json.NewDecoder(f).Decode(&s) == nil {
			return s.SessionID
		}
		return ""
	}

	// JSONL: sessionId is in line 1 (header).
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	if sc.Scan() {
		var h journalHeader
		if json.Unmarshal(sc.Bytes(), &h) == nil {
			return h.SessionID
		}
	}
	return ""
}

// buildWinnerMap scans all session paths and builds a sessionId -> file map,
// where the file with the lowest pathPriority wins.
// Must be called with a.mu held or during lazy init protected by mu.
func (a *Adapter) buildWinnerMap() {
	paths, err := a.SessionPaths()
	if err != nil || len(paths) == 0 {
		a.winnerMap = make(map[string]string)
		return
	}

	// Sort by priority ascending so we process highest-priority first.
	sort.Slice(paths, func(i, j int) bool {
		pi, pj := pathPriority(paths[i]), pathPriority(paths[j])
		if pi != pj {
			return pi < pj
		}
		return paths[i] < paths[j]
	})

	m := make(map[string]string)
	for _, p := range paths {
		sid := sessionIDFromFile(p)
		if sid == "" {
			continue
		}
		if _, exists := m[sid]; !exists {
			m[sid] = p // first (highest priority) wins
		}
	}
	a.winnerMap = m
}

// ensureWinnerMap lazily builds the winner map once per Adapter instance.
func (a *Adapter) ensureWinnerMap() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.winnerMap == nil {
		a.buildWinnerMap()
	}
}

// Parse implements model.Adapter. For non-winner files (cross-format dedup),
// returns zero events but a valid cursor.
func (a *Adapter) Parse(file string, cursor []byte, idleCutoff time.Time) ([]model.Event, []byte, error) {
	a.ensureWinnerMap()

	// Determine the sessionId for this file.
	sid := sessionIDFromFile(file)

	// Check if this file is the winner for its sessionId.
	a.mu.Lock()
	winner, hasSID := a.winnerMap[sid]
	a.mu.Unlock()

	fi, err := os.Stat(file)
	if err != nil {
		return nil, cursor, err
	}
	newCursor := state.EncodeByteCursor(fi.Size())

	if sid == "" || !hasSID || winner != file {
		// Non-winner: emit nothing.
		return nil, newCursor, nil
	}

	// Parse the file.
	if strings.HasSuffix(file, ".json") {
		return a.parseMonolithic(file, sid, newCursor)
	}
	return a.parseJournal(file, sid, cursor, newCursor)
}

func (a *Adapter) parseMonolithic(file, sid string, newCursor []byte) ([]model.Event, []byte, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, newCursor, err
	}

	var s monolithicSession
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, newCursor, err
	}

	var events []model.Event
	for _, msg := range s.Messages {
		if msg.Type != "user" {
			continue
		}
		text := extractContent(msg.Content)
		if text == "" {
			continue
		}
		events = append(events, model.Event{
			SourceTool: "GEMINI",
			Surface:    "cli",
			SessionID:  sid,
			PromptText: text,
			PromptTs:   msToNaiveTS(msg.Timestamp),
		})
	}
	return events, newCursor, nil
}

func (a *Adapter) parseJournal(file, sid string, cursor, newCursor []byte) ([]model.Event, []byte, error) {
	fromOffset := state.DecodeByteCursor(cursor, 0)

	f, err := os.Open(file)
	if err != nil {
		return nil, newCursor, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)

	// Always skip header line (line 1) regardless of cursor.
	// We track byte offset to respect cursor.
	var offset int64
	lineNum := 0

	var events []model.Event
	for sc.Scan() {
		line := sc.Bytes()
		lineLen := int64(len(line)) + 1 // +1 for newline

		lineNum++
		if lineNum == 1 {
			// Header line — skip.
			offset += lineLen
			continue
		}

		lineStart := offset
		offset += lineLen

		if lineStart < fromOffset {
			// Already processed in a prior scan.
			continue
		}

		// Skip $set lines.
		if bytes.Contains(line, []byte(`"$set"`)) {
			continue
		}

		var jl journalLine
		if json.Unmarshal(line, &jl) != nil {
			continue
		}
		if jl.Type != "user" {
			continue
		}
		text := extractContent(jl.Content)
		if text == "" {
			continue
		}
		events = append(events, model.Event{
			SourceTool: "GEMINI",
			Surface:    "cli",
			SessionID:  sid,
			PromptText: text,
			PromptTs:   msToNaiveTS(jl.Timestamp),
		})
	}
	return events, newCursor, nil
}

// extractContent converts a Gemini content field (string or [{text:...}]) to plain text.
func extractContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ""
	}

	// Try string first.
	if trimmed[0] == '"' {
		var s string
		if json.Unmarshal(trimmed, &s) == nil {
			return s
		}
	}

	// Try [{text:...}] array.
	if trimmed[0] == '[' {
		var parts []struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(trimmed, &parts) == nil {
			var sb strings.Builder
			for _, p := range parts {
				sb.WriteString(p.Text)
			}
			return sb.String()
		}
	}

	return ""
}

// msToNaiveTS converts a millisecond epoch timestamp to model.NaiveTS.
func msToNaiveTS(ms int64) model.NaiveTS {
	if ms == 0 {
		return model.NaiveTS(time.Time{})
	}
	return model.NaiveTS(time.UnixMilli(ms).UTC())
}
