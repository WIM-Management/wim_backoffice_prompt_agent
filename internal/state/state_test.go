package state

import (
	"path/filepath"
	"testing"
)

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "state.json"))
	st, _ := s.Load()
	st.Files["/a.jsonl"] = FileState{Offset: 10, Size: 100}
	if err := s.Save(st); err != nil {
		t.Fatal(err)
	}
	st2, _ := s.Load()
	if st2.Files["/a.jsonl"].Offset != 10 {
		t.Fatal("offset lost")
	}
}
