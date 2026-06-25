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

	// Desktop OAuth client for enroll (Google "Desktop app" client — the secret
	// is non-confidential by design for installed apps). Set via env after the
	// client is created in Google Cloud.
	GoogleClientID     string
	GoogleClientSecret string
	GoogleHostedDomain string
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

	hd := os.Getenv("WIM_PROMPT_GOOGLE_HD")
	if hd == "" {
		hd = "wimcorp.co.kr"
	}

	return Config{
		BaseURL:            base,
		ScanInterval:       15 * time.Minute,
		IdleCutoff:         10 * time.Minute,
		Dir:                dir,
		GoogleClientID:     os.Getenv("WIM_PROMPT_GOOGLE_CLIENT_ID"),
		GoogleClientSecret: os.Getenv("WIM_PROMPT_GOOGLE_CLIENT_SECRET"),
		GoogleHostedDomain: hd,
	}
}
