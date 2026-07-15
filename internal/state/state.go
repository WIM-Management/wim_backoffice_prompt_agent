package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type FileState struct {
	Offset int64  `json:"offset"`
	Size   int64  `json:"size"`
	Mtime  int64  `json:"mtime"`
	Cursor []byte `json:"cursor,omitempty"`
}

type Data struct {
	Files           map[string]FileState `json:"files"`
	LastSentTs      map[string]string    `json:"lastSentTs"`
	LastUpdateCheck time.Time            `json:"lastUpdateCheck"`
}

type Store struct {
	path string
	mu   sync.Mutex
}

func New(path string) *Store { return &Store{path: path} }

func (s *Store) Load() (Data, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := Data{Files: map[string]FileState{}, LastSentTs: map[string]string{}}
	b, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	_ = json.Unmarshal(b, &d)
	if d.Files == nil {
		d.Files = map[string]FileState{}
	}
	return d, nil
}

func (s *Store) Save(d Data) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = os.MkdirAll(filepath.Dir(s.path), 0o700)
	b, _ := json.MarshalIndent(d, "", "  ")
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
