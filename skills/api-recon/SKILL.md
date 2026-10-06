---
name: api-recon
설명: 웹사이트 API 인터페이스 수집 시 호출되는 스킬입니다.
---

# API Recon(프런트엔드 인터페이스 정찰)

**인증**을 전제로 **백엔드 API**(경로, 메서드, 매개변수, 응답 본문), **프론트엔드 라우팅**, **UI 기능 트리거 포인트**(탭, 팝업창, 테이블 작업 등)를 최대한 완벽하게 검색합니다.

---

## 경계 및 금지 사항(상담원은 반드시 읽어야 함 · 위반은 경계를 넘는 것을 의미함)

이 스킬은 **API/매개변수 표면 정찰**만 수행하며 취약점 마이닝 또는 침투 단계는 수행하지 않습니다.

### 작업 경계

| 범위 | 허용됨 | 금지됨 |
|---|---|---|
| **대상** | 열거형 path、method、매개변수、라우팅、UI 트리거 포인트 | SQLi/XSS/울트라 바이어스/폭발/fuzz 취약점、패키지 수정 공격、파괴적인 작전 |
| **인증** | Hook + stub/mock 우회**클라이언트**로그인 문 | 사용자에게 계정 비밀번호를 묻거나 추측하는 행위；실제 로그인 폼 제출을 시도해 보세요 |
| **런타임** | 자격 증명이 없습니다. hook 인터페이스，사용 mock 응답하라 SPA 로그인 후 쉘에 들어가십시오. | 실제 백엔드 세션을 사용하여 계속 진행하는 프로세스 |

### 자격 증명 없는 동적 분석(3단계 기본값)

1. 합격 `preload.js` / `runtime_harvest.js` **가로채고 stub** 로그인、권한、메뉴 등 bootstrap 인터페이스；
2. 비즈니스 쿼리 인터페이스는 올바른 구조, 성공적인 비즈니스 코드 및 빈 데이터**가 포함된 모의 본문을 반환합니다.
3. 백엔드나 401 환경 없이 로그인 후 페이지를 계속 렌더링하도록 프런트 엔드를 활성화하여 더 많은 XHR/fetch/WebSocket을 트리거합니다.
4. **빈 데이터, 빈 테이블, 자리 표시자 UI가 예상됩니다** - 이에 대한 실제 로그인이나 취약점 테스트로 전환하지 마세요.

**한 문장**: 모의 객체를 사용하여 프런트 엔드 라우팅 및 구성 요소 마운트를 지원하고 **아웃바운드 요청만 기록**합니다. 백엔드가 무엇을 반환하는지는 중요하지 않습니다. 중요한 것은 프런트 엔드가 **전송**할 인터페이스입니다.

### 하드밴 처리

| 금지됨 | 대안 |
|---|---|
| Phase 1 완료 전 grep/curl/Read 스승님 entry `index-*.js` 추출 API path | 달려요 `OUTDIR/harvest_static.py` |
| 손글씨 `extract_apis.py` 및 기타 대안 harvest 님의 스크립트 | 변경 `OUTDIR/harvest_static.py` 이후 재실행 |
| 마찬가지예요 grep/명령이 실패했습니다. ≥2 번이 계속 반복되네요 | 변화 전략：읽기 tool_logs、변경 harvest、확인 reference |
| 접근제어 건너뛰기 A/B，직접 실행 `scripts/` 원본 버전 | 복사 OUTDIR 그리고 타겟에 따라 변경 |
| 실제 사용자 이름/비밀번호、OTP、OAuth 및 기타 인증 | stub/mock（위 내용 참조） |
| 에게「실제 데이터 얻기」건너뛰었습니다. stub，권한 과잉/주입 테스트 | 녹음만 outbound，속 recon 경계 |
| 삭제、민감한 데이터 내보내기、일괄 쓰기 등 되돌릴 수 없는 작업 | coverage 같은 버튼을 클릭하세요 |
| 완료되지 않음 runtime + 동적 열거，은 모든 페이지와 인터페이스를 획득했다고 주장합니다. | 또 만나요「완전한 정의」또는 제한 사항을 표시하십시오. |
| 완료되지 않은 매개변수 트리거 매트릭스 + diff，은 모든 매개 변수를 마스터했다고 주장합니다. | Phase 3b 매트릭스 + Phase 5 diff |
| 싱글을 사용하세요 runtime 샘플 추론 필요/선택사항 | 여러 샘플 diff 또는 확인 규칙/오류 추론 |

---

## 2계층 모델 + 작동 모드

| 레이어 | 출력 | 상한 |
|---|---|---|
| **정적**（JS bundle） | 전액 endpoint 경로、라우팅 초안、그룹 패키지 포인트 필드 후보 | 없음 HTTP 방법；매개변수는 다음과 같아야 합니다. Phase 1b；런타임 연결 누락 URL |
| **런타임**（실시간 대화） | 방법 + body + 응답 + 뉴스 URL + WS/SSE；여러 샘플 diff 완료 매개변수 | 요청을 보내기 전에 페이지가 실제로 렌더링되어야 합니다.；단일 샘플로는 필수 필드를 결정하기에 충분하지 않습니다./선택사항 |

| 작동 모드 | 엔진 | 해당 |
|---|---|---|
| **depth** | `runtime_harvest.js`（Puppeteer） | API 목록、METHOD/params/응답 본문、WS/SSE、재현 가능한 배치 실행 |
| **coverage** | browser + `preload.js` | 점 Tab/팝업창/양식，기능 포인트 적용 범위가 더 깊어졌습니다. |
| **both** | 먼저 depth 다시 coverage | 가장 완벽한，시간이 가장 오래 걸린다. |

**매개변수 방법**(범용 스크립트 없음): 경로에 대해 수집/일반을 사용합니다. 매개변수에는 **앵커 포인트 창 확장 + UI 바인딩 체인 + 다중 샘플 차이 + 오류 반전**을 사용하세요(grep 레시피는 [reference.md](reference.md)의 섹션 J 참조).

---

## 완전한 정의

모든 것이 만족되었을 때만 정찰이 완료되었다고 주장할 수 있습니다.

- [ ] **정적**：Phase 1 harvest 출력 `api_static.txt`、`routes.txt`、`js/`
- [ ] **런타임**: 깊이 또는 적용 범위 중 하나 이상; 적용 범위/둘 다 **훅 유효 + 동적 열거 링**이어야 합니다.
- [ ] **쉘에 들어가세요**：사업장 방문 path 시 페이 `/login`（주의 hash 라우팅）
- [ ] **매개변수**：coverage/both 완전한 매개변수 트리거 매트릭스 + `param_samples.json`；Phase 5 병합 `params_merged.json`
- [ ] **깊이**(모듈 페이지가 비어 있는 경우): 4단계 권한 트리가 복원되고 **모듈 수준 API**가 나타날 때까지 다시 실행됩니다(로케일/부트스트랩뿐만 아니라).
- [ ] **배송**：Phase 5 출력완료（또 만나요 Phase 5 출력 테이블）；`insert_assets` 서비스 및 엔드포인트 자산 쓰기

---

## 스크립트 및 접근 제어

`scripts/` 이것은 참조 템플릿일 뿐입니다.，**금지됨**원본 버전을 직접 실행하여 최종 결과로 활용。

**규칙**：먼저 읽어보세요 → 타겟에 따라 변경 → 쓰기 `OUTDIR`（ `recon/`）→ 참고 `CHANGES.md`；일치하지 않으면 방법론에 따라 다시 작성，차용구조만。

| 접근 제어 | 언제 | 참고 스크립트 → OUTDIR 복사 | 일반적으로 필요한 변경 사항 |
|---|---|---|---|
| **A（정적）** | Phase 0 이후、**처음이에요**달려요 harvest/spider 전 | `harvest_static.py` / `spider_mpa.py` | **대부분의 사이트 기본값 regex 직접 실행 가능**；만 manifest/방언이 맞지 않을 때 변경 endpoint 레귤러、webpack/Vite `publicPath`、MPA exclude/cookie |
| **B（런타임）** | Phase 2 이후、달려요 depth/coverage 전 | `runtime_harvest.js` / `preload.js` + `config.json` | Cookie/localStorage 키、neutralize 성공가치、stubs、login 레귤러、api 접두사、hash/history |

**SPA 강제 순서**(교환 불가능, 단계 번호는 "먼저 탐색한 다음 스크립트"보다 우선합니다):

| 단계 | 필수 | 금지됨 |
|---|---|---|
| Phase 0 완료 후 | 다음 Bash = `python3 OUTDIR/harvest_static.py <URL> OUTDIR` | curl/grep/Read 스승님 entry `index-*.js`（보통 >500KB） |
| 접근 제어 A | 스크립트 복사 → 필요에 따라 약간의 변경 → **즉시 실행** | 먼저 수동으로 추출하세요. API 그럼 결정하세요 harvest |
| Phase 1 완료 전 | `wc -l` 검증출력；404 변경 harvest 다시 시도해보세요 | 손글씨 extract 스크립트；다운로드되지 않음 URL 반복적으로 grep |
| Phase 1b 이후 | grep 만 `OUTDIR/js/*.js` | 사용자 bundle 대신 harvest |

- ✅ 복사 `harvest_static.py` → （선택사항）변경 regex → **지금 달려보세요**
- ❌ 컬 메인 번들 → grep 여러번 → 임시 추출 작성 → 최종 수확
- **MPA**：Phase 0 다음글 Bash = `python3 OUTDIR/spider_mpa.py ...`

---

## 도구 및 출력 제약

| 제약 | 설명 |
|---|---|
| 대용량 파일 | >100KB 님 `index-*.js` **금지됨** Read/grep 컨텍스트를 입력하세요.；사용 OUTDIR 스크립트 일괄 처리 |
| grep 출력 | 필수 `\| head -20` 또는 `-m 5`；대화만 예약됨 path 요약，게시하지 마세요 bundle 단편 |
| 확인 | 사용 `wc -l`、`ls \| wc -l`；아니요 Read 전체 디렉터리 |
| regex 예비탐사 | 선택사항、≤1 회、만 ≤50KB 작다 chunk 또는 HTML；형식적 정적 harvest 이 우선합니다 |
| reference | 레시피/템플릿/문제 해결을 위해 만나요 [reference.md](reference.md)，반복하지 마세요 inline 전문 |

---

## 실행 로드맵

```
Phase 0 카테고리 + OUTDIR
  → 접근 제어 A → Phase 1 harvest（★ 즉시 실행 ★）
  → Phase 1b 매개변수 반전
  → Phase 2 인증의 세 가지 문 → config.json
  → 접근 제어 B → Phase 3 런타임 + 매개변수 매트릭스
  → Phase 4 권한 트리（필요할 때）→ 재방송 Phase 3
  → Phase 5 통합보고서 + insert_assets검색된 모든 서비스 일괄 삽입、끝점api자산，검색된 자산을 삽입할 때 누락이 허용되지 않습니다.
```

순서대로 확인하세요. **이전 항목이 완료될 때까지 다음 단계로 진입하지 마세요**.

1. [ ] **Phase 0**：예비탐사 SPA/MPA；생성 `OUTDIR` → [Phase 0](#phase-0--카테고리)
2. [ ] **접근 제어 A + Phase 1**：스크립트 복사 → **즉시** harvest → `wc -l` 확인 → [Phase 1](#phase-1--정적)
3. [ ] **Phase 1b**：앵커 포인트 확장 창 + 바인딩 레이어 → `param_candidates.json` → [Phase 1b](#phase-1b--매개변수 반전)
4. [ ] **Phase 2**：인증의 세 가지 문 → `config.json` → [Phase 2](#phase-2--인증의 세 가지 문)
5. [ ] **접근 제어 B**: 런타임 스크립트 조정 → [3단계](#phase-3--runtime)
6. [ ] **Phase 3**：depth / coverage / both；쉘 진입 확인；매개변수 트리거 매트릭스 → `param_samples.json`
7. [ ] **4단계**(필요한 경우): 권한 트리 → 패치 스텁 → 3단계 다시 실행 → [4단계](#phase-4--권한 트리 복원)
8. [ ] **Phase 5**：결합 출력 + 신고 + `insert_assets` → [Phase 5](#phase-5--통합 및 보고)

---

## 0단계 — 분류

풀 항목 HTML，**생성 `OUTDIR`**（바꾸지 마세요 skill 내부 `scripts/`）：

- **SPA**：빈 쉘 + `<div id=app>` + chunk → Phase 1–5
- **MPA**：SSR + `<form>`、없음 endpoint bundle → 접근 제어 A 이후：

```bash
python3 recon/spider_mpa.py <BASE_URL> <OUTDIR> [--cookie "session=..."] [--max 300] [--depth 5] [--exclude "logout|delete|destroy"]
```

출력 `forms.txt`、`links.txt`、`api_inline.txt`。SPA 만약에 forms ≈ 0 → 잘라내기 Phase 1。

---

## 1단계 — 정적

[스크립트 및 접근 제어](#Scripts and Access Control) · [도구 및 출력 제약 조건](#Tools and Output Constraints)을 준수합니다.

```bash
python3 recon/harvest_static.py <BASE_URL> <OUTDIR>
```

harvest：분석 HTML script → webpack/Vite manifest → 모두 다운로드 lazy chunk → 출력 `js/`、`api_static.txt`、`routes.txt`、`chunkmap.txt`。

```bash
wc -l OUTDIR/api_static.txt OUTDIR/routes.txt
ls OUTDIR/js | wc -l
```

- 청크 수와 매니페스트 수: 404를 변경해야 합니다. 수확하고 다시 시도해 보세요. 청크를 하나씩 수동으로 말리지 마십시오.
- `api_static.txt` 너무 적다 → 진정하세요 OUTDIR 내부 endpoint 정규화 후 재실행（또 만나요 reference）

### 1b단계 — 매개변수 역방향

경로는 1단계의 경로입니다. 매개변수 필드는 별도로 재검토해야 합니다. grep 규칙은 [도구 및 출력 제약 조건](#Tools and Output Constraints)을 참조하세요.

**완료 기준**: 필드 이름, 전송 위치, 유형 추론, 필수 여부, 샘플 값, 신뢰 수준 등 중요한 인터페이스에서 답변할 수 있습니다.

#### 1b.0 — 전송 형식

| 양식 | 매개변수는 어디에 있나요? | 정적 우선 |
|---|---|---|
| REST JSON | body + query | path 앵커포인트 옆 `(params\|data\|body)\s*:\s*\{` |
| GraphQL | `variables` | gql 템플릿、`$page: Int` |
| 전통 form | urlencoded | `<form>`、`FormData` |
| 파일 업로드 | multipart | `FormData.append` |
| 경로 매개변수 | `/user/:id` | 라우팅 테이블 + `useParams` / `$route.params` |
| 암호화/서명 | 바오진 `sign`/`data` | Hook 암호화 기능 입력 매개변수（reference D 축제） |

출력：각 인터페이스 라벨 `transport: query|json|form|graphql|encrypted`。

#### 1b.1 — 앵커 포인트 창 확장

알려진 경로를 앵커로 사용하여 창을 확장하여 패키지 개체를 찾습니다.

```bash
grep -n '"/api/user/list"' OUTDIR/js/*.js | head -20
grep -rhoaE '.{0,120}("/api[^"]+").{0,200}' OUTDIR/js/*.js | head -20
grep -rhoaE '(params|data|body|payload)\s*:\s*\{' OUTDIR/js/*.js | head -20
```

| 포장층 | 매개변수 단서 |
|---|---|
| axios 예 | `data` / `params` |
| 통일 request | 인터셉터는 전역 필드를 주입합니다. |
| OpenAPI 클라이언트 | 생성 method 서명 |
| React Query / SWR | hook 두 번째 매개변수 |
| Vue composable | composable 매개변수를 입력하세요. |

유형 잔류물：`yup`/`zod`/rules、`Form.Item name=`、임베디드 Swagger。

→ `param_candidates.json`：`{ path, fields[], source: "static-callsite", confidence }`

#### 1b.2 — 바인딩 레이어

```
Form field → onFinish/handleSubmit → transform → API payload
```

| 바인딩 소스 | 기술 |
|---|---|
| 양식 submit | 팔로우 submit → transform → API |
| 양식 검색 | `getFieldsValue()` → `params` |
| 라우팅 | `:id` / `?tab=` |
| 인터셉터 | 글로벌 `tenantId`、페이징、sign |
| 열거형 select | `options` → API 열거형 값 |

DevTools call stack 님으로부터 `fetch`/`XHR.send` 패키지 기능 추적。

#### 1b.3 — 그룹 패키지에 대한 질문 3개(≠ 2단계 인증 게이트 3개)

| 물었습니다 | 뭐라고 대답하고 싶으신가요? |
|---|---|
| **조립** | payload 어디야? build、transform 흔적 |
| **확인** | required、pattern、enum |
| **전송** | path / query / body / multipart / 머리 |

인터셉터 도어（Phase 2）우연히 전역 주입 필드를 읽습니다.（Authorization、`X-Tenant-Id`、sign）。

#### 1b.4 — 3단계와 연결

후보 필드는 정적/바인딩 레이어에서 나옵니다. **필수/선택/조건부 종속성**은 3단계 매개변수 행렬 + diff + 5단계 오류 반전이어야 합니다.

---

## 2단계 — 인증의 세 가지 문

에 `OUTDIR/js/` grep（와 함께 `head`），쓰기 `config.json`（레시피 보기 reference）：

| 문 | 질문 | 키워드 |
|---|---|---|
| **렌더링 도어** | 로그인 여부를 확인하는 방법？ | `isLogin`、`getToken`、Cookie/localStorage |
| **인터셉터 도어** | 무엇이 점프를 촉발하나요? `/login`？ | `response_code`、`errno`、axios interceptor |
| **콘텐츠 게이트** | 메뉴/권위는 어디서 오는가?？ | `menu`、`permission`、`role`、`acl`、`routes` |

localStorage 키를 자격 증명으로 사용하지 마세요. 청크/요청 체인에서 확인해야 합니다.

**종료 = 접근 제어 B**：결론은 이렇습니다. `config.json`，그리고 변경 `OUTDIR/runtime_harvest.js` / `preload.js`。

### 2b단계 — API 관찰(선택 사항)

사용 OUTDIR 내부 `preload.js` 세션 키 이름을 확인하세요.、Authorization、중첩 API URL：

| 구성 | 출력 |
|---|---|
| `recordDetail: true` | `__API_RECON_DETAIL__` |
| `observe.xhrHeaders: true` | headers 관찰 |
| `extractUrlsFromResponse: true` | 내면의 아이에게 응답하라 API |
| `observe.storageReads/cookieReads: true` | 백필 config |
| `neutralizeVueRouter: true` | `__API_RECON_ROUTES__` |

coverage 매 라운드마다 내보내기：`__API_RECON_LOG__`、`__API_RECON_DETAIL__`、`__API_RECON_ROUTES__`、`__API_RECON_OBSERVE__`。

---

## 3단계 — 런타임

액세스 제어 B를 통과해야 합니다. [경계 및 금지]를 준수합니다(#경계 및 금지 에이전트-필수 읽기--위반은 경계를 넘는 것을 의미함) · 자격 증명 모의 정책이 없습니다.

`config.json` 설정 `"runtimeMode": "depth" | "coverage" | "both"`（템플릿 보기 reference）。

### 후크 및 스텁(깊이 + 적용 범위로 공유)

| 레이어 | 범위 | 목적 |
|---|---|---|
| L1 정확함 | auth/권한/bootstrap stub | 첫 화면 인증 통과 |
| L2 부정교정 | 모두 JSON 응답 | 로그인 코드가 없습니다. → 성공 |
| L3 사실대로 말해주세요 | 놓쳤어요 L1 님 `/api` 등 | 성공 본문이 비어 있음，열어라 UI |

- **depth**：fake auth + `forward` 사업코드 변경 + `stubs`；트래버스 `routes`（hash/history）；출력 `runtime_api.json`
- **coverage**：**document-start** 주사 `preload.js`（CDP `addScriptToEvaluateOnNewDocument` 또는 Userscript）

확인：`window.__API_RECON_PRELOAD__` 존재합니다；비즈니스 path 답장이 없습니다 `/login`。

```bash
cd recon && npm install
node runtime_harvest.js config.json
```

### 3b — 적용 범위 동적 열거(필수)

1. 기본 탐색/사이드바 - 클릭할 때마다 네트워크가 나올 때까지 1~3초 기다립니다.
2. Tab — `role=tab`、`.ant-tabs-tab`
3. 양식 - 첫 번째 행 보기/수정/세부정보
4. 도구 모음 - 내보내기, 필터링, 새로 만들기(**되돌릴 수 없는 삭제 방지**)
5. 각 모듈 - API/라우팅 병합
6. SPA — 예 `routes.txt` 보장되지 않음 path 제어됨 `pushState`（MPA 금지됨）

**매개변수 트리거 매트릭스**(필수): 각 모듈은 작업 유형 **diff 다중 샘플**에 따라 한 번 기록됩니다.

| 작동 | 일반적으로 추가 매개변수 |
|---|---|
| 목록 첫 화면 | 페이징 + 기본 필터 |
| 검색하려면 클릭하세요. | keyword、filter |
| 고급 필터링 | 더 보기 optional |
| 새로운/편집 | 완료 entity |
| 배치/수출/정렬 | `ids[]`、`exportType`、`sortField` |

**stub 다음 outbound body/headers 그래도 그렇죠**——요청 시。녹음 중 → `scan_raw.json`、`param_samples.json`、`api_detail.json`。

- **Vue**：`neutralizeVueRouter: true` + document-start preload
- **React**：`routes.txt` + 사이드바를 클릭하세요 + `pushState`
- **둘 다**: 먼저 3a 깊이, 그 다음 3b 적용 범위

---

## 4단계 — 권한 트리 복원

**트리거**: 모듈 페이지가 비어 있음 / 경로당 부트스트랩만 있음(예: 로케일) → 콘텐츠 게이트를 통과하지 못했습니다.

| 현상 | 의미 |
|---|---|
| 쉘에 성공적으로 들어갔습니다. | 렌더링 도어 + 인터셉터 게이트를 통과했습니다 |
| 사이드바에 항목이 없습니다./공백을 클릭하세요. | stub shape 또는 권한 코드가 불완전합니다. |
| 모든 루트 API 똑같고 극소수 | `v-if permission` 실패 |
| `routes.txt` 훨씬 적습니다. bundle | 팔로우 필수 auth 모듈 완성 |

```bash
grep -rhoaE '"/api[^"]*(permission|perm|role|menu|acl)[^"]*"' OUTDIR/js/*.js | sort -u | head -30
grep -rhoaE 'userRouteAuth|getResultTree|routeMap|routeLink|menuList|authList' OUTDIR/js/*.js | head -20
```

일반적인 체인：`role_permissions`（flat codes）+ `permissions/all`（tree）→ `getResultTree` → `userRouteAuth[CODE].url`。

```bash
python3 recon/extract_route_map.py recon/js recon/
python3 recon/build_perm_tree.py recon/js recon/ --config recon/config.json
```

중간 출력：`route_map.json`、`userRouteAuth.json`、`permissions_tree.json`、`*_stub.json`、`perm_codes_all.txt`。

stub 확인：외층 `response_code` 인터셉터 게이트와 일치；flat codes 그리고 tree 정렬；`routes` 재정의 `route_map` 모두 link。

업데이트 `config.json` 이후**재방송 Phase 3**。대 SPA 조정 가능 `waitUntil`、`routeTimeout`、`perRouteMs`（또 만나요 reference A3/I 축제）。

---

## 5단계 - 통합 및 보고

### 출력 테이블

| 파일 | 무대 | 내용 |
|---|---|---|
| `js/`、`api_static.txt`、`routes.txt`、`chunkmap.txt` | 1 | 정적 bundle 그리고 path |
| `param_candidates.json` | 1b | 정적 매개변수 필드 후보 |
| `config.json` | 2 | 세 개의 문 + runtime 구성 |
| `runtime_api.json` | 3a | depth 상세녹음（포함 WS/SSE） |
| `param_samples.json`、`scan_raw.json`、`api_detail.json` | 3b | 여러 샘플、로그를 클릭하세요、detail |
| `route_map.json` 등 | 4 | 권한 트리 중간 파일（실행된다면） |
| `params_merged.json` | 5 | 매개변수 필드 병합 + 자신감 |
| `api_merged.txt` | 5 | `METHOD /path [params] [static\|runtime\|both]` |
| `site_map.json` | 5 | 라우팅、API、params、기능 포인트、제한사항 |
| **insert_assets** | 5 | 모든 서비스가 제공됩니다.、엔드포인트 자산이 자산 라이브러리에 기록됩니다. |

### 5b — 매개변수 병합

님으로부터 `param_samples.json` diff，**범용 병합 스크립트 없음**。신뢰 규칙 보기 reference J7（높음/에/낮음/발동 예정）。

### 5c — 오류 추론

인증 범위 내에서 불완전한 요청을 보낼 수 있습니다. 400（**속성 매개변수 recon，비취약성 테스트**）：`field 'x' is required`、열거 오류 등。주의 `data` 포장、`variables`、암호화 전 `bizData`。

보고서에는 다음 사항이 명시되어야 합니다.：runtimeMode、정적/런타임 API 번호、매개변수 신뢰도、발견된 모듈、참조 스크립트 관련 `CHANGES.md` 요약。

`site_map.json` 제안된 구조：

```json
{
  "site": "https://example.com",
  "runtimeMode": "both",
  "appType": "vue-spa",
  "routeGuardStrategy": ["nav-neutralize", "L1-auth", "L2-patch", "forward"],
  "apisFromStatic": [],
  "apisFromRuntime": [],
  "apis": [],
  "params": [{ "method": "POST", "path": "/api/user/list", "transport": "json", "fields": [] }],
  "frontendRoutes": [],
  "routesVerifiedByClick": [],
  "featuresTriggered": [],
  "limitations": ""
}
```

더 많은 필드와 grep 레시피를 보려면 [reference.md](reference.md)를 참조하세요.

---

## 일반 지침

- **프레임워크 독립적**: webpack/Vite/Angular 지연 로드 방식은 동일합니다.
- **전송**: REST/JSON, GraphQL, WebSocket, SSE; gRPC-web이 범위에 속하지 않습니다.
- **SSR**: 클라이언트 가져오기를 기록할 수 있습니다. RSC/서버 작업을 완전히 열거할 수 없습니다.
- **사각지대**: JSVMP, WASM, HMAC/mTLS 강력한 검증 → 정적 + 주석 제한
- **매개변수 사각지대**: 조건부 연결, 숨겨진 매개변수, WASM 패키지 → "트리거됨"/"접근 불가능"
- **정적은 안전망입니다**: 정적은 런타임이 차단되어도 끝점을 열거할 수 있습니다.

---

## 추가 리소스

- Grep 레시피、`config.json` 템플릿、문제 해결、Hook、매개변수 반전 J 축제、site_map 템플릿：**[reference.md](reference.md)**
- 참조 스크립트 경로는 [스크립트 및 접근 제어](#스크립트 및 접근 제어) 표를 참조하세요.
