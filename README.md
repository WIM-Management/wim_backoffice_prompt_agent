# wim-backoffice-prompt-agent

WIM 백오피스 **프롬프트 인사이트** 로컬 수집 에이전트.

## 무엇이고 왜

`wim-backoffice-prompt-agent`는 각 개발자 머신에서 백그라운드로 돌며, AI 코딩 CLI들의 **로컬 세션 파일**(**Claude Code** `~/.claude/projects/**/*.jsonl`, **Codex** `~/.codex/sessions/**/rollout-*.jsonl`)에서 완결된 대화 턴(프롬프트 + 응답)을 주기적으로 읽습니다. 시크릿을 마스킹하고, 로컬 디스크 큐에 영속한 뒤, 배치로 WIM 백엔드(`PATCH /api/v1/prompt-insights/events`)에 업로드합니다.

이 방식의 장점:

- **정액제 유지** — LLM을 게이트웨이/종량제로 우회하지 않고 로컬 파일만 읽으므로 토큰 추가 비용이 없습니다.
- **멀티벤더** — 어댑터 계층으로 도구를 확장합니다. 현재 **Claude Code**(`~/.claude`)·**Codex**(`~/.codex`) 2종을 수집하며, 이후(Cursor 등)는 어댑터 추가로 확장.
- **사람 단위 귀속** — 머신별 enroll(Google 로그인 → `employee.email`)로, 계정 공유와 무관하게 실제 사람에 귀속됩니다.

> 수집 대상은 **로컬 파일에 기록되는 CLI 도구**(Claude Code·Codex)뿐입니다. 서버사이드 봇(Wimmy)·웹챗(claude.ai 웹)은 로컬에 세션이 안 남으므로 이 에이전트로는 수집되지 않습니다(범위 밖).

> **보관 기간(30일) 제약**: Claude Code는 시작 시 `cleanupPeriodDays`(기본 **30일**)보다 오래된 세션 transcript를 자동 삭제합니다(판정 기준은 파일 mtime — 오래된 세션도 `--resume`로 다시 열면 갱신돼 살아남음). 따라서 이 에이전트가 읽을 수 있는 건 **최근 30일 내 세션뿐**이며, **설치 이전에 이미 만료된 과거 세션은 소급 수집이 불가능**합니다. 반대로 데몬이 30일 안에 한 번만 돌면 유실 없이 실시간 수집되므로, 만료가 문제되는 건 오직 과거 소급뿐입니다.

## 설치 (원라이너)

릴리스는 공개 배포 레포 [**wim_backoffice_prompt_agent_releases**](https://github.com/WIM-Management/wim_backoffice_prompt_agent_releases)에만 발행된다 (이 repo는 소스+태그만, private). 인증·GitHub 계정 없이 설치 가능:

macOS / Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/WIM-Management/wim_backoffice_prompt_agent_releases/main/install.sh | bash
```

Windows (PowerShell):

```powershell
irm https://raw.githubusercontent.com/WIM-Management/wim_backoffice_prompt_agent_releases/main/install.ps1 | iex
```

설치 스크립트(배포 레포의 `install.sh`/`install.ps1`)가 최신 릴리스 다운로드 → SHA256 검증 → PATH 배치(mac/linux는 `/usr/local/bin` 또는 `~/.local/bin`, windows는 `%LOCALAPPDATA%\wim-backoffice-prompt-agent`+사용자 PATH) → `enroll`(브라우저 로그인) → `install`(데몬 등록)까지 한 번에 한다. 바이너리 설치까지만 하려면 `--no-setup`(sh) / `$env:WIM_PROMPT_NO_SETUP=1`(ps1). **스크립트 수정은 배포 레포에서** — 이 repo엔 설치 스크립트를 두지 않는다(이중 소스 방지).

> **Windows**: 디바이스 토큰은 DPAPI로 암호화해 `%USERPROFILE%\.wim-backoffice-prompt-agent\device-token.dpapi`에 저장하고, `install`은 작업 스케줄러(Task Scheduler)에 `WimBackofficePromptAgent` 태스크를 등록한다(관리자 권한 불필요). 릴리스 바이너리는 GUI 서브시스템(`-H=windowsgui`)으로 빌드되어 **주기 실행 시 콘솔 창이 뜨지 않는다.** 터미널에서 직접 실행하는 대화형 명령(`enroll` 등)은 부모 콘솔에 재연결해 출력이 정상적으로 보인다. 데몬(`run-once`) 진단 로그는 아래 `agent.log`로 남는다.
>
> 기존 설치본은 self-update로 새 바이너리를 받으면 창 깜빡임이 사라진다(작업 액션 변경 불필요). 즉시 우회가 필요하면 작업 액션을 `conhost.exe --headless "<exe>" run-once`로 바꿔도 된다.

## 빌드

```bash
# macOS (Apple Silicon / Intel)
GOOS=darwin GOARCH=arm64 go build -o bin/wim-backoffice-prompt-agent ./cmd/wim-backoffice-prompt-agent
GOOS=darwin GOARCH=amd64 go build -o bin/wim-backoffice-prompt-agent ./cmd/wim-backoffice-prompt-agent

# Linux (x86_64 / arm64 — Jetson Orin 등)
GOOS=linux GOARCH=amd64 go build -o bin/wim-backoffice-prompt-agent ./cmd/wim-backoffice-prompt-agent
GOOS=linux GOARCH=arm64 go build -o bin/wim-backoffice-prompt-agent ./cmd/wim-backoffice-prompt-agent

# Windows (릴리스는 -H=windowsgui로 콘솔 창을 없앤다; 로컬 디버깅은 생략 가능)
GOOS=windows GOARCH=amd64 go build -ldflags "-H=windowsgui" -o bin/wim-backoffice-prompt-agent.exe ./cmd/wim-backoffice-prompt-agent
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
./wim-backoffice-prompt-agent enroll
```

1. 브라우저에서 `https://backoffice.wimcorp.co.kr/prompt-agent/enroll` 열고 회사 Google 계정으로 로그인
2. 화면에 표시된 토큰을 복사
3. 터미널에서 `wim-backoffice-prompt-agent enroll` 실행 → 프롬프트에 붙여넣기

맥·윈도우·리눅스(SSH 포함) 모두 동일합니다. 포트/터널 설정이 필요 없습니다.

`POST /api/v1/prompt-insights/enroll`로 디바이스 토큰을 받아 저장합니다(mac Keychain / Linux `~/.wim-backoffice-prompt-agent/device-token` 0600 파일 / Windows DPAPI 파일).

staging 백엔드 대상 테스트 시에만 env override (백엔드 base URL과 enroll 페이지 URL을 함께):

```bash
export WIM_PROMPT_BASE_URL=https://staging-backoffice-api.wimcorp.co.kr
export WIM_PROMPT_ENROLL_URL=https://staging-backoffice.wimcorp.co.kr/prompt-agent/enroll
./wim-backoffice-prompt-agent enroll
```

**전제 조건:**
- 백엔드 `employees`에 본인 행의 `email`이 채워져 있어야 함(없으면 enroll 500: "일치하는 직원이 없음").

#### 공유 머신 — 여러 사람이 각자 폴더로 (`--config-dir`)

한 머신을 여러 직원이 각자 다른 Claude 설정 폴더(`~/.claude`, `~/.claude-melle` …)로 쓸 때, **폴더마다 따로 enroll**하면 각 폴더의 프롬프트가 그 폴더 주인에게 귀속된다. 폴더별로 **다른 디바이스 토큰**이 저장된다.

```bash
# 기본 폴더(~/.claude) — 옵션 없이
./wim-backoffice-prompt-agent enroll
# 추가 폴더 — 그 사람이 자기 구글 계정으로 로그인
./wim-backoffice-prompt-agent enroll --config-dir .claude-melle       # ~/.claude-melle 로 해석
./wim-backoffice-prompt-agent enroll --config-dir /abs/path/.claude-x # 절대경로도 가능
```

- `--config-dir` 값은 **절대경로면 그대로**, 아니면 `~/` 기준으로 해석한다.
- 등록된 폴더는 `~/.wim-backoffice-prompt-agent/registry.json`에 기록되며, **명시적으로 enroll한 폴더만** 수집한다(자동 발견 없음). `run-once`/데몬이 등록된 전 폴더를 각자 토큰으로 순회한다.
- 데몬은 한 번만 `install`하면 되고, 인원 추가는 `enroll --config-dir`만 하면 된다(데몬 재설치 불필요).
- 등록 해제 + 토큰 삭제:
  ```bash
  ./wim-backoffice-prompt-agent enroll --forget --config-dir .claude-melle
  ```
  (기본 `~/.claude`는 forget할 수 없다.)

### `install` / `uninstall` — 주기 데몬 등록/해제

```bash
./wim-backoffice-prompt-agent install
./wim-backoffice-prompt-agent uninstall
```

- **macOS**: `launchd` 사용자 에이전트(`co.wimcorp.wim-backoffice-prompt-agent`)를 등록해 주기적으로 `run-once` 실행.
- **Linux**: `systemd --user` 타이머(`wim-backoffice-prompt-agent.timer`)를 등록(+ `loginctl enable-linger`로 로그아웃 중에도 동작).

바이너리 경로는 설치 시점에 `os.Executable()`로 잡으므로, **바이너리를 옮긴 뒤** `install` 하세요.

### `run-once` — 1회 스캔·마스킹·업로드

```bash
WIM_PROMPT_BASE_URL=https://staging-backoffice-api.wimcorp.co.kr ./wim-backoffice-prompt-agent run-once   # staging 대상 테스트 예시
```

전체 파이프라인을 **두 갈래**로 실행한다:

- **폴더 패스** — 등록된 config 폴더마다 Claude Code(`<configDir>/projects/**/*.jsonl`)를 스캔해 **그 폴더의 토큰**으로 업로드.
- **머신 패스** — Codex(`~/.codex/sessions/**/rollout-*.jsonl`)를 머신당 **1회만** 스캔해 primary(`~/.claude`) 토큰으로 귀속(config 폴더에 종속되지 않는 머신 전역 소스라). primary 토큰이 없으면 머신 패스는 skip.

각 패스는 아래 단계를 돈다:

1. **완결된 새 턴**을 스캔(다음 사람 프롬프트가 오거나 파일이 idle이면 완결로 판정).
2. 시크릿 패턴 마스킹(`sk-…`, PEM 키, GitHub 토큰, AWS 키).
3. 이벤트를 큐에 enqueue(디스크 영속). 기본 폴더는 `queue/`, 추가 폴더는 `queue/<slug>/`.
4. 파일별 byte offset을 `~/.wim-backoffice-prompt-agent/state.json`에 전진(**enqueue 성공 후에만** — 크래시 시 유실 0).
5. 큐 드레인: 100건 배치로 해당 토큰으로 업로드. 일시 실패 시 큐 파일을 디스크에 남겨 다음 실행에 자동 재시도.

패스별로 격리 실행되므로, 한 소스의 실패(토큰 만료 등)가 다른 소스 수집을 막지 않는다.

### `status` — 현재 설정 표시

```bash
./wim-backoffice-prompt-agent status
```

버전·데이터 디렉터리·백엔드 URL·스캔 주기·OS와 **등록된 config 폴더 목록**(각 토큰 유무)을 출력합니다.

## 설정

| 환경 변수              | 기본값                                                    | 설명                                          |
|------------------------|-----------------------------------------------------------|-----------------------------------------------|
| `WIM_PROMPT_BASE_URL`  | `https://backoffice-api.wimcorp.co.kr` (prod)            | enroll·업로드 대상 백엔드 base URL.           |
| `WIM_PROMPT_ENROLL_URL`| `https://backoffice.wimcorp.co.kr/prompt-agent/enroll`   | 웹 로그인으로 id_token을 발급/표시하는 페이지. |

스캔 주기(15분)·idle 임계(10분)·데이터 디렉터리 등은 컴파일 내장 기본값(`internal/config/config.go`). 설정 파일은 필요 없습니다.

## 파싱 규칙 (요점)

각 도구의 세션 파일은 깨끗한 채팅 로그가 아니라 다중화된 이벤트 스트림이라, 어댑터마다 자기 포맷에 맞는 규칙으로 사람 프롬프트만 골라냅니다(Codex는 `rollout-*.jsonl`를 처리). 대표로 **Claude Code** 규칙은 다음과 같습니다:

- **default-deny**: `type=="user"` && 사이드체인 아님 && tool_result 아님. 합성/하니스 주입(`<task-notification>`·`<command-*>`·`<system-reminder>` 등)은 제외. 이미지 첨부 등 배열 content는 text 블록만 추출.
- **message.id 그룹핑**: 한 메시지가 여러 줄로 쪼개지므로, 응답 텍스트·토큰 수를 `message.id` 단위로 1회만 집계(중복·과대계상 방지).
- **완결성**: 응답이 끝난(다음 사람 프롬프트 존재 OR 파일 idle + `stop_reason∈{end_turn,stop_sequence}`) 턴만 방출. 생성 중인 마지막 턴은 보류.

## 데이터 디렉터리

런타임 상태는 `~/.wim-backoffice-prompt-agent/`에 보관(자동 생성):

```
~/.wim-backoffice-prompt-agent/
  state.json        # 파일별 byte offset(스캐너 진행 상황)
  queue/            # 디스크 영속 이벤트 배치(자동 드레인)
  agent.log         # 데몬(run-once/self-update) 진단 로그(5MB 초과 시 agent.log.1로 롤오버)
```

데몬은 콘솔 없이(Windows GUI 빌드 / launchd·systemd) 돌기 때문에 진단 출력을 `agent.log`에 남긴다. 대화형 명령(`enroll`/`install`/`status`/`update`)은 터미널(stdout)로 바로 출력한다.

토큰은 OS별로 저장합니다 — macOS는 login Keychain, Windows는 DPAPI 파일, Linux는 `~/.wim-backoffice-prompt-agent/device-token`(0600) 파일.

## 플랫폼 / 알려진 제약

- **지원 OS = macOS · Linux · Windows**(3종 모두 `enroll`·`install`·`run-once` 구현 완료). macOS/Linux가 P1, Windows가 P2로 이어서 구현됐다. Windows는 토큰을 DPAPI 파일로 저장하고 데몬은 작업 스케줄러(Task Scheduler)로 등록한다(위 설치 섹션 참고). Linux는 토큰을 0600 파일로 저장한다(외부 도구 불필요, 아래 참고).
- **macOS 백그라운드 항목 알림은 OS 정책 — 코드로 완전 제거 불가**. `install`이 등록하는 launchd 사용자 에이전트를 macOS(Ventura+)가 로그인/백그라운드 항목으로 취급해 "백그라운드 항목이 추가됨" 계열 알림을 띄운다. 15분 주기 실행 자체는 재등록하지 않으므로(=주기적 재알림 아님), self-update가 릴리스마다 바이너리를 교체할 때 재고지될 수 있는 정도다. 확인은 **시스템 설정 > 일반 > 로그인 항목**에서 가능하다. plist에 `StandardOutPath`/`StandardErrorPath`를 걸어 데몬 출력을 `agent.log`로 캡처하지만(재설치 시 반영), 알림 자체를 없애지는 못한다.
- **수집 어댑터 = Claude Code · Codex**(2종 구현 완료). 수집원은 `~/.claude/projects/**/*.jsonl`·`~/.codex/sessions/**/rollout-*.jsonl` 두 갈래다. Cursor 등 타 도구는 **미구현**(어댑터 추가로 확장 예정).
- **30일 보관 한계**: Claude Code가 30일(`cleanupPeriodDays` 기본값) 지난 transcript를 자동 삭제하므로, **에이전트 설치 이전의 오래된 세션은 소급 수집 불가**. 설치 이후엔 데몬 주기 스캔(15분)으로 만료 전에 전부 잡히므로 유실 없음. 보관을 늘리려면 각 머신 `~/.claude/settings.json`의 `cleanupPeriodDays`를 키워야 하며 이는 에이전트 범위 밖이다.
- **Linux는 외부 의존성 없음**: 예전엔 `secret-tool`(libsecret)로 키링에 저장했으나, 실행 중인 secret service(gnome-keyring + D-Bus 세션)를 요구해 헤드리스 서버·SSH·WSL·컨테이너에서 못 썼다. 이제 `~/.wim-backoffice-prompt-agent/device-token`(0600 파일)에 저장하므로 데스크톱/헤드리스 무관하게 동작한다.

## 테스트

```bash
go test ./...
```

전 패키지 테스트 + `internal/e2e/`의 통합 테스트(임시 디렉터리에 가짜 세션을 만들어 mock HTTP 서버 대상으로 파이프라인 전체를 검증, 키체인·데몬 불필요)를 포함합니다.

## 프라이버시

전사 프롬프트 역량 코칭을 위한 fleet-wide 수집이며, 열람 모델은 **NAMED_ADMIN**(개인 실명으로 관리자 열람)입니다. 전송 전 로컬 Redactor + 중앙 PII 마스킹의 이중 안전망이 적용됩니다. `enroll`은 명시적 동의 행위입니다.
