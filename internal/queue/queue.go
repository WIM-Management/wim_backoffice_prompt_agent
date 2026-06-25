package queue

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/model"
)

type Queue struct {
	dir string
	mu  sync.Mutex
	seq int
}

func New(dir string) *Queue {
	_ = os.MkdirAll(dir, 0o700)
	return &Queue{dir: dir}
}

func (q *Queue) Enqueue(evs []model.Event) error {
	if len(evs) == 0 {
		return nil
	}
	q.mu.Lock()
	q.seq++
	name := filepath.Join(q.dir, time.Now().UTC().Format("20060102T150405.000000")+"-"+strconv.Itoa(q.seq)+".json")
	q.mu.Unlock()

	b, _ := json.Marshal(evs)
	tmp := name + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, name)
}

func (q *Queue) Drain(fn func([]model.Event) error) error {
	files, _ := filepath.Glob(filepath.Join(q.dir, "*.json"))
	sort.Strings(files)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var evs []model.Event
		if json.Unmarshal(b, &evs) != nil {
			_ = os.Remove(f)
			continue
		}
		if err := fn(evs); err != nil {
			return err // 실패 시 파일 보존(다음에 재시도)
		}
		_ = os.Remove(f)
	}
	return nil
}
