package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/model"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/state"
)

type Adapter struct{ home string }

func New(home string) *Adapter { return &Adapter{home} }
func (a *Adapter) Name() string { return "CODEX" }
func (a *Adapter) SessionPaths() ([]string, error) {
	return filepath.Glob(filepath.Join(a.home, ".codex", "sessions", "*", "*", "*", "rollout-*.jsonl"))
}

type rawLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type sessionMetaPayload struct {
	ID         string `json:"id"`
	Cwd        string `json:"cwd"`
	Originator string `json:"originator"`
	Source     string `json:"source"`
}

type responseItemPayload struct {
	Type    string            `json:"type"`
	Role    string            `json:"role"`
	Content []contentFragment `json:"content"`
}

type turnContextPayload struct {
	Model string `json:"model"`
	Cwd   string `json:"cwd"`
}

type contentFragment struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func parseTS(s string) model.NaiveTS {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t = time.Time{}
	}
	return model.NaiveTS(t.UTC())
}

func (a *Adapter) Parse(file string, cursor []byte, idleCutoff time.Time) ([]model.Event, []byte, error) {
	fromOffset := state.DecodeByteCursor(cursor, 0)

	fi, err := os.Stat(file)
	if err != nil {
		return nil, cursor, err
	}
	// Rotation guard: only affects the emission gate (fromOffset), not the read.
	if fromOffset > fi.Size() {
		fromOffset = 0
	}

	// Always read from offset 0: session_meta is only on line 1 and must be
	// available on every scan regardless of the fromOffset emission gate.
	f, err := os.Open(file)
	if err != nil {
		return nil, cursor, err
	}
	defer f.Close()

	fileIdle := fi.ModTime().Before(idleCutoff)

	// Collect lines and their start byte offsets while reading from the top.
	br := bufio.NewReader(f)
	var lines []rawLine
	var lineOffsets []int64
	var pos int64
	for {
		b, rerr := br.ReadBytes('\n')
		if len(b) > 0 {
			var rl rawLine
			if json.Unmarshal(b, &rl) == nil {
				lines = append(lines, rl)
				lineOffsets = append(lineOffsets, pos)
			}
			pos += int64(len(b))
		}
		if rerr != nil {
			break
		}
	}

	fileEnd := state.EncodeByteCursor(fi.Size())

	// Parse session_meta (always at line 0; reading from 0 guarantees it's present).
	var meta sessionMetaPayload
	foundMeta := false
	for _, l := range lines {
		if l.Type == "session_meta" {
			_ = json.Unmarshal(l.Payload, &meta)
			foundMeta = true
			break
		}
	}
	if !foundMeta {
		return nil, cursor, fmt.Errorf("unsupported codex format (no session_meta): %s", file)
	}
	if meta.Originator == "codex_exec" || meta.Source == "exec" {
		return nil, fileEnd, nil
	}

	// Interactive session: pair user input_text → assistant output_text.
	// assembleEvents returns the firstUnsettledOffset (-1 if everything settled).
	events, firstUnsettledOffset := assembleEvents(lines, lineOffsets, meta, fromOffset, fileIdle)

	newOffset := fi.Size()
	if firstUnsettledOffset >= 0 {
		newOffset = firstUnsettledOffset
	}
	return events, state.EncodeByteCursor(newOffset), nil
}

// isCodexSynthetic returns true for harness-injected messages that are not
// genuine human prompts. Developer-role messages are always synthetic.
// For user-role messages, a HasPrefix check on the trimmed text identifies
// known injection preambles (never Contains, to avoid false positives on
// quoted text mid-message).
func isCodexSynthetic(role, text string) bool {
	if role == "developer" {
		return true
	}
	t := strings.TrimSpace(text)
	injectedPrefixes := []string{
		"<environment_context>",
		"<permissions instructions>",
		"<turn_aborted>",
		"<user_instructions>",
		"<apps_instructions>",
		"<skills_instructions>",
		"<collaboration_mode>",
		"<plugins_instructions>",
		"<user_shell_command>",
		"# AGENTS.md instructions",
	}
	for _, pfx := range injectedPrefixes {
		if strings.HasPrefix(t, pfx) {
			return true
		}
	}
	return false
}

// assembleEvents pairs user input_text → assistant output_text lines, applying:
//   - Emission gate: only emit pairs whose user prompt line offset >= fromOffset
//     (skips already-emitted turns from prior scans).
//   - Settle-gating: a trailing unpaired user prompt is held back (cursor stops
//     before it) unless fileIdle is true, in which case it is emitted as-is.
//
// Returns (events, firstUnsettledOffset) where firstUnsettledOffset is -1 when
// everything is settled (cursor may advance to fileEnd).
func assembleEvents(lines []rawLine, lineOffsets []int64, meta sessionMetaPayload, fromOffset int64, fileIdle bool) ([]model.Event, int64) {
	type pendingPrompt struct {
		text       string
		ts         string
		model      string
		lineOffset int64 // byte offset of the user prompt line
	}
	var out []model.Event
	var pending *pendingPrompt
	var curModel string

	for i, l := range lines {
		// Skip compacted records entirely — their replacement_history would
		// double-count history already emitted in prior real records.
		if l.Type == "compacted" {
			continue
		}
		// Track the most recent model seen before any prompt.
		if l.Type == "turn_context" {
			var tcp turnContextPayload
			if json.Unmarshal(l.Payload, &tcp) == nil && tcp.Model != "" {
				curModel = tcp.Model
			}
			continue
		}
		if l.Type != "response_item" {
			continue
		}
		var rip responseItemPayload
		if json.Unmarshal(l.Payload, &rip) != nil {
			continue
		}
		if rip.Type != "message" {
			continue
		}

		switch rip.Role {
		case "user":
			text := joinFragments(rip.Content, "input_text")
			if text != "" && !isCodexSynthetic("user", text) {
				pending = &pendingPrompt{
					text:       text,
					ts:         l.Timestamp,
					model:      curModel,
					lineOffset: lineOffsets[i],
				}
			}
		case "developer":
			// Always synthetic — skip without touching pending.
		case "assistant":
			if pending == nil {
				continue
			}
			text := joinFragments(rip.Content, "output_text")
			if text == "" {
				continue
			}
			// Emit only if the user prompt line is at or beyond fromOffset
			// (prior turns have already been emitted in a previous scan).
			if pending.lineOffset >= fromOffset {
				out = append(out, model.Event{
					SourceTool:     "CODEX",
					Surface:        "cli",
					SessionID:      meta.ID,
					PromptText:     pending.text,
					ResponseText:   text,
					PromptTs:       parseTS(pending.ts),
					ProjectContext: meta.Cwd,
					Model:          pending.model,
				})
			}
			pending = nil
		}
	}

	// Settle-gating: trailing unpaired user prompt.
	if pending != nil {
		if fileIdle {
			// Session is done: emit even without a response (idle flush).
			if pending.lineOffset >= fromOffset {
				out = append(out, model.Event{
					SourceTool:     "CODEX",
					Surface:        "cli",
					SessionID:      meta.ID,
					PromptText:     pending.text,
					ResponseText:   "",
					PromptTs:       parseTS(pending.ts),
					ProjectContext: meta.Cwd,
					Model:          pending.model,
				})
			}
			return out, -1 // settled by idle; cursor advances to fileEnd
		}
		// Assistant still generating: stop cursor before the user prompt line
		// so it will be re-read on the next scan.
		return out, pending.lineOffset
	}

	return out, -1 // everything settled
}

func joinFragments(frags []contentFragment, fragType string) string {
	var sb strings.Builder
	for _, f := range frags {
		if f.Type == fragType {
			sb.WriteString(f.Text)
		}
	}
	return sb.String()
}
