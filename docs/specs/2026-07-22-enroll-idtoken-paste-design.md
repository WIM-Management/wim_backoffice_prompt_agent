# enroll: loopback 콜백 → 웹 로그인 + id_token 붙여넣기 전환

- 날짜: 2026-07-22
- 상태: 설계 확정 (구현 대기)
- 대상: `wim-backoffice-prompt-agent` (독립 레포) + 백오피스 프론트 라우트 1개
- backend 변경: **없음**

## 배경 / 문제

현재 `enroll`은 OAuth 2.0 PKCE **loopback 콜백** 흐름이다(`internal/enroll/oauth.go`):
브라우저를 열어 Google 로그인 → Google이 `http://127.0.0.1:<port>/callback` 으로 리다이렉트 →
agent가 그 포트에 띄운 임시 HTTP 서버가 인가 코드를 받아 토큰 교환.

이 방식은 **agent가 도는 머신 = 브라우저가 도는 머신**일 때만 성립한다. 실제 운영에서:

- 맥/윈도우: 각자 로컬에서 브라우저가 직접 뜸 → 정상 동작.
- **리눅스 머신은 전부 SSH로 사용** → agent는 원격 리눅스에, 브라우저는 로컬 맥/윈도우에 있다.
  Google이 로컬 브라우저를 `127.0.0.1:<port>` 로 보내지만 리스너는 원격 리눅스에 있어
  **`ERR_CONNECTION_REFUSED`** (실측: 동욱 케이스, 랜덤 포트 33563).

`--port` 고정 + `ssh -L` 터널 우회가 이미 있으나(main.go:273), 포트 지정·터널 세션 유지가
번거롭고 비개발자에겐 무리다. 설치 원라이너(`curl | bash`)의 자동 흐름은 이 안내조차 못 준다.

## 목표

옛 kb 스킬 시절 방식으로 회귀: **웹에서 Google 로그인 → id_token을 화면에 표시 → 복사 →
쉘에 붙여넣기.** loopback 리스너·포트·터널을 전부 제거하고 맥/윈도우/SSH 리눅스에서
**단일 흐름**으로 통일한다.

## 핵심 사실 (설계를 가볍게 만드는 근거)

1. **backend는 이미 웹 client_id aud를 허용한다.** `GoogleOAuth2Service.buildVerifier()`가
   `setAudience(listOf(clientId, agentClientId, appClientId))` — 웹 로그인 client_id(`oauth2.google.client-id`)
   로 발급된 id_token을 그대로 검증 통과시킨다. enroll 엔드포인트
   `POST /api/v1/prompt-insights/enroll` 는 `{idToken, label, issuePromptAgentToken}` 만 받는다.
2. **프론트는 이미 GIS 기반이다.** `src/routes/Login.tsx` 가 `@react-oauth/google` 의
   `<GoogleOAuthProvider clientId={VITE_OAUTH_GOOGLE_CLIENT_ID}>` + `<GoogleLogin>` 를 쓰고,
   성공 콜백의 `credentialResponse.credential` 이 곧 **id_token**(aud = 웹 client_id)이다.
   새 페이지는 그 credential 을 `authApi.googleLogin` 에 보내는 대신 **화면에 표시**만 하면 된다.
3. **CLI `enroll.Run` 은 이미 id_token 문자열만 필요로 한다.** `IDTokenFn` 인터페이스가
   id_token 을 반환하면 나머지(enroll POST → device 토큰 keychain 저장)는 무변경.

→ 필요한 변경은 **(A) 프론트 페이지 1개**, **(B) CLI의 IDTokenFn 을 loopback → 붙여넣기로 교체**, 둘뿐.

## 설계

### A. 프론트 라우트 `/prompt-agent/enroll` (공개)

- 위치: `src/routes/PromptAgentEnroll.tsx`, `App.tsx` 라우터에 등록. **로그인 가드 없음**(공개 페이지).
- 기존 `<GoogleOAuthProvider clientId={VITE_OAUTH_GOOGLE_CLIENT_ID}>` + `<GoogleLogin>` 재사용.
- 상태:
  - 초기: 안내 + Google 로그인 버튼.
  - 로그인 성공(`credentialResponse.credential`): id_token 을 **모노스페이스 박스 + 복사 버튼**으로 표시.
    안내문 — "이 토큰을 터미널의 `wim-backoffice-prompt-agent enroll` 프롬프트에 붙여넣으세요.
    약 1시간 후 만료됩니다."
  - 실패/거부: 에러 메시지 + 다시 시도.
- **토큰은 화면 표시만.** localStorage/세션 저장·리다이렉트·백엔드 전송 없음(민감 자격증명 최소 노출).
- 테마: 기존 프론트 컴포넌트/팔레트 재사용.
- 배포 오리진: 기존 프론트(staging/prod). 웹 client_id·Authorized JS origin 이미 등록됨 →
  **Google Console 변경 불필요**(같은 오리진에 라우트만 추가).

### B. CLI: loopback 제거 + 붙여넣기 IDTokenFn

**삭제**
- `internal/enroll/oauth.go` 전체(loopback listen·PKCE·`waitForCode`·`exchange`·`openBrowser`·
  `writeCallbackPage`) 및 `internal/enroll/oauth_test.go`.
- `main.go` enroll 서브커맨드의 `--port` 플래그와 `OAuthConfig` 조립부.
- `OAuthConfig`(ClientID/ClientSecret/PKCE/Port) 및 관련 설정 로딩 — CLI 측에서 더는 불필요.
  (backend 검증용 client 설정은 backend 소관, 변경 없음.)

**신설** — `internal/enroll/paste.go`
```
// PasteIDToken 은 사용자가 웹에서 로그인해 얻은 Google id_token 을 터미널에서 읽어 반환하는 IDTokenFn.
// enrollURL: "<base>/prompt-agent/enroll" 안내 출력용.
// r: 토큰을 읽을 입력원(운영은 /dev/tty, 테스트는 strings.Reader).
func PasteIDToken(enrollURL string, r io.Reader) enroll.IDTokenFn
```
- 동작: enrollURL 을 안내 출력("브라우저에서 아래 주소를 열어 로그인 후, 표시된 토큰을 붙여넣으세요")
  → `r` 에서 한 줄 읽기 → `strings.TrimSpace` → 빈 값이면 명확한 에러 → 반환.
- 운영 배선(main.go): 입력원을 **`/dev/tty` 직접 open**(Windows 는 `CONIN$`).
  이유는 §C. 열기 실패(제어 터미널 없음)면 "무인 환경입니다. 브라우저 있는 환경에서
  `wim-backoffice-prompt-agent enroll` 를 실행하세요" 에러.
- enroll URL 은 `cfg.BaseURL` 에서 파생(`<BaseURL 의 프론트 오리진>/prompt-agent/enroll`).
  BaseURL 은 API 오리진이므로, 프론트 오리진을 별도 설정값으로 두거나 매핑한다
  (staging/prod 각각). → **구현 시 확정 항목**(아래 Open Questions).

### C. `curl | bash` stdin 함정 (핵심)

설치 원라이너는 `curl -fsSL … | bash` 라서 **bash(및 그 자식 프로세스)의 stdin 이
스크립트 파이프**다. 붙여넣기를 `os.Stdin` 에서 읽으면 파이프의 잔여/EOF 를 읽어 즉시 실패한다.

→ 붙여넣기 입력은 반드시 **제어 터미널(`/dev/tty`)을 직접 open** 해서 읽는다.
`curl | bash` 라도 대화형 셸이면 `/dev/tty` 는 살아 있어 정상 동작한다.
`/dev/tty` open 실패 = 진짜 무인(터미널 없음) → 위의 명확한 에러로 분기.

install.sh 는 기존대로 바이너리 설치 후 `install` 서브커맨드를 호출하되, enroll 단계가
`/dev/tty` 에서 붙여넣기를 받는다. (install.sh 자체 구조 변경 없음.)

### D. 문서 / 안내 문구

- README·설치 안내에서 "브라우저 자동 열림 / loopback / `--port` / `ssh -L` 터널" 관련 문구 제거,
  "웹에서 로그인 → 토큰 붙여넣기" 흐름으로 교체.
- `enroll --help` 갱신(`--port` 삭제 반영).

## 테스트

- `PasteIDToken`: `strings.NewReader("<jwt>\n")` 주입으로 trim/빈값/정상 반환 유닛테스트.
- `enroll.Run`: 기존 테스트 유지(IDTokenFn 를 목으로 주입 — 붙여넣기든 loopback이든 동일 계약).
- 삭제되는 `oauth_test.go` 제거로 인한 커버리지 공백은 loopback 코드 삭제와 동시라 무의미(삭제 코드).
- 프론트: 최소 렌더 테스트(로그인 성공 시 토큰 표시·복사 버튼) — 기존 FE 테스트 컨벤션 따름.
- 수동 E2E: staging 프론트 `/prompt-agent/enroll` 로그인 → 토큰 복사 → 리눅스(SSH) 쉘
  `wim-backoffice-prompt-agent enroll` 붙여넣기 → device 토큰 발급·저장 확인 → 수집 1회.

## 트레이드오프 / 결정

- 로컬 맥/윈도우도 이제 **복사-붙여넣기 1회** 필요(기존엔 브라우저 클릭만). 수용 —
  대신 전 환경 단일 흐름, 포트/터널/방화벽 이슈 소멸, 비개발자 실패율 감소.
- **loopback 완전 제거**(폴백 유지 안 함) — 코드 경로 단순화. 사용자 결정(2026-07-22).
- 페이지는 **백오피스 프론트 라우트**(별도 정적 호스팅 아님) — origin 이미 승인됨, 인프라 0.

## Open Questions (구현 시 확정)

1. **프론트 오리진 파생**: CLI `cfg.BaseURL`(API 오리진)에서 프론트 오리진을 어떻게 얻나 —
   (a) 별도 설정 `WIM_PROMPT_ENROLL_URL` 신설, (b) BaseURL 호스트 치환 규칙,
   (c) 안내는 상대 문구("백오피스 웹의 /prompt-agent/enroll")로 두고 URL 하드 의존 회피.
   → 구현 착수 시 결정(가장 단순한 (a) 유력).
2. `install` 서브커맨드가 enroll 을 부를 때도 `/dev/tty` 경로가 동일 적용되는지 배선 확인.
