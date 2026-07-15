package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/state"
)

func TestCorruptFileReturnsError(t *testing.T) {
	a := New("")
	evs, _, err := a.Parse(filepath.Join("testdata", "corrupt.jsonl"), nil, time.Time{})
	if err == nil {
		t.Fatal("corrupt.jsonl: want non-nil error, got nil")
	}
	if len(evs) != 0 {
		t.Fatalf("corrupt.jsonl: want 0 events, got %d", len(evs))
	}
}

func TestExecSessionSkipped(t *testing.T) {
	a := New("")
	evs, _, err := a.Parse(filepath.Join("testdata", "exec.jsonl"), nil, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 0 {
		t.Fatalf("exec session: want 0 events, got %d", len(evs))
	}
}

func TestFilterInjected(t *testing.T) {
	a := New("")
	evs, _, err := a.Parse(filepath.Join("testdata", "injected.jsonl"), nil, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Fatalf("injected session: want 1 event, got %d", len(evs))
	}
	if evs[0].PromptText != "What is the capital of France?" {
		t.Errorf("PromptText: want %q, got %q", "What is the capital of France?", evs[0].PromptText)
	}
}

func TestFilterCompacted(t *testing.T) {
	a := New("")
	evs, _, err := a.Parse(filepath.Join("testdata", "compacted.jsonl"), nil, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Fatalf("compacted session: want 1 event, got %d", len(evs))
	}
	if evs[0].PromptText != "What is 3+3?" {
		t.Errorf("PromptText: want %q, got %q", "What is 3+3?", evs[0].PromptText)
	}
}

func TestModelSwitch(t *testing.T) {
	a := New("")
	evs, _, err := a.Parse(filepath.Join("testdata", "model_switch.jsonl"), nil, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 3 {
		t.Fatalf("model_switch: want 3 events, got %d", len(evs))
	}

	// Build a map from prompt text → event for order-independent assertions.
	byPrompt := make(map[string]string, len(evs))
	for _, e := range evs {
		byPrompt[e.PromptText] = e.Model
	}

	cases := []struct {
		prompt string
		model  string
	}{
		{"Prompt C before any turn_context", ""},
		{"Prompt A after gpt-5.5", "gpt-5.5"},
		{"Prompt B after gpt-5.3-codex-spark", "gpt-5.3-codex-spark"},
	}
	for _, tc := range cases {
		got, ok := byPrompt[tc.prompt]
		if !ok {
			t.Errorf("no event with PromptText %q", tc.prompt)
			continue
		}
		if got != tc.model {
			t.Errorf("prompt %q: want Model=%q, got %q", tc.prompt, tc.model, got)
		}
	}
}

func TestInteractiveSession(t *testing.T) {
	a := New("")
	evs, _, err := a.Parse(filepath.Join("testdata", "interactive.jsonl"), nil, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Fatalf("interactive session: want 1 event, got %d", len(evs))
	}
	e := evs[0]
	if e.SourceTool != "CODEX" {
		t.Errorf("SourceTool: want CODEX, got %q", e.SourceTool)
	}
	if e.Surface != "cli" {
		t.Errorf("Surface: want cli, got %q", e.Surface)
	}
	if e.SessionID != "sess-abc" {
		t.Errorf("SessionID: want sess-abc, got %q", e.SessionID)
	}
	if e.PromptText != "Hello, what is 2+2?" {
		t.Errorf("PromptText: want %q, got %q", "Hello, what is 2+2?", e.PromptText)
	}
	if e.ResponseText != "2+2 equals 4." {
		t.Errorf("ResponseText: want %q, got %q", "2+2 equals 4.", e.ResponseText)
	}
	if e.ProjectContext != "/Users/x/proj" {
		t.Errorf("ProjectContext: want /Users/x/proj, got %q", e.ProjectContext)
	}
	if e.Model != "" {
		t.Errorf("Model: want empty (Task 5), got %q", e.Model)
	}
}

// ---------------------------------------------------------------------------
// Regression test: TestCodexIncrementalAppend
//
// Reproduces two coupled bugs in the original Parse implementation:
//
//  Bug A (data loss — CRITICAL): Parse always returned fileEnd as the cursor
//  even when the last turn was an unpaired user prompt (assistant not yet
//  arrived). On the next scan the cursor was past the prompt → the turn was
//  permanently lost.
//
//  Bug B (permanent error): Parse sought to fromOffset before reading, so
//  session_meta (only on line 1) was invisible on every scan after the first,
//  causing every subsequent call to error with "no session_meta".
// ---------------------------------------------------------------------------

// rolloutLine encodes one JSONL line for a rollout file.
func rolloutLine(typ string, payload interface{}) string {
	p, _ := json.Marshal(payload)
	line, _ := json.Marshal(map[string]interface{}{
		"timestamp": "2026-05-08T05:42:33.000Z",
		"type":      typ,
		"payload":   json.RawMessage(p),
	})
	return string(line) + "\n"
}

func userLine(text string) string {
	return rolloutLine("response_item", map[string]interface{}{
		"type": "message",
		"role": "user",
		"content": []map[string]interface{}{
			{"type": "input_text", "text": text},
		},
	})
}

func assistantLine(text string) string {
	return rolloutLine("response_item", map[string]interface{}{
		"type": "message",
		"role": "assistant",
		"content": []map[string]interface{}{
			{"type": "output_text", "text": text},
		},
	})
}

func metaLine(id, model string) string {
	return rolloutLine("session_meta", map[string]interface{}{
		"id":         id,
		"cwd":        "/tmp/proj",
		"originator": "codex-tui",
		"source":     "cli",
	}) + rolloutLine("turn_context", map[string]interface{}{
		"model": model,
		"cwd":   "/tmp/proj",
	})
}

func TestCodexIncrementalAppend(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "rollout-test.jsonl")

	// ---------- Step 1: write session_meta + turn_context + user prompt (no assistant yet) ----------
	content := metaLine("incr-sess", "gpt-5-codex") + userLine("Q1")
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatalf("write step 1: %v", err)
	}

	a := New("")
	// idleCutoff far in the PAST → file is NOT idle (assistant still generating)
	notIdle := time.Now().Add(-24 * time.Hour)

	// Bug A: before fix, Parse returned fileEnd (cursor past Q1) → Q1 stranded.
	// After fix: Parse must return 0 events and a cursor NOT past the user prompt.
	evs1, cur1, err := a.Parse(file, nil, notIdle)
	if err != nil {
		t.Fatalf("step 1 Parse: %v", err)
	}
	if len(evs1) != 0 {
		t.Fatalf("step 1: want 0 events (assistant not yet arrived), got %d", len(evs1))
	}
	// Cursor must stop before the end of the file so Q1 is re-read next scan.
	fileSize1 := int64(len(content))
	cursorOffset1 := state.DecodeByteCursor(cur1, -1)
	if cursorOffset1 >= fileSize1 {
		t.Fatalf("step 1: cursor offset %d >= fileSize %d — user prompt Q1 would be stranded (bug A)", cursorOffset1, fileSize1)
	}

	// ---------- Step 2: append assistant response ----------
	content2 := content + assistantLine("A1")
	if err := os.WriteFile(file, []byte(content2), 0o644); err != nil {
		t.Fatalf("write step 2: %v", err)
	}

	// Bug B: before fix, Parse sought to fromOffset (past line 1) → "no session_meta" error.
	// After fix: Parse reads from 0, finds session_meta, emits the Q1→A1 pair.
	evs2, _, err := a.Parse(file, cur1, notIdle)
	if err != nil {
		t.Fatalf("step 2 Parse: %v (bug B: seek past session_meta)", err)
	}
	if len(evs2) != 1 {
		t.Fatalf("step 2: want 1 event (Q1→A1 pair), got %d", len(evs2))
	}
	e := evs2[0]
	if e.PromptText != "Q1" {
		t.Errorf("step 2: PromptText = %q, want %q", e.PromptText, "Q1")
	}
	if e.ResponseText != "A1" {
		t.Errorf("step 2: ResponseText = %q, want %q", e.ResponseText, "A1")
	}
	if e.Model != "gpt-5-codex" {
		t.Errorf("step 2: Model = %q, want %q", e.Model, "gpt-5-codex")
	}
}

// TestCodexIdleTrailingPrompt verifies that a trailing unpaired user prompt is
// emitted when the file is idle (session done, assistant won't respond).
func TestCodexIdleTrailingPrompt(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "rollout-idle.jsonl")

	content := metaLine("idle-sess", "gpt-5-codex") + userLine("IdleQ")
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Make the file appear old (mtime < idleCutoff) by setting mtime to past.
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(file, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	a := New("")
	// idleCutoff in the future → file is idle.
	idleCutoff := time.Now().Add(time.Hour)

	evs, _, err := a.Parse(file, nil, idleCutoff)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("idle trailing prompt: want 1 event, got %d", len(evs))
	}
	if evs[0].PromptText != "IdleQ" {
		t.Errorf("PromptText = %q, want %q", evs[0].PromptText, "IdleQ")
	}
	_ = fmt.Sprintf // keep fmt import used
}
