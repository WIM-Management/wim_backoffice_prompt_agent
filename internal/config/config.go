package config

import (
	"os"
	"path/filepath"
	"time"
)

// Config holds runtime configuration for wim-prompt-agent.
type Config struct {
	BaseURL      string
	ScanInterval time.Duration
	IdleCutoff   time.Duration
	Dir          string
}

// Default returns sensible production defaults.
// Override WIM_PROMPT_BASE_URL to point at a different backend.
func Default() Config {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".wim-prompt-agent")

	base := os.Getenv("WIM_PROMPT_BASE_URL")
	if base == "" {
		base = "https://staging-backoffice-api.wimcorp.co.kr"
	}

	return Config{
		BaseURL:      base,
		ScanInterval: 15 * time.Minute,
		IdleCutoff:   10 * time.Minute,
		Dir:          dir,
	}
}
