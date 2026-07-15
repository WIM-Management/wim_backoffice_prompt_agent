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
