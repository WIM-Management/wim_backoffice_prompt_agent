# enroll id_token 붙여넣기 전환 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** loopback OAuth 콜백을 제거하고, 웹 로그인으로 얻은 Google id_token을 쉘에 붙여넣어 enroll하도록 전환한다(맥/윈도우/SSH 리눅스 단일 흐름).

**Architecture:** CLI `enroll`의 `IDTokenFn`을 loopback(`oauth.go`)에서 `PasteIDToken`(터미널 입력)으로 교체. id_token은 백오피스 프론트의 공개 라우트 `/prompt-agent/enroll`에서 기존 `<GoogleLogin>`으로 발급·표시. backend는 이미 웹 client_id aud를 허용하므로 무변경.

**Tech Stack:** Go 1.x (표준 라이브러리), React + `@react-oauth/google` + vitest (프론트, 별도 레포).

## Global Constraints

- 이름은 어디서든 `wim-backoffice-prompt-agent` — 축약 금지.
- Go 모듈 경로: `github.com/WIM-Management/wim_backoffice_prompt_agent`.
- 커밋 author는 레포 설정(`jax@wimcorp.co.kr`) — `--author`/`-c user.email` 덮어쓰기 금지.
- **backend 변경 없음** — 웹 로그인 client_id로 발급된 id_token을 `GoogleOAuth2Service.buildVerifier()`가 이미 aud 허용.
- 프론트는 **별도 레포**(`~/sources/wim_backoffice_frontend`) — 자체 워크트리 브랜치 → PR base=`staging`. CLI 변경(prompt-agent 레포)과 커밋/PR 분리.
- 프론트 파생 URL 기본값: `https://backoffice.wimcorp.co.kr/prompt-agent/enroll`(prod), 오버라이드 env `WIM_PROMPT_ENROLL_URL`.
- CLI 검증: `cd ~/sources/wim_backoffice_prompt_agent/.worktrees/feat/enroll-idtoken-paste && go test ./... && go build ./...`.

---

## File Structure

**prompt-agent 레포 (CLI)**
- Modify: `internal/config/config.go` — `EnrollURL` 필드 + `WIM_PROMPT_ENROLL_URL` env; OAuth 관련 죽은 필드/기본값 제거.
- Create: `internal/enroll/paste.go` — `PasteIDToken(enrollURL string, r io.Reader) IDTokenFn`.
- Create: `internal/enroll/paste_test.go` — 유닛테스트.
- Delete: `internal/enroll/oauth.go`, `internal/enroll/oauth_test.go` — loopback 전체.
- Create: `cmd/wim-backoffice-prompt-agent/tty_unix.go` — `//go:build !windows`, `/dev/tty` open.
- Create: `cmd/wim-backoffice-prompt-agent/tty_windows.go` — `//go:build windows`, `os.Stdin` 반환.
- Modify: `cmd/wim-backoffice-prompt-agent/main.go` — `cmdEnroll`/`cmdEnrollDispatch`/`ensureEnrolled` 배선 교체, `--port` 삭제, OAuth 가드 제거.
- Modify: `.github/workflows/release.yml` — OAuth client_id/secret ldflags 주입 제거(죽은 설정).
- Modify: `README.md` — 설치/enroll 안내를 붙여넣기 흐름으로.

**frontend 레포 (별도)**
- Create: `src/routes/PromptAgentEnroll.tsx` — 공개 enroll 토큰 페이지.
- Modify: `src/routes/index.ts` — barrel export 추가.
- Modify: `src/App.tsx` — 인증 게이트 이전 공개 라우트 분기.
- Create: `src/routes/PromptAgentEnroll.test.tsx` — 렌더 테스트.

---

## Task 1: Config에 EnrollURL 추가 + OAuth 죽은 설정 제거

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go` (없으면 생성)

**Interfaces:**
- Produces: `config.Config.EnrollURL string`; `config.Default()`가 `WIM_PROMPT_ENROLL_URL`(미설정 시 `https://backoffice.wimcorp.co.kr/prompt-agent/enroll`)로 채움. `GoogleClientID`/`GoogleClientSecret`/`GoogleHostedDomain` 필드 및 `DefaultGoogleClientID`/`DefaultGoogleClientSecret` 변수 제거.

- [ ] **Step 1: 실패 테스트 작성** — `internal/config/config_test.go`

```go
package config

import (
	"os"
	"testing"
)

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
```

- [ ] **Step 2: 실패 확인**

Run: `cd ~/sources/wim_backoffice_prompt_agent/.worktrees/feat/enroll-idtoken-paste && go test ./internal/config/ -run TestDefaultEnrollURL`
Expected: FAIL (`EnrollURL` 필드 없음 → 컴파일 에러)

- [ ] **Step 3: config.go 수정** — OAuth 죽은 설정 제거 + EnrollURL 추가

`Config` 구조체에서 `GoogleClientID`/`GoogleClientSecret`/`GoogleHostedDomain` 3줄과 그 위 주석 블록을 삭제하고 `EnrollURL string` 추가:

```go
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
```

파일 상단 `DefaultGoogleClientID`/`DefaultGoogleClientSecret` var 블록(주석 포함) 전체를 삭제. `Default()` 본문에서 `hd`/`clientID`/`clientSecret` 관련 줄을 삭제하고 `enrollURL`을 추가, 반환 구조체를 교체:

```go
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
```

- [ ] **Step 4: 통과 확인** (config 패키지만 — main.go는 아직 옛 필드 참조하지만 이 패키지 단독 테스트는 통과)

Run: `go test ./internal/config/ -run TestDefaultEnrollURL`
Expected: PASS

- [ ] **Step 5: 커밋**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): EnrollURL 추가 + loopback OAuth 죽은 설정 제거"
```

---

## Task 2: PasteIDToken IDTokenFn 신설

**Files:**
- Create: `internal/enroll/paste.go`
- Test: `internal/enroll/paste_test.go`

**Interfaces:**
- Consumes: `enroll.IDTokenFn = func() (string, error)` (기존, `enroll.go`에 정의됨).
- Produces: `func PasteIDToken(enrollURL string, r io.Reader) IDTokenFn` — 반환된 함수는 `enrollURL` 안내를 stdout에 출력하고 `r`에서 한 줄을 읽어 trim한 문자열을 반환. 빈 줄이면 에러.

- [ ] **Step 1: 실패 테스트 작성** — `internal/enroll/paste_test.go`

```go
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
```

- [ ] **Step 2: 실패 확인**

Run: `go test ./internal/enroll/ -run TestPasteIDToken`
Expected: FAIL (`PasteIDToken` undefined)

- [ ] **Step 3: paste.go 구현**

```go
package enroll

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// PasteIDToken returns an IDTokenFn that guides the user to the web enroll page,
// then reads a single pasted Google id_token line from r. Used instead of the
// loopback OAuth flow so enroll works on headless/SSH boxes (browser on the
// user's laptop, agent on a remote Linux host) with no port or tunnel.
func PasteIDToken(enrollURL string, r io.Reader) IDTokenFn {
	return func() (string, error) {
		fmt.Printf(
			"\n브라우저에서 아래 주소를 열어 회사 Google 계정으로 로그인한 뒤,\n"+
				"화면에 표시된 토큰을 복사해 여기에 붙여넣고 Enter를 누르세요:\n\n  %s\n\n토큰> ",
			enrollURL)
		line, err := bufio.NewReader(r).ReadString('\n')
		if err != nil && err != io.EOF {
			return "", fmt.Errorf("read pasted token: %w", err)
		}
		token := strings.TrimSpace(line)
		if token == "" {
			return "", fmt.Errorf("빈 토큰입니다 — 웹 페이지에 표시된 토큰을 붙여넣으세요")
		}
		return token, nil
	}
}
```

- [ ] **Step 4: 통과 확인**

Run: `go test ./internal/enroll/ -run TestPasteIDToken`
Expected: PASS

- [ ] **Step 5: 커밋**

```bash
git add internal/enroll/paste.go internal/enroll/paste_test.go
git commit -m "feat(enroll): PasteIDToken IDTokenFn (웹 로그인 토큰 붙여넣기)"
```

---

## Task 3: TTY 입력 헬퍼 (unix /dev/tty, windows os.Stdin)

**Files:**
- Create: `cmd/wim-backoffice-prompt-agent/tty_unix.go`
- Create: `cmd/wim-backoffice-prompt-agent/tty_windows.go`

**Interfaces:**
- Produces: `func openPasteInput() (io.ReadCloser, error)` (main 패키지) — enroll 붙여넣기를 읽을 입력원. unix는 `/dev/tty`(파이프 설치 `curl | bash` 에서도 제어 터미널 직결), windows는 `os.Stdin`(이미 `attachParentConsole()`가 `CONIN$`로 재연결).

- [ ] **Step 1: tty_unix.go 작성**

```go
//go:build !windows

package main

import (
	"io"
	"os"
)

// openPasteInput opens the controlling terminal for reading the pasted enroll
// token. Reading os.Stdin would fail under `curl -fsSL install.sh | bash`
// because the child's stdin is the script pipe, not the keyboard — /dev/tty is
// the real terminal and stays available in an interactive shell.
func openPasteInput() (io.ReadCloser, error) {
	return os.OpenFile("/dev/tty", os.O_RDONLY, 0)
}
```

- [ ] **Step 2: tty_windows.go 작성**

```go
//go:build windows

package main

import (
	"io"
	"os"
)

// openPasteInput returns os.Stdin, which attachParentConsole() has already
// reattached to the parent terminal's CONIN$ (the -H=windowsgui build has no
// console of its own). Wrapped so the caller can Close() uniformly without
// closing the process's real stdin.
func openPasteInput() (io.ReadCloser, error) {
	return io.NopCloser(os.Stdin), nil
}
```

- [ ] **Step 3: 빌드 확인 (양 OS 태그 컴파일)**

Run: `go build ./cmd/... && GOOS=windows go build ./cmd/...`
Expected: 두 빌드 모두 성공 (아직 openPasteInput 미사용이면 `declared and not used`는 함수라 무관; 패키지 컴파일만 확인)

- [ ] **Step 4: 커밋**

```bash
git add cmd/wim-backoffice-prompt-agent/tty_unix.go cmd/wim-backoffice-prompt-agent/tty_windows.go
git commit -m "feat(cli): openPasteInput 제어 터미널 헬퍼(/dev/tty · CONIN$)"
```

---

## Task 4: main.go 배선 교체 + loopback 삭제

**Files:**
- Modify: `cmd/wim-backoffice-prompt-agent/main.go`
- Delete: `internal/enroll/oauth.go`, `internal/enroll/oauth_test.go`
- Test: `cmd/wim-backoffice-prompt-agent/main_test.go` (기존, enroll dispatch 테스트가 있으면 `--port` 제거 반영)

**Interfaces:**
- Consumes: `config.Config.EnrollURL`(Task 1), `enroll.PasteIDToken`(Task 2), `openPasteInput`(Task 3).
- Produces: `cmdEnroll(cfg config.Config, configDir string) error` (기존 `port int` 파라미터 제거). `cmdEnrollDispatch`는 `--port` 플래그 없이 `cmdEnroll(cfg, configDir)` 호출. `ensureEnrolled`는 `cmdEnroll(cfg, registry.DefaultConfigDir())` 호출.

- [ ] **Step 1: oauth.go / oauth_test.go 삭제**

```bash
git rm internal/enroll/oauth.go internal/enroll/oauth_test.go
```

- [ ] **Step 2: `cmdEnroll` 교체** (main.go ~296-330)

기존 `cmdEnroll(cfg, configDir, port)` 전체를 아래로 교체. OAuth 가드(`if cfg.GoogleClientID == ""`)와 `enroll.OAuthConfig{...}`/`oauth.GoogleIDToken` 조립을 제거하고 `PasteIDToken`+`openPasteInput`로 배선:

```go
// cmdEnroll runs the device enrollment flow for one config dir: register it,
// obtain a Google id_token by pasting it from the web enroll page, POST it to
// backend enroll, and store the returned device token under the dir's token key.
func cmdEnroll(cfg config.Config, configDir string) error {
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return err
	}
	// 레지스트리 upsert 먼저 — slug 충돌 등을 로그인 전에 걸러낸다.
	entry, err := registry.New(registryPath(cfg)).Upsert(configDir)
	if err != nil {
		return err
	}

	tty, err := openPasteInput()
	if err != nil {
		return fmt.Errorf(
			"제어 터미널을 열 수 없습니다(무인 환경?) — 브라우저가 있는 환경에서 " +
				"`wim-backoffice-prompt-agent enroll`을 실행하세요: %w", err)
	}
	defer tty.Close()

	host, _ := os.Hostname()
	if host == "" {
		host = "unknown"
	}
	label := host
	if !registry.IsDefault(configDir) {
		label = host + ":" + registry.Slug(configDir)
	}
	e := enroll.New(cfg.BaseURL, enroll.NewKeychainStore(entry.TokenKey),
		enroll.PasteIDToken(cfg.EnrollURL, tty))
	if err := e.Run(label); err != nil {
		return err
	}
	fmt.Printf("✅ enroll 완료: %s (token=%s)\n", configDir, entry.TokenKey)
	return nil
}
```

- [ ] **Step 3: `cmdEnrollDispatch`에서 `--port` 제거** (main.go ~269-290)

`port := fs.Int("port", ...)` 줄을 삭제하고, 마지막 호출을 `return cmdEnroll(cfg, configDir)`로 변경(forget 분기는 유지).

- [ ] **Step 4: `ensureEnrolled` 호출 갱신** (main.go ~375)

`return cmdEnroll(cfg, registry.DefaultConfigDir(), 0)` → `return cmdEnroll(cfg, registry.DefaultConfigDir())`.

- [ ] **Step 5: import 정리**

`internal/enroll`은 유지. 옛 `oauth` 참조가 없으면 그대로. 사용 안 하게 된 import(있다면) 제거. `io`가 tty 파일에서만 쓰이면 main.go엔 불필요.

- [ ] **Step 6: main_test.go 갱신**

`main_test.go`에 enroll `--port` 파싱을 검증하는 테스트가 있으면 삭제/수정. `unexpected argument` 관련 테스트는 유지.

Run: `grep -n "port\|Port" cmd/wim-backoffice-prompt-agent/main_test.go` 로 확인 후 해당 케이스만 제거.

- [ ] **Step 7: 전체 빌드·테스트**

Run: `go build ./... && GOOS=windows go build ./... && go test ./...`
Expected: 모두 PASS (loopback 삭제로 oauth_test 사라짐, 나머지 그린)

- [ ] **Step 8: 커밋**

```bash
git add -u
git add cmd/wim-backoffice-prompt-agent/main.go
git commit -m "feat(enroll): loopback 콜백 제거, 붙여넣기 흐름으로 배선(--port 삭제)"
```

---

## Task 5: release.yml ldflags 정리 + README 갱신

**Files:**
- Modify: `.github/workflows/release.yml`
- Modify: `README.md`

**Interfaces:** 없음(빌드/문서).

- [ ] **Step 1: release.yml ldflags 확인**

Run: `grep -n "DefaultGoogleClientID\|DefaultGoogleClientSecret\|WIM_PROMPT_GOOGLE\|ldflags" .github/workflows/release.yml`

- [ ] **Step 2: OAuth client ldflags 주입 제거**

`-X .../internal/config.DefaultGoogleClientID=...` / `DefaultGoogleClientSecret=...` 조각을 ldflags 문자열에서 삭제(그 var들은 Task 1에서 제거됨 → 남겨두면 빌드 경고/무의미). `-X main.Version=...`는 유지.

- [ ] **Step 3: README enroll 안내 교체**

"브라우저 자동 열림 / loopback / `--port` / `ssh -L` 터널" 관련 문구를 찾아 아래 흐름으로 교체:

> ### enroll
> 1. 브라우저에서 `https://backoffice.wimcorp.co.kr/prompt-agent/enroll` 열고 회사 Google 계정으로 로그인
> 2. 화면에 표시된 토큰을 복사
> 3. 터미널에서 `wim-backoffice-prompt-agent enroll` 실행 → 프롬프트에 붙여넣기
>
> 맥·윈도우·리눅스(SSH 포함) 모두 동일합니다. 포트/터널 설정이 필요 없습니다.

Run: `grep -n "port\|ssh -L\|loopback\|127.0.0.1\|콜백" README.md` 로 잔여 문구 점검 후 제거.

- [ ] **Step 4: 커밋**

```bash
git add .github/workflows/release.yml README.md
git commit -m "docs(enroll): 붙여넣기 흐름 안내 + OAuth ldflags 정리"
```

---

## Task 6: [frontend 레포] 공개 enroll 페이지

> ⚠️ 이 태스크는 **frontend 레포**(`~/sources/wim_backoffice_frontend`)에서 자체 워크트리 브랜치(`feat/prompt-agent-enroll-page`)로 수행하고, PR base=`staging`. 앞 태스크(prompt-agent)와 커밋/PR 분리.

**Files:**
- Create: `src/routes/PromptAgentEnroll.tsx`
- Modify: `src/routes/index.ts`
- Modify: `src/App.tsx`
- Test: `src/routes/PromptAgentEnroll.test.tsx`

**Interfaces:**
- Consumes: `import.meta.env.VITE_OAUTH_GOOGLE_CLIENT_ID`(기존, Login.tsx와 동일), `@react-oauth/google`의 `GoogleOAuthProvider`/`GoogleLogin`/`CredentialResponse`.
- Produces: 라우트 `/prompt-agent/enroll` (인증 게이트 우회, 공개).

- [ ] **Step 1: 실패 테스트 작성** — `src/routes/PromptAgentEnroll.test.tsx` (기존 `AdminOnly.test.tsx` 패턴 참고)

```tsx
import { render, screen } from '@testing-library/react'
import { describe, it, expect, vi } from 'vitest'
import PromptAgentEnroll from './PromptAgentEnroll'

// GIS 실제 호출 없이 provider/버튼 렌더만 검증
vi.mock('@react-oauth/google', () => ({
  GoogleOAuthProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  GoogleLogin: () => <button>Google로 로그인</button>,
}))

describe('PromptAgentEnroll', () => {
  it('로그인 버튼과 안내 문구를 렌더한다', () => {
    render(<PromptAgentEnroll />)
    expect(screen.getByText('Google로 로그인')).toBeInTheDocument()
    expect(screen.getByText(/붙여넣/)).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: 실패 확인**

Run: `cd ~/sources/wim_backoffice_frontend/.worktrees/feat/prompt-agent-enroll-page && yarn vitest run src/routes/PromptAgentEnroll.test.tsx`
Expected: FAIL (모듈 없음)

- [ ] **Step 3: PromptAgentEnroll.tsx 구현**

```tsx
import { useState } from 'react'
import { GoogleOAuthProvider, GoogleLogin, CredentialResponse } from '@react-oauth/google'

const GOOGLE_CLIENT_ID = import.meta.env.VITE_OAUTH_GOOGLE_CLIENT_ID || ''

export default function PromptAgentEnroll() {
  const [token, setToken] = useState<string | null>(null)
  const [copied, setCopied] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const handleSuccess = (res: CredentialResponse) => {
    if (!res.credential) {
      setError('토큰을 받지 못했습니다. 다시 시도해 주세요.')
      return
    }
    setError(null)
    setToken(res.credential)
  }

  const copy = async () => {
    if (!token) return
    await navigator.clipboard.writeText(token)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <GoogleOAuthProvider clientId={GOOGLE_CLIENT_ID}>
      <div className="min-h-screen bg-ink text-body flex items-center justify-center p-6">
        <div className="w-full max-w-xl bg-card border border-border rounded-2xl p-8">
          <h1 className="text-xl font-bold mb-2">prompt-agent 기기 등록</h1>
          <p className="text-muted text-sm mb-6">
            회사 Google 계정으로 로그인하면 등록 토큰이 표시됩니다.
            그 토큰을 복사해 터미널의{' '}
            <code className="text-gold">wim-backoffice-prompt-agent enroll</code>{' '}
            프롬프트에 붙여넣으세요.
          </p>

          {!token && (
            <div className="flex flex-col gap-3">
              <GoogleLogin onSuccess={handleSuccess} onError={() => setError('로그인에 실패했습니다.')} />
              {error && <p className="text-red text-sm">{error}</p>}
            </div>
          )}

          {token && (
            <div className="flex flex-col gap-3">
              <label className="text-sm text-muted">등록 토큰 (약 1시간 후 만료)</label>
              <textarea
                readOnly
                value={token}
                onFocus={(e) => e.currentTarget.select()}
                className="font-mono text-xs bg-ink border border-border rounded-lg p-3 h-28 break-all"
              />
              <button
                onClick={copy}
                className="self-start bg-gold text-ink font-semibold rounded-lg px-4 py-2"
              >
                {copied ? '복사됨 ✓' : '토큰 복사'}
              </button>
              <p className="text-muted text-xs">
                이 토큰은 화면에만 표시되며 저장되지 않습니다. 붙여넣은 뒤 이 탭을 닫으세요.
              </p>
            </div>
          )}
        </div>
      </div>
    </GoogleOAuthProvider>
  )
}
```

> 색 토큰(`bg-ink`/`bg-card`/`border-border`/`text-muted`/`text-gold`/`text-red`)이 tailwind 설정에 없으면, `Login.tsx`가 실제 쓰는 클래스명을 그대로 차용해 맞춘다(렌더 테스트는 클래스와 무관하게 통과).

- [ ] **Step 4: barrel + 라우트 배선**

`src/routes/index.ts`에 추가:
```ts
export { default as PromptAgentEnroll } from './PromptAgentEnroll'
```

`src/App.tsx`에서 인증 게이트(`if (!user) { return <Login /> }`) **직전**에 공개 분기를 추가하고 import를 확장:
```tsx
// import 라인(9번): PromptAgentEnroll 추가
import { Login, /* ...기존... */, PromptAgentEnroll } from '@/routes'

// ... 게이트 직전(현재 78번째 줄 "// 로그인 안 된 상태" 위):
// 공개 라우트: 인증 게이트 이전에 처리 (설치형 에이전트 토큰 발급)
if (location.pathname === '/prompt-agent/enroll') {
  return <PromptAgentEnroll />
}

// 로그인 안 된 상태
if (!user) {
  return <Login />
}
```

- [ ] **Step 5: 통과 확인**

Run: `yarn vitest run src/routes/PromptAgentEnroll.test.tsx`
Expected: PASS

- [ ] **Step 6: 타입/빌드 확인**

Run: `yarn tsc --noEmit && yarn build`
Expected: 성공

- [ ] **Step 7: 커밋**

```bash
git add src/routes/PromptAgentEnroll.tsx src/routes/PromptAgentEnroll.test.tsx src/routes/index.ts src/App.tsx
git commit -m "feat(enroll): prompt-agent 공개 토큰 발급 페이지(/prompt-agent/enroll)"
```

---

## Task 7: E2E 수동 검증 (staging)

**Interfaces:** 없음(검증).

- [ ] **Step 1: frontend feature → staging PR 머지 후 배포 확인**

`https://staging-backoffice.wimcorp.co.kr/prompt-agent/enroll` 접속 → 로그인 버튼 표시(미인증 상태에서도 접근 가능=게이트 우회 확인).

- [ ] **Step 2: 토큰 발급**

로그인 → 토큰 textarea 표시 + 복사 동작 확인.

- [ ] **Step 3: CLI 붙여넣기 enroll (staging BaseURL)**

로컬/원격 리눅스 쉘에서:
```bash
WIM_PROMPT_BASE_URL=https://staging-backoffice-api.wimcorp.co.kr \
WIM_PROMPT_ENROLL_URL=https://staging-backoffice.wimcorp.co.kr/prompt-agent/enroll \
./wim-backoffice-prompt-agent enroll
```
프롬프트에 복사한 토큰 붙여넣기 → `✅ enroll 완료` 확인.

- [ ] **Step 4: `curl | bash` 경로 확인 (핵심)**

install.sh 파이프 실행에서도 enroll 프롬프트가 `/dev/tty`로 입력을 받는지(파이프 EOF로 실패하지 않는지) 확인.

- [ ] **Step 5: 토큰 유효성 확인**

`./wim-backoffice-prompt-agent status` 또는 수집 1회 → backend cursor 200(정상) 확인.

---

## Self-Review

**Spec coverage:**
- A(프론트 페이지) → Task 6. B(loopback 제거 + 붙여넣기) → Task 2·4. C(`curl|bash`/dev/tty) → Task 3·4(Step2 가드)·7(Step4). D(문서) → Task 5. Config 파생 URL(Open Q1) → Task 1. install 배선(Open Q2) → Task 4(ensureEnrolled)·Task 3. backend 무변경 → 계획에 backend 태스크 없음. ✅ 갭 없음.
- **필수 정정 반영**: OAuth 가드(`if cfg.GoogleClientID == ""`) 제거를 Task 4 Step 2에서 명시(가드 잔존 시 새 흐름 즉시 에러 → 반드시 제거).

**Placeholder scan:** 각 코드 스텝에 실제 코드 포함. `grep` 지시는 삭제 대상 특정용(플레이스홀더 아님). ✅

**Type consistency:** `PasteIDToken(enrollURL string, r io.Reader) IDTokenFn`(Task 2) = `cmdEnroll`에서 `enroll.PasteIDToken(cfg.EnrollURL, tty)`(Task 4)로 일치. `openPasteInput() (io.ReadCloser, error)`(Task 3) = Task 4에서 `tty, err := openPasteInput()` 일치. `config.EnrollURL`(Task 1) = Task 4 사용 일치. `cmdEnroll(cfg, configDir)`(port 제거) = dispatch·ensureEnrolled 양쪽 갱신 일치. ✅
