package gemini

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helper: build a temp home dir with the Gemini session layout
// ---------------------------------------------------------------------------

// writeFile creates parent dirs and writes content to path.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Unit test: pathPriority
// ---------------------------------------------------------------------------

func TestPathPriority(t *testing.T) {
	cases := []struct {
		path string
		want int
	}{
		// nested (chats/<uuid>/<file>)
		{"/home/.gemini/tmp/proj/chats/abc123/session.jsonl", 0},
		{"/home/.gemini/tmp/proj/chats/someuuid/x.json", 0},
		// monolithic
		{"/home/.gemini/tmp/proj/chats/session-1720-abc.json", 1},
		// journal
		{"/home/.gemini/tmp/proj/chats/session-1720-abc.jsonl", 2},
	}
	for _, c := range cases {
		got := pathPriority(c.path)
		if got != c.want {
			t.Errorf("pathPriority(%q) = %d, want %d", c.path, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Unit test: extractContent
// ---------------------------------------------------------------------------

func TestExtractContent(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{`"hello world"`, "hello world"},
		{`[{"text":"foo"},{"text":"bar"}]`, "foobar"},
		{`""`, ""},
		{`[]`, ""},
		{``, ""},
	}
	for _, c := range cases {
		raw := json.RawMessage(c.raw)
		got := extractContent(raw)
		if got != c.want {
			t.Errorf("extractContent(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Integration test: journal.jsonl fixture
// ---------------------------------------------------------------------------

func TestJournalParsing(t *testing.T) {
	// Build a temp home that has the journal fixture under the Gemini layout.
	home := t.TempDir()
	chatsDir := filepath.Join(home, ".gemini", "tmp", "proj", "chats")

	src, err := os.ReadFile("testdata/journal.jsonl")
	if err != nil {
		t.Fatalf("read testdata/journal.jsonl: %v", err)
	}
	writeFile(t, filepath.Join(chatsDir, "session-1720-jrn.jsonl"), string(src))

	a := New(home)
	paths, err := a.SessionPaths()
	if err != nil {
		t.Fatalf("SessionPaths: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("SessionPaths returned empty")
	}

	var events []interface{}
	for _, p := range paths {
		evs, _, err := a.Parse(p, nil, time.Time{})
		if err != nil {
			t.Fatalf("Parse(%q): %v", p, err)
		}
		for _, e := range evs {
			events = append(events, e)
		}
	}

	// Expect exactly 1 user event ("hello from journal"), $set and gemini lines skipped.
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
}

func TestJournalContent(t *testing.T) {
	home := t.TempDir()
	chatsDir := filepath.Join(home, ".gemini", "tmp", "proj", "chats")

	src, err := os.ReadFile("testdata/journal.jsonl")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	writeFile(t, filepath.Join(chatsDir, "session-1720-jrn.jsonl"), string(src))

	a := New(home)
	paths, _ := a.SessionPaths()

	for _, p := range paths {
		evs, _, _ := a.Parse(p, nil, time.Time{})
		for _, ev := range evs {
			if ev.PromptText != "hello from journal" {
				t.Errorf("unexpected PromptText: %q", ev.PromptText)
			}
			if ev.SourceTool != "GEMINI" {
				t.Errorf("unexpected SourceTool: %q", ev.SourceTool)
			}
			if ev.SessionID != "journal-session" {
				t.Errorf("unexpected SessionID: %q", ev.SessionID)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Integration test: cross-format dedup — nested beats monolithic
// ---------------------------------------------------------------------------

func TestCrossFormatDedup_NestedWins(t *testing.T) {
	home := t.TempDir()
	chatsDir := filepath.Join(home, ".gemini", "tmp", "proj", "chats")

	// Write monolithic file (chats/session-*.json)
	monoSrc, err := os.ReadFile("testdata/monolithic.json")
	if err != nil {
		t.Fatalf("read testdata/monolithic.json: %v", err)
	}
	writeFile(t, filepath.Join(chatsDir, "session-1720-mono.json"), string(monoSrc))

	// Write nested file (chats/<uuid>/session.jsonl) — same sessionId "dup-session"
	nestedSrc, err := os.ReadFile("testdata/nested/dup/session.jsonl")
	if err != nil {
		t.Fatalf("read testdata/nested: %v", err)
	}
	writeFile(t, filepath.Join(chatsDir, "dup-uuid", "session.jsonl"), string(nestedSrc))

	a := New(home)
	paths, err := a.SessionPaths()
	if err != nil {
		t.Fatalf("SessionPaths: %v", err)
	}

	var allEvents []interface{}
	var promptTexts []string
	for _, p := range paths {
		evs, _, err := a.Parse(p, nil, time.Time{})
		if err != nil {
			t.Fatalf("Parse(%q): %v", p, err)
		}
		for _, ev := range evs {
			allEvents = append(allEvents, ev)
			promptTexts = append(promptTexts, ev.PromptText)
		}
	}

	// Exactly 1 event (not 2 — no duplicate from monolithic).
	if len(allEvents) != 1 {
		t.Fatalf("expected 1 event (nested wins), got %d: %v", len(allEvents), promptTexts)
	}

	// The surviving event must come from the nested source.
	if promptTexts[0] != "from nested" {
		t.Errorf("expected prompt from nested source, got %q", promptTexts[0])
	}
}

// ---------------------------------------------------------------------------
// Integration test: monolithic-only (no nested) — monolithic parses normally
// ---------------------------------------------------------------------------

func TestMonolithicOnly(t *testing.T) {
	home := t.TempDir()
	chatsDir := filepath.Join(home, ".gemini", "tmp", "proj", "chats")

	src, err := os.ReadFile("testdata/monolithic.json")
	if err != nil {
		t.Fatalf("read testdata/monolithic.json: %v", err)
	}
	writeFile(t, filepath.Join(chatsDir, "session-1720-mono.json"), string(src))

	a := New(home)
	paths, _ := a.SessionPaths()

	var prompts []string
	for _, p := range paths {
		evs, _, err := a.Parse(p, nil, time.Time{})
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		for _, ev := range evs {
			prompts = append(prompts, ev.PromptText)
		}
	}

	if len(prompts) != 1 || prompts[0] != "from monolithic" {
		t.Errorf("expected 1 prompt 'from monolithic', got %v", prompts)
	}
}

// Regression: newer Gemini writes message `id` as a UUID string, not an int.
// The adapter must parse those sessions instead of skipping them as corrupt
// (id is not used, so its JSON type must not gate parsing).
func TestMonolithicStringID(t *testing.T) {
	home := t.TempDir()
	chatsDir := filepath.Join(home, ".gemini", "tmp", "proj", "chats")

	src, err := os.ReadFile("testdata/monolithic_stringid.json")
	if err != nil {
		t.Fatalf("read testdata/monolithic_stringid.json: %v", err)
	}
	writeFile(t, filepath.Join(chatsDir, "session-1720-strid.json"), string(src))

	a := New(home)
	paths, _ := a.SessionPaths()

	var prompts []string
	for _, p := range paths {
		evs, _, err := a.Parse(p, nil, time.Time{})
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		for _, ev := range evs {
			prompts = append(prompts, ev.PromptText)
		}
	}

	if len(prompts) != 1 || prompts[0] != "from string id monolithic" {
		t.Errorf("expected 1 prompt 'from string id monolithic', got %v", prompts)
	}
}

// ---------------------------------------------------------------------------
// Unit test: authoritative — pure winner-selection logic
// ---------------------------------------------------------------------------

func TestAuthoritativeSelection(t *testing.T) {
	// Simulate the winner-selection logic directly using pathPriority.
	// Three paths, all with same sessionId: nested wins.
	paths := []string{
		"/h/.gemini/tmp/p/chats/session-1.json",   // monolithic, priority 1
		"/h/.gemini/tmp/p/chats/session-1.jsonl",  // journal, priority 2
		"/h/.gemini/tmp/p/chats/uuid1/file.jsonl", // nested, priority 0
	}

	// Replicate the winner-selection algorithm.
	best := paths[0]
	for _, p := range paths[1:] {
		if pathPriority(p) < pathPriority(best) {
			best = p
		}
	}

	if pathPriority(best) != 0 {
		t.Errorf("expected nested (priority 0) to win, got %q (priority %d)", best, pathPriority(best))
	}
}

// ---------------------------------------------------------------------------
// Task 7: Parse tests — pairing, injection filter, model, cwd
// ---------------------------------------------------------------------------

// TestParseInfoJournal verifies that info/$set/null/typeless/slash-command lines
// are excluded and only the single real user→gemini pair is emitted.
func TestParseInfoJournal(t *testing.T) {
	home := t.TempDir()
	chatsDir := filepath.Join(home, ".gemini", "tmp", "proj", "chats")

	src, err := os.ReadFile("testdata/info_journal.jsonl")
	if err != nil {
		t.Fatalf("read testdata/info_journal.jsonl: %v", err)
	}
	writeFile(t, filepath.Join(chatsDir, "session-info.jsonl"), string(src))

	a := New(home)
	paths, err := a.SessionPaths()
	if err != nil {
		t.Fatalf("SessionPaths: %v", err)
	}

	var evs []interface {
		GetFields() (string, string, string)
	}
	type fields struct{ prompt, response, model string }
	var got []fields
	for _, p := range paths {
		events, _, err := a.Parse(p, nil, time.Time{})
		if err != nil {
			t.Fatalf("Parse(%q): %v", p, err)
		}
		for _, e := range events {
			got = append(got, fields{e.PromptText, e.ResponseText, e.Model})
		}
	}
	_ = evs

	if len(got) != 1 {
		t.Fatalf("expected exactly 1 event, got %d: %+v", len(got), got)
	}
	g := got[0]
	if g.prompt != "real gemini prompt" {
		t.Errorf("PromptText = %q, want %q", g.prompt, "real gemini prompt")
	}
	if !strings.Contains(g.response, "sure, doing it") {
		t.Errorf("ResponseText = %q, want to contain %q", g.response, "sure, doing it")
	}
	if g.model != "gemini-3-flash-preview" {
		t.Errorf("Model = %q, want %q", g.model, "gemini-3-flash-preview")
	}
}

// TestParseSlashCommandExcluded confirms a /help user message is not emitted.
func TestParseSlashCommandExcluded(t *testing.T) {
	home := t.TempDir()
	chatsDir := filepath.Join(home, ".gemini", "tmp", "proj", "chats")

	src, err := os.ReadFile("testdata/info_journal.jsonl")
	if err != nil {
		t.Fatalf("read testdata/info_journal.jsonl: %v", err)
	}
	writeFile(t, filepath.Join(chatsDir, "session-info.jsonl"), string(src))

	a := New(home)
	paths, _ := a.SessionPaths()
	for _, p := range paths {
		events, _, _ := a.Parse(p, nil, time.Time{})
		for _, e := range events {
			if strings.HasPrefix(e.PromptText, "/") {
				t.Errorf("slash command leaked into events: PromptText=%q", e.PromptText)
			}
		}
	}
}

// TestParseCwdFromDirectories verifies that directories[0] is used as ProjectContext.
func TestParseCwdFromDirectories(t *testing.T) {
	home := t.TempDir()
	chatsDir := filepath.Join(home, ".gemini", "tmp", "myproj", "chats")

	session := map[string]interface{}{
		"sessionId":   "cwd-dir-session",
		"directories": []string{"/Users/x/proj"},
		"messages": []map[string]interface{}{
			{"id": 1, "timestamp": 1720000010000, "type": "user", "content": "hello"},
			{"id": 2, "timestamp": 1720000020000, "type": "gemini", "content": "hi", "model": "gemini-3-flash-preview"},
		},
	}
	data, _ := json.Marshal(session)
	writeFile(t, filepath.Join(chatsDir, "session-cwd.json"), string(data))

	a := New(home)
	paths, _ := a.SessionPaths()
	for _, p := range paths {
		events, _, err := a.Parse(p, nil, time.Time{})
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		if events[0].ProjectContext != "/Users/x/proj" {
			t.Errorf("ProjectContext = %q, want %q", events[0].ProjectContext, "/Users/x/proj")
		}
	}
}

// TestParseCwdFromProjectRoot verifies that .project_root is read when directories is absent.
func TestParseCwdFromProjectRoot(t *testing.T) {
	home := t.TempDir()
	projDir := "realproj"
	chatsDir := filepath.Join(home, ".gemini", "tmp", projDir, "chats")

	// Write .project_root
	writeFile(t, filepath.Join(home, ".gemini", "tmp", projDir, ".project_root"), "/Users/x/realproj\n")

	session := map[string]interface{}{
		"sessionId": "cwd-root-session",
		"messages": []map[string]interface{}{
			{"id": 1, "timestamp": 1720000010000, "type": "user", "content": "hello"},
			{"id": 2, "timestamp": 1720000020000, "type": "gemini", "content": "hi", "model": "gemini-3-flash-preview"},
		},
	}
	data, _ := json.Marshal(session)
	writeFile(t, filepath.Join(chatsDir, "session-root.json"), string(data))

	a := New(home)
	paths, _ := a.SessionPaths()
	for _, p := range paths {
		events, _, err := a.Parse(p, nil, time.Time{})
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		if events[0].ProjectContext != "/Users/x/realproj" {
			t.Errorf("ProjectContext = %q, want %q", events[0].ProjectContext, "/Users/x/realproj")
		}
	}
}

// TestParseCwdFromProjectsJSON verifies the projects.json reverse-map fallback.
func TestParseCwdFromProjectsJSON(t *testing.T) {
	home := t.TempDir()
	projDir := "proj"
	chatsDir := filepath.Join(home, ".gemini", "tmp", projDir, "chats")

	// Write projects.json at the .gemini root (verified real location — name==tmp dir basename).
	writeFile(t, filepath.Join(home, ".gemini", "projects.json"),
		`{"projects":{"/Users/x/proj":"proj"}}`)

	session := map[string]interface{}{
		"sessionId": "cwd-pj-session",
		"messages": []map[string]interface{}{
			{"id": 1, "timestamp": 1720000010000, "type": "user", "content": "hello"},
			{"id": 2, "timestamp": 1720000020000, "type": "gemini", "content": "hi", "model": "gemini-3-flash-preview"},
		},
	}
	data, _ := json.Marshal(session)
	writeFile(t, filepath.Join(chatsDir, "session-pj.json"), string(data))

	a := New(home)
	paths, _ := a.SessionPaths()
	for _, p := range paths {
		events, _, err := a.Parse(p, nil, time.Time{})
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		if events[0].ProjectContext != "/Users/x/proj" {
			t.Errorf("ProjectContext = %q, want %q", events[0].ProjectContext, "/Users/x/proj")
		}
	}
}

// ---------------------------------------------------------------------------
// Task 8: Cursor tests — emitted-identity cursor + mtime/size fast-path
// ---------------------------------------------------------------------------

// makeMonolithicSession builds a monolithic JSON session with the given prompts.
// Each prompt is paired with a gemini response "response to <prompt>".
// Timestamps are spaced 10s apart starting at base (milliseconds).
func makeMonolithicSession(sessionID string, prompts []string, baseMs int64) map[string]interface{} {
	msgs := []map[string]interface{}{}
	ts := baseMs
	for _, p := range prompts {
		msgs = append(msgs, map[string]interface{}{
			"id": len(msgs) + 1, "timestamp": ts, "type": "user", "content": p,
		})
		ts += 10000
		msgs = append(msgs, map[string]interface{}{
			"id": len(msgs) + 1, "timestamp": ts, "type": "gemini",
			"content": "response to " + p, "model": "gemini-test",
		})
		ts += 10000
	}
	return map[string]interface{}{
		"sessionId": sessionID,
		"messages":  msgs,
	}
}

// writeMonolithicSession serialises and writes a monolithic session JSON file.
func writeMonolithicSession(t *testing.T, path string, session map[string]interface{}) {
	t.Helper()
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("marshal session: %v", err)
	}
	writeFile(t, path, string(data))
}

// TestCursorEmitsOnlyNew verifies that:
//  1. First Parse (nil cursor) emits all settled prompts.
//  2. After appending a new prompt (size increases, mtime may be same second),
//     second Parse emits only the new prompt.
func TestCursorEmitsOnlyNew(t *testing.T) {
	home := t.TempDir()
	chatsDir := filepath.Join(home, ".gemini", "tmp", "proj", "chats")
	filePath := filepath.Join(chatsDir, "session-cursor-test.json")

	// Write 2 prompts.
	sess := makeMonolithicSession("cursor-session", []string{"prompt one", "prompt two"}, 1720000000000)
	writeMonolithicSession(t, filePath, sess)

	a := New(home)

	// First parse: nil cursor → should emit both prompts.
	evs1, cur1, err := a.Parse(filePath, nil, time.Time{})
	if err != nil {
		t.Fatalf("Parse #1: %v", err)
	}
	if len(evs1) != 2 {
		t.Fatalf("Parse #1: expected 2 events, got %d", len(evs1))
	}
	if evs1[0].PromptText != "prompt one" {
		t.Errorf("Parse #1 ev[0].PromptText = %q, want %q", evs1[0].PromptText, "prompt one")
	}
	if evs1[1].PromptText != "prompt two" {
		t.Errorf("Parse #1 ev[1].PromptText = %q, want %q", evs1[1].PromptText, "prompt two")
	}

	// Verify cursor has 2 emitted identities.
	var c1 geminiCursor
	if err := json.Unmarshal(cur1, &c1); err != nil {
		t.Fatalf("decode cursor #1: %v", err)
	}
	if len(c1.Emitted) != 2 {
		t.Errorf("cursor #1 emitted len = %d, want 2", len(c1.Emitted))
	}

	// Append a 3rd prompt to the file (size grows; mtime may stay same second).
	sess2 := makeMonolithicSession("cursor-session",
		[]string{"prompt one", "prompt two", "prompt three"}, 1720000000000)
	writeMonolithicSession(t, filePath, sess2)

	// Second parse: cursor from first call → should emit only prompt three.
	// A fresh Adapter is needed because winnerMap is per-instance.
	a2 := New(home)
	evs2, cur2, err := a2.Parse(filePath, cur1, time.Time{})
	if err != nil {
		t.Fatalf("Parse #2: %v", err)
	}
	if len(evs2) != 1 {
		t.Fatalf("Parse #2: expected 1 event (only new prompt), got %d: %+v", len(evs2), evs2)
	}
	if evs2[0].PromptText != "prompt three" {
		t.Errorf("Parse #2 ev[0].PromptText = %q, want %q", evs2[0].PromptText, "prompt three")
	}

	// Cursor should now have 3 emitted identities.
	var c2 geminiCursor
	if err := json.Unmarshal(cur2, &c2); err != nil {
		t.Fatalf("decode cursor #2: %v", err)
	}
	if len(c2.Emitted) != 3 {
		t.Errorf("cursor #2 emitted len = %d, want 3", len(c2.Emitted))
	}
}

// TestCursorFastPathSkip verifies that a second Parse with an unchanged file
// (same size and mtime) returns zero events and the same cursor.
func TestCursorFastPathSkip(t *testing.T) {
	home := t.TempDir()
	chatsDir := filepath.Join(home, ".gemini", "tmp", "proj", "chats")
	filePath := filepath.Join(chatsDir, "session-fastpath-test.json")

	sess := makeMonolithicSession("fastpath-session", []string{"only prompt"}, 1720000000000)
	writeMonolithicSession(t, filePath, sess)

	a := New(home)
	evs1, cur1, err := a.Parse(filePath, nil, time.Time{})
	if err != nil {
		t.Fatalf("Parse #1: %v", err)
	}
	if len(evs1) != 1 {
		t.Fatalf("Parse #1: expected 1 event, got %d", len(evs1))
	}

	// Second parse with same file (no writes) — fast-path must skip.
	a2 := New(home)
	evs2, cur2, err := a2.Parse(filePath, cur1, time.Time{})
	if err != nil {
		t.Fatalf("Parse #2: %v", err)
	}
	if len(evs2) != 0 {
		t.Errorf("Parse #2 (fast-path): expected 0 events, got %d: %+v", len(evs2), evs2)
	}

	// Cursor should be unchanged (same bytes).
	if string(cur2) != string(cur1) {
		t.Errorf("Parse #2 (fast-path): cursor changed:\n  before: %s\n  after:  %s", cur1, cur2)
	}
}

// ---------------------------------------------------------------------------
// Task 9: fail-loud tests — corrupt/unsupported files return explicit errors
// ---------------------------------------------------------------------------

// TestCorruptMonolithicReturnsError verifies that a corrupt .json file yields
// a non-nil error and 0 events.
func TestCorruptMonolithicReturnsError(t *testing.T) {
	home := t.TempDir()
	chatsDir := filepath.Join(home, ".gemini", "tmp", "proj", "chats")

	src, err := os.ReadFile("testdata/corrupt.json")
	if err != nil {
		t.Fatalf("read testdata/corrupt.json: %v", err)
	}
	filePath := filepath.Join(chatsDir, "session-corrupt.json")
	writeFile(t, filePath, string(src))

	a := New(home)
	paths, err := a.SessionPaths()
	if err != nil {
		t.Fatalf("SessionPaths: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no paths found")
	}

	evs, _, parseErr := a.Parse(paths[0], nil, time.Time{})
	if parseErr == nil {
		t.Fatal("corrupt monolithic: want non-nil error, got nil")
	}
	if len(evs) != 0 {
		t.Fatalf("corrupt monolithic: want 0 events, got %d", len(evs))
	}
}

// TestCorruptJournalReturnsError verifies that a .jsonl whose header line is
// not valid JSON returns a non-nil error and 0 events.
func TestCorruptJournalReturnsError(t *testing.T) {
	home := t.TempDir()
	chatsDir := filepath.Join(home, ".gemini", "tmp", "proj", "chats")

	src, err := os.ReadFile("testdata/corrupt_journal.jsonl")
	if err != nil {
		t.Fatalf("read testdata/corrupt_journal.jsonl: %v", err)
	}
	filePath := filepath.Join(chatsDir, "session-corrupt.jsonl")
	writeFile(t, filePath, string(src))

	a := New(home)
	paths, err := a.SessionPaths()
	if err != nil {
		t.Fatalf("SessionPaths: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no paths found")
	}

	evs, _, parseErr := a.Parse(paths[0], nil, time.Time{})
	if parseErr == nil {
		t.Fatal("corrupt journal: want non-nil error, got nil")
	}
	if len(evs) != 0 {
		t.Fatalf("corrupt journal: want 0 events, got %d", len(evs))
	}
}

// TestHeaderOnlyJournalIsValid verifies that a journal with only a header line
// (no messages) yields 0 events with a nil error — it is a valid empty session.
func TestHeaderOnlyJournalIsValid(t *testing.T) {
	home := t.TempDir()
	chatsDir := filepath.Join(home, ".gemini", "tmp", "proj", "chats")

	// A valid header-only journal: just the header line, no messages.
	content := `{"sessionId":"empty-session","directories":["/Users/x/proj"]}` + "\n"
	filePath := filepath.Join(chatsDir, "session-empty.jsonl")
	writeFile(t, filePath, content)

	a := New(home)
	paths, err := a.SessionPaths()
	if err != nil {
		t.Fatalf("SessionPaths: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no paths found")
	}

	evs, _, parseErr := a.Parse(paths[0], nil, time.Time{})
	if parseErr != nil {
		t.Fatalf("header-only journal: want nil error, got %v", parseErr)
	}
	if len(evs) != 0 {
		t.Fatalf("header-only journal: want 0 events, got %d", len(evs))
	}
}

// ---------------------------------------------------------------------------
// Regression test: TestGeminiWinnerPromotion
//
// Reproduces the non-winner cursor bug: the original code persisted real
// mtime/size in the non-winner noopCur. When the higher-priority sibling was
// later deleted and the file became the winner, the fast-path
// (statSize == prev.Size && statMtime == prev.MtimeNano) skipped it →
// its events were never emitted (stranded).
//
// Fix: non-winner (and sid=="" corrupt) branches use sentinel mtime/size=0
// so any future winner-promotion forces a re-parse.
// ---------------------------------------------------------------------------

func TestGeminiWinnerPromotion(t *testing.T) {
	home := t.TempDir()
	chatsDir := filepath.Join(home, ".gemini", "tmp", "proj", "chats")

	// Monolithic file with sessionId "dup" — lower priority (will lose to nested).
	monoContent := map[string]interface{}{
		"sessionId": "dup",
		"messages": []map[string]interface{}{
			{"id": 1, "timestamp": int64(1720000010000), "type": "user", "content": "from monolithic dup"},
			{"id": 2, "timestamp": int64(1720000020000), "type": "gemini", "content": "mono response", "model": "gemini-test"},
		},
	}
	monoData, _ := json.Marshal(monoContent)
	monoPath := filepath.Join(chatsDir, "session-dup.json")
	writeFile(t, monoPath, string(monoData))

	// Nested file (chats/<uuid>/x.jsonl) with same sessionId "dup" — higher priority (wins).
	nestedDir := filepath.Join(chatsDir, "dup-uuid")
	nestedPath := filepath.Join(nestedDir, "x.jsonl")
	nestedContent := `{"sessionId":"dup","directories":["/tmp/nested"]}` + "\n" +
		`{"id":1,"timestamp":1720000010000,"type":"user","content":"from nested dup"}` + "\n" +
		`{"id":2,"timestamp":1720000020000,"type":"gemini","content":"nested response","model":"gemini-test"}` + "\n"
	writeFile(t, nestedPath, nestedContent)

	// Step 1: parse all paths with a fresh adapter (nil cursor).
	// Nested wins → its event is emitted. Monolithic is non-winner → 0 events.
	// Capture the monolithic's returned cursor (it has real content unchanged).
	a1 := New(home)
	paths1, err := a1.SessionPaths()
	if err != nil {
		t.Fatalf("SessionPaths: %v", err)
	}

	var monoCursor []byte
	var totalEvents int
	for _, p := range paths1 {
		evs, cur, err := a1.Parse(p, nil, time.Time{})
		if err != nil {
			t.Fatalf("Parse(%q): %v", p, err)
		}
		totalEvents += len(evs)
		if p == monoPath {
			monoCursor = cur
		}
	}

	// Exactly 1 event from nested; monolithic is non-winner.
	if totalEvents != 1 {
		t.Fatalf("step 1: want 1 event (nested wins), got %d", totalEvents)
	}
	if monoCursor == nil {
		t.Fatal("step 1: monolithic cursor is nil")
	}

	// Verify that the monolithic cursor has sentinel mtime/size=0 (the fix).
	var mc geminiCursor
	if err := json.Unmarshal(monoCursor, &mc); err != nil {
		t.Fatalf("decode monolithic cursor: %v", err)
	}
	if mc.MtimeNano != 0 || mc.Size != 0 {
		t.Errorf("step 1: non-winner cursor mtime=%d size=%d, want 0/0 (fix not applied — winner-promotion would strand)", mc.MtimeNano, mc.Size)
	}

	// Step 2: delete the nested file so monolithic becomes the winner.
	if err := os.Remove(nestedPath); err != nil {
		t.Fatalf("remove nested: %v", err)
	}

	// Use a fresh adapter (per scan-lifetime contract) and parse monolithic with
	// the cursor from step 1. Content is unchanged — only the winner changed.
	// Before the fix: fast-path (statSize==prev.Size && statMtime==prev.MtimeNano)
	// would skip it → 0 events → STRANDED.
	// After the fix: sentinel 0 never matches real stat → re-parse forced → emits.
	a2 := New(home)
	evs2, _, err := a2.Parse(monoPath, monoCursor, time.Time{})
	if err != nil {
		t.Fatalf("step 2 Parse: %v", err)
	}
	if len(evs2) != 1 {
		t.Fatalf("step 2: want 1 event (monolithic now winner), got %d — winner-promotion strand bug", len(evs2))
	}
	if evs2[0].PromptText != "from monolithic dup" {
		t.Errorf("step 2: PromptText = %q, want %q", evs2[0].PromptText, "from monolithic dup")
	}
}

// TestParseMultipleGeminiResponses verifies that multiple consecutive gemini
// messages are joined and the first model is used.
func TestParseMultipleGeminiResponses(t *testing.T) {
	home := t.TempDir()
	chatsDir := filepath.Join(home, ".gemini", "tmp", "proj", "chats")

	session := map[string]interface{}{
		"sessionId": "multi-resp-session",
		"messages": []map[string]interface{}{
			{"id": 1, "timestamp": 1720000010000, "type": "user", "content": "question"},
			{"id": 2, "timestamp": 1720000020000, "type": "gemini", "content": "part one", "model": "gemini-3-flash-preview"},
			{"id": 3, "timestamp": 1720000030000, "type": "gemini", "content": "part two", "model": "gemini-3-flash-other"},
		},
	}
	data, _ := json.Marshal(session)
	writeFile(t, filepath.Join(chatsDir, "session-multi.json"), string(data))

	a := New(home)
	paths, _ := a.SessionPaths()
	for _, p := range paths {
		events, _, err := a.Parse(p, nil, time.Time{})
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		e := events[0]
		if !strings.Contains(e.ResponseText, "part one") || !strings.Contains(e.ResponseText, "part two") {
			t.Errorf("ResponseText = %q, want both parts joined", e.ResponseText)
		}
		if e.Model != "gemini-3-flash-preview" {
			t.Errorf("Model = %q, want first gemini model", e.Model)
		}
	}
}
