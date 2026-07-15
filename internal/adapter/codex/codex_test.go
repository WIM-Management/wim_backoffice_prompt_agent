package codex

import (
	"path/filepath"
	"testing"
	"time"
)

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
