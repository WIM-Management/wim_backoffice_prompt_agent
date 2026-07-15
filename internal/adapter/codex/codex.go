package codex

import (
	"bufio"
	"encoding/json"
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
	if fromOffset > fi.Size() {
		fromOffset = 0
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, cursor, err
	}
	defer f.Close()
	if _, err := f.Seek(fromOffset, 0); err != nil {
		return nil, cursor, err
	}

	br := bufio.NewReader(f)
	var lines []rawLine
	for {
		b, rerr := br.ReadBytes('\n')
		if len(b) > 0 {
			var rl rawLine
			if json.Unmarshal(b, &rl) == nil {
				lines = append(lines, rl)
			}
		}
		if rerr != nil {
			break
		}
	}

	fileEnd := state.EncodeByteCursor(fi.Size())

	// Parse session_meta from first line to determine if exec session.
	var meta sessionMetaPayload
	for _, l := range lines {
		if l.Type == "session_meta" {
			_ = json.Unmarshal(l.Payload, &meta)
			break
		}
	}
	if meta.Originator == "codex_exec" || meta.Source == "exec" {
		return nil, fileEnd, nil
	}

	// Interactive session: pair user input_text → assistant output_text.
	events := assembleEvents(lines, meta)
	return events, fileEnd, nil
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

func assembleEvents(lines []rawLine, meta sessionMetaPayload) []model.Event {
	type pendingPrompt struct {
		text  string
		ts    string
		model string
	}
	var out []model.Event
	var pending *pendingPrompt
	var curModel string

	for _, l := range lines {
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
				pending = &pendingPrompt{text: text, ts: l.Timestamp, model: curModel}
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
			pending = nil
		}
	}
	return out
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
