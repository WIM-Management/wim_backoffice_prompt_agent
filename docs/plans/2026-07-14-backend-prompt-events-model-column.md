# Backend: prompt_events.model 컬럼 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `prompt_events`에 nullable `model` 컬럼을 추가하고, 인제스트 페이로드 → 저장 → admin 응답까지 모델 문자열을 전파한다.

**Architecture:** Flyway 멱등 마이그레이션으로 `model TEXT NULL` 추가(운영 `ddl-auto=validate`). 헥사고날 계층대로 인제스트 DTO(`EventItem`) → 애플리케이션 레코드(`PromptEventRecord`) → 퍼시스터(`toEntity`) → JPA 엔티티 → admin 응답 DTO에 `model` 필드를 순차 추가. content_hash 공식·dedup 유니크 키는 **불변**.

**Tech Stack:** Kotlin, Spring Boot 2.7, JPA/Hibernate, Flyway, JUnit.

## Global Constraints

- DB 외래키 제약 금지(해당 없음 — 컬럼만 추가).
- 스키마 변경은 `migration`(Flyway) 멱등 SQL로만. 운영 `ddl-auto=validate` → 엔티티와 마이그레이션 동시 반영 필수.
- 수정 엔드포인트 PATCH, DTO는 컨트롤러 인라인 금지·별도 파일, `@field:Schema` 필수.
- content_hash 공식(`ContentHasher.kt`)·dedup 유니크 `(employee_id, source_tool, content_hash)` **변경 금지**. model은 해시·키에 넣지 않는다.
- 작업은 `wim_backoffice` 레포에 별도 워크트리(`feat/prompt-events-model`)를 파서, base=`staging` PR로 올린다. `main`/`staging` 직접 커밋 금지.
- 모델 문자열은 원문 그대로 저장(정규화 금지). 컬럼 타입 `TEXT`(varchar 아님).

---

### Task 1: Flyway 마이그레이션 — model 컬럼 추가

**Files:**
- Create: `backend/migration/src/main/resources/db/migration/V20260714_001__add_model_to_prompt_events.sql`
- Reference: `backend/migration/src/main/resources/db/migration/V20260624_003__create_prompt_insights.sql` (기존 테이블 정의)

**Interfaces:**
- Produces: `prompt_events.model TEXT NULL` 컬럼.

- [ ] **Step 1: 마이그레이션 SQL 작성 (멱등)**

```sql
-- prompt_events 에 실제 사용 모델(model) 컬럼 추가.
-- nullable: 과거 데이터·모델 미보고 도구·백필(REPO_FILE)은 NULL.
-- content_hash / dedup 키에는 포함하지 않는다(모델은 dedup 정체성이 아님).
ALTER TABLE prompt_events ADD COLUMN IF NOT EXISTS model TEXT;
```

- [ ] **Step 2: 로컬 부팅으로 마이그레이션 적용 확인**

Run: `cd backend && ./gradlew :app-api:bootRun` (H2/로컬 프로파일) 또는 마이그레이션 테스트 태스크
Expected: Flyway가 `V20260714_001`을 적용, 부팅 성공(`ddl-auto=validate` 통과는 Task 2 엔티티 반영 후).

- [ ] **Step 3: 멱등성 확인 — 재적용 무해**

Run: 동일 DB에 마이그레이션 재실행(또는 SQL을 psql로 2회)
Expected: 2회차 `ADD COLUMN IF NOT EXISTS`가 no-op, 에러 없음.

- [ ] **Step 4: 커밋**

```bash
git add backend/migration/src/main/resources/db/migration/V20260714_001__add_model_to_prompt_events.sql
git commit -m "feat(prompt-insights): add nullable model column to prompt_events"
```

---

### Task 2: JPA 엔티티에 model 필드

**Files:**
- Modify: `backend/prompt-insights/src/main/kotlin/dev/wimcorp/prompt_insights/jpa/PromptEventJpaEntity.kt`

**Interfaces:**
- Consumes: Task 1의 `model` 컬럼.
- Produces: `PromptEventJpaEntity.model: String?` (nullable).

- [ ] **Step 1: 엔티티 필드 추가**

파일을 열어 기존 `contentHash` 필드 패턴을 따라 nullable 컬럼 매핑을 추가한다:

```kotlin
@Column(name = "model")
var model: String? = null,
```

(생성자/프로퍼티 위치는 파일의 기존 필드 순서·스타일에 맞춘다. `ddl-auto=validate`라 컬럼명 `model` 정확히 일치해야 부팅됨.)

- [ ] **Step 2: 부팅으로 validate 통과 확인**

Run: `cd backend && ./gradlew :app-api:bootRun`
Expected: Hibernate validate 통과(엔티티 ↔ Task1 컬럼 일치), 부팅 성공.

- [ ] **Step 3: 커밋**

```bash
git add backend/prompt-insights/src/main/kotlin/dev/wimcorp/prompt_insights/jpa/PromptEventJpaEntity.kt
git commit -m "feat(prompt-insights): map model column on PromptEventJpaEntity"
```

---

### Task 3: 애플리케이션 레코드 + 퍼시스터 전파

**Files:**
- Modify: `backend/prompt-insights/src/main/kotlin/dev/wimcorp/prompt_insights/application/PromptEventRecord.kt`
- Modify: `backend/prompt-insights/src/main/kotlin/dev/wimcorp/prompt_insights/application/PromptEventPersister.kt` (`toEntity`)
- Test: `backend/prompt-insights/src/test/kotlin/dev/wimcorp/prompt_insights/application/PromptEventPersisterTest.kt` (없으면 생성; 있으면 케이스 추가)

**Interfaces:**
- Consumes: `PromptEventJpaEntity.model`.
- Produces: `PromptEventRecord.model: String?`; `toEntity`가 `record.model`을 엔티티로 복사.

- [ ] **Step 1: 실패 테스트 — toEntity가 model을 복사**

```kotlin
@Test
fun `toEntity copies model through`() {
    val record = PromptEventRecord(
        sourceTool = "CODEX", surface = "cli", sessionId = "s1",
        promptText = "hi", responseText = null,
        promptTs = LocalDateTime.parse("2026-07-14T10:00:00"),
        tokenCount = null, projectContext = "/x", clientVersion = "1.0",
        model = "gpt-5.3-codex-spark",
    )
    val entity = PromptEventPersister.toEntity(
        employeeId = UUID.randomUUID(), deviceId = null,
        record = record, contentHash = "abc",
    )
    assertEquals("gpt-5.3-codex-spark", entity.model)
}
```

(생성자 인자·`toEntity` 시그니처는 실제 파일을 읽어 정확히 맞춘다. `model`은 마지막 인자로 추가.)

- [ ] **Step 2: 테스트 실패 확인 (컴파일 에러 = model 인자 없음)**

Run: `cd backend && ./gradlew :prompt-insights:test --tests "*PromptEventPersisterTest*"`
Expected: FAIL (컴파일: `model` 파라미터 없음).

- [ ] **Step 3: 레코드·퍼시스터에 model 추가**

`PromptEventRecord`에 필드 추가:
```kotlin
val model: String?,
```
`PromptEventPersister.toEntity(...)`가 엔티티 생성 시 `model = record.model` 세팅. content_hash 계산부는 손대지 않는다(`ContentHasher.hash(...)` 인자에 model 추가 금지).

- [ ] **Step 4: 테스트 통과 확인**

Run: `cd backend && ./gradlew :prompt-insights:test --tests "*PromptEventPersisterTest*"`
Expected: PASS.

- [ ] **Step 5: 커밋**

```bash
git add backend/prompt-insights/src/main/kotlin/dev/wimcorp/prompt_insights/application/PromptEventRecord.kt \
        backend/prompt-insights/src/main/kotlin/dev/wimcorp/prompt_insights/application/PromptEventPersister.kt \
        backend/prompt-insights/src/test/kotlin/dev/wimcorp/prompt_insights/application/PromptEventPersisterTest.kt
git commit -m "feat(prompt-insights): thread model through record and persister"
```

---

### Task 4: 인제스트 요청 DTO(EventItem)에 model + 매핑

**Files:**
- Modify: `backend/app-api/src/main/kotlin/dev/wimcorp/app_api/prompt_insights/web/request/IngestPromptEventsRequest.kt`
- Modify: `EventItem` → `PromptEventRecord` 매핑 지점(같은 파일 또는 컨트롤러/서비스; 실제 위치는 `PromptEventIngestController` 경로 따라 확인)
- Test: `backend/app-api/src/test/kotlin/dev/wimcorp/app_api/prompt_insights/PromptEventIngestControllerTest.kt`

**Interfaces:**
- Consumes: `PromptEventRecord.model`.
- Produces: `EventItem.model: String?` (요청 JSON `model` 수용), null·present 양쪽 허용.

- [ ] **Step 1: 실패 테스트 — model 포함 페이로드 인제스트 후 저장됨**

`PromptEventIngestControllerTest`에 케이스 추가: `events[0].model="gpt-5.3-codex-spark"`로 PATCH → 201/200, 저장된 엔티티 `model` 일치. 그리고 **model 없는 페이로드도 여전히 성공(null 저장)** 케이스도 추가(구 에이전트 호환).

```kotlin
@Test
fun `ingest accepts and stores model, and tolerates missing model`() {
    // model 있는 이벤트 1건 + model 없는 이벤트 1건 PATCH
    // 저장 결과: 각각 model="gpt-5.3-codex-spark", model=null
}
```

(요청 빌더·엔드포인트·인증 헤더는 기존 테스트 패턴 그대로 재사용.)

- [ ] **Step 2: 실패 확인**

Run: `cd backend && ./gradlew :app-api:test --tests "*PromptEventIngestControllerTest*"`
Expected: FAIL (model 미매핑 → 저장 null 또는 컴파일 에러).

- [ ] **Step 3: EventItem·매핑에 model 추가**

`EventItem`에 `@field:Schema(description = "실제 사용 모델", example = "gpt-5.3-codex-spark") val model: String? = null` 추가. `EventItem → PromptEventRecord` 매핑에 `model = it.model` 추가. 기본값 `null`이라 구 페이로드 호환.

- [ ] **Step 4: 통과 확인**

Run: `cd backend && ./gradlew :app-api:test --tests "*PromptEventIngestControllerTest*"`
Expected: PASS (양쪽 케이스).

- [ ] **Step 5: 커밋**

```bash
git add backend/app-api/src/main/kotlin/dev/wimcorp/app_api/prompt_insights/web/request/IngestPromptEventsRequest.kt \
        backend/app-api/src/test/kotlin/dev/wimcorp/app_api/prompt_insights/PromptEventIngestControllerTest.kt
git commit -m "feat(prompt-insights): accept optional model in ingest payload"
```

---

### Task 5: admin 응답 DTO에 model 노출

**Files:**
- Modify: `backend/app-api/src/main/kotlin/dev/wimcorp/app_api/prompt_insights/web/response/PromptEventResponse.kt`
- Test: `backend/app-api/src/test/kotlin/dev/wimcorp/app_api/prompt_insights/PromptInsightsAdminControllerTest.kt`

**Interfaces:**
- Consumes: `PromptEventJpaEntity.model`.
- Produces: admin `GET /api/v1/prompt-insights/admin/events` 응답에 `model` 필드.

- [ ] **Step 1: 실패 테스트 — admin 응답에 model 포함**

`PromptInsightsAdminControllerTest`에 케이스 추가: model 세팅된 이벤트를 저장 → admin events 조회 → 응답 JSON에 `model` 값 존재.

- [ ] **Step 2: 실패 확인**

Run: `cd backend && ./gradlew :app-api:test --tests "*PromptInsightsAdminControllerTest*"`
Expected: FAIL.

- [ ] **Step 3: 응답 DTO에 model 추가**

`PromptEventResponse`에 `@field:Schema(description = "실제 사용 모델") val model: String?` 추가, `from(e: PromptEventJpaEntity)`에 `model = e.model` 매핑.

- [ ] **Step 4: 통과 확인**

Run: `cd backend && ./gradlew :app-api:test --tests "*PromptInsightsAdminControllerTest*"`
Expected: PASS.

- [ ] **Step 5: 커밋**

```bash
git add backend/app-api/src/main/kotlin/dev/wimcorp/app_api/prompt_insights/web/response/PromptEventResponse.kt \
        backend/app-api/src/test/kotlin/dev/wimcorp/app_api/prompt_insights/PromptInsightsAdminControllerTest.kt
git commit -m "feat(prompt-insights): expose model in admin event response"
```

---

### Task 6: dedup 불변 회귀 가드 + 전체 테스트

**Files:**
- Test: `backend/prompt-insights/src/test/kotlin/dev/wimcorp/prompt_insights/application/PromptEventDedupRealDbIntegrationTest.kt` (기존)

**Interfaces:**
- Consumes: 전 태스크.

- [ ] **Step 1: dedup 불변 회귀 테스트 추가**

같은 `(employee_id, source_tool, content_hash)`인데 **model만 다른** 두 이벤트를 순차 인제스트 → **두 번째는 DUPLICATE**(1행만 존재, model=first-writer)임을 단언. 이는 수용된 한계를 고정하는 회귀 가드.

```kotlin
@Test
fun `same hash different model dedups to first writer`() {
    // ingest(model="A") then ingest(model="B") with identical sourceTool/sessionId/promptText/promptTs
    // assert: 1 row, model == "A"
}
```

- [ ] **Step 2: 전체 모듈 테스트**

Run: `cd backend && ./gradlew :prompt-insights:test :app-api:test`
Expected: PASS (전부).

- [ ] **Step 3: PR 직전 문서 점검**

`docs/FEATURES.md`(기능 카탈로그)에 prompt-insights가 model을 수집·노출한다는 항목 반영, `docs/CHANGELOG` 해당 섹션 추가(엄브렐러 CLAUDE.md 규칙).

- [ ] **Step 4: 커밋 + staging PR**

```bash
git add -A
git commit -m "test(prompt-insights): guard model-excluded dedup invariant; docs"
git push -u origin feat/prompt-events-model
gh pr create --base staging --title "feat(prompt-insights): capture model on prompt_events" --body "..."
```

## Self-Review 결과
- 스펙 §3.2 전부 태스크로 커버(마이그레이션·엔티티·레코드·퍼시스터·인제스트·admin·dedup 불변).
- 플레이스홀더 없음(테스트 코드는 실제 시그니처를 파일에서 확인해 맞추라는 지시 포함).
- 타입 일관: `model: String?` 전 계층 동일 명칭.
