// Package e2e verifies the full pipeline: scanner → redact → queue → upload.
//
// The test wires the components manually (no real keychain, no daemon) so it
// runs entirely in a temp directory without external dependencies.
package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/adapter/claudecode"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/model"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/queue"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/redact"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/scanner"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/state"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/uploader"
)

// basic.jsonl content (Task 4 fixture): one settled prompt→response pair followed
// by a next human line that settles the first turn.
const basicJSONL = `{"type":"user","isSidechain":false,"sessionId":"s1","cwd":"/repo","gitBranch":"main","timestamp":"2026-06-24T09:00:00Z","message":{"role":"user","content":"버그 고쳐줘"}}
{"type":"assistant","isSidechain":false,"sessionId":"s1","timestamp":"2026-06-24T09:00:05Z","message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"고쳤습니다."}],"usage":{"output_tokens":10},"stop_reason":"end_turn"}}
{"type":"user","isSidechain":false,"sessionId":"s1","timestamp":"2026-06-24T09:01:00Z","message":{"role":"user","content":"고마워"}}
`

func TestEndToEnd(t *testing.T) {
	// ── 1) Temp HOME with a fake Claude Code session file ──────────────────────
	tmp := t.TempDir()

	// Override HOME so claudecode.Adapter.SessionPaths() globs tmp/.claude/...
	t.Setenv("HOME", tmp)

	sessDir := filepath.Join(tmp, ".claude", "projects", "proj")
	if err := os.MkdirAll(sessDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sessFile := filepath.Join(sessDir, "sess.jsonl")
	if err := os.WriteFile(sessFile, []byte(basicJSONL), 0o600); err != nil {
		t.Fatal(err)
	}

	// ── 2) Mock PATCH server: count received events ────────────────────────────
	var receivedCount atomic.Int64
	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/api/v1/prompt-insights/events" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read error", http.StatusInternalServerError)
			return
		}
		// Parse {"events":[...]} and count inner events
		var payload struct {
			Events []json.RawMessage `json:"events"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		receivedCount.Add(int64(len(payload.Events)))
		receivedBody = body
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// ── 3) Wire pipeline manually ──────────────────────────────────────────────
	stateFile := filepath.Join(tmp, "state.json")
	queueDir := filepath.Join(tmp, "queue")

	sc := scanner.New(
		[]model.Adapter{claudecode.New(filepath.Join(tmp, ".claude"))},
		state.New(stateFile),
		10*time.Minute,
	)

	evs, commit := sc.ScanOnce()

	// Redact secrets from collected events
	for i := range evs {
		evs[i].PromptText = redact.Scrub(evs[i].PromptText)
		evs[i].ResponseText = redact.Scrub(evs[i].ResponseText)
		evs[i].ClientVersion = "test"
	}

	q := queue.New(queueDir)
	if err := q.Enqueue(evs); err != nil {
		t.Fatal("enqueue:", err)
	}
	if err := commit(); err != nil {
		t.Fatal("commit:", err)
	}

	up := uploader.New(srv.URL, func() (string, error) { return "tok", nil }, 100)
	if err := q.Drain(up.Send); err != nil {
		t.Fatal("drain:", err)
	}

	// ── 4) Assertions ─────────────────────────────────────────────────────────
	if got := receivedCount.Load(); got < 1 {
		t.Fatalf("server received %d events, want >= 1", got)
	}

	// Verify promptText is present in the payload
	if len(receivedBody) == 0 {
		t.Fatal("server received empty body")
	}
	var payload struct {
		Events []struct {
			PromptText string `json:"promptText"`
		} `json:"events"`
	}
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatal("parse body:", err)
	}
	if len(payload.Events) == 0 {
		t.Fatal("no events in payload")
	}
	if payload.Events[0].PromptText == "" {
		t.Fatalf("promptText empty in first event, body=%s", receivedBody)
	}

	t.Logf("E2E OK: %d event(s) uploaded, promptText=%q",
		receivedCount.Load(), payload.Events[0].PromptText)
}
