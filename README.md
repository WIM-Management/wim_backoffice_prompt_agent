# wim-prompt-agent

WIM 백오피스 **프롬프트 인사이트** 로컬 수집 에이전트.

## 무엇이고 왜

`wim-prompt-agent`는 각 개발자 머신에서 백그라운드로 돌며, **Claude Code의 로컬 세션 파일**(`~/.claude/projects/**/*.jsonl`)에서 완결된 대화 턴(프롬프트 + 응답)을 주기적으로 읽습니다. 시크릿을 마스킹하고, 로컬 디스크 큐에 영속한 뒤, 배치로 WIM 백엔드(`PATCH /api/v1/prompt-insights/events`)에 업로드합니다.

이 방식의 장점:

- **정액제 유지** — LLM을 게이트웨이/종량제로 우회하지 않고 로컬 파일만 읽으므로 토큰 추가 비용이 없습니다.
- **멀티벤더** — 어댑터 계층으로 도구를 확장합니다. P1은 Claude Code, 이후(Codex·Cursor 등)는 P2+.
- **사람 단위 귀속** — 머신별 enroll(Google 로그인 → `employee.email`)로, 계정 공유와 무관하게 실제 사람에 귀속됩니다.

> 수집 대상은 **로컬 파일에 기록되는 도구**뿐입니다. 서버사이드 봇(Wimmy)·웹챗(claude.ai 웹)은 로컬에 세션이 안 남으므로 이 에이전트로는 수집되지 않습니다(범위 밖).

## 설치 (원라이너)

private repo라 [gh CLI](https://cli.github.com) 로그인이 전제다 (`brew install gh` / `winget install GitHub.cli` → `gh auth login`).

macOS / Linux:

```bash
gh api repos/WIM-Management/wim_backoffice_prompt_agent/contents/scripts/install.sh \
  -H "Accept: application/vnd.github.raw" | bash
```

Windows (PowerShell):

```powershell
gh api repos/WIM-Management/wim_backoffice_prompt_agent/contents/scripts/install.ps1 `
  -H "Accept: application/vnd.github.raw" | Out-String | Invoke-Expression
```

[`scripts/install.sh`](scripts/install.sh)·[`scripts/install.ps1`](scripts/install.ps1)이 최신 릴리스 바이너리 다운로드 → SHA256 검증 → PATH 배치(mac/linux는 `/usr/local/bin` 또는 `~/.local/bin`, windows는 `%LOCALAPPDATA%\wim-prompt-agent`+사용자 PATH) → `enroll`(브라우저 로그인) → `install`(데몬 등록)까지 한 번에 한다. 바이너리 설치까지만 하려면 `--no-setup`(sh) / `$env:WIM_PROMPT_NO_SETUP=1`(ps1).

> **Windows**: 디바이스 토큰은 DPAPI로 암호화해 `%USERPROFILE%\.wim-prompt-agent\device-token.dpapi`에 저장하고, `install`은 작업 스케줄러(Task Scheduler)에 `WimPromptAgent` 태스크를 등록한다(관리자 권한 불필요). 콘솔 앱 특성상 주기 실행 순간 창이 잠깐 떴다 사라질 수 있다.

### 수동 설치

[GitHub Releases](https://github.com/WIM-Management/wim_backoffice_prompt_agent/releases)에서 플랫폼별 바이너리를 직접 받아도 된다 (`darwin-arm64` / `darwin-amd64` / `linux-amd64` / `linux-arm64`(Jetson Orin 등) / `windows-amd64.exe`, `SHA256SUMS`로 무결성 검증). private repo라 브라우저(로그인 세션) 또는 `gh release download`로 받는다 — 비인증 `curl`은 404.

설치 후: `enroll`(기기 등록, Google 로그인) → `install`(15분 주기 백그라운드 수집). 아래 명령어 섹션 참고.

## 빌드

```bash
# macOS (Apple Silicon / Intel)
GOOS=darwin GOARCH=arm64 go build -o bin/wim-prompt-agent ./cmd/wim-prompt-agent
GOOS=darwin GOARCH=amd64 go build -o bin/wim-prompt-agent ./cmd/wim-prompt-agent

# Linux (x86_64 / arm64 — Jetson Orin 등)
GOOS=linux GOARCH=amd64 go build -o bin/wim-prompt-agent ./cmd/wim-prompt-agent
GOOS=linux GOARCH=arm64 go build -o bin/wim-prompt-agent ./cmd/wim-prompt-agent

# Windows
GOOS=windows GOARCH=amd64 go build -o bin/wim-prompt-agent.exe ./cmd/wim-prompt-agent
```

Go 1.22+ 필요. **외부 의존성 없음**(`go.mod` 표준 라이브러리만).

로컬 빌드 바이너리의 버전은 `dev`로 표시됩니다. 릴리스 버전은 CI가 태그명을 ldflags로 주입합니다(아래).

## 릴리스

`v*` 태그를 push하면 GitHub Actions([release.yml](.github/workflows/release.yml))가 테스트 → OS별 바이너리 5종(`darwin-arm64`/`darwin-amd64`/`linux-amd64`/`linux-arm64`/`windows-amd64.exe`) 빌드 → `SHA256SUMS` 생성 → GitHub Release 발행까지 자동으로 합니다. 수동 빌드·업로드 불필요.

```bash
git tag v0.2.0
git push origin v0.2.0
```

바이너리 `version` 출력에는 태그명이 그대로 찍힙니다(`-X main.Version=<태그>`). 코드 안의 버전 상수를 bump할 필요가 없습니다.

## 명령어

### `enroll` — 이 디바이스를 백엔드에 등록

```bash
./wim-prompt-agent enroll
```

**릴리스 바이너리는 env 설정이 필요 없다** — Google OAuth 클라이언트 id/secret이 릴리스 빌드에 내장돼 있다(CI가 repo secrets `WIM_PROMPT_GOOGLE_CLIENT_ID/SECRET`를 ldflags `-X internal/config.DefaultGoogleClientID/Secret`으로 주입; 데스크톱 앱 client secret은 Google 정책상 기밀 아님). 로컬 개발 빌드이거나 다른 클라이언트/백엔드를 쓰려면 env로 override:

```bash
export WIM_PROMPT_GOOGLE_CLIENT_ID=<desktop-client-id>
export WIM_PROMPT_GOOGLE_CLIENT_SECRET=<desktop-client-secret>   # 데스크톱 client는 비밀 아님
export WIM_PROMPT_BASE_URL=https://staging-backoffice-api.wimcorp.co.kr
./wim-prompt-agent enroll
```

Google OAuth 2.0 **PKCE loopback** 플로우를 실행합니다 — 브라우저를 열어 로그인하면 `127.0.0.1` 콜백으로 인가 코드를 받아 Google `id_token`으로 교환하고, `POST /api/v1/prompt-insights/enroll`로 디바이스 토큰을 받아 **OS 키체인**(mac Keychain / Linux libsecret)에 저장합니다.

**전제 조건:**
- Google Cloud **"Desktop app" OAuth client**의 id/secret을 위 env로 설정.
- 백엔드가 그 client_id를 audience로 받아들이도록 Vault `oauth2.google.agent-client-id`에 같은 데스크톱 client_id 설정.
- 백엔드 `employees`에 본인 행의 `email`이 채워져 있어야 함(없으면 enroll 500: "일치하는 직원이 없음").

### `install` / `uninstall` — 주기 데몬 등록/해제

```bash
./wim-prompt-agent install
./wim-prompt-agent uninstall
```

- **macOS**: `launchd` 사용자 에이전트(`co.wimcorp.promptagent`)를 등록해 주기적으로 `run-once` 실행.
- **Linux**: `systemd --user` 타이머(`wim-prompt-agent.timer`)를 등록(+ `loginctl enable-linger`로 로그아웃 중에도 동작).

바이너리 경로는 설치 시점에 `os.Executable()`로 잡으므로, **바이너리를 옮긴 뒤** `install` 하세요.

### `run-once` — 1회 스캔·마스킹·업로드

```bash
WIM_PROMPT_BASE_URL=https://staging-backoffice-api.wimcorp.co.kr ./wim-prompt-agent run-once
```

전체 파이프라인을 1회 실행:

1. `~/.claude/projects/**/*.jsonl`에서 **완결된 새 턴**을 스캔(다음 사람 프롬프트가 오거나 파일이 idle이면 완결로 판정).
2. 시크릿 패턴 마스킹(`sk-…`, PEM 키, GitHub 토큰, AWS 키).
3. 이벤트를 `~/.wim-prompt-agent/queue/`에 enqueue(디스크 영속).
4. 파일별 byte offset을 `~/.wim-prompt-agent/state.json`에 전진(**enqueue 성공 후에만** — 크래시 시 유실 0).
5. 큐 드레인: 100건 배치로 백엔드 업로드. 일시 실패 시 큐 파일을 디스크에 남겨 다음 실행에 자동 재시도.

### `status` — 현재 설정 표시

```bash
./wim-prompt-agent status
```

버전·데이터 디렉터리·백엔드 URL·스캔 주기·OS를 출력합니다.

## 설정

| 환경 변수                          | 기본값                                          | 설명                                       |
|------------------------------------|-------------------------------------------------|--------------------------------------------|
| `WIM_PROMPT_BASE_URL`              | `https://staging-backoffice-api.wimcorp.co.kr`  | enroll·업로드 대상 백엔드 base URL.         |
| `WIM_PROMPT_GOOGLE_CLIENT_ID`      | (미설정)                                        | 데스크톱 OAuth client id (`enroll`에 필수). |
| `WIM_PROMPT_GOOGLE_CLIENT_SECRET`  | (미설정)                                        | 데스크톱 OAuth client secret (비밀 아님).   |
| `WIM_PROMPT_GOOGLE_HD`             | `wimcorp.co.kr`                                 | Google `hd` 호스티드 도메인 힌트.          |

스캔 주기(15분)·idle 임계(10분)·데이터 디렉터리 등은 컴파일 내장 기본값(`internal/config/config.go`). 설정 파일은 필요 없습니다.

## 파싱 규칙 (요점)

Claude Code의 jsonl은 깨끗한 채팅 로그가 아니라 다중화된 이벤트 스트림이라, 다음 불변식으로 사람 프롬프트만 골라냅니다:

- **default-deny**: `type=="user"` && 사이드체인 아님 && tool_result 아님. 합성/하니스 주입(`<task-notification>`·`<command-*>`·`<system-reminder>` 등)은 제외. 이미지 첨부 등 배열 content는 text 블록만 추출.
- **message.id 그룹핑**: 한 메시지가 여러 줄로 쪼개지므로, 응답 텍스트·토큰 수를 `message.id` 단위로 1회만 집계(중복·과대계상 방지).
- **완결성**: 응답이 끝난(다음 사람 프롬프트 존재 OR 파일 idle + `stop_reason∈{end_turn,stop_sequence}`) 턴만 방출. 생성 중인 마지막 턴은 보류.

## 데이터 디렉터리

런타임 상태는 `~/.wim-prompt-agent/`에 보관(자동 생성):

```
~/.wim-prompt-agent/
  state.json        # 파일별 byte offset(스캐너 진행 상황)
  queue/            # 디스크 영속 이벤트 배치(자동 드레인)
```

토큰은 평문 파일이 아니라 **OS 키체인**에 저장합니다.

## 플랫폼 / 알려진 제약

- **P1 = macOS + Linux**. Windows는 P2(`install`·`enroll`·키체인 미구현).
- **Linux는 `libsecret-tools` 필요**(키체인 백엔드가 `secret-tool` 호출):
  ```bash
  sudo apt-get install libsecret-tools   # Debian/Ubuntu
  sudo dnf install libsecret             # Fedora/RHEL
  ```

## 테스트

```bash
go test ./...
```

전 패키지 테스트 + `internal/e2e/`의 통합 테스트(임시 디렉터리에 가짜 세션을 만들어 mock HTTP 서버 대상으로 파이프라인 전체를 검증, 키체인·데몬 불필요)를 포함합니다.

## 프라이버시

전사 프롬프트 역량 코칭을 위한 fleet-wide 수집이며, 열람 모델은 **NAMED_ADMIN**(개인 실명으로 관리자 열람)입니다. 전송 전 로컬 Redactor + 중앙 PII 마스킹의 이중 안전망이 적용됩니다. `enroll`은 명시적 동의 행위입니다.
