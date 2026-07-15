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
	// Port pins the loopback callback listener to a fixed port (0 = random).
	// Needed on headless/remote boxes so `ssh -L PORT:127.0.0.1:PORT` can
	// tunnel the browser callback from your laptop back to the agent.
	Port int
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

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", c.Port))
	if err != nil {
		return "", fmt.Errorf("loopback listen: %w", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	authURL := c.authorizeURL(redirectURI, challenge, state)
	// Always try to open the browser AND always print the URL — on headless/
	// remote boxes `xdg-open` "succeeds" (returns nil) without ever opening a
	// browser, so relying on its error would silently hide the URL.
	_ = openBrowser(authURL)
	fmt.Printf("\n브라우저를 자동으로 여는 중입니다. 열리지 않으면 아래 URL을 브라우저에 직접 붙여넣어 인증하세요:\n\n%s\n\n", authURL)
	if c.Port != 0 {
		// Fixed port → almost certainly a headless/SSH session. Remind the user
		// how to tunnel the callback back to this machine from their laptop.
		fmt.Printf("원격(SSH) 환경이면 로컬 PC에서 아래처럼 포트 포워딩한 뒤 위 URL을 로컬 브라우저에서 여세요:\n  ssh -L %d:127.0.0.1:%d <이 서버>\n\n", port, port)
	}
	fmt.Println("브라우저 인증을 기다리는 중... (최대 5분)")

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
			writeCallbackPage(w, false, "등록에 실패했어요", "Google 인증이 거부되었습니다 ("+e+"). 이 탭을 닫고 터미널에서 다시 시도해 주세요.")
			ch <- result{err: fmt.Errorf("oauth error: %s", e)}
			return
		}
		if q.Get("state") != state {
			writeCallbackPage(w, false, "등록에 실패했어요", "인증 상태가 일치하지 않습니다. 이 탭을 닫고 터미널에서 다시 시도해 주세요.")
			ch <- result{err: fmt.Errorf("oauth state mismatch")}
			return
		}
		writeCallbackPage(w, true, "기기 등록 완료!", "이 탭을 닫으셔도 됩니다. 이제 15분마다 자동으로 수집됩니다.")
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

// writeCallbackPage renders the browser-facing enroll result page.
// 백오피스 프론트 테마(frontend src/utils/theme.ts의 다크 네이비+골드 팔레트)와 맞춘 카드형 페이지.
func writeCallbackPage(w http.ResponseWriter, ok bool, title, msg string) {
	// theme.ts 토큰: ink 11 17 32 · card 30 45 69 · border 42 63 95 · muted 185 200 220 ·
	// body 245 249 255 · gold 240 180 41 · green 16 185 129 · red 239 68 68
	icon, accent := "✓", "#10B981"
	if !ok {
		icon, accent = "✕", "#EF4444"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html>
<html lang="ko"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>WIM Backoffice · wim-backoffice-prompt-agent</title>
<link rel="stylesheet" href="https://cdn.jsdelivr.net/gh/orioncactus/pretendard@v1.3.9/dist/web/static/pretendard.min.css">
<style>
  body { margin:0; min-height:100vh; display:flex; align-items:center; justify-content:center;
         font-family:'Pretendard','Malgun Gothic','맑은 고딕',-apple-system,sans-serif;
         background:rgb(11 17 32); color:rgb(245 249 255); }
  .card { background:rgb(30 45 69); border:1px solid rgb(42 63 95); border-radius:14px;
          padding:44px 52px; text-align:center; max-width:420px;
          box-shadow:0 12px 40px rgb(0 0 0 / .45); }
  .badge { width:60px; height:60px; border-radius:50%%; background:%s; color:#fff; font-size:30px;
           line-height:60px; margin:0 auto 20px; font-weight:700; }
  h1 { font-size:21px; font-weight:700; margin:0 0 10px; color:rgb(245 249 255); }
  .sub { font-size:14.5px; color:rgb(185 200 220); margin:0; line-height:1.65; }
  .brand { margin-top:26px; padding-top:18px; border-top:1px solid rgb(42 63 95);
           font-size:12px; color:rgb(185 200 220 / .7); letter-spacing:.4px; }
  .brand b { color:rgb(240 180 41); font-weight:600; }
</style></head>
<body><div class="card">
  <div class="badge">%s</div>
  <h1>%s</h1>
  <p class="sub">%s</p>
  <div class="brand"><b>WIM Backoffice</b> · wim-backoffice-prompt-agent</div>
</div></body></html>`, accent, icon, title, msg)
}
