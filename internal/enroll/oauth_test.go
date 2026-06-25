package enroll

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPKCEPair(t *testing.T) {
	v, c, err := pkcePair()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(v))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if c != want {
		t.Fatalf("challenge %q != S256(verifier) %q", c, want)
	}
	if strings.ContainsAny(v, "+/=") {
		t.Fatalf("verifier not base64url-safe: %q", v)
	}
}

func TestAuthorizeURL(t *testing.T) {
	cfg := OAuthConfig{ClientID: "cid", HostedDomain: "wimcorp.co.kr"}
	raw := cfg.authorizeURL("http://127.0.0.1:5000/callback", "chal", "st")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	for k, want := range map[string]string{
		"client_id":             "cid",
		"response_type":         "code",
		"code_challenge":        "chal",
		"code_challenge_method": "S256",
		"state":                 "st",
		"hd":                    "wimcorp.co.kr",
		"scope":                 "openid email",
	} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
}

func TestExchange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("grant_type") != "authorization_code" || r.FormValue("code_verifier") == "" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"invalid_request"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id_token":"eyJ.fake.tok"}`))
	}))
	defer srv.Close()
	orig := googleTokenURL
	googleTokenURL = srv.URL
	defer func() { googleTokenURL = orig }()

	idt, err := OAuthConfig{ClientID: "cid"}.exchange("code123", "verifier123", "http://127.0.0.1:5000/callback")
	if err != nil {
		t.Fatal(err)
	}
	if idt != "eyJ.fake.tok" {
		t.Fatalf("id_token = %q", idt)
	}
}

func TestGoogleIDTokenNoClientID(t *testing.T) {
	if _, err := (OAuthConfig{}).GoogleIDToken(); err == nil {
		t.Fatal("expected error when client id unset")
	}
}
