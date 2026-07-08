package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLastUpdateCheckRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := New(filepath.Join(dir, "state.json"))
	ts := time.Date(2026, 7, 9, 3, 0, 0, 0, time.UTC)

	d, _ := s.Load()
	d.LastUpdateCheck = ts
	if err := s.Save(d); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, _ := s.Load()
	if !got.LastUpdateCheck.Equal(ts) {
		t.Errorf("LastUpdateCheck = %v, want %v", got.LastUpdateCheck, ts)
	}
}

func TestLegacyStateHasZeroLastUpdateCheck(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	// 옛 포맷(필드 없음)
	os.WriteFile(path, []byte(`{"files":{},"lastSentTs":{}}`), 0o600)
	got, _ := New(path).Load()
	if !got.LastUpdateCheck.IsZero() {
		t.Errorf("legacy LastUpdateCheck = %v, want zero", got.LastUpdateCheck)
	}
}
