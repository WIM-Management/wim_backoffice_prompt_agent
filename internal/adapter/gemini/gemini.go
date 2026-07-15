package gemini

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/model"
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

// emittedSetCap is the maximum number of identities retained in the emitted set.
// When exceeded, the oldest entries (insertion order) are dropped. Dropping the
// oldest may cause a re-emission on the next scan, but the server dedup
// (content_hash unique key) absorbs the duplicate safely — it is mildly wasteful
// but not incorrect.
const emittedSetCap = 2000

// perScanEmitCap is the maximum number of new events emitted in a single Parse
// call. If more new events exist than this cap, the first perScanEmitCap are
// emitted and the remainder are held for the next scan. When truncating, the
// returned cursor's mtime/size are reset to zero (sentinel) so the fast-path
// does not skip the file next scan and the remaining events can be emitted.
const perScanEmitCap = 500

// geminiCursor is the JSON cursor persisted between scans for each file.
// Emitted is an ordered slice of 4-tuple identity strings used as a set.
// Ordering is insertion order (oldest first) so the cap drops the oldest entries.
type geminiCursor struct {
	MtimeNano int64    `json:"mtimeNano"`
	Size      int64    `json:"size"`
	Emitted   []string `json:"emitted"`
}

// promptIdentity returns the canonical 4-tuple string for an event:
// "GEMINI|<sessionId>|<promptText>|<promptTs>" where promptTs uses the same
// second-resolution format as NaiveTS.MarshalJSON.
func promptIdentity(ev model.Event) string {
	ts := time.Time(ev.PromptTs).UTC().Format("2006-01-02T15:04:05")
	return ev.SourceTool + "|" + ev.SessionID + "|" + ev.PromptText + "|" + ts
}

// decodeCursor deserialises a cursor. Nil/empty input returns a zero-value cursor.
func decodeCursor(raw []byte) geminiCursor {
	if len(raw) == 0 {
		return geminiCursor{}
	}
	var c geminiCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return geminiCursor{}
	}
	return c
}

// encodeCursor serialises a cursor. Errors are silently swallowed; callers
// receive nil which is treated as an empty cursor on the next decode.
func encodeCursor(c geminiCursor) []byte {
	b, _ := json.Marshal(c)
	return b
}

// gateEmissions filters events to only those whose identity is not already in
// the emitted set of cur, adds new identities, applies the emittedSetCap, and
// applies the perScanEmitCap. It returns the filtered events and an updated
// cursor ready to persist.
//
// mtime/size in the returned cursor are always set to the values provided in
// statMtime/statSize, EXCEPT when perScanEmitCap truncation occurs: in that
// case they are reset to zero so the next scan re-parses (the fast-path checks
// size && mtime; sentinel 0 guarantees a mismatch against any real stat).
func gateEmissions(events []model.Event, cur geminiCursor, statMtime, statSize int64) ([]model.Event, geminiCursor) {
	// Build a fast-lookup presence map from the ordered slice.
	present := make(map[string]struct{}, len(cur.Emitted))
	for _, id := range cur.Emitted {
		present[id] = struct{}{}
	}

	emitted := make([]string, len(cur.Emitted))
	copy(emitted, cur.Emitted)

	var out []model.Event
	for _, ev := range events {
		id := promptIdentity(ev)
		if _, seen := present[id]; seen {
			continue
		}
		out = append(out, ev)
	}

	truncated := len(out) > perScanEmitCap
	if truncated {
		fmt.Fprintf(os.Stderr,
			"gemini: perScanEmitCap (%d) exceeded for session %q — emitting first %d, %d deferred to next scan\n",
			perScanEmitCap, func() string {
				if len(out) > 0 {
					return out[0].SessionID
				}
				return "(unknown)"
			}(), perScanEmitCap, len(out)-perScanEmitCap)
		out = out[:perScanEmitCap]
	}

	// Add only the emitted identities to the set.
	for _, ev := range out {
		id := promptIdentity(ev)
		if _, seen := present[id]; !seen {
			emitted = append(emitted, id)
			present[id] = struct{}{}
		}
	}

	// Apply emittedSetCap: drop oldest entries when over cap.
	if len(emitted) > emittedSetCap {
		drop := len(emitted) - emittedSetCap
		emitted = emitted[drop:]
	}

	newCur := geminiCursor{
		MtimeNano: statMtime,
		Size:      statSize,
		Emitted:   emitted,
	}

	// When we truncated, reset mtime/size to zero so the fast-path does not
	// skip the file next scan (sentinel 0 never matches a real stat value).
	if truncated {
		newCur.MtimeNano = 0
		newCur.Size = 0
	}

	return out, newCur
}

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
//
// Cursor strategy: emitted-identity cursor with mtime/size fast-path.
//   - If size and mtime are unchanged since the last scan, skip parsing entirely.
//   - Otherwise parse the full file, filter to events whose identity is not
//     already in the emitted set, add new identities, and re-encode the cursor.
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
	statSize := fi.Size()
	statMtime := fi.ModTime().UnixNano()

	prev := decodeCursor(cursor)

	if sid == "" {
		// sessionId could not be extracted — attempt to parse to surface corruption
		// errors. If parsing succeeds (valid but empty/no sessionId), skip silently.
		// Use sentinel mtime/size=0 so a future successful parse (e.g. after the
		// file is fixed) is not short-circuited by the fast-path.
		if statSize == prev.Size && statMtime == prev.MtimeNano {
			return nil, cursor, nil
		}
		if strings.HasSuffix(file, ".json") {
			_, parseErr := a.parseMonolithic(file, "")
			if parseErr != nil {
				return nil, cursor, parseErr
			}
		} else {
			_, parseErr := a.parseJournal(file, "")
			if parseErr != nil {
				return nil, cursor, parseErr
			}
		}
		// Sentinel 0: a file that later parses successfully must not be stranded
		// by a primed cursor — force a re-parse on the next scan.
		noopCur := geminiCursor{MtimeNano: 0, Size: 0, Emitted: prev.Emitted}
		return nil, encodeCursor(noopCur), nil
	}

	if !hasSID || winner != file {
		// Non-winner: emit no events. Use sentinel mtime/size=0 so that if this
		// file later becomes the winner (its higher-priority sibling is deleted),
		// the fast-path does not skip it — a re-parse is forced on the next scan.
		noopCur := geminiCursor{MtimeNano: 0, Size: 0, Emitted: prev.Emitted}
		return nil, encodeCursor(noopCur), nil
	}

	// mtime/size fast-path: if the file has not changed, skip re-parsing.
	if statSize == prev.Size && statMtime == prev.MtimeNano {
		return nil, cursor, nil
	}

	// File changed (or first scan): parse and gate emissions.
	var events []model.Event
	if strings.HasSuffix(file, ".json") {
		events, err = a.parseMonolithic(file, sid)
	} else {
		events, err = a.parseJournal(file, sid)
	}
	if err != nil {
		return nil, cursor, err
	}

	out, newCur := gateEmissions(events, prev, statMtime, statSize)
	return out, encodeCursor(newCur), nil
}

// parseMonolithic parses a monolithic .json file and returns all settled prompt events.
func (a *Adapter) parseMonolithic(file, sid string) ([]model.Event, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	var s monolithicSession
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("unsupported gemini format (corrupt monolithic json): %s: %w", file, err)
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
	return pairMessages(msgs, sid, cwd), nil
}

// parseJournal parses a JSONL journal file and returns all settled prompt events.
// The full file is always re-parsed; identity-based dedup in gateEmissions handles
// incremental emission (so the old byte-offset cursor is no longer needed here).
func (a *Adapter) parseJournal(file, sid string) ([]model.Event, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)

	lineNum := 0
	var dirs []string
	var msgs []normalizedMsg

	for sc.Scan() {
		line := sc.Bytes()
		lineNum++

		if lineNum == 1 {
			// Header line — must be valid JSON; if not, the file is corrupt.
			var h journalHeader
			if err := json.Unmarshal(line, &h); err != nil {
				return nil, fmt.Errorf("unsupported gemini format (corrupt journal): %s", file)
			}
			dirs = h.Directories
			continue
		}

		var jl journalLine
		if json.Unmarshal(line, &jl) != nil {
			continue
		}
		if jl.Type != "user" && jl.Type != "gemini" {
			continue
		}

		msgs = append(msgs, normalizedMsg{
			Type:      jl.Type,
			Content:   jl.Content,
			Model:     jl.Model,
			Timestamp: jl.Timestamp,
		})
	}

	if lineNum == 0 {
		return nil, fmt.Errorf("unsupported gemini format (corrupt journal): %s", file)
	}

	cwd := a.resolveCwd(file, dirs)
	return pairMessages(msgs, sid, cwd), nil
}

// pairMessages walks a normalized message list and pairs user prompts with
// following gemini responses.
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
