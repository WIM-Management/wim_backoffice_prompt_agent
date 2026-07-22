package config

import (
	"os"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	// Clear env override so we test the real default.
	os.Unsetenv("WIM_PROMPT_BASE_URL")

	cfg := Default()

	if cfg.Dir == "" {
		t.Error("Dir must not be empty")
	}

	if cfg.ScanInterval != 15*time.Minute {
		t.Errorf("ScanInterval: want 15m, got %v", cfg.ScanInterval)
	}

	if cfg.IdleCutoff != 10*time.Minute {
		t.Errorf("IdleCutoff: want 10m, got %v", cfg.IdleCutoff)
	}

	if cfg.BaseURL == "" {
		t.Error("BaseURL must not be empty")
	}
}

func TestDefaultConfig_EnvOverride(t *testing.T) {
	os.Setenv("WIM_PROMPT_BASE_URL", "https://example.com")
	defer os.Unsetenv("WIM_PROMPT_BASE_URL")

	cfg := Default()

	if cfg.BaseURL != "https://example.com" {
		t.Errorf("BaseURL env override: want https://example.com, got %v", cfg.BaseURL)
	}
}

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
