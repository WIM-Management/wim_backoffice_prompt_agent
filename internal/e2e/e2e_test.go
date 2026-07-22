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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/adapter/claudecode"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/adapter/codex"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/model"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/queue"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/redact"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/scanner"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/state"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/uploader"
)

// multiToolEvent is the JSON shape captured from the upload server in TestEndToEndMultiTool.
type multiToolEvent struct {
	SourceTool     string `json:"sourceTool"`
	PromptText     string `json:"promptText"`
	Model          string `json:"model"`
	ProjectContext string `json:"projectContext"`
}

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

// claudeMultiToolJSONL is a settled Claude turn whose human prompt contains a
// GitHub PAT (ghp_ token) to prove secret masking runs through the production
// redact.Scrub path. The assistant line carries "model":"claude-opus-4-8".
// The trailing second human line settles the first turn (same pattern as basicJSONL).
const claudeMultiToolJSONL = `{"type":"user","isSidechain":false,"sessionId":"s-mt","cwd":"/repo","gitBranch":"main","timestamp":"2026-06-24T09:00:00Z","message":{"role":"user","content":"내 토큰은 ghp_abcdefghijklmnopqrstuvwxyz0123 이야"}}
{"type":"assistant","isSidechain":false,"sessionId":"s-mt","timestamp":"2026-06-24T09:00:05Z","message":{"id":"m-mt1","role":"assistant","model":"claude-opus-4-8","content":[{"type":"text","text":"알겠습니다."}],"usage":{"output_tokens":5},"stop_reason":"end_turn"}}
{"type":"user","isSidechain":false,"sessionId":"s-mt","timestamp":"2026-06-24T09:01:00Z","message":{"role":"user","content":"고마워"}}
`

// codexMultiToolJSONL is a single settled Codex interactive session: session_meta
// → turn_context (model) → user response_item → assistant response_item.
// One user→assistant pair emits exactly 1 event with Model=gpt-5.3-codex-spark.
const codexMultiToolJSONL = `{"timestamp":"2026-06-24T09:00:00Z","type":"session_meta","payload":{"id":"cdx-1","cwd":"/repo","originator":"codex-tui","source":"cli"}}
{"timestamp":"2026-06-24T09:00:01Z","type":"turn_context","payload":{"model":"gpt-5.3-codex-spark","cwd":"/repo"}}
{"timestamp":"2026-06-24T09:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"코덱스 프롬프트"}]}}
{"timestamp":"2026-06-24T09:00:05Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"코덱스 응답"}]}}
`

func TestEndToEndMultiTool(t *testing.T) {
	// ── 1) Temp HOME with fixtures for all adapters ───────────────────────────
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Claude: tmp/.claude/projects/proj/sess.jsonl
	claudeSessDir := filepath.Join(tmp, ".claude", "projects", "proj")
	if err := os.MkdirAll(claudeSessDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeSessDir, "sess.jsonl"), []byte(claudeMultiToolJSONL), 0o600); err != nil {
		t.Fatal(err)
	}

	// Codex: tmp/.codex/sessions/2026/06/24/rollout-2026-06-24T09-00-00-abc.jsonl
	codexSessDir := filepath.Join(tmp, ".codex", "sessions", "2026", "06", "24")
	if err := os.MkdirAll(codexSessDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexSessDir, "rollout-2026-06-24T09-00-00-abc.jsonl"), []byte(codexMultiToolJSONL), 0o600); err != nil {
		t.Fatal(err)
	}

	// ── 2) Mock PATCH server: collect all events across batches ───────────────
	var mu sync.Mutex
	var allBodies [][]byte
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
		mu.Lock()
		allBodies = append(allBodies, body)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// ── 3) Wire pipeline: all adapters, exactly like TestEndToEnd ─────────────
	stateFile := filepath.Join(tmp, "state.json")
	queueDir := filepath.Join(tmp, "queue-mt")

	sc := scanner.New(
		[]model.Adapter{
			claudecode.New(filepath.Join(tmp, ".claude")),
			codex.New(tmp),
		},
		state.New(stateFile),
		10*time.Minute,
	)

	evs, commit := sc.ScanOnce()

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

	// ── 4) Decode all received events ─────────────────────────────────────────
	var received []multiToolEvent
	mu.Lock()
	bodies := allBodies
	mu.Unlock()

	for _, body := range bodies {
		var payload struct {
			Events []multiToolEvent `json:"events"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("parse server body: %v\nbody=%s", err, body)
		}
		received = append(received, payload.Events...)
	}

	// ── 5) Assertions ─────────────────────────────────────────────────────────

	// Total event count must be exactly 2 (one per tool).
	if len(received) != 2 {
		t.Fatalf("want 2 events (one per tool), got %d; events=%+v", len(received), received)
	}

	// Collect sourceTool → event for per-tool assertions.
	byTool := make(map[string]multiToolEvent, 2)
	for _, ev := range received {
		byTool[ev.SourceTool] = ev
	}

	// Both source tools must be present.
	for _, want := range []string{"CLAUDE_CODE", "CODEX"} {
		if _, ok := byTool[want]; !ok {
			t.Errorf("missing sourceTool %q in received events; got tools: %v", want, toolKeys(byTool))
		}
	}

	// Per-tool model assertions.
	wantModels := map[string]string{
		"CLAUDE_CODE": "claude-opus-4-8",
		"CODEX":       "gpt-5.3-codex-spark",
	}
	for tool, wantModel := range wantModels {
		ev, ok := byTool[tool]
		if !ok {
			continue // already reported above
		}
		if ev.Model == "" {
			t.Errorf("tool %s: model is empty, want %q", tool, wantModel)
		} else if ev.Model != wantModel {
			t.Errorf("tool %s: model=%q, want %q", tool, ev.Model, wantModel)
		}
	}

	// Secret masking: claude event must have «REDACTED» and must NOT contain the raw token.
	const rawSecret = "ghp_abcdefghijklmnopqrstuvwxyz0123"
	if claudeEv, ok := byTool["CLAUDE_CODE"]; ok {
		if !strings.Contains(claudeEv.PromptText, "«REDACTED»") {
			t.Errorf("CLAUDE_CODE promptText missing «REDACTED»; got %q", claudeEv.PromptText)
		}
		if strings.Contains(claudeEv.PromptText, rawSecret) {
			t.Errorf("CLAUDE_CODE promptText still contains raw secret %q", rawSecret)
		}
	}

	// projectContext must be non-empty for Claude.
	for _, tool := range []string{"CLAUDE_CODE"} {
		if ev, ok := byTool[tool]; ok && ev.ProjectContext == "" {
			t.Errorf("tool %s: projectContext is empty", tool)
		}
	}

	t.Logf("MultiTool E2E OK: %d events; tools=%v", len(received), toolKeys(byTool))
	for tool, ev := range byTool {
		t.Logf("  %s: model=%q projectContext=%q promptText=%q", tool, ev.Model, ev.ProjectContext, ev.PromptText)
	}
}

func toolKeys(m map[string]multiToolEvent) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
