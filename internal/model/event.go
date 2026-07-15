package model

import (
	"strings"
	"time"
)

type Event struct {
	SourceTool      string  `json:"sourceTool"`
	Surface         string  `json:"surface"`
	SessionID       string  `json:"sessionId,omitempty"`
	PromptText      string  `json:"promptText"`
	ResponseText    string  `json:"responseText,omitempty"`
	PromptTs        NaiveTS `json:"promptTs"`
	TokenCount      *int    `json:"tokenCount,omitempty"`
	ProjectContext  string  `json:"projectContext,omitempty"`
	ClientVersion   string  `json:"clientVersion"`
	Model           string  `json:"model,omitempty"`
}

// NaiveTS marshals as naive UTC "2006-01-02T15:04:05" (exported — 어댑터가 생성).
type NaiveTS time.Time

func (t NaiveTS) MarshalJSON() ([]byte, error) {
	return []byte(`"` + time.Time(t).UTC().Format("2006-01-02T15:04:05") + `"`), nil
}

func (t *NaiveTS) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	parsed, err := time.Parse("2006-01-02T15:04:05", s)
	if err != nil {
		return err
	}
	*t = NaiveTS(parsed)
	return nil
}

// Adapter: 로컬 도구별 세션 → Event. 구현은 internal/adapter/*.
type Adapter interface {
	Name() string                                                                        // sourceTool ("CLAUDE_CODE")
	SessionPaths() ([]string, error)                                                     // 글롭 결과(절대경로)
	Parse(file string, cursor []byte, idleCutoff time.Time) ([]Event, []byte, error) // settled만, 새 cursor
}
