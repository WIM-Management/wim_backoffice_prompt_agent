package claudecode

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/model"
)

type Adapter struct{ configDir string }

// New builds an adapter that scans <configDir>/projects/*/*.jsonl.
// configDir is the resolved Claude config directory (e.g. ~/.claude, ~/.claude-melle).
func New(configDir string) *Adapter { return &Adapter{configDir: configDir} }
func (a *Adapter) Name() string     { return "CLAUDE_CODE" }
func (a *Adapter) SessionPaths() ([]string, error) {
	return filepath.Glob(filepath.Join(a.configDir, "projects", "*", "*.jsonl"))
}

var syntheticMarkers = []string{
	"<task-notification>", "<command-name>", "<command-message>", "<command-args>",
	"<local-command-stdout>", "<system-reminder>", "Caveat: The messages below",
}

// syntheticPrefixes: 하네스가 user 역할로 주입하지만 사람이 친 프롬프트가 아닌 블록.
// 태그가 없어 syntheticMarkers(Contains)로는 못 거른다. 이들은 항상 고정 문구로
// "시작"하므로 prefix로 판정한다 — 중간에 이 문구를 인용한 실제 프롬프트까지
// 잘못 제외하지 않기 위함(Contains 대신 HasPrefix).
var syntheticPrefixes = []string{
	"This session is being continued from a previous conversation", // 컴팩션 요약 주입
	"Base directory for this skill:",                               // 스킬 활성화 프리앰블
	"## Context Usage",                                             // /context 명령 렌더 출력
	"[Request interrupted by user",                                 // 인터럽트 마커(…]·… for tool use] 둘 다)
}

type rawLine struct {
	Type        string `json:"type"`
	IsSidechain bool   `json:"isSidechain"`
	SessionID   string `json:"sessionId"`
	Cwd         string `json:"cwd"`
	GitBranch   string `json:"gitBranch"`
	Entrypoint  string `json:"entrypoint"`
	Timestamp   string `json:"timestamp"`
	Message     struct {
		ID      string          `json:"id"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		Usage   *struct {
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		StopReason string `json:"stop_reason"`
	} `json:"message"`
}

// Parse: 줄을 읽어 settled 사람프롬프트→응답 페어만 Event로 반환. fromOffset 이후만.
// idleCutoff: 이보다 파일 mtime이 오래면 "파일 idle"로 보고 마지막 턴도 end_turn이면 방출.
func (a *Adapter) Parse(file string, fromOffset int64, idleCutoff time.Time) ([]model.Event, int64, error) {
	fi, err := os.Stat(file)
	if err != nil {
		return nil, fromOffset, err
	}
	if fromOffset > fi.Size() {
		fromOffset = 0 // 로테이션
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, fromOffset, err
	}
	defer f.Close()
	if _, err := f.Seek(fromOffset, 0); err != nil {
		return nil, fromOffset, err
	}

	fileIdle := fi.ModTime().Before(idleCutoff)

	// 줄 + 각 줄의 시작 바이트 오프셋(절대)을 함께 수집
	br := bufio.NewReader(f)
	var lines []rawLine
	var lineOffsets []int64
	cur := fromOffset
	for {
		b, rerr := br.ReadBytes('\n')
		if len(b) > 0 {
			var rl rawLine
			if json.Unmarshal(b, &rl) == nil {
				lines = append(lines, rl)
				lineOffsets = append(lineOffsets, cur)
			}
			cur += int64(len(b))
		}
		if rerr != nil {
			break // io.EOF 포함
		}
	}

	events, firstUnsettled := assemble(lines, fileIdle)
	newOffset := fi.Size() // 전부 settled → 끝까지 소비
	if firstUnsettled >= 0 && firstUnsettled < len(lineOffsets) {
		newOffset = lineOffsets[firstUnsettled] // 미완결 프롬프트 줄 '앞'에서 멈춤
	}
	return events, newOffset, nil
}

// assemble: lines → settled Event 목록 + firstUnsettled(미완결 첫 사람프롬프트의 lines 인덱스, 없으면 -1).
func assemble(lines []rawLine, fileIdle bool) ([]model.Event, int) {
	// 1) 사람 프롬프트 인덱스 + 텍스트 추출(default-deny)
	var humanIdx []int
	humanText := map[int]string{}
	for i, l := range lines {
		if l.Type != "user" || l.IsSidechain {
			continue
		}
		text, ok := humanPromptText(l.Message.Content)
		if !ok || isSynthetic(text) {
			continue
		}
		humanIdx = append(humanIdx, i)
		humanText[i] = text
	}

	var out []model.Event
	surface := firstEntrypoint(lines)
	firstUnsettled := -1

	for hi, idx := range humanIdx {
		endResp := len(lines)
		nextHuman := false
		if hi+1 < len(humanIdx) {
			endResp = humanIdx[hi+1]
			nextHuman = true
		}
		respLines := lines[idx+1 : endResp]
		if !nextHuman {
			if !fileIdle || !lastAssistantTerminal(respLines) {
				firstUnsettled = idx // 미완결 → 여기서 멈춤
				break
			}
		}
		resp, tokens := assembleResponse(respLines)
		l := lines[idx]
		out = append(out, model.Event{
			SourceTool:     "CLAUDE_CODE",
			Surface:        surface,
			SessionID:      l.SessionID,
			PromptText:     humanText[idx],
			ResponseText:   resp,
			PromptTs:       parseTS(l.Timestamp),
			TokenCount:     tokens,
			ProjectContext: l.Cwd + branchSuffix(l.GitBranch),
		})
	}
	return out, firstUnsettled
}

func humanPromptText(content json.RawMessage) (string, bool) {
	var s string
	if json.Unmarshal(content, &s) == nil {
		return s, true // 문자열
	}
	var blocks []map[string]any
	if json.Unmarshal(content, &blocks) == nil {
		var sb strings.Builder
		for _, b := range blocks {
			if b["type"] == "tool_result" {
				return "", false // 도구출력
			}
			if b["type"] == "text" {
				if t, ok := b["text"].(string); ok {
					sb.WriteString(t)
				}
			}
		}
		if sb.Len() > 0 {
			return sb.String(), true
		}
	}
	return "", false
}

func isSynthetic(text string) bool {
	for _, m := range syntheticMarkers {
		if strings.Contains(text, m) {
			return true
		}
	}
	trimmed := strings.TrimSpace(text)
	for _, p := range syntheticPrefixes {
		if strings.HasPrefix(trimmed, p) {
			return true
		}
	}
	return false
}

// assembleResponse: distinct message.id 단위로 text 1회·output_tokens 1회.
func assembleResponse(lines []rawLine) (string, *int) {
	seenText := map[string]bool{}
	seenTok := map[string]bool{}
	var sb strings.Builder
	total := 0
	hasTok := false
	for _, l := range lines {
		if l.Type != "assistant" || l.IsSidechain {
			continue
		}
		id := l.Message.ID
		if id == "" {
			id = "line:" + l.Timestamp // 폴백
		}
		if !seenTok[id] && l.Message.Usage != nil {
			total += l.Message.Usage.OutputTokens
			hasTok = true
			seenTok[id] = true
		}
		var blocks []map[string]any
		if json.Unmarshal(l.Message.Content, &blocks) == nil {
			for _, b := range blocks {
				if b["type"] != "text" {
					continue
				}
				t, _ := b["text"].(string)
				key := id + "|" + t
				if seenText[key] {
					continue
				}
				seenText[key] = true
				sb.WriteString(t)
			}
		}
	}
	if !hasTok {
		return sb.String(), nil
	}
	return sb.String(), &total
}

func lastAssistantTerminal(lines []rawLine) bool {
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i].Type == "assistant" && !lines[i].IsSidechain {
			sr := lines[i].Message.StopReason
			return sr == "end_turn" || sr == "stop_sequence"
		}
	}
	return false
}

func firstEntrypoint(lines []rawLine) string {
	for _, l := range lines {
		if l.Entrypoint != "" {
			return l.Entrypoint
		}
	}
	return "unknown"
}

func branchSuffix(b string) string {
	if b == "" {
		return ""
	}
	return " @" + b
}

func parseTS(s string) model.NaiveTS {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t = time.Time{}
	}
	return model.NaiveTS(t.UTC())
}
