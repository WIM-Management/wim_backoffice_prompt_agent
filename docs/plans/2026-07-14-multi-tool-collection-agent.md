# Agent: Multi-tool 수집(Codex·Gemini) + 모델 캡처 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Go 수집 에이전트가 Claude Code에 더해 Codex·Gemini 세션을 수집하고, 전 어댑터가 사용 모델을 캡처하도록 확장한다.

**Architecture:** 어댑터 인터페이스를 int64 offset → opaque `cursor []byte`로 리팩터(적재 상태를 어댑터가 자기 의미로 인코딩). Codex는 append-only 바이트오프셋, Gemini는 emitted-identity 셋 + mtime fast-path. 머신전역 소스(`~/.codex`,`~/.gemini`)는 머신-primary 토큰으로 1회 수집. redact는 기존 중앙 경로에 패턴만 확장.

**Tech Stack:** Go 1.22(표준 라이브러리만), launchd/systemd 데몬, JSON.

## Global Constraints

- 외부 의존성 추가 금지(표준 라이브러리만 — `go.mod` 유지).
- `model.Event`의 필드는 `json` 태그로 backend 계약과 일치. `Model string \`json:"model,omitempty"\``.
- content_hash는 클라이언트가 계산 안 함(서버 전담). 클라이언트는 이벤트만 올림.
- commit-after-enqueue 순서 불변(§4.5 zero-loss): Enqueue(디스크) → commit(cursor 전진) → Drain(업로드).
- 수집 소스는 **구조화 세션 파일만**. `~/.codex/history.jsonl`·`~/.gemini/tmp/*/logs.json` 절대 읽지 않음.
- fail-loud는 **파일 단위 skip + 에러/로그**, 스캔 전체 abort 금지.
- 이 워크트리(`feat/multi-tool-collection-and-model-capture`)에서 작업, push는 `git push -u origin <branch>`.

---

### Task 1: Event.Model 필드 + cursor 인터페이스 리팩터

**Files:**
- Modify: `internal/model/event.go`
- Modify: `internal/state/state.go` (`FileState`에 `Cursor []byte`)
- Modify: `internal/scanner/scanner.go` (어댑터 반환 cursor/FileState 통째 영속)
- Modify: `internal/adapter/claudecode/claudecode.go` (`Parse` 시그니처 전환 + 레거시 offset seed)
- Test: `internal/adapter/claudecode/claudecode_test.go`, `internal/state/state_test.go`

**Interfaces:**
- Produces:
  - `model.Event.Model string`
  - `model.Adapter.Parse(file string, cursor []byte, now time.Time) ([]model.Event, []byte, error)`
  - `state.FileState{ Offset int64; Size int64; Mtime int64; Cursor []byte }` (Offset 유지 = 전환기 dual-write)
  - claude/codex용 헬퍼: `EncodeByteCursor(off int64) []byte` / `DecodeByteCursor(cur []byte, legacyOffset int64) int64` (nil이면 legacyOffset seed)

- [ ] **Step 1: 실패 테스트 — 레거시 offset seed + cursor 왕복**

`state_test.go`에 레거시 state.json(`{"files":{"/x":{"offset":100}}}`) 로드 → `DecodeByteCursor(fs.Cursor, fs.Offset) == 100` 단언. 신 state는 `EncodeByteCursor(250)` 왕복 == 250.

- [ ] **Step 2: 실패 확인**

Run: `go test ./internal/state/... ./internal/adapter/claudecode/...`
Expected: FAIL (심볼 없음).

- [ ] **Step 3: 인터페이스·상태·헬퍼 구현**

- `event.go`: `Adapter.Parse` 시그니처 교체, `Event.Model` 추가.
- `state.go`: `Cursor []byte` 필드 추가(`json:"cursor,omitempty"`). `Load()`의 관대한 unmarshal 유지.
- `EncodeByteCursor`/`DecodeByteCursor`(예: `strconv`로 int64 ↔ []byte; nil/빈 → legacyOffset).
- `scanner.go`: `Parse(p, d.Files[p].Cursor, now)` 호출, 반환 cursor를 **FileState에 통째로 저장**(기존 `FileState{Offset: newOff}` 합성 제거 — mtime/size/cursor 보존). 전환기: byte 어댑터는 cursor + Offset 양쪽 기록.
- `claudecode.go`: `Parse`가 cursor를 byte offset으로 디코드(`DecodeByteCursor(cursor, 0)`), 기존 로직 그대로, 새 offset을 `EncodeByteCursor`로 반환. 레거시 seed 경로는 scanner가 `d.Files[p].Offset`을 넘겨 처리하거나, cursor nil 시 Offset 사용.

- [ ] **Step 4: 통과 확인 + 기존 claude 골든 회귀**

Run: `go test ./internal/...`
Expected: PASS (claude 기존 테스트 포함 — resume/idle/synthetic 동작 불변).

- [ ] **Step 5: 커밋**

```bash
git add internal/model/event.go internal/state/state.go internal/scanner/scanner.go internal/adapter/claudecode/ 
git commit -m "refactor(adapter): opaque cursor interface + Event.Model; preserve legacy offset"
```

---

### Task 2: Claude 어댑터 모델 캡처

**Files:**
- Modify: `internal/adapter/claudecode/claudecode.go` (`rawLine.Message`에 `Model`, 이벤트 귀속)
- Test: `internal/adapter/claudecode/claudecode_test.go` + `testdata/model_multi.jsonl`

**Interfaces:**
- Consumes: Task1 `Event.Model`.
- Produces: claude 이벤트가 `Model` = 응답 assistant 라인의 `message.model`(`<synthetic>` 제외 → 빈 문자열).

- [ ] **Step 1: 실패 테스트 — 멀티모델 + synthetic 제외**

`testdata/model_multi.jsonl`: 사람 프롬프트 2건, 각 응답 assistant 라인 model이 `claude-opus-4-8` / `claude-fable-5`, 그리고 `<synthetic>` 섞인 라인. 파싱 결과 이벤트[0].Model=="claude-opus-4-8", [1].Model=="claude-fable-5", `<synthetic>`는 무시.

- [ ] **Step 2: 실패 확인**

Run: `go test ./internal/adapter/claudecode/... -run Model`
Expected: FAIL.

- [ ] **Step 3: 구현**

`rawLine.Message`에 `Model string \`json:"model"\`` 추가. `assembleResponse`가 응답 라인 중 실모델을 집계(같은 message.id 기준 첫 비-`<synthetic>` model). 이벤트 생성 시 `Model: respModel`. `<synthetic>`·빈 값은 제외.

- [ ] **Step 4: 통과 확인**

Run: `go test ./internal/adapter/claudecode/...`
Expected: PASS.

- [ ] **Step 5: 커밋**

```bash
git add internal/adapter/claudecode/
git commit -m "feat(claudecode): capture per-turn model, exclude <synthetic>"
```

---

### Task 3: Codex 어댑터 — 골격 + 경로 + exec 제외

**Files:**
- Create: `internal/adapter/codex/codex.go`
- Test: `internal/adapter/codex/codex_test.go` + `testdata/` (interactive.jsonl, exec.jsonl)

**Interfaces:**
- Produces: `codex.New(home string) *Adapter`; `Name()=="CODEX"`; `SessionPaths()` = `<home>/.codex/sessions/*/*/*/rollout-*.jsonl`; append-only byte cursor.

- [ ] **Step 1: 실패 테스트 — exec 세션 skip, interactive만 수집**

`testdata/exec.jsonl`(session_meta originator=codex_exec) → 이벤트 0건. `testdata/interactive.jsonl`(originator=codex-tui, 사람 프롬프트 1 + 응답) → 이벤트 1건.

- [ ] **Step 2: 실패 확인**

Run: `go test ./internal/adapter/codex/...`
Expected: FAIL (패키지 없음).

- [ ] **Step 3: 골격 구현**

`rawLine{Type string; Payload json.RawMessage}` 후 `type`별 분기. `session_meta` payload에서 `originator`/`source`/`cwd` 파싱 → `originator=="codex_exec" || source=="exec"`이면 **파일 전체 skip**(빈 이벤트, cursor=파일끝). append-only라 byte cursor(Task1 헬퍼) 사용.

- [ ] **Step 4: 통과 확인**

Run: `go test ./internal/adapter/codex/...`
Expected: PASS.

- [ ] **Step 5: 커밋**

```bash
git add internal/adapter/codex/
git commit -m "feat(codex): adapter skeleton, session_meta parse, exec exclusion"
```

---

### Task 4: Codex — 프롬프트/응답 페어링 + 주입 필터 + compacted

**Files:**
- Modify: `internal/adapter/codex/codex.go`
- Test: `internal/adapter/codex/codex_test.go` + `testdata/injected.jsonl`, `testdata/compacted.jsonl`

**Interfaces:**
- Produces: 사람 프롬프트(role=user/input_text) → 응답(role=assistant/output_text) 페어. 주입·developer·compacted 제외.

- [ ] **Step 1: 실패 테스트 — 주입/개발자/compacted 제외, 실프롬프트만**

`testdata/injected.jsonl`: developer 라인, user에 `# AGENTS.md instructions`·`<environment_context>`·`<permissions instructions>`·`<turn_aborted>` 주입, 그리고 진짜 프롬프트 1건 → 이벤트 1건(진짜만). `testdata/compacted.jsonl`: `type=compacted`(replacement_history 3건) + 그 뒤 진짜 프롬프트 1건 → 이벤트 1건(replacement_history 미방출, 후속 프롬프트 수집).

- [ ] **Step 2: 실패 확인**

Run: `go test ./internal/adapter/codex/... -run Filter`
Expected: FAIL.

- [ ] **Step 3: 구현**

- 주입 판정: `isCodexSynthetic(role, text)` — `role=="developer"` true; 또는 TrimSpace 후 첫 토큰이 다음 태그로 시작: `<environment_context>`,`<permissions instructions>`,`<turn_aborted>`,`<user_instructions>`,`<apps_instructions>`,`<skills_instructions>`,`<collaboration_mode>`,`<plugins_instructions>`, 또는 프리앰블 `# AGENTS.md instructions`.
- `type=compacted` 레코드는 그 `replacement_history`만 무시(스킵), 파싱 커서는 계속 진행.
- 페어링: 사람(user,input_text,비주입) 다음 assistant(output_text)까지가 응답. claude와 동일한 "다음 사람 프롬프트 전까지" 경계.

- [ ] **Step 4: 통과 확인**

Run: `go test ./internal/adapter/codex/...`
Expected: PASS.

- [ ] **Step 5: 커밋**

```bash
git add internal/adapter/codex/
git commit -m "feat(codex): prompt/response pairing, structural injection filter, compacted handling"
```

---

### Task 5: Codex — per-turn 모델 귀속

**Files:**
- Modify: `internal/adapter/codex/codex.go`
- Test: `internal/adapter/codex/codex_test.go` + `testdata/model_switch.jsonl`

**Interfaces:**
- Produces: 각 프롬프트에 "그 위치 이전 최신 `turn_context.payload.model`"를 귀속; 선행 turn_context 없으면 `Model=""`(NULL).

- [ ] **Step 1: 실패 테스트 — 모델 전환 + 선행 없음**

`testdata/model_switch.jsonl`: turn_context(model=gpt-5.5) → 프롬프트A → turn_context(model=gpt-5.3-codex-spark) → 프롬프트B. 그리고 맨 앞 turn_context 없이 온 프롬프트C. 결과: A.Model=="gpt-5.5", B.Model=="gpt-5.3-codex-spark", C.Model=="".

- [ ] **Step 2: 실패 확인**

Run: `go test ./internal/adapter/codex/... -run Model`
Expected: FAIL.

- [ ] **Step 3: 구현**

파싱 순회 중 `type=turn_context` 만나면 `curModel = payload.model` 갱신. 프롬프트 이벤트 생성 시 `Model: curModel`(초기값 ""). cwd는 session_meta.cwd, surface="cli".

- [ ] **Step 4: 통과 확인 + 패키지 전체**

Run: `go test ./internal/adapter/codex/...`
Expected: PASS.

- [ ] **Step 5: 커밋**

```bash
git add internal/adapter/codex/
git commit -m "feat(codex): per-turn model attribution with null fallback"
```

---

### Task 6: Gemini 어댑터 — 골격 + 3포맷 감지 + 세션당 단일 권위

**Files:**
- Create: `internal/adapter/gemini/gemini.go`
- Test: `internal/adapter/gemini/gemini_test.go` + `testdata/` (monolithic.json, journal.jsonl, nested/uuid/x.jsonl)

**Interfaces:**
- Produces: `gemini.New(home string) *Adapter`; `Name()=="GEMINI"`; `SessionPaths()` = monolithic/journal/nested 3글롭 합집합; 포맷 감지 + 세션당 우선순위(C중첩>A모놀리식>B저널) 채택.

- [ ] **Step 1: 실패 테스트 — 크로스포맷 dedup(같은 sessionId면 1개만)**

같은 `sessionId`가 monolithic.json과 nested/x.jsonl 양쪽에 존재 → 한 스캔에서 **nested만 채택, monolithic skip**. 프롬프트가 중복 방출되지 않음(sessionId별 1소스).

- [ ] **Step 2: 실패 확인**

Run: `go test ./internal/adapter/gemini/...`
Expected: FAIL.

- [ ] **Step 3: 구현**

- `SessionPaths`: `<home>/.gemini/tmp/*/chats/session-*.json`, `...session-*.jsonl`, `...chats/*/*.jsonl`, `...chats/*/*.json` 글롭 합집합.
- 포맷 감지: 확장자 + 첫 바이트(`{` 단일객체 vs 줄단위). 각 파일에서 sessionId 추출.
- **주의: SessionPaths는 파일 목록만 반환**하므로, 크로스포맷 우선순위는 어댑터 내부 상태(스캔당 seen sessionId)로 처리하거나, `Parse`에서 sessionId 확인 후 하위-우선 파일이면 빈 방출. → 스캔 1회 내 sessionId→선택파일 맵을 어댑터가 lazily 구성(첫 접근 시 SessionPaths 전체를 우선순위로 정렬해 sessionId별 승자 결정, 비승자 파일 Parse는 no-op).

- [ ] **Step 4: 통과 확인**

Run: `go test ./internal/adapter/gemini/...`
Expected: PASS.

- [ ] **Step 5: 커밋**

```bash
git add internal/adapter/gemini/
git commit -m "feat(gemini): adapter skeleton, 3-format detection, per-session authoritative source"
```

---

### Task 7: Gemini — 파싱 + 구조적 주입 필터 + 모델 + cwd

**Files:**
- Modify: `internal/adapter/gemini/gemini.go`
- Test: `internal/adapter/gemini/gemini_test.go` + `testdata/info_journal.jsonl`, `testdata/projects.json`

**Interfaces:**
- Produces: `type=user`(parts/string) → 프롬프트, `type=gemini` → 응답, Model=응답 메시지 model. `type=info/null`·`$set` 저널·슬래시·빈 제외. cwd=directories[0] 없으면 tmp dirname→projects.json 역매핑(모호 시 dirname).

- [ ] **Step 1: 실패 테스트 — info/journal 제외, parts join, 모델, cwd**

`journal` 포맷에 `$set`·`type=null`·`type=info`("Request cancelled.") 섞고 진짜 user(parts `[{text}]`)+gemini(model=gemini-3-flash-preview) 페어 → 이벤트 1건, Model 일치. cwd: directories 없는 모놀리식은 `testdata/projects.json`{"projects":{"/Users/x/proj":"proj"}} + tmp dir명 `proj` → cwd=="/Users/x/proj".

- [ ] **Step 2: 실패 확인**

Run: `go test ./internal/adapter/gemini/... -run Parse`
Expected: FAIL.

- [ ] **Step 3: 구현**

- 메시지 정규화: monolithic은 `messages[]`, journal은 줄단위 필터(`$set`·헤더·null 제외). content가 배열이면 parts.text join, 문자열이면 그대로.
- 주입 제외(구조 우선): `type in {info,null}` skip, `$set` skip, 텍스트가 `/`로 시작(슬래시) 또는 빈 문자열 skip. (영어 프리픽스 의존 최소화; `System:` 잔여는 명시 프리픽스로 보조하되 문서화.)
- 모델: 프롬프트에 대응하는 다음 `type=gemini` 메시지의 `model`.
- cwd: 파일에 `directories[]` 있으면 [0]; 없으면 tmp 디렉토리명 → projects.json(`{"projects":{path:name}}`) 역매핑(name→path). name 충돌·미발견이면 dirname 그대로(approximate).

- [ ] **Step 4: 통과 확인**

Run: `go test ./internal/adapter/gemini/...`
Expected: PASS.

- [ ] **Step 5: 커밋**

```bash
git add internal/adapter/gemini/
git commit -m "feat(gemini): parse messages, structural injection filter, model, cwd resolution"
```

---

### Task 8: Gemini — emitted-identity cursor + mtime fast-path

**Files:**
- Modify: `internal/adapter/gemini/gemini.go`
- Test: `internal/adapter/gemini/gemini_test.go` + `testdata` same-second append 시나리오

**Interfaces:**
- Produces: cursor JSON `{mtimeNano int64, size int64, emitted []string}` (emitted = 방출한 4-튜플 `sourceTool|sessionId|promptText|promptTs`의 셋). 셋에 없는 프롬프트만 방출. size OR mtime 변경 없으면 파일 skip.

- [ ] **Step 1: 실패 테스트 — 재스캔 시 신규만 방출, 동일-초 append 감지**

1차 Parse(cursor nil) → 2건 방출, cursor에 emitted 2. 파일에 프롬프트 1건 추가(mtime 동일 초, size 증가) → 2차 Parse(cursor) → **신규 1건만** 방출(size 변경으로 게이트 통과, emitted 셋으로 기존 2건 제외).

- [ ] **Step 2: 실패 확인**

Run: `go test ./internal/adapter/gemini/... -run Cursor`
Expected: FAIL.

- [ ] **Step 3: 구현**

- cursor 디코드: nil이면 빈 셋. `os.Stat`으로 mtime(UnixNano)·size. **size != prev.size || mtimeNano != prev.mtime**이면 파싱, 아니면 no-op(빈 방출, cursor 유지).
- 파싱 후 각 settled 프롬프트의 4-튜플 identity 계산, `emitted` 셋에 없으면 방출 + 셋에 추가. cursor 재인코드(mtime,size,emitted). emitted 셋은 파일당 상한(예 최근 N개) 두어 무한증가 방지(초과 시 오래된 것 제거 — 재방출은 서버 dedup이 흡수).
- 재방출 페이로드 상한: 한 번에 방출 이벤트 수 상한(예 500) 두고 초과분은 다음 스캔.

- [ ] **Step 4: 통과 확인**

Run: `go test ./internal/adapter/gemini/...`
Expected: PASS.

- [ ] **Step 5: 커밋**

```bash
git add internal/adapter/gemini/
git commit -m "feat(gemini): emitted-identity cursor with mtime/size fast-path"
```

---

### Task 9: fail-loud 미지원 포맷 (파일 단위)

**Files:**
- Modify: `internal/adapter/gemini/gemini.go`, `internal/adapter/codex/codex.go`
- Test: 각 `*_test.go` (깨진/미지원 파일)

**Interfaces:**
- Produces: 미지원/파싱불가 파일은 이벤트 0 + 에러 로그(stderr), 스캔 계속.

- [ ] **Step 1: 실패 테스트 — 깨진 파일은 skip+로그, 나머지 정상**

미지원 포맷/깨진 JSON 파일과 정상 파일 혼재 → 정상 파일 이벤트는 수집, 깨진 파일은 빈 방출(에러 반환은 하되 scanner가 파일 단위 격리).

- [ ] **Step 2: 실패 확인 → Step 3: 구현**

Parse가 해당 파일에서 recover/에러 반환, scanner는 이미 파일 단위 `continue`(현 `scanner.go` `if err != nil { continue }`). 어댑터는 미지원 포맷 감지 시 명시적 error(`fmt.Errorf("unsupported gemini format: %s", file)`)를 반환해 로그에 남게.

- [ ] **Step 4: 통과 → Step 5: 커밋**

```bash
git add internal/adapter/gemini/ internal/adapter/codex/
git commit -m "feat(adapters): fail-loud per-file on unsupported/corrupt formats"
```

---

### Task 10: redact 패턴셋 확장

**Files:**
- Modify: `internal/redact/redact.go`
- Test: `internal/redact/redact_test.go`

**Interfaces:**
- Produces: `redact.Scrub`가 Google OAuth(`ya29.`, `1//`), `bearer <tok>`, 앵커된 `(api[_-]?key|secret|token|password)=value`도 마스킹. 기존 `sk-`/PEM/`gh[pousr]_`/`AKIA` 유지.

- [ ] **Step 1: 실패 테스트 — 신규 패턴 pos + 과다치환 neg**

pos: `ya29.abc...`, `1//0abc...`, `Bearer sk_live_...`, `api_key=SECRET123` 마스킹됨. neg: `format=json`, `--mode=fast`는 **치환 안 됨**.

- [ ] **Step 2: 실패 확인**

Run: `go test ./internal/redact/...`
Expected: FAIL.

- [ ] **Step 3: 구현**

기존 패턴 배열에 추가:
- `ya29\.[0-9A-Za-z_\-]+`, `1//[0-9A-Za-z_\-]+`
- `(?i)bearer\s+[A-Za-z0-9._\-]+`
- `(?i)(api[_-]?key|secret|token|password)\s*=\s*\S+` (키 이름 앵커 — 일반 `key=` 과다치환 방지)

- [ ] **Step 4: 통과 확인**

Run: `go test ./internal/redact/...`
Expected: PASS (신규 + 기존 회귀).

- [ ] **Step 5: 커밋**

```bash
git add internal/redact/
git commit -m "feat(redact): add Google OAuth, bearer, anchored key=value patterns"
```

---

### Task 11: 배선 — 머신-primary + codex/gemini 등록

**Files:**
- Modify: `cmd/wim-backoffice-prompt-agent/main.go`
- Modify: `internal/registry/registry.go` (primary 개념)
- Test: `internal/registry/registry_test.go`, `internal/e2e/e2e_test.go`

**Interfaces:**
- Consumes: `codex.New`, `gemini.New`.
- Produces: `runOnce`가 루프 밖에서 primary 엔트리를 1회 결정하고, 그 토큰/큐로 `~/.codex`+`~/.gemini`를 1회 수집. no/다중-primary·토큰폐기는 skip+로그.

- [ ] **Step 1: 실패 테스트 — 머신 소스 1회, 정확한 토큰 귀속**

registry에 default + 추가 엔트리 2개일 때, codex/gemini 어댑터는 **정확히 1회**(primary 토큰)만 등록됨. primary 토큰 없으면(합성 default에 토큰 부재) 머신 패스 skip.

- [ ] **Step 2: 실패 확인**

Run: `go test ./internal/registry/... ./internal/e2e/...`
Expected: FAIL.

- [ ] **Step 3: 구현**

- `registry`: primary 판정(예: `IsPrimary` 플래그 또는 `DefaultTokenKey` 보유 + 실제 enroll된 토큰). helper `PrimaryEntry(entries) (Entry, bool)`.
- `runOnce`: 기존 per-entry 루프는 그대로(claude만). 루프 후 `PrimaryEntry`로 primary 결정 → 있으면 `scanner.New([]model.Adapter{codex.New(home), gemini.New(home)}, store, idle)`로 머신 패스 1회 실행, primary 토큰의 `queueDirFor`로 enqueue/drain. 토큰 없으면 로그+skip(폴백 금지).
- claude 어댑터 등록(`collectDir`)은 불변.

- [ ] **Step 4: 통과 확인 + 전체**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 5: 커밋**

```bash
git add cmd/ internal/registry/ internal/e2e/
git commit -m "feat(wiring): machine-primary pass for codex/gemini collection"
```

---

### Task 12: 통합 검증 + 로컬 실행

**Files:**
- Test: `internal/e2e/e2e_test.go`

- [ ] **Step 1: e2e — 실제 collectDir 경로로 3어댑터 + redact 실행**

임시 HOME에 claude/codex/gemini 픽스처 배치 → `run-once` 상당 경로 실행 → 큐에 3툴 이벤트, 시크릿 마스킹됨, 모델 채워짐 확인. (e2e가 프로덕션 scrub 경로를 실제로 태우도록 — 재구현 금지.)

- [ ] **Step 2: 전체 테스트 + vet + build (전 OS 타깃)**

Run: `go vet ./... && go test ./... && GOOS=darwin GOARCH=arm64 go build ./... && GOOS=linux GOARCH=amd64 go build ./... && GOOS=windows GOARCH=amd64 go build ./...`
Expected: 전부 성공.

- [ ] **Step 3: 실제 머신 로컬 리허설(선택, 읽기전용)**

로컬 `~/.codex`·`~/.gemini`로 `run-once`를 **테스트 백엔드/드라이런**으로 돌려 파싱 결과·모델·cwd·주입필터를 육안 검증(실 업로드 금지 or 스테이징 토큰).

- [ ] **Step 4: 커밋 + PR**

```bash
git add -A
git commit -m "test(e2e): end-to-end multi-tool collection with redaction and model"
git push -u origin feat/multi-tool-collection-and-model-capture
gh pr create --title "feat: Codex/Gemini collection + model capture" --body "..."
```

## Self-Review 결과
- 스펙 §4~9·11 커버: cursor 리팩터(T1), claude 모델(T2), codex(T3-5), gemini(T6-8), fail-loud(T9), redact(T10), 배선(T11), e2e(T12).
- 배포 순서(§10): backend 계획 먼저 → redact(T10) 포함 agent 릴리스. **T10은 신규 어댑터 활성(T11) 전 머지되어야** 함(플랜 내 순서로 보장).
- 플레이스홀더 없음(테스트는 실제 파일 시그니처 확인 지시). 타입 일관: `Parse(file, cursor []byte, now)`·`Event.Model`·cursor 헬퍼 명칭 전 태스크 동일.
- 잔여 리스크(스펙 §12): 모델 collision 수용, Gemini `System:` 잔여 프리픽스, 버전 드리프트 fail-loud — 계획에 반영됨.
```
