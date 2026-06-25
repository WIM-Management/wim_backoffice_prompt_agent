package enroll

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Google OAuth endpoints (overridable in tests).
var (
	googleAuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL = "https://oauth2.googleapis.com/token"
)

// OAuthConfig holds the desktop OAuth client credentials. For a Google
// "Desktop app" client the secret is NON-confidential by design (public client),
// so embedding/configuring it in the agent is acceptable.
type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	HostedDomain string // optional `hd` hint, e.g. "wimcorp.co.kr"
}

// GoogleIDToken runs the OAuth 2.0 PKCE loopback flow: opens the user's browser,
// captures the auth code on a localhost callback, exchanges it, and returns the
// Google id_token (aud = ClientID). This is the concrete IDTokenFn for enroll.
func (c OAuthConfig) GoogleIDToken() (string, error) {
	if c.ClientID == "" {
		return "", fmt.Errorf("OAuth client id not configured (set WIM_PROMPT_GOOGLE_CLIENT_ID)")
	}
	verifier, challenge, err := pkcePair()
	if err != nil {
		return "", err
	}
	state, err := randString(16)
	if err != nil {
		return "", err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("loopback listen: %w", err)
	}
	defer ln.Close()
	redirectURI := fmt.Sprintf("http://%s/callback", ln.Addr().String())

	authURL := c.authorizeURL(redirectURI, challenge, state)
	if err := openBrowser(authURL); err != nil {
		fmt.Printf("Open this URL in your browser to authorize:\n%s\n", authURL)
	}

	code, err := waitForCode(ln, state)
	if err != nil {
		return "", err
	}
	return c.exchange(code, verifier, redirectURI)
}

func (c OAuthConfig) authorizeURL(redirectURI, challenge, state string) string {
	q := url.Values{
		"client_id":             {c.ClientID},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"scope":                 {"openid email"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
		"access_type":           {"offline"},
		"prompt":                {"select_account"},
	}
	if c.HostedDomain != "" {
		q.Set("hd", c.HostedDomain)
	}
	return googleAuthURL + "?" + q.Encode()
}

// waitForCode serves a single localhost request and returns the auth code.
func waitForCode(ln net.Listener, state string) (string, error) {
	type result struct {
		code string
		err  error
	}
	ch := make(chan result, 1)
	srv := &http.Server{}
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			fmt.Fprintf(w, "Authorization failed: %s. You may close this tab.", e)
			ch <- result{err: fmt.Errorf("oauth error: %s", e)}
			return
		}
		if q.Get("state") != state {
			fmt.Fprint(w, "State mismatch. You may close this tab.")
			ch <- result{err: fmt.Errorf("oauth state mismatch")}
			return
		}
		fmt.Fprint(w, "Enrolled. You may close this tab and return to the terminal.")
		ch <- result{code: q.Get("code")}
	})
	srv.Handler = mux
	go srv.Serve(ln)
	defer srv.Close()

	select {
	case res := <-ch:
		return res.code, res.err
	case <-time.After(5 * time.Minute):
		return "", fmt.Errorf("timed out waiting for browser authorization")
	}
}

func (c OAuthConfig) exchange(code, verifier, redirectURI string) (string, error) {
	form := url.Values{
		"code":          {code},
		"client_id":     {c.ClientID},
		"client_secret": {c.ClientSecret},
		"redirect_uri":  {redirectURI},
		"grant_type":    {"authorization_code"},
		"code_verifier": {verifier},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, googleTokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()
	var tr struct {
		IDToken string `json:"id_token"`
		Error   string `json:"error"`
		ErrDesc string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if tr.IDToken == "" {
		return "", fmt.Errorf("no id_token in response (error=%s %s)", tr.Error, tr.ErrDesc)
	}
	return tr.IDToken, nil
}

func pkcePair() (verifier, challenge string, err error) {
	verifier, err = randString(32)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func randString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func openBrowser(u string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "linux":
		cmd = "xdg-open"
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler"}
	default:
		return fmt.Errorf("unsupported OS for browser open: %s", runtime.GOOS)
	}
	return exec.Command(cmd, append(args, u)...).Start()
}
