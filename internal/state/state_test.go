package state

import (
	"encoding/json"
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

func TestDecodeByteCursorLegacySeed(t *testing.T) {
	// legacy state.json has no cursor field — simulate by unmarshalling old format
	raw := `{"files":{"/x":{"offset":100}}}`
	var d Data
	d.Files = map[string]FileState{}
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatal(err)
	}
	fs := d.Files["/x"]
	// Cursor is nil/empty; DecodeByteCursor should fall back to Offset
	got := DecodeByteCursor(fs.Cursor, fs.Offset)
	if got != 100 {
		t.Fatalf("want 100, got %d", got)
	}
}

func TestEncodeByteCursorRoundTrip(t *testing.T) {
	cur := EncodeByteCursor(250)
	got := DecodeByteCursor(cur, 0)
	if got != 250 {
		t.Fatalf("want 250, got %d", got)
	}
}
