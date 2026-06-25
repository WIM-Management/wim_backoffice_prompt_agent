package redact_test

import (
	"strings"
	"testing"

	"github.com/WIM-Management/wim_backoffice_prompt_agent/internal/redact"
)

func TestScrub(t *testing.T) {
	in := "key sk-ant-abc123DEF456ghi789jkl012mno345 and -----BEGIN PRIVATE KEY-----\nx\n-----END PRIVATE KEY-----"
	out := redact.Scrub(in)
	if strings.Contains(out, "sk-ant-abc123") || strings.Contains(out, "BEGIN PRIVATE KEY") {
		t.Fatalf("not scrubbed: %s", out)
	}
}

func TestScrub_GitHubToken(t *testing.T) {
	in := "token ghp_abcdefghijklmnopqrstuvwxyz1234"
	out := redact.Scrub(in)
	if strings.Contains(out, "ghp_") {
		t.Fatalf("github token not scrubbed: %s", out)
	}
}

func TestScrub_AWSKey(t *testing.T) {
	in := "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE1234"
	out := redact.Scrub(in)
	if strings.Contains(out, "AKIA") {
		t.Fatalf("AWS key not scrubbed: %s", out)
	}
}

func TestScrub_NoFalsePositives(t *testing.T) {
	in := "hello world, this is a normal string"
	out := redact.Scrub(in)
	if out != in {
		t.Fatalf("false positive scrub: %s", out)
	}
}
