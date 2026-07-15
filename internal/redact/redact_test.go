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

// --- New pattern positive tests ---

func TestScrub_GoogleOAuthAccessToken(t *testing.T) {
	in := "access_token: ya29.aBcD1234EfGhIjKlMnOpQrStUvWxYz"
	out := redact.Scrub(in)
	if strings.Contains(out, "ya29.") {
		t.Fatalf("Google OAuth access token not scrubbed: %s", out)
	}
}

func TestScrub_GoogleOAuthRefreshToken(t *testing.T) {
	in := "refresh_token: 1//0aBcDeFgHiJkLmNoPqRsTuVwXyZ"
	out := redact.Scrub(in)
	if strings.Contains(out, "1//0") {
		t.Fatalf("Google OAuth refresh token not scrubbed: %s", out)
	}
}

func TestScrub_BearerToken(t *testing.T) {
	in := "Authorization: Bearer sk_live_abc123def456"
	out := redact.Scrub(in)
	if strings.Contains(out, "sk_live_abc123") {
		t.Fatalf("Bearer token not scrubbed: %s", out)
	}
}

func TestScrub_BearerTokenCaseInsensitive(t *testing.T) {
	in := "authorization: bearer MySecretToken123"
	out := redact.Scrub(in)
	if strings.Contains(out, "MySecretToken123") {
		t.Fatalf("bearer token (lowercase) not scrubbed: %s", out)
	}
}

func TestScrub_ApiKeyAssignment(t *testing.T) {
	in := "api_key=SECRET123"
	out := redact.Scrub(in)
	if strings.Contains(out, "SECRET123") {
		t.Fatalf("api_key assignment not scrubbed: %s", out)
	}
}

func TestScrub_ApiKeyDashAssignment(t *testing.T) {
	in := "api-key=TOPSECRET456"
	out := redact.Scrub(in)
	if strings.Contains(out, "TOPSECRET456") {
		t.Fatalf("api-key assignment not scrubbed: %s", out)
	}
}

func TestScrub_PasswordAssignment(t *testing.T) {
	in := "password=hunter2"
	out := redact.Scrub(in)
	if strings.Contains(out, "hunter2") {
		t.Fatalf("password assignment not scrubbed: %s", out)
	}
}

func TestScrub_TokenAssignmentWithSpaces(t *testing.T) {
	in := "token = abc.def"
	out := redact.Scrub(in)
	if strings.Contains(out, "abc.def") {
		t.Fatalf("token assignment with spaces not scrubbed: %s", out)
	}
}

func TestScrub_SecretAssignment(t *testing.T) {
	in := "secret=my_very_secret_value"
	out := redact.Scrub(in)
	if strings.Contains(out, "my_very_secret_value") {
		t.Fatalf("secret assignment not scrubbed: %s", out)
	}
}

// --- Negative tests: generic key=value must NOT be redacted ---

func TestScrub_FormatEqJson_NoRedact(t *testing.T) {
	in := "format=json"
	out := redact.Scrub(in)
	if out != in {
		t.Fatalf("false positive: 'format=json' was altered to: %s", out)
	}
}

func TestScrub_ModeFlag_NoRedact(t *testing.T) {
	in := "--mode=fast"
	out := redact.Scrub(in)
	if out != in {
		t.Fatalf("false positive: '--mode=fast' was altered to: %s", out)
	}
}

func TestScrub_GenericKeyValue_NoRedact(t *testing.T) {
	in := "key=value"
	out := redact.Scrub(in)
	if out != in {
		t.Fatalf("false positive: 'key=value' was altered to: %s", out)
	}
}

func TestScrub_CountValue_NoRedact(t *testing.T) {
	in := "count=42"
	out := redact.Scrub(in)
	if out != in {
		t.Fatalf("false positive: 'count=42' was altered to: %s", out)
	}
}

// --- Combined test: multiple secret types in one string ---

func TestScrub_Combined_MultipleSecrets(t *testing.T) {
	in := "token ghp_abcdefghijklmnopqrstuvwxyz1234 and api_key=SUPERSECRET and Authorization: Bearer myJWT.token.here"
	out := redact.Scrub(in)
	if strings.Contains(out, "ghp_") {
		t.Fatalf("combined: github token not scrubbed: %s", out)
	}
	if strings.Contains(out, "SUPERSECRET") {
		t.Fatalf("combined: api_key value not scrubbed: %s", out)
	}
	if strings.Contains(out, "myJWT.token.here") {
		t.Fatalf("combined: bearer token not scrubbed: %s", out)
	}
}
