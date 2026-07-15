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
//
// SCAN-LIFETIME CONTRACT: one *Adapter instance corresponds to exactly one scan.
// The cross-format winner map (sessionId -> authoritative file) is built lazily
// once per instance and never invalidated, so a single instance MUST NOT be
// reused across scan cycles — a session file created between scans would be
// invisible to a stale map. The runtime honors this: the binary is `run-once`
// (one scan per process, then exit; the daemon re-execs each interval) and the
// wiring calls gemini.New(home) fresh inside every runOnce pass. Do not cache a
// gemini.Adapter across scans without adding per-scan map invalidation.
// (Resetting the map inside Parse would be wrong: Parse is called once per file
// within a scan and all files of one scan must share the same winner map.)
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

	if parentBase != "chats" {
		return 0 // nested — highest priority
	}
	if strings.HasSuffix(base, ".json") {
		return 1 // monolithic
	}
	return 2 // journal .jsonl
}

// monolithicSession is the top-level structure of a monolithic .json file.
type monolithicSession struct {
	SessionID   string          `json:"sessionId"`
	Directories []string        `json:"directories,omitempty"`
	Messages    []monolithicMsg `json:"messages"`
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
	SessionID   string   `json:"sessionId"`
	Directories []string `json:"directories,omitempty"`
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

// normalizedMsg is a unified message representation used by the pairing logic.
type normalizedMsg struct {
	Type      string
	Content   json.RawMessage
	Model     string
	Timestamp int64
}

// offsetMsg wraps normalizedMsg with the byte offset of the line in a journal file.
type offsetMsg struct {
	normalizedMsg
	lineOffset int64
}

// sessionIDFromFile reads just enough of a file to extract its sessionId.
func sessionIDFromFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	if strings.HasSuffix(path, ".json") {
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
func (a *Adapter) buildWinnerMap() {
	paths, err := a.SessionPaths()
	if err != nil || len(paths) == 0 {
		a.winnerMap = make(map[string]string)
		return
	}

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

	sid := sessionIDFromFile(file)

	a.mu.Lock()
	winner, hasSID := a.winnerMap[sid]
	a.mu.Unlock()

	fi, err := os.Stat(file)
	if err != nil {
		return nil, cursor, err
	}
	newCursor := state.EncodeByteCursor(fi.Size())

	if sid == "" || !hasSID || winner != file {
		return nil, newCursor, nil
	}

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

	msgs := make([]normalizedMsg, len(s.Messages))
	for i, m := range s.Messages {
		msgs[i] = normalizedMsg{
			Type:      m.Type,
			Content:   m.Content,
			Model:     m.Model,
			Timestamp: m.Timestamp,
		}
	}

	cwd := a.resolveCwd(file, s.Directories)
	events := pairMessages(msgs, sid, cwd)
	return events, newCursor, nil
}

// parseJournal parses a JSONL journal file with byte-cursor support.
// All messages (user + gemini) are collected with their byte offsets so that
// pairing is correct even when only a tail of the file is new.
func (a *Adapter) parseJournal(file, sid string, cursor, newCursor []byte) ([]model.Event, []byte, error) {
	fromOffset := state.DecodeByteCursor(cursor, 0)

	f, err := os.Open(file)
	if err != nil {
		return nil, newCursor, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)

	var offset int64
	lineNum := 0
	var dirs []string
	var msgs []offsetMsg

	for sc.Scan() {
		line := sc.Bytes()
		lineLen := int64(len(line)) + 1 // +1 for newline

		lineNum++
		if lineNum == 1 {
			// Header line — extract directories if present.
			var h journalHeader
			if json.Unmarshal(line, &h) == nil {
				dirs = h.Directories
			}
			offset += lineLen
			continue
		}

		lineStart := offset
		offset += lineLen

		// $set journal-update lines carry no type:"user"/"gemini", so the type
		// filter below skips them structurally. We do NOT byte-match "$set" to
		// avoid false-positives on user prompts that quote the literal "$set".
		var jl journalLine
		if json.Unmarshal(line, &jl) != nil {
			continue
		}
		if jl.Type != "user" && jl.Type != "gemini" {
			continue
		}

		msgs = append(msgs, offsetMsg{
			normalizedMsg: normalizedMsg{
				Type:      jl.Type,
				Content:   jl.Content,
				Model:     jl.Model,
				Timestamp: jl.Timestamp,
			},
			lineOffset: lineStart,
		})
	}

	cwd := a.resolveCwd(file, dirs)

	var events []model.Event
	for i := 0; i < len(msgs); i++ {
		om := msgs[i]
		if om.Type != "user" {
			continue
		}

		text := extractContent(om.Content)
		if shouldSkipUserText(text) {
			continue
		}

		// Cursor: only emit events whose user-message line is new.
		if om.lineOffset < fromOffset {
			continue
		}

		responseText, responseModel := collectGeminiResponsesOffset(msgs[i+1:])

		events = append(events, model.Event{
			SourceTool:     "GEMINI",
			Surface:        "cli",
			SessionID:      sid,
			PromptText:     text,
			ResponseText:   responseText,
			Model:          responseModel,
			PromptTs:       msToNaiveTS(om.Timestamp),
			ProjectContext: cwd,
		})
	}

	return events, newCursor, nil
}

// pairMessages walks a normalized message list and pairs user prompts with
// following gemini responses. Used by parseMonolithic.
func pairMessages(msgs []normalizedMsg, sid, cwd string) []model.Event {
	var events []model.Event
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		if m.Type != "user" {
			continue
		}

		text := extractContent(m.Content)
		if shouldSkipUserText(text) {
			continue
		}

		responseText, responseModel := collectGeminiResponses(msgs[i+1:])

		events = append(events, model.Event{
			SourceTool:     "GEMINI",
			Surface:        "cli",
			SessionID:      sid,
			PromptText:     text,
			ResponseText:   responseText,
			Model:          responseModel,
			PromptTs:       msToNaiveTS(m.Timestamp),
			ProjectContext: cwd,
		})
	}
	return events
}

// shouldSkipUserText returns true for messages that are injection artifacts
// and should not be emitted as prompt events.
//
// Structural filter (primary): empty text and slash commands are reliably
// identifiable by structure. The "System:" prefix is a best-effort supplement
// to catch residual injection leaks that arrive as type=="user" messages; it
// is NOT a general English-phrase blocklist.
func shouldSkipUserText(text string) bool {
	// Trim first so leading whitespace can't smuggle a slash command / empty text past the filter.
	text = strings.TrimSpace(text)
	if text == "" {
		return true
	}
	if strings.HasPrefix(text, "/") {
		return true
	}
	// Best-effort supplement: catches residual System:-prefixed injection leaks.
	if strings.HasPrefix(text, "System:") {
		return true
	}
	return false
}

// collectGeminiResponses collects consecutive gemini messages from a
// []normalizedMsg slice (stopping at the next user message) and returns
// joined response text and the first non-empty model string.
func collectGeminiResponses(msgs []normalizedMsg) (text, firstModel string) {
	var sb strings.Builder
	for _, m := range msgs {
		if m.Type == "user" {
			break
		}
		if m.Type != "gemini" {
			continue
		}
		sb.WriteString(extractContent(m.Content))
		if firstModel == "" && m.Model != "" {
			firstModel = m.Model
		}
	}
	return sb.String(), firstModel
}

// collectGeminiResponsesOffset is the offsetMsg variant used by parseJournal.
func collectGeminiResponsesOffset(msgs []offsetMsg) (text, firstModel string) {
	var sb strings.Builder
	for _, m := range msgs {
		if m.Type == "user" {
			break
		}
		if m.Type != "gemini" {
			continue
		}
		sb.WriteString(extractContent(m.Content))
		if firstModel == "" && m.Model != "" {
			firstModel = m.Model
		}
	}
	return sb.String(), firstModel
}

// resolveCwd determines the working directory for a session file.
//
// Resolution order:
//  1. directories[0] from the session file (most authoritative).
//  2. Read <home>/.gemini/tmp/<projectDir>/.project_root (verified present in real data).
//  3. <home>/.gemini/projects.json reverse-map: {"projects":{"<abs path>":"<name>"}}
//     → abs path for name==projectDir (verified location: projects.json lives at the
//     .gemini root, NOT inside tmp/<projectDir>/; name equals the tmp dir basename).
//  4. <projectDir> basename as-is (approximate fallback).
func (a *Adapter) resolveCwd(sessionFilePath string, directories []string) string {
	// (a) directories array.
	if len(directories) > 0 && directories[0] != "" {
		return directories[0]
	}

	// Extract <projectDir> from path: .../tmp/<projectDir>/chats/...
	tmpBase := filepath.Join(a.home, ".gemini", "tmp")
	rel, err := filepath.Rel(tmpBase, sessionFilePath)
	if err != nil {
		return ""
	}
	parts := strings.SplitN(rel, string(filepath.Separator), 2)
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	projectDir := parts[0]
	tmpProjDir := filepath.Join(tmpBase, projectDir)

	// (b) .project_root file — primary real-world source.
	if data, err := os.ReadFile(filepath.Join(tmpProjDir, ".project_root")); err == nil {
		if p := strings.TrimSpace(string(data)); p != "" {
			return p
		}
	}

	// (c) projects.json reverse-map — lives at the .gemini root (verified), not tmp/<projectDir>/.
	if data, err := os.ReadFile(filepath.Join(a.home, ".gemini", "projects.json")); err == nil {
		var pj struct {
			Projects map[string]string `json:"projects"`
		}
		if json.Unmarshal(data, &pj) == nil {
			var found string
			collision := false
			for absPath, name := range pj.Projects {
				if name == projectDir {
					if found == "" {
						found = absPath
					} else {
						collision = true
						break
					}
				}
			}
			if !collision && found != "" {
				return found
			}
		}
	}

	// (d) basename fallback.
	return projectDir
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
