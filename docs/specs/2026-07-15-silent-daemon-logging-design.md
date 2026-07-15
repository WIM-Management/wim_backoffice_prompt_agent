# 조용한 백그라운드 실행 + 파일 로깅 설계

- 작성일: 2026-07-15
- 브랜치: `fix/silent-daemon-logging`
- 관련 리포트: `cmd 창 주기적 깜빡임 — 원인 분석 및 조치 리포트` (2026-07-15)

## 배경 / 문제

배포된 에이전트가 15분 주기(launchd / systemd / Task Scheduler)로 `run-once`를 실행한다.
현재 두 가지 사용자 체감 문제가 있다.

1. **Windows: cmd 콘솔 창 깜빡임** — 바이너리가 콘솔 서브시스템(PE Subsystem=3)으로
   빌드돼 있어, Task Scheduler가 15분마다 실행할 때마다 검은 콘솔 창이 번쩍였다 사라진다.
   - 원인 코드: `.github/workflows/release.yml` (windows 빌드에 `-H=windowsgui` 없음),
     `internal/daemon/taskscheduler.go:14` (`schtasks ... /TR "<exe> run-once"`).
2. **macOS: "백그라운드 항목" 알림** — LaunchAgent 등록에 대해 macOS가 로그인/백그라운드
     항목으로 고지한다. self-update가 릴리스마다 바이너리를 교체(`os.Rename`)할 때 macOS의
     background-task 관리자(btm)가 재고지하는 것으로 추정된다.

### 조사로 확정된 사실

- `InstallMac`(= `launchctl load`)는 **`install` 명령에서만** 호출된다.
  `run-once`(15분 주기)와 `maybeSelfUpdate`는 launchd를 **재등록하지 않는다**
  (`internal/daemon/launchd.go:44`, `cmd/wim-backoffice-prompt-agent/main.go:340`).
  → **15분마다 재등록되는 버그는 없다.**
- self-update는 바이너리 inode만 `os.Rename`으로 원자적 교체한다
  (`internal/updater/replace_unix.go`). plist는 건드리지 않는다.
- 진단 출력은 전부 `stdout`/`stderr`로 나간다 (`main.go:66,103,194`, 등).
  macOS launchd plist에는 `StandardOutPath`가 없어 **데몬 로그는 이미 유실 중**이고,
  Windows를 `-H=windowsgui`로 빌드하면 stderr가 사라져 같은 유실이 발생한다.
  → 콘솔 제거는 **파일 로깅 전환을 선행 조건으로 요구**한다.
- SSH/헤드리스 enroll(localhost 콜백) 문제는 **이미 해결·머지됨**(PR #21,
  `enroll --port N` + `ssh -L` 터널). 본 작업 범위 밖.

## 목표 (수용 기준)

- [ ] Windows: 스케줄러가 `run-once`를 실행해도 콘솔 창이 뜨지 않는다.
- [ ] Windows: 사용자가 터미널에서 `enroll`/`status` 등을 직접 실행하면 출력(특히 OAuth
      URL)이 **정상적으로 콘솔에 보인다**.
- [ ] 데몬 경로(`run-once`, `maybeSelfUpdate`)의 진단 출력이
      `~/.wim-backoffice-prompt-agent/agent.log`에 기록된다(크기 상한 포함).
- [ ] macOS plist에 `StandardOutPath`/`StandardErrorPath`가 로그 파일로 설정된다.
- [ ] macOS 알림이 OS 정책상 완전 제거 불가임을 문서화하고, 확인 경로를 안내한다.
- [ ] 기존 동작(수집·enroll·self-update·주기·태스크명)은 변경되지 않는다.

## 비목표 (범위 밖)

- 15분 주기 자체 변경.
- SSH/헤드리스 enroll(PR #21에서 처리 완료).
- Linux(콘솔 증상 없음) — 파일 로깅 공통 적용 대상이나 별도 개선은 하지 않음.
- macOS 백그라운드 알림의 완전 제거(OS 정책상 불가).

## 설계

### 1. 파일 로깅 (공통 기반)

새 패키지 `internal/agentlog`(가칭) 또는 기존 위치에 로그 싱크를 추가한다.

- 로그 파일: `~/.wim-backoffice-prompt-agent/agent.log`.
- 크기 상한(예: 5MB) 초과 시 단순 롤오버(`agent.log` → `agent.log.1`, 1세대만 유지).
- **대화형 vs 데몬 출력 분리:**
  - 대화형 명령(`enroll`, `install`, `uninstall`, `status`, `update`)의 사용자 대상 출력은
    **stdout 유지**. enroll의 OAuth URL 등은 사용자가 봐야 하므로 파일로 보내지 않는다.
  - 데몬 경로(`run-once`, `maybeSelfUpdate`)의 진단/에러 출력은 **파일 로그로** 보낸다.
- 구현 방향: 현재 `fmt.Fprintln(os.Stderr, ...)` / `fmt.Printf(...)` 직접 호출을,
  데몬 경로에 한해 로그 싱크 호출로 치환한다. 대화형 경로는 그대로 둔다.

인터페이스(단위 경계):
- 무엇을 하나: 데몬 진단 메시지를 타임스탬프와 함께 로그 파일에 append, 상한 시 롤오버.
- 어떻게 쓰나: `agentlog.Setup()` 1회 호출 → `log.Printf` 스타일 또는 전용 함수.
- 의존: `os.UserHomeDir`, 파일 I/O만. 네트워크/설정 의존 없음.

### 2. Windows: `-H=windowsgui` + AttachConsole

- **빌드:** `.github/workflows/release.yml`의 windows 빌드 ldflags에 `-H=windowsgui` 추가.
  로컬 빌드 안내(`README.md`)도 함께 갱신.
- **AttachConsole (엣지케이스 처리):** windowsgui는 *모든* 실행에서 콘솔을 뗀다. 그대로 두면
  터미널에서 직접 `enroll`을 돌려도 OAuth URL이 안 보여 인증이 불가능하다.
  → `main` 시작 시 Windows 전용으로 `kernel32.AttachConsole(ATTACH_PARENT_PROCESS)` 호출:
  - 부모 콘솔 있음(터미널 실행) → stdout/stderr 핸들을 그 콘솔에 재연결 → 대화형 출력 정상.
  - 부모 콘솔 없음(스케줄러 실행) → 실패를 무시하고 조용히 진행. 로그는 파일로.
  - 파일: `cmd/wim-backoffice-prompt-agent/console_windows.go`(build tag `windows`) +
    non-windows용 no-op `console_other.go`.
- **스케줄러 액션·주기·태스크명은 변경하지 않는다.** (`conhost --headless` 래핑은 채택 안 함 —
  windowsgui가 근본 해결이며 구형 Win10 conhost 유무에 의존하지 않음.)

### 3. macOS: plist 로그 경로 + 문서화

- `internal/daemon/launchd.go`의 plist 템플릿에 다음 추가:
  - `<key>StandardOutPath</key><string>~전개된 agent.log 경로</string>`
  - `<key>StandardErrorPath</key><string>~전개된 동일 경로</string>`
  - (경로는 `$HOME` 전개된 절대경로로 기록.)
- 재등록 버그가 없음은 조사로 확정 → 추가 멱등화 코드는 불필요.
- README/운영 문서에: macOS의 백그라운드 항목 알림은 launchd 등록에 대한 **OS 정책**이라
  코드로 완전 제거 불가하며, self-update 시 재고지될 수 있음을 명시. 사용자 확인 경로
  (시스템 설정 > 일반 > 로그인 항목)를 안내.

## 데이터 흐름

```
install (대화형)
  └─ InstallMac/Windows/Linux  → 스케줄러 등록 (stdout로 진행상황)
run-once (데몬, 스케줄러가 15분마다 호출)
  ├─ runOnce(cfg)        → 수집. 에러 → agent.log (Windows: 콘솔 없음 / mac: plist가 캡처)
  └─ maybeSelfUpdate     → 업데이트. 로그 → agent.log
enroll/status/update (대화형, 터미널)
  └─ Windows: AttachConsole 성공 → 콘솔로 출력 / 그 외: stdout
```

## 엣지케이스 / 실패경로

- **Windows 터미널 enroll**: AttachConsole 성공 시 URL 보임. 실패(부모 콘솔 없음) 시에도
  URL은 로그 파일에 남도록 enroll URL 출력은 stdout + 로그 양쪽 고려. (구현 시 결정:
  enroll은 대화형이므로 stdout 유지가 기본, 로그 병행은 선택.)
- **로그 파일 쓰기 실패**(권한/디스크): 로깅 실패가 수집·업데이트를 막지 않도록 best-effort.
- **홈 디렉터리 미확정**(`os.UserHomeDir` 에러): 로그를 임시 위치 또는 무음 폴백, 크래시 금지.
- **롤오버 경합**(동시 실행): 스케줄러 특성상 동시성 낮음. rename 기반 단순 롤오버로 충분.
- **macOS plist 경로에 공백/한글 홈**: `$HOME` 전개된 절대경로를 XML-escape하여 기록.

## 테스트 전략

- `internal/updater`, `internal/daemon` 기존 테스트 유지·확장.
- 파일 로깅: 롤오버 임계 초과 시 세대 교체, 쓰기 실패 시 무해 폴백 단위 테스트.
- launchd plist: `StandardOutPath` 포함 및 경로 전개/이스케이프 골든 테스트.
- Windows AttachConsole: 순수 로직 분리가 어려우면 빌드 태그 컴파일 확인 +
  수동 검증 체크리스트(터미널 enroll URL 노출 / 스케줄 무음)로 보완.
- 릴리스 워크플로: windows 빌드 산출물의 PE Subsystem이 GUI(2)인지 확인하는 수동/CI 스텝.

## 롤아웃

- 머지 후 새 릴리스 → 기존 설치본은 self-update로 새 바이너리 수신.
- **주의:** 이미 등록된 Windows 스케줄러 액션은 여전히 `"<exe> run-once"`를 가리키므로,
  새 바이너리(windowsgui)면 재설치 없이도 창이 사라진다(액션 변경 불필요).
- macOS plist의 `StandardOutPath`는 **재설치(`install` 재실행) 시** 반영된다. 기존 설치본은
  self-update로 바이너리만 갱신되므로 plist는 안 바뀜 → 문서에 "로그 경로 적용은 재설치 필요" 명시.
