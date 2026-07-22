package enroll

import (
	"strings"
	"testing"
)

func TestPasteIDTokenReadsAndTrims(t *testing.T) {
	fn := PasteIDToken("https://x.example/e", strings.NewReader("  eyJhbGciOi.token.sig  \n"))
	got, err := fn()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got != "eyJhbGciOi.token.sig" {
		t.Fatalf("token = %q", got)
	}
}

func TestPasteIDTokenEmptyErrors(t *testing.T) {
	fn := PasteIDToken("https://x.example/e", strings.NewReader("\n"))
	if _, err := fn(); err == nil {
		t.Fatal("expected error on empty input")
	}
}
