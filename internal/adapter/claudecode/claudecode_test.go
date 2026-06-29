package claudecode

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/model"
)

func parseFixture(t *testing.T, name string, idle time.Time) []model.Event {
	t.Helper()
	a := New()
	evs, _, err := a.Parse(filepath.Join("testdata", name), 0, idle)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func TestBasic(t *testing.T) {
	evs := parseFixture(t, "basic.jsonl", time.Time{}) // idle 무관(다음 사람프롬프트로 settled)
	if len(evs) != 1 {
		t.Fatalf("want 1, got %d", len(evs))
	}
	e := evs[0]
	if e.PromptText != "버그 고쳐줘" || e.ResponseText != "고쳤습니다." {
		t.Fatalf("%+v", e)
	}
	if e.Surface != "cli" && e.Surface != "unknown" {
		t.Fatalf("surface %q", e.Surface)
	}
	if e.ProjectContext == "" {
		t.Fatal("projectContext empty")
	}
}

func TestSyntheticDropped(t *testing.T) {
	evs := parseFixture(t, "synthetic.jsonl", time.Time{})
	if len(evs) != 1 || evs[0].PromptText != "진짜 사람 질문" {
		t.Fatalf("synthetic leaked: %+v", evs)
	}
}

func TestHarnessInjectionPrefixesSynthetic(t *testing.T) {
	// 태그 없이 user 역할로 주입되는 하네스 블록 — prefix로 제외돼야 한다.
	synthetic := []string{
		"This session is being continued from a previous conversation that ran out of context. The summary below covers the earlier portion.",
		"Base directory for this skill: /Users/x/.claude/plugins/superpowers/skills/foo\n\n# Foo",
		"## Context Usage\n\n**Model:** claude-opus-4-8[1m]  \n**Tokens:** 91.8k / 1m (9%)",
	}
	for _, s := range synthetic {
		if !isSynthetic(s) {
			t.Errorf("주입 블록이 합성으로 안 걸림: %.60q", s)
		}
	}
	// 음성: 같은 문구를 중간에 인용/언급한 실제 프롬프트는 제외하면 안 된다(prefix라서 안전).
	real := []string{
		"컨텍스트 사용량(## Context Usage) 좀 알려줘",
		"이 스킬의 base directory for this skill 경로 어디야?",
		"방금 previous conversation 내용 요약해줘",
	}
	for _, s := range real {
		if isSynthetic(s) {
			t.Errorf("실제 프롬프트를 합성으로 오판: %q", s)
		}
	}
}

func TestMultilineMessageGrouping(t *testing.T) {
	evs := parseFixture(t, "multiline_msg.jsonl", time.Time{})
	if len(evs) != 1 {
		t.Fatalf("want 1, got %d", len(evs))
	}
	if evs[0].ResponseText != "최종 답" {
		t.Fatalf("response dup/wrong: %q", evs[0].ResponseText)
	}
	if evs[0].TokenCount == nil || *evs[0].TokenCount != 50 {
		t.Fatalf("token not once: %v", evs[0].TokenCount)
	} // 줄 단위였다면 100
}

func TestArrayContent(t *testing.T) {
	evs := parseFixture(t, "array_content.jsonl", time.Time{})
	if len(evs) != 1 || evs[0].PromptText != "이 이미지 봐줘" {
		t.Fatalf("%+v", evs)
	}
}

func TestInterruptedNotEmittedEvenIfIdle(t *testing.T) {
	evs := parseFixture(t, "interrupted.jsonl", time.Now().Add(time.Hour)) // idleCutoff 미래 = 파일 idle 취급
	if len(evs) != 0 {
		t.Fatalf("interrupted turn emitted: %+v", evs)
	}
}
