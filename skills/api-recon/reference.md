# api-recon — 참조 매뉴얼

Grep 레시피、`config.json` 템플릿 및 문제 해결。모두 grep  `js/` 디렉토리 실행。bundle 한 줄을 사용할 때 먼저 `js-beautify` 또는 `sed 's/}/}\n/g'`，일반적으로 컨텍스트 창 포함 raw grep 그렇죠。

## 스크립트 설명

`scripts/` 의 모든 파일은**참조 템플릿**，실행 전 대상 부위에 맞게 조정해야 함。일반적인 변경 사항：

| 스크립트 | 조정이 필요한 공통 항목 |
|---|---|
| `harvest_static.py` | endpoint 레귤러、webpack/Vite manifest 분석、마이크로 프론트엔드 publicPath、다시 시도해보세요/동시성 |
| `runtime_harvest.js` | neutralize 필드 이름 및 성공 값、stub 일치 규칙 및 body 구조、routes 출처、WS 녹음 중、`waitUntil`/`routeTimeout`/`proxy` |
| `preload.js` | `loginPathRe`、L1 stubs、`neutralize.fields`、`apiPattern`、활성화되어 있나요? L3、`recordDetail`、`observe.*`、`neutralizeVueRouter` |
| `spider_mpa.py` | `--exclude` 파괴적인 링크、cookie、depth/max、동일 도메인 필터링 |
| `extract_route_map.py` | `routeMap` / `routeLink` 레귤러、KEY 명명 패턴 |
| `build_perm_tree.py` | `userRouteAuth` 분석、`ROOTS`/`PREFIX_PARENT` 계층적 휴리스틱、stub 외부 필드 이름 |
| `config.json` | 위의 모든 사이트의 독점 매개변수에 대한 통합 입구 |

조정된 파일을 작업 작업 디렉터리에 배치하는 것이 좋습니다.（ `recon/`），보고서의 참조 스크립트와 관련된 특정 변경 사항을 지정합니다.。

---

## A. 3개의 역방향 게이트

### A1. 렌더링 게이트 — "로그인되었는지 어떻게 확인하나요?"

```bash
grep -rhoaE '.{0,40}(isLogin|isAuthenticated|loggedIn|hasLogin|requireAuth)\b.{0,80}' js | head
grep -rhoaE 'function (getUser|getToken|getAuth)[0-9]?\([^)]*\)\{.{0,200}' js | head
grep -rhoaE '(localStorage|sessionStorage)\.getItem\("[^"]+"\)' js | sort -u
grep -rhoaE '(Cookies?|cookie)\.(get|load)\("[^"]+"\)' js | sort -u
grep -rhoaE '\batob\(|JSON\.parse\(|jwt|decode' js | head
```

링크 찾기 `isLogin = f(getUser())` → `getUser = decode(storage.read(KEY))`，알았어 **저장 키**、**컨테이너**（Cookie vs localStorage）、**인코딩**：

| 인코딩 | config 위조방법 |
|---|---|
| 일반 텍스트 문자열 / `"1"` / token | `"value": "anything-truthy"` |
| `JSON.parse(x)` | `"value": "json:{\"id\":1,\"username\":\"admin\"}"` |
| `JSON.parse(atob(x))` | `"value": "b64json:{\"id\":1,\"username\":\"admin\"}"` |
| JWT | 서명 없음/`alg:none` JWT，또는 bundle 내부 키 서명 |
| 암호화（SM2/AES/RSA） | 하드코드된 키를 찾아보세요；렌더 게이트는 디코딩 가능해야 합니다. blob 할 수 있을 때 forge；그렇지 않으면 정적이 됩니다. |

→ 쓰기 `cookies` / `localStorage`。

### A2. 인터셉터 게이트 — "무엇이 /login을 트리거합니까?"

```bash
grep -rhoaE '.{0,60}(interceptors\.response|axios|request\.use).{0,120}' js | head
grep -rhoaE '.{0,40}(response_code|errcode|errno|\bcode\b|\bret\b|\bstatus\b)\s*[=!]==?\s*[\-0-9]{1,4}.{0,60}' js | head -20
grep -rhoaE '.{0,40}(로그인 안됨|다시 로그인해주세요|로그인이 만료되었습니다|unauthorized|로그인이 잘못되었습니다.|인증|token.{0,10}invalid).{0,40}' js | head
grep -rhoaE '.{0,30}(location\.href|router\.(push|replace)|navigate)\([^)]*login[^)]*\)' js | head
```

알았어：**필드명**、**성공가치**（보통 `0` 또는 `200`）、**점프를 유발하는 실패 값**。사용 junk session 확인：

```bash
curl -sk -X POST -H 'Cookie: <fakekey>=junk' https://target/api/<protected> -d '{}' -H 'Content-Type: application/json'
```

→ 쓰기 `neutralize.fields` + `neutralize.success`。

### A3. 콘텐츠 게이트 - "메뉴/권한은 어디에서 오는가?"

```bash
grep -rhoaE '"/api[^"]*(permission|perm|role|menu|acl|resource|nav)[^"]*"' js | sort -u
grep -rhoaE '.{0,30}(menus|permissions|menuList|routeList|authList|role_permissions)\b.{0,120}' js | head
grep -rhoaE 'userRouteAuth|getResultTree|routeMap|routeLink|hasPermission|checkAuth' js | head
grep -rhoaE '([A-Z_][A-Z0-9_]*):\{name:"[^"]*",link:"/[^"]+"\}' js | head
```

**2개 데이터 계층**(공통 엔터프라이즈 백엔드):

| API | 일반 payload | 소비자 |
|---|---|---|
| `.../role_permissions` | `{ permissions: string[], role_type }` | 루트 가드、버튼 수준 ACL |
| `.../permissions/all` | `tree[{ code, position, children }]` | 사이드바 메뉴 렌더링 |
| bundle 내부 `userRouteAuth` | `{ CODE: { url, name? } }` | code → 프런트엔드 path |
| bundle 내부 `routeMap` | `{ KEY: { name, link } }` | 별칭 해상도（webpack `o.DASHBOARD`） |

소비자 코드를 읽어 확인하세요.：`getResultTree(tree, permissions)` 필터링 방법、`v-if` / `hasAuth(code)` 확인할 필드。

**수제 forge**（소규모 사이트）：빌드 permissive payload → `stubs`。

**전체 권한 트리 복원**(대형 사이트, 사이드바/하위 모듈이 여전히 비어 있음): **섹션 I**을 참조하세요.

---

## B. config.json 템플릿

```json
{
  "baseUrl": "https://target/",
  "runtimeMode": "both",
  "chromium": "/usr/bin/chromium",

  "cookies": [
    { "name": "auth", "value": "b64json:{\"id\":1,\"username\":\"admin\",\"role\":\"admin\",\"func\":{},\"permissions\":[\"*\"]}" }
  ],
  "localStorage": { "token": "faketoken", "isLogin": "1" },

  "neutralize": {
    "fields": ["response_code", "code", "errno", "ret", "status"],
    "success": 0,
    "flags": { "success": true, "message": "ok" }
  },
  "forward": true,
  "loginUrlPattern": "/login",
  "apiPattern": "/api/|/rest/|/graphql",

  "mockTier": "L1+L2",
  "recordDetail": true,
  "observe": {
    "storageReads": false,
    "cookieReads": false,
    "xhrHeaders": true
  },
  "neutralizeVueRouter": true,
  "stubs": [
    {
      "match": "permissions/all|/menu|role_permissions",
      "body": {
        "response_code": 0, "code": 0,
        "data": {
          "permissions": ["*"],
          "menus": [
            { "name": "dashboard", "path": "/dashboard", "show": true, "children": [] },
            { "name": "alert", "path": "/alert", "show": true, "children": [] }
          ]
        }
      }
    }
  ],

  "explore": {
    "clickTabs": true,
    "clickTables": true,
    "pushStateFallback": true,
    "maxMenuItems": 50
  },

  "routes": ["/dashboard", "/alert", "/asset", "/device", "/report", "/config", "/system"],
  "waitMs": 1500, "perRouteMs": 900, "headless": true,
  "waitUntil": "domcontentloaded",
  "routeTimeout": 12000,
  "proxy": "",

  "captureResponses": true, "recordWs": true, "respMax": 600
}
```

필드 설명:
- `runtimeMode`：`depth`（Puppeteer）、`coverage`（browser MCP）、`both`
- `cookies[].value` 접두사：`b64json:` → base64(JSON)；`json:` → 원본 JSON；접두사 없음 → 리터럴
- `forward: true` 실제 요청을 전달하고 코드 필드를 다시 작성합니다.；`false` 완전 오프라인 stub
- `mockTier`：coverage 모드 preload 계층 구조 활성화， `L1+L2`、`L1+L2+L3`
- `routes` 님으로부터 `routes.txt`；forge 메뉴 후 harness 자동 추가 `<a href>`
- `captureResponses` / `recordWs` 만 depth 모드가 유효합니다
- `waitUntil`：대 SPA 사용 `domcontentloaded`，피하세요 `networkidle2` 보류 중
- `routeTimeout`：단일 경로 `page.goto` 시간 초과（밀리초）
- `proxy`：Puppeteer `--proxy-server`；도 설정할 수 있습니다. `HTTP_PROXY` / `HTTPS_PROXY`

### B1. 이중 스텁 템플릿(role_permissions + 권한/모두)

```json
"stubs": [
  {
    "match": "role_permissions",
    "body": {
      "response_code": 0,
      "data": {
        "permissions": ["MONITOR", "MONITOR_ALERT", "THREAT", "ASSETS_RISK"],
        "role_type": "SUPER_ADMIN"
      }
    }
  },
  {
    "match": "permissions/all",
    "body": {
      "response_code": 0,
      "data": [
        {
          "code": "MONITOR",
          "position": 1,
          "children": [
            { "code": "MONITOR_ALERT", "position": 1, "children": [] }
          ]
        }
      ]
    }
  }
]
```

외부 필드 이름（`response_code` / `code` / `data`）연락 필수 A2 인터셉터 게이트 일관성；`permissions` 덮어야 함 tree  leaf code。

---

## C. 적용 범위 모드: 사전 로드 구성

편집 `scripts/preload.js` 상단 `CONFIG` 개체，또는 통해 CDP 주입 전 교체：

```javascript
const CONFIG = {
  loginPathRe: /\/(login|signin)(\/|$|\?)/i,
  mockTier: 'L1+L2',
  forward: true,
  recordDetail: true,
  extractUrlsFromResponse: true,
  neutralizeVueRouter: true,
  observe: { storageReads: false, cookieReads: false, xhrHeaders: true },
  neutralize: { fields: ['response_code', 'code'], success: 0 },
  stubs: [ /* 마찬가지예요 config.json stubs */ ],
  apiPattern: /\/(api|apis|v\d+|dev|internal|graphql)\//i,
};
```

확인：`window.__API_RECON_PRELOAD__ === true` 그리고 pathname 안정적。

녹음 결과 내보내기:

```javascript
JSON.stringify({
  apis: [...window.__API_RECON_LOG__],
  detail: window.__API_RECON_DETAIL__,
  routes: [...(window.__API_RECON_ROUTES__ || [])],
  observe: window.__API_RECON_OBSERVE__,
}, null, 2)
```

---

## D. 프리로드/런타임 Hook 기능

사전 로드(적용 범위) 및 Runtime_harvest(깊이) 내장 브라우저 후크 기능 및 적용 범위:

| Hook 능력 | 예 API 발견의 가치 | 재정의 |
|---|---|---|
| Hook fetch / XHR.open | 입학요청 URL/방법 | ✅ `recordDetail` + `__API_RECON_LOG__` |
| Hook XHR.setRequestHeader | 찾음 Authorization 기다리고 있어요 | ✅ `observe.xhrHeaders` |
| Hook localStorage/cookie 읽기 | 세션 키 이름을 확인하세요. | ⚠️ 선택사항 `observe.storageReads/cookieReads` |
| Vue 경로 가져오기 | 완료 frontendRoutes | ✅ `__API_RECON_ROUTES__`（경로 로드됨） |
| Vue 루트 가드 무력화 / 로그인 점프 차단 | 모듈 트리거 열기 API | ✅ `neutralizeVueRouter` + 네이티브 점프 무력화 |
| React 경로 가져오기 | 보충 라우팅 | ⚠️ 정적 + 클릭；전담 없음 Hook |
| 페이지 점프 차단（로그인 path） | 탈퇴 페이지 분석 | ⚠️ 로그인만 차단합니다. path，비즈니스 탐색 차단 방지 |
| Hook 암호화 라이브러리（CryptoJS/SM 등） | 암호화 매개변수 → 일반 텍스트 API body | ❌ 수동으로 수행해야 함 Hook 암호화 기능 입력 매개변수；결론 집필 config |
| 안티 디버깅 bypass | 그렇지 않으면 runtime 녹음할 수 없습니다 API | ❌ 수동으로 처리해야 함；정적은 여전히 사용 가능합니다. |

---

## E. 엔드포인트 추출 규칙성(정적이 너무 적은 경우)

에 `harvest_static.py` 님 `extract_endpoints` 진정하세요，또는 수동으로：

```bash
grep -rhoaE '"/[a-z][A-Za-z0-9_/\-]{3,}"' js | sort -u
grep -rhoaE '/api/[a-zA-Z0-9_./-]+' js | sort -u
```

---

## F. 문제 해결

| 현상 | 이유 → 처리 중 |
|---|---|
| 정적 API 극소수 | endpoint 방언 불일치 → 일반적인 규칙을 완화하세요（D 축제） |
| chunk 번호 ≪ manifest | CSS-only 또는 배포되지 않음 chunk；404 재시도함 |
| runtime 로그인 페이지가 계속 표시됩니다. | 렌더링 게이트 오류 → 검토 A1：키 이름、컨테이너、인코딩、domain |
| 쉘에 들어가지만 모듈이 비어 있습니다. | 콘텐츠 게이트 → forge 메뉴（A3）；`routes` path 틀렸을 수도 있겠네요 |
| 각 경로만 해당 bootstrap/locale | 불완전한 권한 코드 → I 섹션 권한 트리 복원；확인 `role_permissions` + `permissions/all` 더블 stub |
| 사이드 컬럼에 항목이 있지만 하위 페이지가 비어 있습니다. | tree 누락 intermediate 노드 또는 code 그리고 `userRouteAuth` 일관성이 없다 |
| 각 API 올점프 로그인 | 인터셉터 도어 → 확인 `neutralize`；중첩된 필드를 확장해야 합니다. walk 논리 |
| WS 프레임은 0 | 사용자 상호작용 필요 subscribe；확장됨 `perRouteMs` |
| 응답 본문이 비어 있습니다. | 만 `forward: true` 가끔 진짜 반응이 나오곤 하는데 |
| Chromium 없어짐 | 설치 chromium 또는 설정 `config.chromium` / `CHROMIUM` |
| Mock 아직도 로그인이 많이 되어있습니다 | Hook 너무 늦었거나 누락되었습니다. `location.href` setter → document-start + preload |
| 목록이 완전히 비어 있습니다. | L3 빈 배열이 정상입니다.；계속 Tab/설정/세부사항 |
| 실수로 Redux action 라우팅 시 | 필터에 다음이 포함됨 get/set/change/clear/toggle/upload  path |
| Vue 로그인이 계속 진행 중입니다. | preload 아니요 document-start → 분사시기 변경；또는 `neutralizeVueRouter: false` 수동으로 가드 지우기 |
| 응답에는 다음이 포함됩니다. URL 그런데 들어가지 않았어요 log | 열려있습니다 `extractUrlsFromResponse`；또는 `__API_RECON_DETAIL__` 수동 추출 |
| 모르겠어요 Authorization 1위 | 열려있습니다 `observe.xhrHeaders` 또는 DevTools 요청 헤더 보기 |
| runtime 엄청 느림 / 시간 초과 | 변경 `waitUntil: domcontentloaded`；삭제 `routeTimeout`；사용하지 마세요 `networkidle2` |
| 에이전트 연결 실패 | 확인 `proxy` / 환경변수；Puppeteer 그리고 curl 프록시 포트가 동일합니다. |

---

## G. 굳건한 골

서버 측에서 세션(위조할 수 없는 서명된 쿠키, 서버 측 렌더링 및 스텁할 수 없는 메뉴)을 점차적으로 확인하면 런타임이 셸에서 중단됩니다. 예상되는 동작:

- **끝점 열거를 수행하기에 충분한 정적** — 코드의 모듈 경로
- 승인된 경우，사용**실제대화**똑같이 실행하세요 harness：`forward: true`、필요없어요 neutralize，진실을 포착하다 methods/params/responses

---

## H. 단일 작업 목록

1. 인증범위 확인
2. **읽기** `scripts/harvest_static.py` → 목표에 따라 조정 → 달려라 → 검토 `api_static.txt`、`routes.txt`
3. **Phase 1b**：path 앵커 포인트 확장 창 + 바인딩 레이어 → `param_candidates.json`（J 축제）
4. 역방향 A1/A2/A3 → 글쓰기 사이트 독점 `config.json`
5. **읽고 조정하세요** `runtime_harvest.js` / `preload.js` 실행 전
6. `runtimeMode=depth`：`npm install` → 조정된 실행 harvest 스크립트
7. `runtimeMode=coverage/both`：document-start 조정된 것을 주입합니다. preload → browser MCP 동적 열거 + **매개변수 트리거 매트릭스**
8. 모듈이 렌더링되지 않음 → **섹션 I 권한 트리 복원** → 패치 스텁 → 다시 실행
9. 매개변수 다중 샘플 diff + 오류 추론 → `params_merged.json`
10. 병합 → `site_map.json` + `api_merged.txt`，정직한 주석 취재、공백 및 스크립트 변경

---

## I. 권한 트리 복원(4단계 심화)

언제 forge 단순하다 `menus: [{ path, show: true }]` 유효하지 않음、하위 모듈이 아직 작동하지 않습니다. mount 일 때 사용됩니다.。

### I1. 인증 모듈 찾기

```bash
grep -l 'userRouteAuth' js/*.js
grep -l 'routeMap\|routeLink' js/*.js
grep -rhoaE 'getResultTree|role_permissions|permissions/all' js | head
```

레코드: **권한 API 경로**, **응답 필드 이름**, **소비 청크 파일 이름**.

### I2. RouteMap 추출

```bash
python3 scripts/extract_route_map.py recon/js recon/
# 출력 recon/route_map.json
```

만약에 `[!] no routeMap pattern found`：진정하세요 `extract_route_map.py` 종정，또는 수동으로 grep：

```bash
grep -rhoaE '([A-Z_][A-Z0-9_]*):\{name:"[^"]*",link:"/[^"]+"\}' js | head -20
```

### I3. 빌드 권한 트리 + 스텁

```bash
python3 scripts/build_perm_tree.py recon/js recon/ --config recon/config.json
```

스크립트 논리:
1. 분석 `userRouteAuth={MONITOR:{url:...},...}`（포함 webpack 별칭 `He=o.DASHBOARD`）
2. 사용 `route_map.json` 분석 alias → 진짜 path
3. 언론 code 접두사 추론 parent（`MONITOR_ALERT` → `MONITOR`）
4. 출력 `permissions_tree.json`、`permissions_all_stub.json`、`role_permissions_stub.json`
5. `--config` 일 때 자동으로 작성됩니다. `config.json` 님 `stubs` 및 확장 `routes`

**대상별 조정**(스크립트 상단):
- `DEFAULT_ROOTS`：최상위 모듈 code 목록
- `DEFAULT_PREFIX_PARENT`：`PREFIX_` → parent 매핑
- `DEFAULT_EXTRA_PARENT`：접두사가 아닌 관계 orphan 노드

### I4. 스텁 일관성 확인

```bash
# permissions 수량은 ≈ userRouteAuth 출품작 수
wc -l recon/perm_codes_all.txt
# routes 을 재정의해야 합니다. route_map 모두 link
python3 -c "import json; m=json.load(open('recon/route_map.json')); r=set(json.load(open('recon/config.json'))['routes']); print('missing', [v['link'] for v in m.values() if v['link'] not in r])"
```

### I5. 런타임을 다시 실행하고 비교하세요.

```bash
node recon/runtime_harvest.js recon/config.json
# 비교 forge 전후 runtime_api.json 항목수；확인 /attack、/asset 모듈이 나타나면 기다려주세요. API
```

| forge 전 | forge 이후（성공） |
|---|---|
| 각 경로는 동일합니다. 3–5 글 bootstrap | 경로가 다르면 트리거가 다릅니다. module API |
| 만 `/api/locale/language` | 등장 `/api/web/...` 모듈 endpoint |
| `routes.txt` 1자리 라우팅 | `routes` 80–110+ 님으로부터 route_map |

### I6. 여전히 실패

- **커버리지 모드**: 사이드바 + 탭을 클릭하세요. 상호작용 후 권한 제한이 요청될 수 있습니다.
- **스텁 필드**: 실제 API(컬 + 실제 세션)와 스텁 중첩 비교
- **추가 가드**：grep `hasPermission|checkRole|func.` 및 기타 버튼 수준 확인，확장자 `role_permissions.permissions`
- **정적 전체 공개**：모듈 API path 그래도 `api_static.txt`，runtime 추가만 가능 METHOD/body；매개변수 예약됨 `param_candidates.json` + 녹음된 샘플

---

## J. 매개변수 역엔지니어링(1b/5b/5c단계)

**방법론, 비범용 스크립트. ** 경로를 찾으려면 일반 규칙을 사용하세요. 앵커 창 확장 + UI 바인딩 체인 + 다중 샘플 비교 + 오류 반전을 사용하여 매개변수를 찾습니다.

### J1. 앵커 포인트 확장 창 - 경로에서 패키지 객체 찾기

```bash
# 에게 Phase 1 알려진 path 앵커입니다
grep -n '"/api/user/list"' js/*.js
grep -rhoaE '.{0,120}("/api[^"]+").{0,200}' js | head
grep -rhoaE '(params|data|body|payload)\s*:\s*\{' js | head
grep -rhoaE '(get|post|put|delete|patch)\([^,]+,\s*\{' js | head
```

### J2. 포장층 및 투과 형태

```bash
# axios / 통일 request
grep -rhoaE '(axios|request)\.(get|post|put|delete|patch)\(' js | head
grep -rhoaE 'interceptors\.(request|response)' js | head

# GraphQL
grep -rhoaE '(query|mutation)\s+\w+|gql`|graphql\(' js | head
grep -rhoaE '\$[a-zA-Z_]+\s*:\s*(Int|String|Boolean|\[)' js | head

# FormData / multipart
grep -rhoaE 'FormData|\.append\(' js | head

# 경로 매개변수
grep -rhoaE 'path:\s*"/[^"]*:[^"]+"' js | head
grep -rhoaE 'useParams|route\.params|\$route\.params' js | head
```

### J3. 검증 게이트 — 필수 / 형식 / 열거

```bash
grep -rhoaE '(required|message|pattern|enum|validator)\s*:' js | head
grep -rhoaE 'yup\.|zod\.|async-validator|Form\.Item|a-form-item|el-form-item' js | head
grep -rhoaE 'rules\s*:\s*\[|name:\s*["\'][a-zA-Z_]+["\']' js | head
grep -rhoaE 'label.*value|options\s*:\s*\[' js | head
```

### J4. 바인딩 레이어 — 양식 → API

```bash
grep -rhoaE 'onFinish|handleSubmit|getFieldsValue|validateFields' js | head
grep -rhoaE '(pick|omit|transform|dayjs|moment)\(' js | head
```

runtime 자리를 채워주세요：DevTools → Network → 요청 → **프로그램 시작**（call stack）님으로부터 `fetch`/`send` 패키지 기능 추적。

### J5. 암호화 매개변수

```bash
grep -rhoaE 'encrypt|decrypt|sign|CryptoJS|sm2|sm3|sm4|RSA|AES' js | head
```

**암호문의 필드를 추측하지 마세요.** — Hook 암호화 기능**매개변수를 입력하세요.**，암호화 전 기록 plaintext payload；결론 집필 `config.json` / `param_candidates.json`。

### J6. 매개변수 트리거 매트릭스(3단계에 필요)

작업당 한 번씩 각 모듈을 기록하고, diff 요청 본문/쿼리:

| 작동 | 팔로우 |
|---|---|
| 목록 첫 화면 | 페이징 기본값 |
| 검색 | keyword、filters |
| 고급 필터링 | optional 필드 |
| 새로운/편집 | 완료 entity |
| 배치/수출 | `ids[]`、`exportType` |
| 정렬/페이지 넘기기 | `sortField`、`order` |

출력 `param_samples.json`：`[{ "path", "method", "action": "search", "body", "query", "headers" }]`

### J7. 신뢰 규칙

| 자신감 | 조건 |
|---|---|
| **높음** | 정적 callsite + runtime ≥2 샘플이 일관됨 |
| **에** | 정적 전용，아니면 그냥 1 회 runtime |
| **낮음** | 응답/오류 추론，2차 검증 없음 |
| **발동 예정** | 정적 알려진 필드，UI/권한이 허용되지 않습니다 |

### J8. 빠른 장면 매칭

| 장면 | 주문 |
|---|---|
| REST 목록 페이지 | J1 그룹 패키지 개체 → J6 4번 diff → J3 rules |
| 새로운/양식 편집 | J3 Form name → J4 submit 체인 → runtime 제출 + 일부러 비워두고 보려고 400 |
| GraphQL | J2 variables 성명서 → runtime 각각 operation 기록 variables |
| 암호화 body | J5 Hook 매개변수를 입력하세요. → 암호화 전 필드가 실제 필드입니다. params |

### J9. api-recon 단계를 사용한 매핑

| api-recon | 매개변수 recon |
|---|---|
| Phase 1 정적 | J1 앵커 포인트 확장 창 |
| Phase 2 A2 인터셉터 | 글로벌 주입 분야（tenantId、sign） |
| Phase 3 runtime | J6 트리거 매트릭스 + `param_samples.json` |
| Phase 4 권한 트리 | 모듈마다 형태가 다릅니다. → 권한이 충분한 경우에만 모든 필드가 트리거됩니다. |
| Phase 5 병합 | `params_merged.json` + 자신감；단일 샘플이 필요하지 않습니다. |

### J10. 문제 해결

| 현상 | 처리 중 |
|---|---|
| 정적 필드 이름 runtime 나타나지 않음 | 마크「발동 예정」；보충 권한 트리 / 고급 필터를 클릭하세요. / 연계 select 각각 option |
| 마찬가지예요 path 다르다 body 모양 | 정상 — 언론 `action` 분할된 기록，강제로 병합하지 마세요. schema |
| stub 답변이 틀렸는데 보고싶다 params | **보세요 outbound 요청** body/headers，팔로우하지 마세요 stub 응답 역방향 푸시 |
| 400 신고 nested field | 외부 포장에 주의하세요 `data`/`bizData`/`variables` |
| GraphQL 보기만 가능 operation 이름 | 펼치기 `variables` JSON；정적 검색 `$var: Type` |

---
