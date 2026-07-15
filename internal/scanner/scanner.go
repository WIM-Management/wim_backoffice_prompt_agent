package scanner

import (
	"fmt"
	"os"
	"time"

	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/model"
	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/state"
)

type Scanner struct {
	adapters []model.Adapter
	store    *state.Store
	idle     time.Duration
}

func New(a []model.Adapter, s *state.Store, idle time.Duration) *Scanner {
	return &Scanner{a, s, idle}
}

// ScanOnce: 새 Event들과, '이번 스캔의 offset 전진'을 디스크에 반영하는 commit 함수를 반환한다.
// caller는 events를 영속 큐에 enqueue한 '뒤'에 commit()을 호출해 offset을 전진시켜야 유실 0(§4.5).
func (s *Scanner) ScanOnce() ([]model.Event, func() error) {
	d, _ := s.store.Load()
	idleCut := time.Now().Add(-s.idle)
	var all []model.Event
	pending := map[string]state.FileState{}
	for _, ad := range s.adapters {
		paths, err := ad.SessionPaths()
		if err != nil {
			fmt.Fprintf(os.Stderr, "SessionPaths 실패 [%s]: %v\n", ad.Name(), err)
			continue
		}
		for _, p := range paths {
			cur := d.Files[p]
			cursorArg := cur.Cursor
			if len(cursorArg) == 0 && cur.Offset > 0 {
				cursorArg = state.EncodeByteCursor(cur.Offset) // legacy migration: seed from old offset
			}
			evs, newCursor, err := ad.Parse(p, cursorArg, idleCut)
			if err != nil {
				fmt.Fprintf(os.Stderr, "수집 skip [%s %s]: %v\n", ad.Name(), p, err)
				continue
			}
			all = append(all, evs...)
			fs := cur // preserve Size/Mtime
			fs.Cursor = newCursor
			fs.Offset = state.DecodeByteCursor(newCursor, cur.Offset) // dual-write for legacy readers
			pending[p] = fs
		}
	}
	commit := func() error {
		cur, _ := s.store.Load()
		for p, fsNew := range pending {
			cur.Files[p] = fsNew
		}
		return s.store.Save(cur)
	}
	return all, commit
}
