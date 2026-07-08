package config

import (
	"os"
	"path/filepath"
	"time"
)

// 릴리스 빌드 시 ldflags로 주입되는 내장 OAuth 클라이언트 기본값.
// 데스크톱 앱 클라이언트의 secret은 Google 정책상 기밀이 아니므로 바이너리 내장이 허용된다.
// env(WIM_PROMPT_GOOGLE_CLIENT_ID/SECRET)가 설정돼 있으면 env가 우선한다(로컬 개발·클라이언트 교체용).
//
//	go build -ldflags "-X .../internal/config.DefaultGoogleClientID=<id> -X .../internal/config.DefaultGoogleClientSecret=<secret>"
var (
	DefaultGoogleClientID     = ""
	DefaultGoogleClientSecret = ""
)

// Config holds runtime configuration for wim-backoffice-prompt-agent.
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
	dir := filepath.Join(home, ".wim-backoffice-prompt-agent")

	base := os.Getenv("WIM_PROMPT_BASE_URL")
	if base == "" {
		base = "https://backoffice-api.wimcorp.co.kr"
	}

	hd := os.Getenv("WIM_PROMPT_GOOGLE_HD")
	if hd == "" {
		hd = "wimcorp.co.kr"
	}

	clientID := os.Getenv("WIM_PROMPT_GOOGLE_CLIENT_ID")
	if clientID == "" {
		clientID = DefaultGoogleClientID
	}
	clientSecret := os.Getenv("WIM_PROMPT_GOOGLE_CLIENT_SECRET")
	if clientSecret == "" {
		clientSecret = DefaultGoogleClientSecret
	}

	return Config{
		BaseURL:            base,
		ScanInterval:       15 * time.Minute,
		IdleCutoff:         10 * time.Minute,
		Dir:                dir,
		GoogleClientID:     clientID,
		GoogleClientSecret: clientSecret,
		GoogleHostedDomain: hd,
	}
}
