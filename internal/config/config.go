package config

import (
	"os"
	"path/filepath"
	"time"
)

// Config holds runtime configuration for wim-backoffice-prompt-agent.
type Config struct {
	BaseURL      string
	ScanInterval time.Duration
	IdleCutoff   time.Duration
	Dir          string

	// EnrollURL: 웹 로그인으로 Google id_token을 발급/표시하는 페이지.
	// enroll 프롬프트가 사용자에게 안내한다. WIM_PROMPT_ENROLL_URL로 오버라이드.
	EnrollURL string
}

// Default returns sensible production defaults.
// Override WIM_PROMPT_BASE_URL / WIM_PROMPT_ENROLL_URL to point elsewhere.
func Default() Config {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".wim-backoffice-prompt-agent")

	base := os.Getenv("WIM_PROMPT_BASE_URL")
	if base == "" {
		base = "https://backoffice-api.wimcorp.co.kr"
	}

	enrollURL := os.Getenv("WIM_PROMPT_ENROLL_URL")
	if enrollURL == "" {
		enrollURL = "https://backoffice.wimcorp.co.kr/prompt-agent/enroll"
	}

	return Config{
		BaseURL:      base,
		ScanInterval: 15 * time.Minute,
		IdleCutoff:   10 * time.Minute,
		Dir:          dir,
		EnrollURL:    enrollURL,
	}
}
