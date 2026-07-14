# Multi-tool 수집 확장 + 모델 캡처 — 설계 (v3)

*작성일: 2026-07-14*
*대상 레포: `wim_backoffice_prompt_agent`(Go, 주) + `wim_backoffice`(Kotlin backend, 종)*
*상태: 설계 확정(2라운드 adversarial 리뷰 반영). 다음 단계 = 구현 계획(writing-plans).*

## 1. 목표

로컬 프롬프트 수집 에이전트가 **Claude Code 외에 Codex·Gemini CLI 세션도 수집**하고, 모든 도구에서 **실제 사용 모델(model)을 함께 캡처**하도록 확장한다. 수집 데이터의 가치는 "각 사람이 자기 AI에게 *무엇을 묻는가*"의 정확한 기록이므로, 정합성(무성 유실·이중계상 0)과 프라이버시(시크릿 미유출)를 최우선으로 한다.

## 2. 범위 / 비범위

**범위**
- 신규 어댑터 2종: `codex`(CODEX), `gemini`(GEMINI). 둘 다 **프롬프트+응답 페어** 캡처(Claude와 일관 — 결정 A).
- 모든 어댑터(claude 포함)에 **모델 캡처**.
- 어댑터 인터페이스를 opaque cursor로 리팩터.
- 머신 전역 소스(`~/.codex`, `~/.gemini`) 수집 배선.
- redact 패턴셋 확장(신규 시크릿 형태) — 배포 전 필수.
- backend: `prompt_events.model` 컬럼 + 인제스트/admin 전파.

**비범위(YAGNI)**
- qwen·copilot 등 그 외 CLI (사용자 지시로 제외).
- `history.jsonl`/`logs.json`(평면 프롬프트) 수집 — 구조화 세션만이 유일 소스(이중계상 방지).
- gateway(litellm) 경로의 model.
- 과거 데이터·`REPO_FILE` 백필의 모델 소급.
- 모델 문자열 정규화(원문 그대로 저장; 정규화는 다운스트림 대시보드 책임).

## 3. 데이터 모델 변경

### 3.1 Agent (`internal/model/event.go`)
- `Event`에 `Model string \`json:"model,omitempty"\`` 추가.

### 3.2 Backend (`prompt_events`)
- 컬럼 추가: **`model TEXT NULL`** (varchar(80) 아님 — Bedrock/Vertex 정규화 id·미래 suffix 대비, Postgres에서 TEXT 비용 0, 레포가 `prompt_text`/`project_context`도 TEXT).
- Flyway 멱등 마이그레이션: `ALTER TABLE prompt_events ADD COLUMN IF NOT EXISTS model TEXT;`
- 전파: `PromptEventRecord`(net-new field) → `PromptEventPersister.toEntity` → `PromptEventJpaEntity` → admin `PromptEventResponse`, 그리고 인제스트 요청 DTO(`IngestPromptEventsRequest.EventItem`)에 model.
- **content_hash 공식·dedup 유니크 키 불변**: `sha256("{sourceTool}|{sessionId}|{promptText}|{promptTs}")`, key `(employee_id, source_tool, content_hash)`. 모델은 dedup 정체성이 아니다.
- **수용된 한계(문서화)**: 같은 프롬프트 텍스트 / 같은 초(promptTs는 초 해상도) / 같은 도구 / 다른 모델이면 dedup으로 1건 collapse(first-writer-wins). 두 리뷰어 firm 권고 = dedup 키 유지가 옳다(모델을 키/해시에 넣으면 모델 스위치 후 재방출이 **이중계상 회귀**를 낳음). 발생 빈도 극히 낮음(동일 초 동일 텍스트 재전송). 대시보드는 프롬프트 분포용이라 개별 collide 1건 오귀속 허용.

## 4. 어댑터 인터페이스 리팩터 (opaque cursor)

현재 `Parse(file, fromOffset int64, idleCutoff) ([]Event, int64, error)`는 append-only 바이트오프셋 전제라 3포맷을 못 담는다. 다음으로 교체:

```
Parse(file string, cursor []byte, now time.Time) ([]Event, []byte, error)
```

- 각 어댑터가 cursor 바이트를 자기 의미로 해석(claude/codex=바이트오프셋 인코딩, gemini=`{mtimeUnixNano, emittedIdentitySet}` JSON).
- `state.FileState`에 `Cursor []byte` 추가. **scanner는 어댑터가 반환한 cursor/FileState를 통째 영속**한다(현재 `scanner.go`가 `FileState{Offset: newOff}`만 써서 Size/Mtime을 버리는 버그 동시 수정 — 안 고치면 Gemini 게이트가 영속 안 됨).
- **commit-after-enqueue 불변 유지**: cursor는 여전히 enqueue 후에만 전진. Parse는 "첫 미완결 프롬프트 이전"에서 멈추는 resume 시맨틱을 보존(claude 기존 동작).
- **마이그레이션 계약**:
  - 레거시 state.json엔 `offset:int64`만 존재. `state.Load()`가 unmarshal 에러를 삼키므로(`_ = json.Unmarshal`), 신 필드는 안전히 nil.
  - 바이트오프셋 어댑터: `Cursor==nil && Offset>0`이면 Offset에서 cursor를 seed.
  - 전환기엔 **`Offset`+`Cursor` 양쪽 기록**(롤백 시 구 바이너리가 Offset으로 재개 가능 → 재방출 폭주 방지).
  - `[]byte`는 JSON에서 base64 문자열로 마샬됨(불투명 유지). 외부/손상 cursor는 거부하고 파일을 처음부터 재파싱(dedup이 흡수).
  - 레거시 state.json fixture 왕복 테스트 필수.

## 5. Codex 어댑터 (`internal/adapter/codex`, Name="CODEX")

- **SessionPaths**: `~/.codex/sessions/*/*/*/rollout-*.jsonl` (append-only 확인됨 → 바이트오프셋 cursor).
- **비대화형 제외**: `session_meta.payload`의 `originator==codex_exec` 또는 `source==exec`이면 파일 skip. config 플래그(default: 제외).
- **모델(per-turn)**: `turn_context.payload.model`에서 읽고 "현재까지 본 최신 turn_context"를 추적, 프롬프트를 그 시점 유효 모델로 귀속. **선행 turn_context가 없으면 `model=NULL`(빈 문자열/추측 금지)**. `session_meta`엔 model 없음(주의).
- **compacted 처리(레코드 단위)**: `type=compacted` 레코드의 `replacement_history` **배열만** 방출 제외. 그 뒤 이어지는 정상 `response_item/message/role=user` 프롬프트는 계속 파싱(경계 이후 실제 프롬프트 유실 금지). 오직 compacted 안에만 존재하는 프롬프트는 유실 없이 처리되는지 골든으로 확인.
- **사람 프롬프트**: `response_item`/`payload.type=message`/`role=user`/`content[].type=input_text`. 아래는 **role 무관, 첫 비공백 토큰이 태그면 제외**(구조적):
  - `role=developer` 전체
  - user에 주입되는 태그 전체 열거: `<environment_context>`, `<permissions instructions>`, `<turn_aborted>`, `<user_instructions>`, `<apps_instructions>`, `<skills_instructions>`, `<collaboration_mode>`, `<plugins_instructions>` 및 `# AGENTS.md instructions` 프리앰블.
  - 미포착 잔여 변형은 스펙에 "알면서 안 잡는 것"으로 명시(결정으로 남김).
- **응답**: `role=assistant`/`output_text`.
- **cwd/project_context**: `session_meta.payload.cwd` (turn_context.cwd 동일).
- **surface**: `cli`.

## 6. Gemini 어댑터 (`internal/adapter/gemini`, Name="GEMINI")

### 6.1 포맷 3종 + 세션당 단일 권위 포맷
디스크에 3형태 공존:
- (A) 모놀리식 `~/.gemini/tmp/<name>/chats/session-*.json` — `messages[]` 통째, 매 저장 전체 재작성.
- (B) `session-*.jsonl` — 헤더줄(type 없음) + 메시지 + 다수 `{"$set":{lastUpdated}}` 저널 + `type=null` 줄.
- (C) 중첩 `chats/<uuid>/*.{jsonl,json}` — `directories:[...]` 실경로 보유.
- **SessionPaths는 3형태 모두 글롭**(중첩 dir 포함).
- **크로스포맷 이중계상 방지**: 같은 세션이 여러 포맷에 있으면 **세션당 1개 권위 포맷만 채택**. 우선순위는 **(C) 중첩 > (A) 모놀리식 > (B) 헤더jsonl** 고정(C가 `directories[]` 실경로 보유 = 가장 풍부). 세션 동일성은 파일 내 `sessionId`로 판정, 한 스캔 내 같은 sessionId의 하위-우선 포맷 파일은 skip. "서버 dedup에 맡김" 금지. **구현 계획 착수 시 실제 dual-format 샘플로 A↔C의 sessionId 동일성을 1건 검증**해 우선순위 전제를 확정한다.

### 6.2 파싱
- 프롬프트 = `type=user`, `content`는 parts 배열 `[{text}]`(문자열도 허용) → join.
- **구조 신호로 주입 제외**(영어 프리픽스 의존 금지): `type=info`(예 "Request cancelled."), `type=null`, `$set` 저널 레코드, 슬래시/빈 프롬프트. `System: ...` 주입은 프리픽스가 아니라 가능하면 구조로 판정(불가 시 명시적 프리픽스 목록 + 잔여 문서화).
- 응답 = `type=gemini`. **모델**: gemini 메시지의 `model`(세션 중 변함). 프롬프트에 귀속할 모델은 **그 프롬프트에 대한 응답 메시지의 model**(정의 고정 — "응답 시점 모델").
- **cwd**: 신형은 `directories[0]`; 없으면 tmp 디렉토리명 → `~/.gemini/projects.json`(중첩 `{"projects":{realPath:shortname}}` 키)의 역매핑. **basename 충돌 시** 모호 → tmp dirname을 그대로 두고 context를 approximate로 표기(추측 금지). `projectHash`는 shortname과 다르므로 매핑에 쓰지 않음.

### 6.3 정합성/비용
- **emitted-identity 셋 필수(optional 아님)**: cursor에 이미 방출한 프롬프트의 서버 해시 4-튜플(sourceTool|sessionId|promptText|promptTs) 셋을 보관, **셋에 없는 프롬프트만 방출** → 스캔당 O(신규). 서버 dedup은 백스톱일 뿐.
- **mtime/size 게이트는 비용용 fast-path만**(정합성 근거 아님): `mtime`은 **UnixNano**, 게이트는 **size 변경 OR mtime 변경**(AND 아님) — 같은 초 재작성에 신규 프롬프트 놓치는 무성 유실 방지. 동일-초 append 골든 테스트.
- **재방출 페이로드 상한**: 대용량 세션이 큐에 통째로 반복 적재되지 않도록 배치 크기/상한 가드.

## 7. Claude 어댑터 변경

- assistant 라인의 `message.model`을 per-turn으로 파싱해 이벤트에 귀속. **`<synthetic>` 모델 제외**(NULL 처리). 한 파일에 여러 모델 혼재 정상.

## 8. 배선 (`cmd/.../main.go`)

- **머신-primary 개념 도입**(registry): 어느 enroll 엔트리가 이 머신의 머신전역 소스를 대표하는지 명시 플래그/전용 토큰.
- **primary 토큰은 `runOnce` 루프 밖에서 정확히 1회 결정**. `~/.codex`+`~/.gemini`는 **머신당 1회** 수집(per-config-dir Claude 루프와 분리).
- 머신 cursor 키 = 실제 파일 경로(공유 `state.json`에서 Claude 경로와 disjoint라 안전). 머신 이벤트 큐 = primary 토큰 큐(`queueDirFor`).
- **엣지**: no-primary / 다중-primary(설정 거부) / primary 토큰 폐기·만료 → 메트릭 남기고 머신 패스 skip(다른 토큰으로 **폴백 금지**). `List()`가 합성하는 default 엔트리에 토큰이 없는 경우도 이 가드로 처리.
- `cmdForget`이 비-primary 엔트리를 지워도 머신 cursor는 건드리지 않음.

## 9. Redaction (배포 전 필수)

- redact는 이미 `main.go`에서 `PromptText`/`ResponseText`에 **중앙 1회** 적용 → 신규 어댑터도 그 필드만 채우면 자동 커버(어댑터별 scrub 추가 금지, drift 방지).
- **시크릿 지나는 필드 확인**: `ProjectContext`/`SessionID`는 미scrub — 경로/UUID라 시크릿 아님을 확인(맞으면 그대로).
- **패턴셋 확장**(현재 `sk-`, PEM key, `gh[pousr]_`, `AKIA`만): 추가 = Google OAuth `ya29.`·`1//`(refresh), `bearer <token>`, `key=value`. `key=value`는 **과다치환 금지** — 키 이름이 `(api[_-]?key|secret|token|password)` 류일 때만 앵커.
- **테스트**: 신규 패턴별 positive 1 + negative 1(예: `format=json`은 치환 X). e2e가 프로덕션 scrub 경로를 실제로 태우도록 정리(현재 재구현 → drift 위험).

## 10. 배포 순서

1. **Backend 먼저**: 마이그레이션(model TEXT NULL) + 인제스트가 model null/present 양쪽 수용 확인.
2. **redact 패턴셋 확장 릴리스**(신규 어댑터 활성 전 반드시 — 응답 캡처가 시크릿 표면을 넓히므로).
3. **Agent 릴리스**: 신규 어댑터 + 모델 캡처 + cursor 리팩터. model은 additive/nullable이라 순서 어긋나도 무해하나 backend-first가 안전.

## 11. 테스트 전략

- 어댑터별 골든 `testdata`: Codex(compacted, exec, developer-role, user-주입태그, per-turn 모델변경, 선행 turn_context 없음); Gemini(3포맷, `$set` 저널, `type=null/info`, 중첩 dir, 크로스포맷 동일세션 dedup, content parts/string, 동일-초 append); Claude(멀티모델, `<synthetic>` 제외).
- cursor: 레거시 state.json fixture 왕복, 롤백 재방출 방지.
- redact: 패턴별 pos/neg.
- backend: 마이그레이션 멱등, model 왕복(ingest→admin), null 수용.

## 12. 수용된 한계 / 남은 리스크

- 모델 collision(같은 프롬프트·같은 초·다른 모델) first-writer-wins — 문서화된 수용.
- Codex/Gemini **버전 드리프트**: 타 개발자 머신의 신버전 포맷이 미지원이면 **fail-loud(파일 단위 skip + 에러/메트릭)**로 드러나게(무성 0수집 금지). 스캔 전체 abort 금지(파일 단위 격리).
- Gemini `System:` 주입을 구조로 못 잡는 잔여분은 명시적 프리픽스 + 문서화.
- 응답 전문 캡처(결정 A)로 시크릿·IP 표면 확대 → §9 redact 확장이 완화책이자 배포 전제.
