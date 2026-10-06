package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

// Worker is an LLM work agent (docs §4.4): it claims ONE intent, completes it
// with real tools (Bash: kali tooling through the recording proxy), writes the
// FACTS it found back into the graph, and stops. It does NOT generate new
// directions (that is the planner's job) and does NOT keep exploring toward the
// goal on its own. Multiple workers run concurrently as goroutines.
// WebSearchOpts is the web-search backend selection the server pushes into each
// agent (planner/worker/main). Enabled=false leaves the web_search tool off.
// Backend is "ddgs" (no key), "brave-free" (BraveKey required), "tavily"
// (TavilyKey required), or "deepseek" (DeepSeek* required, filled from the
// active LLM profile). It maps directly onto agentcore.Options.
// Proxy is a dedicated egress proxy for the search request (http/https/socks5),
// independent of the traffic-recording MITM proxy — set it when the search endpoint
// is only reachable via a VPN/SOCKS proxy. Empty = direct.
//
// 주의 deepseek 백엔드의 성격이 다른 3개와 다릅니다：DeepSeek 직접 호출할 수 있는 검색 인터페이스가 없습니다.，
// 검색은 다음에만 존재합니다. Anthropic 호환 가능 messages 내부 인터페이스(web_search_20250305 server
// tool)，따라서 각 검색은 하나의 모델 호출을 사용합니다.，님이 검색 요청을 하셨습니다. DeepSeek 서버가 보낸다——
// 이 기계를 통해서는 안 돼요 Proxy，들어오는 트래픽의 흔적이 없습니다.。
type WebSearchOpts struct {
	Enabled   bool
	Backend   string
	BraveKey  string
	TavilyKey string
	Proxy     string
	// DeepSeek* 현재 활성화된 상태에서 LLM 구성(만 anthropic 형식 DeepSeek 공식 엔드포인트)，
	// 별도로 구성하지 않음，수이 LLM 구성 전환 시 변경 사항。
	DeepSeekBaseURL string
	DeepSeekAPIKey  string
	DeepSeekModel   string
}

type Worker struct {
	findingRecorder FindingRecorder
	prov            llm.Provider
	model           string
	workDir         string
	proxyAddr       string
	proxyCACert     string            // recording proxy's CA cert path (for WebFetch HTTPS verify)
	webSearch       WebSearchOpts     // web_search tool backend selection (off by default)
	tx              *transcript.Store // raw LLM conversation persistence (nil = off)
	window          int               // context window in tokens (for compaction)
	windowFn        func() int        // optional dynamic task-chain minimum
	maxTurns        int               // max agent turns per run (0 = unlimited)
	// runTimeout is the wall-clock budget for the main exploration of one intent
	// (0 = unlimited). When it fires, the run is cut and a settlement round is
	// forced so already-identified facts get written back instead of being lost.
	runTimeout time.Duration
	// extraTools are host-provided tools (e.g. traffic query, oast) appended to
	// the worker's graph write-back tools.
	extraTools []actool.CoreTool
	// injectConstraints resolves whether this task's operation constraints get
	// injected into the worker system prompt. Read per run so the settings toggle
	// takes effect without rebuilding the agent. nil = inject (default).
	injectConstraints func() bool
	// nonStreamingFn resolves whether this run uses the non-streaming (Complete)
	// path. Read per run so a profile/task toggle takes effect without rebuilding
	// the agent. nil = streaming (default).
	nonStreamingFn func() bool
	// noaEnabledFn resolves whether this run uses the experimental noa context-
	// compression mechanism. Read per run, like nonStreaming. nil = off (built-in
	// compaction).
	noaEnabledFn func() bool
	// maxTokensFn resolves the per-reply output cap in tokens, on the same
	// per-run basis. nil or 0 = send no cap and let the endpoint decide.
	maxTokensFn func() int
}

// WorkerSessionID returns the stable transcript key used by a worker intent.
// Worker slots are reusable, so the intent id (rather than work#N) is the
// session identity. Keep this helper public so the Worker message API and UI
// can refer to exactly the conversation that will be resumed.
func WorkerSessionID(explorationID, intentID int64) string {
	return fmt.Sprintf("exp%d-worker-i%d", explorationID, intentID)
}

const workerChatMarkerPrefix = "<!-- ARTEX_WORKER_CHAT:"

func workerChatMarker(requestID string) string {
	return workerChatMarkerPrefix + requestID + " -->"
}

func hasWorkerChatMessage(messages []llm.Message, requestID string) bool {
	marker := workerChatMarker(requestID)
	for _, message := range messages {
		if message.Role == llm.RoleUser && strings.Contains(message.Text(), marker) {
			return true
		}
	}
	return false
}

// SetNonStreaming wires a resolver deciding whether runs use the non-streaming
// model path (true = non-streaming). nil/unset = streaming (default). Read per
// run so a profile or task-chain toggle takes effect without rebuilding.
func (w *Worker) SetNonStreaming(fn func() bool) { w.nonStreamingFn = fn }

func (w *Worker) nonStreaming() bool { return w.nonStreamingFn != nil && w.nonStreamingFn() }

// SetNoaEnabled wires a resolver deciding whether runs use the experimental noa
// context-compression mechanism. nil/unset = off (built-in compaction). Read per
// run so the settings toggle takes effect without rebuilding the agent.
func (w *Worker) SetNoaEnabled(fn func() bool) { w.noaEnabledFn = fn }

// SetMaxTokens wires a resolver for the per-reply output cap. nil/unset or 0 =
// send no cap and let the endpoint decide. Read per run, like nonStreaming.
func (w *Worker) SetMaxTokens(fn func() int) { w.maxTokensFn = fn }

func (w *Worker) maxTokens() int {
	if w.maxTokensFn == nil {
		return 0
	}
	return w.maxTokensFn()
}

// SetConstraintInject wires a resolver deciding whether this task's operation
// constraints get injected into the worker system prompt. nil = inject (default).
func (w *Worker) SetConstraintInject(fn func() bool) { w.injectConstraints = fn }

// wantConstraints reports whether constraint injection is enabled (default yes).
func (w *Worker) wantConstraints() bool { return w.injectConstraints == nil || w.injectConstraints() }

// SetRunTimeout configures the per-intent wall-clock budget for the main
// exploration (0 = unlimited). When it fires, the SDK settlement phase still runs
// so facts are never lost to a timeout. Safe to call before Execute.
func (w *Worker) SetRunTimeout(run time.Duration) {
	w.runTimeout = run
}

// settleWrapUpPrompt is injected by the SDK settlement phase when a worker hits its
// turn/time budget: stop probing, write back what was found, then end with a
// plain-text one-liner (which becomes this run's displayed result).
const settleWrapUpPrompt = "예산 소진으로 종료 예정입니다.。더 이상 명령을 실행하지 마십시오./탐지。주문해주세요：(1) 위에서 확인했지만 다시 작성하지 않은 내용을 하나씩 다시 작성해 주세요.——새로운 자산의 경우 insert_assets、결론 탐색/실용적인 목적으로 record_fact、취약점을 확인하기 위해 report_finding；(2) **마지막으로 일반 텍스트 한 문장을 사용하세요.**당신이 한 일을 요약해보세요、어떤 핵심 결론을 얻었습니까?（이 문장은 이 실행의 결과로 표시됩니다.，꼭 출력해주세요）。"

func NewWorker(prov llm.Provider, model, workDir string, tx *transcript.Store, window, maxTurns int, extra ...actool.CoreTool) *Worker {
	return &Worker{prov: prov, model: model, workDir: workDir, tx: tx, window: window, maxTurns: maxTurns, extraTools: extra}
}

// defaultToolsExcept returns actool.DefaultTools() minus the named tools (by
// CoreTool.Name()). Used to trim SDK default tools an agent shouldn't have.
func defaultToolsExcept(exclude ...string) []actool.CoreTool {
	drop := make(map[string]bool, len(exclude))
	for _, n := range exclude {
		drop[n] = true
	}
	all := actool.DefaultTools()
	out := make([]actool.CoreTool, 0, len(all))
	for _, t := range all {
		if !drop[t.Name()] {
			out = append(out, t)
		}
	}
	return out
}

func (w *Worker) SetCompactionWindowResolver(fn func() int) { w.windowFn = fn }

func (w *Worker) compactionWindow() int {
	if w.windowFn != nil {
		return w.windowFn()
	}
	return w.window
}

// SetProxy configures the recording proxy address that workers route target
// traffic through, plus the CA cert path WebFetch trusts to verify HTTPS through
// that MITM proxy. Empty addr disables the hint.
func (w *Worker) SetProxy(addr, caCert string) { w.proxyAddr, w.proxyCACert = addr, caCert }

// SetWebSearch selects the web_search backend for this worker (off by default).
func (w *Worker) SetWebSearch(o WebSearchOpts) { w.webSearch = o }

// proxyEnv builds the Bash-subprocess env that routes child-command HTTP through
// the egress proxy (the recording MITM when capture is on, or the global proxy
// directly when it is off) and, only when a MITM CA is present, makes the common
// toolchain trust it — so tools need no manual -x/--proxy/-k. Each ecosystem reads
// a different CA var (verified empirically): SSL_CERT_FILE→curl/urllib/Go/openssl,
// REQUESTS_CA_BUNDLE→python requests (it ignores SSL_CERT_FILE), CURL_CA_BUNDLE→curl,
// GIT_SSL_CAINFO→git, NODE_EXTRA_CA_CERTS→node; NODE_USE_ENV_PROXY makes Node 24+
// honor the proxy vars. ALL_PROXY is set too so a socks5 egress proxy (which curl
// only reads from ALL_PROXY, not HTTP(S)_PROXY) works in the capture-off path.
// Empty proxyAddr → nil (direct, unchanged env).
func proxyEnv(proxyAddr, caCert string) []string {
	if proxyAddr == "" {
		return nil
	}
	env := []string{
		"HTTP_PROXY=" + proxyAddr, "HTTPS_PROXY=" + proxyAddr,
		"http_proxy=" + proxyAddr, "https_proxy=" + proxyAddr,
		"ALL_PROXY=" + proxyAddr, "all_proxy=" + proxyAddr, // socks5 egress: curl reads only this
		"NODE_USE_ENV_PROXY=1", // Node 24+: honor HTTP(S)_PROXY in built-in fetch/http
	}
	if caCert != "" {
		env = append(env,
			"SSL_CERT_FILE="+caCert,
			"CURL_CA_BUNDLE="+caCert,
			"REQUESTS_CA_BUNDLE="+caCert,
			"GIT_SSL_CAINFO="+caCert,
			"NODE_EXTRA_CA_CERTS="+caCert,
		)
	}
	return env
}

// workerDefaultTmpl is the built-in EDITABLE body (섹션 [A]) of the worker system
// prompt, seeded into agent_prompts. The trafficTool block and the 중간제품 출력 프로토콜
// are NOT here — they are code-owned and appended by workerSystem after rendering
// (섹션 [B]/[C]), so editing the DB body can never drop them.
const workerDefaultTmpl = `귀하는 네트워크 보안 플랫폼 공인 침투 테스트 시스템입니다."집행관"(work agent)。알겠습니다【의도】(한 문장으로 방향을 탐색해보세요)，책임만 진다：**이 뜻을 완수하라、결과를 지식 그래프에 다시 작성합니다.、그럼 그만 돌아가세요。**

**경계（레드라인）**：
1. **받은 뜻대로 하세요**。**초의를 탐색할 때, 초의를 넘어서는 단서를 엿볼 수 있다면 파헤쳐 볼 가치가 있다.**（오류신고 경로가 유출됐네요、다른 자산과 연결될 수 있는 포인트、또 다른 익스플로잇 체인 진입 의심），**에 fact 님 summary 여기를 클릭해서 플래너에게 건네주세요**。
2. 처음으로 차단되었습니다.（payload 필터링됨 / 404 / 에코 없이 주입）탐색했다는 뜻은 아니다——원래 의도를 우회하는 수단을 모두 완성한 뒤 결론을 출력한다.；
3. 승인된 범위 내에서만 작동하십시오.。시스템 상단에 메시지가 표시되는 경우【운영상의 제약】，그게 최우선순위 레드라인이에요：모든 명령/탐지 실행 전 자가 점검，위반 시 조치 없음（받은 의도에 해당하더라도）。

**알아보는대로 답글 달겠습니다.**（사진에 적어야 카운트됩니다，뇌/본문 내용은 포함되지 않습니다.；결과 나오면 바로 쓰세요，단계가 부족할 때까지 저장하지 말고 버리십시오.）。쓰기 저장의 세 가지 유형，그림을 넘지 마세요：
- **새로운 자산/리소스 → insert_assets（자산 맵）**：하위 도메인 / service / endpoint / 지문 / 자격 증명 및 기타 모든 자산【그 자체】。**여기에는 자산만 등록됩니다.；결론 탐색/여기에 쓰지 말라는 판단，사용 record_fact。**
- **결론 탐색/사실 → record_fact（탐험지도，합격 intent_id）**：사용해 보세요。**여러 관찰 내용을 다음과 같이 요약합니다.【하나】사실**（summary 한 문장 요약 + detail요약에 확장을 작성합니다.，실제 실행 프로세스에 의존），속성당 하나의 속성이 없습니다.、하나의 의도는 대개 하나뿐입니다.，분해하면 맵이 무한히 확장됩니다.——**기본으로 하나만 써주세요，손잡고 갈 수 있어요 detail 이 모두 병합되었습니다.**；있는 경우에만【서로 완전히 독립되어 있음、병합할 수 없습니다.】의 결론이 나올 때만 사용됩니다. facts 어레이 스트라이핑，이는 드문 예외입니다.，보통은 아님。**증분만 쓰기**：이때만 기억하세요【신규 인수】님，기존 사실을 바꿔서 다시 쓰지 마세요.（기존 확인만 가능、새로 추가되는 내용이 없으면 기억할 필요가 없습니다.）。**실제로 본 것만 쓰세요**：주다 evidence（한 줄：명령+이를 가장 잘 증명하는 출력 한두 줄，단순하다，자세한 내용은 detail）、마크 confidence（observed=직접보기 / inferred=현상으로 추론）。
- **취약점 확인 → report_finding（탐험지도，포함 PoC，합격 intent_id）**：**이번에는 당신만이 실제로 발동시켰습니다.、재현 가능한 증거 확보（요청/응답 또는 명령 출력）전용**。절대금지"버전/지문이 일치함 CVE""매개변수가 주입 가능한 것으로 보입니다.""외부 취약점 라이브러리/업데이트 로그/코드 diff 추론"확정되면，그것도 확인하지 마세요 CVE 라이브러리 또는 비교 패치 버전이 실제 트리거링을 대체합니다.。발동은 안되지만 의심이 갑니다 → 사용 record_fact 하나만 기억하세요 inferred 사실（피의자+왜 트리거되지 않습니까?）기획자에게 제출，억지로 외우지 마세요 finding。


이 의도를 완성한 후 무엇을 했는지 한 문장으로 요약해보세요.、어떤 사실을 답장으로 썼나요?。`

// workerTrafficBlock is 섹션 [B]: the traffic-tool note, code-injected only when
// traffic capture (recording) is on — i.e. the traffic_* tools actually exist.
// Gated on recording, NOT on the egress proxy: a global proxy with capture off
// routes traffic but records nothing, so the tools would not be there. Not stored,
// not editable.
func workerTrafficBlock(recording bool) string {
	if !recording {
		return ""
	}
	return "\n\n**트래픽 도구**：\n- traffic_search / traffic_get / traffic_blob：답변 검토、방문한 리소스 찾기，**교통상황을 먼저 확인해 보세요、반복하지 마세요 curl 마찬가지예요 URL**。traffic_search **지정해야 함 host**、기본적으로 답장만 3 매우 가벼운 인덱스(id/method/url/status/resp_len，응답 내용이 없습니다.)，더 명시적인 크기 조정이 필요함 limit；가능 body_contains 요청 중/응답 텍스트에서 전체 텍스트 검색(적어도 3 문자，하위 문자열 및 중국어 지원，비밀번호 찾는 방법/열쇠/오류 보고/인트라넷 주소)；특정 기사의 원문을 보려면 다음을 사용하세요. traffic_get(id)，초대형 텍스트는 다음과 같이 표시됩니다. @blob sha256:<hash>，사용 traffic_blob(hash) 섹션의 전문을 확인하세요.。"
}

// artifactSpec is 섹션 [C]: the code-owned, non-editable tail appended to every
// pentest agent's prompt — intermediate artifacts must land in the shared work
// dir, never /tmp. Guaranteed present regardless of how the DB body is edited.
func artifactSpec(dir string) string {
	return "\n\n**중간제품 출력 프로토콜**：스크립트、payload、캡처된 응답 본문、임시데이터 등 모든 중간산물，**항상 이 작업의 작업 디렉터리에 씁니다. " + dir + "**（상대경로는 여기에 써있습니다，절대 경로를 사용할 수도 있습니다.）——**쓰지 마세요 /tmp、다른 절대 경로를 사용하지 마십시오.**。"
}

// workerArtifactSpec is the worker's 섹션 [C]: its per-intent run dir is pre-created
// by the engine (ensureRunDir), so it just writes relative paths there — no manual
// mkdir, no cross-worker name collisions.
func workerArtifactSpec(runDir string) string {
	return "\n\n**중간제품 출력 프로토콜**：스크립트、payload、캡처된 응답 본문、임시데이터 등 모든 중간산물，**이 목적을 위해 항상 전용 작업 디렉토리를 작성하십시오. " + runDir + "**（이 자동으로 생성되었습니다.，그냥 상대경로로 적어주세요，더 이상 수동으로 디렉터리를 만들 필요가 없습니다.）——**쓰지 마세요 /tmp、다른 절대 경로를 사용하지 마십시오.**。"
}

// ensureRunDir builds and creates an agent's working directory under base:
// <base>/tasks/<taskID> for planner/main; <base>/tasks/<taskID>/i<intentID> for a
// worker (intentID<=0 → task dir only). The "tasks/" segment groups per-task dirs
// symmetrically with the chat agent's "sessions/<sessionID>". Best-effort mkdir — on
// failure, writes fail the same way an unwritable CWD would.
func ensureRunDir(base string, taskID, intentID int64) string {
	dir := filepath.Join(base, "tasks", strconv.FormatInt(taskID, 10))
	if intentID > 0 {
		dir = filepath.Join(dir, "i"+strconv.FormatInt(intentID, 10))
	}
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// cmdOutDir is the SDK large-tool-output spill dir under an agent's run dir.
func cmdOutDir(dir string) string { return filepath.Join(dir, "cmd-output") }

func workerSystem(proxyAddr, caCert, dataDir, runDir string) string {
	body := renderSystem("worker", workerDefaultTmpl, WorkerVars{ProxyAddr: proxyAddr, DataDir: dataDir, Now: nowStr()})
	// caCert is present only when the recording MITM is on, which is exactly when
	// the traffic_* tools are registered — so it gates the traffic-tool note.
	// Optional finding guidance is added for every role after tool resolution.
	return body + workerTrafficBlock(caCert != "") + workerArtifactSpec(runDir)
}

// renderIntentTask formats the claimed intent for the worker's launch USER message:
// the intent is the worker's whole job. It used to live in the system prompt; it now
// rides in the first user turn (together with the situational overview) so the system
// prompt stays static/role-only — same move as the planner's situational block.
// intentAssetIDs pulls the intent's target asset ids out of its payload
// (planner's add_intent stores them as a numeric asset_ids array). nil on absence
// or malformed payload.
func intentAssetIDs(intent *db.Node) []int64 {
	if intent == nil {
		return nil
	}
	var p struct {
		AssetIDs []int64 `json:"asset_ids"`
	}
	if err := json.Unmarshal(intent.Payload, &p); err != nil {
		return nil
	}
	return p.AssetIDs
}

func renderIntentTask(intent *db.Node) string {
	return fmt.Sprintf("\n\n【당신이 받은 의도（이번에는 유일한 미션：이것만 하세요、사실만 생성、끝나면 그만）】：\n%s\n의도 id: %d（답장하기 record_fact / report_finding 다음 경우에는 패스하세요.）", string(intent.Payload), intent.ID)
}

// renderWorkerGraphOverview folds the global situational snapshot into the worker's
// launch USER message for AWARENESS ONLY. The framing is deliberately strong: the overview
// must NOT widen the worker's job — it still does only its assigned intent. Its sole
// purpose is letting the worker read context (existing facts/assets/hints)
// so it avoids redundant work and doesn't re-derive what others already found.
func renderWorkerGraphOverview(data map[string]any) string {
	// coverage 판단은 기획자들의 몫「덜 측정된 유형은 무엇입니까? / 범위를 확장하고 싶나요?」의 신호，그리고 worker「받은 만큼만 하세요
	// 그 의도는、드러나지 않은 점을 쫓지 말라」의 책임 경계가 일관되지 않습니다. → 님으로부터 worker 보기에서 제거。data 이번에는 worker
	// 독점 신제품 map，키를 삭제해도 영향을 받지 않습니다. planner。
	delete(data, "coverage")
	b, err := json.Marshal(data)
	if err != nil {
		return "" // fall back silently: the worker just won't have the global context
	}
	return "\n\n【글로벌 탐사 상황（읽기 전용，당신의 의도를 전체적인 그림에 담을 수 있도록 도와주세요）】：\n" +
		"다음은 전체 임무에 대한 현재 탐사 개요이다.。에는 두 가지 용도가 있습니다.：하나는 다른 사람이 발견한 것을 아는 것입니다.，반복하지 마세요；두 번째는 자신의 의도를 탐색할 수 있도록 하는 것입니다.，전체적인 상황과의 관계를 생각해 볼 수 있다.。\n" +
		"**다이버전스는 좋은 것**：의도를 탐구할 때 깊이 생각해보세요.、많은 협회。유일한 경계는——다른 의도는 정말 실행하지 마세요.（그건 또 뭔가요 worker 님의 문제，기획자가 예정）。귀중한 단서가 떠오를 때마다（자산 간 연계、또 다른 익스플로잇 체인 진입 의심、글로벌 차원의 의혹점），**꼭 적어주세요 fact 기획자에게 제출**——이것이 당신의 중요한 결과물입니다，선택사항 아님。차라리 한 가지만 더 보고하고 기획자들이 판단하게 하고 싶습니다.，여러분도 삼키지 마세요.。\n" +
		string(b)
}

// Execute runs one intent. hooks (the per-task Guard) gates every tool call; may
// be nil. emit, if non-nil, receives one ActivityRecord per execution step.
// notifyFinding, if non-nil, is called (intentID, summary) when this worker writes
// a finding (report_finding) so the task's planner wakes mid-flight — with context
// on which intent found what — instead of waiting for the worker to finish.
// Returns the terminal reason (so the engine can distinguish completed vs
// max_turns) and a per-kind breakdown of what was written back (so an intent that
// explored but persisted nothing isn't mistaken for done, and the engine can log
// facts/assets/findings separately instead of lumping them under "facts").
func (w *Worker) Execute(ctx context.Context, name string, taskID int64, as *db.AssetStore, ts *db.ExplorationStore, intent *db.Node, hooks harness.HookRunner, emit func(db.Activity), enr EnrichTrigger, notifyFinding func(int64, string)) (harness.TerminalReason, WriteCounts, error) {
	return w.execute(ctx, name, taskID, as, ts, intent, hooks, emit, enr, notifyFinding, "", "")
}

// ExecuteWithMessage runs the next turn in the same intent conversation with a
// human-authored message. The HTTP handler does not edit the transcript;
// agentcore records the message as a normal user turn when this Worker starts.
// This keeps Worker continuation identical to the regular agent chat flow.
func (w *Worker) ExecuteWithMessage(ctx context.Context, name string, taskID int64, as *db.AssetStore, ts *db.ExplorationStore, intent *db.Node, hooks harness.HookRunner, emit func(db.Activity), enr EnrichTrigger, notifyFinding func(int64, string), requestID, message string) (harness.TerminalReason, WriteCounts, error) {
	return w.execute(ctx, name, taskID, as, ts, intent, hooks, emit, enr, notifyFinding, strings.TrimSpace(requestID), strings.TrimSpace(message))
}

func (w *Worker) execute(ctx context.Context, name string, taskID int64, as *db.AssetStore, ts *db.ExplorationStore, intent *db.Node, hooks harness.HookRunner, emit func(db.Activity), enr EnrichTrigger, notifyFinding func(int64, string), requestID, message string) (harness.TerminalReason, WriteCounts, error) {
	tsx := NewToolSet(ts, name)
	tsx.SetFindingRecorder(w.findingRecorder)
	tsx.SetTaskID(taskID)
	coverageEnabled := as == nil || as.CoverageEnabled(taskID)
	tsx.SetCoverageEnabled(coverageEnabled)
	if as != nil {
		tsx.SetAssetStore(as, as.Companies())
	}
	tsx.SetOwnerNode(intent.ID)         // assets this worker discovers anchor to its intent → visible to the task
	tsx.SetEnrich(enr)                  // async DNS/HTTP auto-completion for assets this worker writes
	tsx.SetNotifyFinding(notifyFinding) // report_finding 창고출고 그 자리에서 일어나 planner，가지고 가세요「어떤 의도인지+finding」
	// base = built-in worker tools ∪ host tools (traffic) ∪ default tools (incl. Bash);
	// then augment with the agent's visible skills/MCP. During the SDK settlement
	// phase, Bash is hidden via Settlement.DisabledTools (no local gating needed).
	base := append(tsx.WorkerTools(), w.extraTools...)
	// worker 일부러 안주는거 MultiEdit/Glob/Grep：파일 정제된 사용 Edit、검색해서 가세요 Bash(grep/find)，
	// 융합도구 표면、가치가 낮은 통화를 줄입니다.。나머지는 SDK 기본 도구(Read/Write/Edit/LS/Bash/Sleep)평소대로。
	base = append(base, defaultToolsExcept("MultiEdit", "Glob", "Grep")...)
	ctx = WithRunInfo(ctx, RunInfo{TaskID: taskID, ExplorationID: explorationID(ts), IntentID: intent.ID})
	tools, def, cleanup := AugmentTools(ctx, "worker", base)
	defer cleanup()

	// 의도는 worker 님【책임만 진다、전체적으로 run 의 불변】→ 시작 명령과 함께、의도 기반 대상 자산
	// 원본 데이터를 모아서 system prompt：system 매번 run 다시 철자를 입력하세요、는 절대로 그렇지 않습니다. compaction 아래로 누르세요，
	// 길다 run 의도는 언제나 거기에 있다，계속 실행시에는 의존하지 않습니다. transcript 역사는 그 첫 메시지를 간직하고 있나요?。가격은 system
	// 믹스인 per-intent 휘발성 데이터、교차 의도 캐시 재사용 손실；의도적인 선택입니다（살리기보다 잃겠다는 의지 token 훨씬 더 심각해요）。
	// 그리고 planner「상황이 막혔어요 user turn」포크는 의도적입니다：planner 원래 의도를 가지고 있는 분、싱글 없음 mandate，
	// worker 예。만【글로벌 상황 overview】시작 상태 유지 user 메시지에——다운그레이드 가능、공차 stale，부숴도 괜찮아。
	// 이 목적을 위한 전용 작업 디렉토리 <workDir>/tasks/<taskID>/i<intentID>，엔진쪽을 먼저 조립합니다。
	runDir := ensureRunDir(w.workDir, taskID, intent.ID)
	// The run-wide intent is not the current tool action. Do not forward it or
	// inherit a parent run's background into the action reviewer.
	ctx = intercept.WithReviewContext(ctx, runDir, intercept.ReviewBackground{})
	overview := renderWorkerGraphOverview(tsx.graphOverviewData())
	sysBody := workerSystem(w.proxyAddr, w.proxyCACert, w.workDir, runDir)
	if w.wantConstraints() {
		sysBody += constraintBlock(ts) // 운영상의 제약(그렇다면)주입 시스템 프롬프트,worker 다음 사항을 엄격히 준수합니다.
	}
	// 인텐트 블록 → 의도 기반 자산 블록 → 시작 명령，추가 system 꼬리（그리고 constraintBlock 동일한 추가 방법）。
	sysBody += renderIntentTask(intent)
	if as != nil {
		if ids := intentAssetIDs(intent); len(ids) > 0 {
			if assets, err := as.GetByIDs(ids); err == nil && len(assets) > 0 {
				if b, err := json.Marshal(assets); err == nil {
					sysBody += "\n\n의도 asset_ids 해당 대상 자산：\n" + string(b)
				}
				// 이러한 자산은 분명히 타겟팅할 의도가 있습니다. → 태스크 테스트 범위에 자동으로 포함됩니다.（그리고 insertAssets 같은 세트
				// 보수적인 세분성）。upsertTaskScope 님 ON CONFLICT DO NOTHING + uq_task_scope
				// 고유 인덱스는 중복 항목이 추가되지 않음을 보장합니다.；재방송/재시도도 멱등성을 갖습니다. no-op。
				// 자산 커버리지 기능이 꺼지면 더 이상 테스트 범위가 누적되지 않습니다.(분모)。
				if coverageEnabled {
					for _, a := range assets {
						_ = as.AddAutoScope(taskID, a.Type, a.Domain, a.URL, a.IP)
					}
				}
			}
		}
	}
	sysBody += "\n\n위 의도대로 실행을 시작하세요：그냥 하세요、사실만 생성、assets、finding、끝나면 그만。"
	system, boundary := deferredSystem(sysBody, def)
	// 작업 수준 deadline( ctx 주사)클램프북 run 님의 벽시계 예산 + 끝나는 단어를 결정하세요(또 만나요 taskclock.go)。
	tc := taskClockFrom(ctx)
	maxDur, clamped := clampMaxDuration(tc.DeadlineUnix, w.runTimeout)
	settle := wrapupSettlement("worker", []string{"Bash"})
	if tc.DeadlineUnix > 0 {
		settle = wrapupSettlementForTask("worker", []string{"Bash"}, clamped)
	}
	opts := agentcore.Options{
		Provider:        w.prov,
		SystemPrompt:    system,
		DynamicBoundary: boundary,
		Tools:           tools,
		DeferredTools:   def.Deferred,
		UnlockSet:       def.Unlock,
		PermissionMode:  permission.ModeBypass,
		// WebFetch 음반대행사 바로가기，그래요 HTTP 그리고 curl 흔적도 남겼습니다；에이전트 로딩 중 CA 성서를 보자 MITM
		// 재계약했습니다 HTTPS 인증서는 다음과 같습니다.【일반인증 통과】（확인을 끄는 대신）。proxy 비어 있으면 직접 연결。
		EnableWebFetch: true,
		WebFetchProxy:  w.proxyAddr,
		WebFetchCACert: w.proxyCACert,
		// 인터넷 검색(선택사항)。ddgs 필요없어요 key；brave-free 필수 BraveKey；tavily 필수 TavilyKey。
		// WebSearchProxy 은 독립 수출 대리인입니다.(http/https/socks5)，트래픽이 기록되어 있음 MITM 상담원은 관련이 없습니다.；비어 있으면 직접 연결。
		EnableWebSearch:       w.webSearch.Enabled,
		WebSearchBackend:      w.webSearch.Backend,
		BraveSearchAPIKey:     w.webSearch.BraveKey,
		TavilySearchAPIKey:    w.webSearch.TavilyKey,
		DeepSeekSearchBaseURL: w.webSearch.DeepSeekBaseURL,
		DeepSeekSearchAPIKey:  w.webSearch.DeepSeekAPIKey,
		DeepSeekSearchModel:   w.webSearch.DeepSeekModel,
		WebSearchProxy:        w.webSearch.Proxy,
		// Bash 하위 명령 HTTP 기본 레코드 프록시 + 믿으세요 CA（도구가 필요하지 않습니다 -x/-k）。
		BashEnv:    proxyEnv(w.proxyAddr, w.proxyCACert),
		WorkingDir: runDir,
		MaxTurns:   w.maxTurns, // 0 = unlimited (configurable in agent management)
		// 벽시계 예산,라운드 경계 판정,중간에 방해하지 마세요;0 = 제한 없음。작업레벨이 있어요 deadline 타임클램프 도착 min(자체 예산,
		// 거리 deadline 남음),이렇게 놔두세요 run 일이 끝나면 자연스럽게 끝내세요.(또 만나요 taskclock.go)。
		MaxDuration: maxDur,
		// 예산 달성(라운드 OR 기간)→ SDK 라운드를 마치세요(숨기기 Bash),식별된 내용을 다시 작성하세요.,배드엔딩은 피하세요。
		// clamped(임무 deadline 클램핑)일 때 사용됩니다. PromptByReason:시간 초과로 인해=임무 완수→작업 시간 초과 단어,
		// 걸음수로 인해=핀치 창 내의 단계 수가 먼저 소진되었습니다.→물러서라 per-run 말씀。아니요 clamped 순도를 유지하라 per-run。
		Settlement: settle,
		// large tool output spills to cmd-output/ with a head + pointer (SDK tool.Capture);
		// full output preserved on disk. 상한을 자르는 데 사용됩니다. SDK 기본값(30000 문자)。
		ToolOutputDir: cmdOutDir(runDir),
		Compaction:    compactionConfig(w.compactionWindow()), // long tool-heavy runs stay within the window
		Todos:         actool.NewTodoStore(),                  // 세션 수준 임시 작업（TodoWrite），순전히 기획용，퇴장 후 패배
		NonStreaming:  w.nonStreaming(),                       // 그게 profile 비스트리밍 모드를 선택하세요. Provider.Complete
		MaxTokens:     w.maxTokens(),                          // 0 = 상한선 없음,서버의 기본값에 따라 결정됩니다.
	}
	if hooks != nil { // typed-nil guard: only set when concrete (avoids harness panic)
		opts.Hooks = hooks
	}
	if w.tx != nil { // persist raw LLM conversation; one file per worked intent
		opts.Transcript = w.tx
		opts.SessionID = WorkerSessionID(ts.ID(), intent.ID)
	}
	intentID := intent.ID
	emitWrap := func(r db.Activity) {
		if emit != nil {
			r.NodeID, r.Worker = &intentID, name
			emit(r)
		}
	}
	// 의도 / 시작 명령 / 인텐트 앵커 자산이 system prompt 발급됨（위 내용 참조 sysBody 조립）。
	// 시작됩니다 user 메시지만 전달됩니다.【글로벌 상황 overview】——큰 그림을 이해하기 위해 분해 가능，부숴도 괜찮아。
	// overview 드물게 marshal 실패는 비어있습니다，시작 단어로 돌아가기，1라운드에서 공백을 피하세요 user 메시지。
	input := overview
	if strings.TrimSpace(input) == "" {
		input = "실행 시작 system 으로부터 받은 의도：그냥 하세요、사실만 생성、assets、finding、끝나면 그만。"
	}

	// 실험적 기능:개봉 후, noa 컨텍스트 압축 인수(아카이브가 집중되어 있습니다. <workDir>/noa/<SessionID> 다음,지속됨)。
	noaSession := WorkerSessionID(ts.ID(), intent.ID)
	enableNoa(&opts, w.noaEnabledFn, w.workDir, noaSession, noaWarn(noaSession))
	ctx = attachSideCapture(ctx, &opts)
	s := agentcore.NewSession(opts)
	defer s.Close() // release the session's background-task manager (temp dir + processes)

	// Resume prior conversation if this intent was paused/blocked/exhausted and is
	// being re-run. The transcript ID is deterministic per intent, so if a prior
	// session exists the worker continues from where it left off instead of
	// restarting from scratch.
	alreadyRecorded := false
	if w.tx != nil {
		_ = s.Resume(opts.SessionID)
		alreadyRecorded = requestID != "" && hasWorkerChatMessage(s.Messages(), requestID)
		if len(s.Messages()) > 0 && message == "" {
			seedUnlockFromHistory(s.Messages(), def.UnlockSkill)
			input = "계속 실행。"
		} else if len(s.Messages()) > 0 {
			seedUnlockFromHistory(s.Messages(), def.UnlockSkill)
		}
	}
	if message != "" {
		if alreadyRecorded {
			input = "마지막 인간 대화에서 입력한 새 인텐트를 계속 실행합니다.。이미 한 일을 반복하지 마세요。"
		} else if len(s.Messages()) > 0 {
			input = workerChatMarker(requestID) + "\n【인공적인 대화 입력을 위한 새로운 의도】\n" + message +
				"\n\n실행하려면 즉시 이 수동 입력을 클릭하십시오.，완료 후 상황에 따라 원래 작업을 계속해야 하는지 여부를 결정합니다.。"
		} else {
			input += "\n\n" + workerChatMarker(requestID) + "\n【인공적인 대화 입력을 위한 새로운 의도】\n" + message +
				"\n\n수동입력을 우선으로 해주세요。"
		}
	}

	// Budgets + settlement are owned by the SDK (MaxTurns/MaxDuration + Settlement):
	// on hit it runs a wrap-up turn and finishes with ReasonMaxTurns/ReasonTimeout.
	// MaxDuration now interrupts an in-flight tool at the wall-clock deadline and
	// enters the wrap-up phase on the live ctx, so a run whose tool overran the budget
	// still settles (no external hard-timeout backstop needed). ctx itself carries only
	// pause / planner kill / shutdown, which the engine distinguishes and re-queues/stops.
	_, reason, err := captureRunSession(ctx, s, input, emitWrap)
	return reason, tsx.Writes(), err
}
