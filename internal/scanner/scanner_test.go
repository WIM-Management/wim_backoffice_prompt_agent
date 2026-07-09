package scanner

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/adapter/claudecode"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/model"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/state"
)

type fakeAdapter struct{ events int }

func (f fakeAdapter) path() string                 { return "/fake/s.jsonl" }
func (f fakeAdapter) Name() string                 { return "FAKE" }
func (f fakeAdapter) SessionPaths() ([]string, error) { return []string{f.path()}, nil }
func (f fakeAdapter) Parse(_ string, _ int64, _ time.Time) ([]model.Event, int64, error) {
	evs := make([]model.Event, f.events)
	return evs, 123, nil // newOffset>0
}

func stateStore(t *testing.T) *state.Store {
	return state.New(filepath.Join(t.TempDir(), "state.json"))
}

func TestScanOnceReturnsCommit(t *testing.T) {
	st := stateStore(t)
	sc := New([]model.Adapter{fakeAdapter{events: 2}}, st, 10*time.Minute)
	evs, commit := sc.ScanOnce()
	if len(evs) != 2 {
		t.Fatalf("want 2 events, got %d", len(evs))
	}
	// commit 전: 저장된 offset 변화 없음
	d0, _ := st.Load()
	if d0.Files[fakeAdapter{}.path()].Offset != 0 {
		t.Fatal("offset advanced before commit")
	}
	if err := commit(); err != nil {
		t.Fatal(err)
	}
	d1, _ := st.Load()
	if d1.Files[fakeAdapter{}.path()].Offset == 0 {
		t.Fatal("offset not advanced after commit")
	}
}

// 로테이션: Parse 가 fromOffset>filesize 면 0부터 재스캔(spec §4.2/§8). 어댑터 단위 테스트.
func TestClaudeCodeRotationReset(t *testing.T) {
	a := claudecode.New("") // Parse는 configDir 무관(경로 직접 지정)
	f := filepath.Join("..", "adapter", "claudecode", "testdata", "basic.jsonl")
	fi, _ := os.Stat(f)
	evs, _, err := a.Parse(f, fi.Size()+999, time.Time{}) // 과거 offset > 현재 크기
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) == 0 {
		t.Fatal("rotation reset failed: expected re-scan from 0")
	}
}
