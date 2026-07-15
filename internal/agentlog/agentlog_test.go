package agentlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrintfWritesTimestampedLine(t *testing.T) {
	dir := t.TempDir()
	Setup(dir)

	Printf("hello %s", "world")

	b, err := os.ReadFile(filepath.Join(dir, "agent.log"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	line := string(b)
	if !strings.Contains(line, "hello world") {
		t.Errorf("log missing message: %q", line)
	}
	// Leading timestamp "YYYY-MM-DD HH:MM:SS " then the message.
	if len(line) < 20 || line[4] != '-' || line[7] != '-' {
		t.Errorf("log line not timestamped: %q", line)
	}
}

func TestPrintfAppends(t *testing.T) {
	dir := t.TempDir()
	Setup(dir)

	Printf("one")
	Printf("two")

	b, _ := os.ReadFile(filepath.Join(dir, "agent.log"))
	if got := strings.Count(string(b), "\n"); got != 2 {
		t.Errorf("want 2 lines, got %d: %q", got, b)
	}
}

func TestRotateFallsBackToTruncateWhenRenameFails(t *testing.T) {
	dir := t.TempDir()
	Setup(dir)
	logPath := filepath.Join(dir, "agent.log")

	// Occupy agent.log.1 with a non-empty directory so os.Rename can't clobber
	// it → the truncate fallback must fire instead of unbounded growth.
	if err := os.MkdirAll(logPath+".1/blocker", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, make([]byte, maxLogBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	Printf("after truncate")

	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if len(b) > maxLogBytes {
		t.Errorf("cap not enforced: log is %d bytes", len(b))
	}
	if !strings.Contains(string(b), "after truncate") {
		t.Errorf("truncated log should hold the new line, got %d bytes", len(b))
	}
}

func TestRotateAtSizeCap(t *testing.T) {
	dir := t.TempDir()
	Setup(dir)
	logPath := filepath.Join(dir, "agent.log")

	// Seed a log already over the cap so the next write triggers rollover.
	if err := os.WriteFile(logPath, make([]byte, maxLogBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	Printf("after rollover")

	if _, err := os.Stat(logPath + ".1"); err != nil {
		t.Errorf("expected rolled-over agent.log.1: %v", err)
	}
	b, _ := os.ReadFile(logPath)
	if !strings.Contains(string(b), "after rollover") {
		t.Errorf("fresh log should hold post-rollover line, got %q", b)
	}
	if strings.Count(string(b), "\n") != 1 {
		t.Errorf("fresh log should have exactly the one new line, got %q", b)
	}
}
