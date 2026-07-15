package gemini

import (
	"encoding/json"
	"os"
	"path/filepath"
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
