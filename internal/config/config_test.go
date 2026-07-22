package config

import (
	"os"
	"testing"
)

func TestDefaultEnrollURL(t *testing.T) {
	os.Unsetenv("WIM_PROMPT_ENROLL_URL")
	if got := Default().EnrollURL; got != "https://backoffice.wimcorp.co.kr/prompt-agent/enroll" {
		t.Fatalf("default EnrollURL = %q", got)
	}
	os.Setenv("WIM_PROMPT_ENROLL_URL", "https://x.example/e")
	defer os.Unsetenv("WIM_PROMPT_ENROLL_URL")
	if got := Default().EnrollURL; got != "https://x.example/e" {
		t.Fatalf("env override EnrollURL = %q", got)
	}
}
