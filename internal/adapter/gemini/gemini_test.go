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

// ---------------------------------------------------------------------------
// Unit test: authoritative — pure winner-selection logic
// ---------------------------------------------------------------------------

func TestAuthoritativeSelection(t *testing.T) {
	// Simulate the winner-selection logic directly using pathPriority.
	// Three paths, all with same sessionId: nested wins.
	paths := []string{
		"/h/.gemini/tmp/p/chats/session-1.json",    // monolithic, priority 1
		"/h/.gemini/tmp/p/chats/session-1.jsonl",   // journal, priority 2
		"/h/.gemini/tmp/p/chats/uuid1/file.jsonl",  // nested, priority 0
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

	var evs []interface{ GetFields() (string, string, string) }
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

	// Write projects.json — same shape as testdata/projects.json
	writeFile(t, filepath.Join(home, ".gemini", "tmp", projDir, "projects.json"),
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
