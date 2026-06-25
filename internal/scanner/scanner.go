package scanner

import (
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
			continue
		}
		for _, p := range paths {
			evs, newOff, err := ad.Parse(p, d.Files[p].Offset, idleCut)
			if err != nil {
				continue
			}
			all = append(all, evs...)
			pending[p] = state.FileState{Offset: newOff}
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
