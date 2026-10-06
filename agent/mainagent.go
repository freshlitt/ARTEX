package agent

import (
	"context"
	"fmt"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

// MainAgent is the thin human-interface orchestrator (docs §4.2 / §7). The human
// chats with it; it observes (read tools), and steers by injecting hints
// (→planner) or direct high-priority intents (→frontier). It does NOT run the
// autonomous intent-generation loop (that is the planner's job).
type MainAgent struct {
	findingRecorder FindingRecorder
	prov            llm.Provider
	model           string
	tx              *transcript.Store                      // raw LLM conversation persistence (nil = off)
	window          int                                    // context window in tokens (for compaction)
	windowFn        func() int                             // optional dynamic task-chain minimum
	maxTurns        int                                    // max agent turns per run (0 = unlimited)
	proxyAddr       string                                 // recording proxy for WebFetch (empty = direct)
	proxyCACert     string                                 // recording proxy's CA cert path (HTTPS verify)
	webSearch       WebSearchOpts                          // web_search tool backend selection (off by default)
	workDir         string                                 // shared work dir (surfaced in prompt as artifact-output target)
	steerWork       func(intentID int64, msg string) error // engine callback: steer a running work (nil = off)
	nonStreamingFn  func() bool                            // resolver: use non-streaming (Complete) path? (nil = streaming)
	noaEnabledFn    func() bool                            // resolver: use experimental noa compaction? (nil = off)
	maxTokensFn     func() int                             // resolver: per-reply output cap (nil/0 = send no cap)
}

// SetNoaEnabled wires a resolver deciding whether runs use the experimental noa
// context-compression mechanism. nil/unset = off (built-in compaction). Read per
// run so the settings toggle takes effect without rebuilding the agent.
func (m *MainAgent) SetNoaEnabled(fn func() bool) { m.noaEnabledFn = fn }

// SetNonStreaming wires a resolver deciding whether runs use the non-streaming
// model path (true = non-streaming). nil/unset = streaming (default).
func (m *MainAgent) SetNonStreaming(fn func() bool) { m.nonStreamingFn = fn }

func (m *MainAgent) nonStreaming() bool { return m.nonStreamingFn != nil && m.nonStreamingFn() }

// SetMaxTokens wires a resolver for the per-reply output cap. nil/unset or 0 =
// send no cap and let the endpoint decide. Read per run, like nonStreaming.
func (m *MainAgent) SetMaxTokens(fn func() int) { m.maxTokensFn = fn }

func (m *MainAgent) maxTokens() int {
	if m.maxTokensFn == nil {
		return 0
	}
	return m.maxTokensFn()
}

func NewMainAgent(prov llm.Provider, model, workDir string, tx *transcript.Store, window, maxTurns int) *MainAgent {
	return &MainAgent{prov: prov, model: model, workDir: workDir, tx: tx, window: window, maxTurns: maxTurns}
}

func (m *MainAgent) SetCompactionWindowResolver(fn func() int) { m.windowFn = fn }

func (m *MainAgent) compactionWindow() int {
	if m.windowFn != nil {
		return m.windowFn()
	}
	return m.window
}

// SetProxy points the main agent's WebFetch at the recording proxy plus the CA
// cert it trusts to verify HTTPS through it (empty addr = direct).
func (m *MainAgent) SetProxy(addr, caCert string) { m.proxyAddr, m.proxyCACert = addr, caCert }

// SetWebSearch selects the web_search backend for the main agent (off by default).
func (m *MainAgent) SetWebSearch(o WebSearchOpts) { m.webSearch = o }

// SetSteerWork wires the engine callback that lets the main agent's steer_work
// tool inject a mid-run course-correction into a running work (nil = tool off).
func (m *MainAgent) SetSteerWork(fn func(intentID int64, msg string) error) { m.steerWork = fn }

// mainAgentDefaultTmpl is the built-in EDITABLE body (섹션 [A]) of the main agent
// prompt, seeded into agent_prompts. Goal is a {{.Goal}} template var; the 중간
// 제품 출력 프로토콜 tail is code-owned (artifactSpec), appended after rendering.
const mainAgentDefaultTmpl = `귀하는 공인 침투 테스트 시스템입니다."스승님 agent"，은 인간 운영자를 위한 인터페이스입니다.。직접 탐색하고 싶지는 않습니다.、자동으로 의도를 지속적으로 생성하지도 않습니다.（그게 기획자의 일이지）。귀하의 책임：

1. 관찰：사용 graph_overview / list_findings / list_facts / list_assets / get_worker_output 현재 진행 상황에 대한 사람들의 질문에 답변。
2. 스티어링（사람의 의도를 시스템에 담는다）：
   - 사람들은 원한다"방향을 바꿔라/특정 유형의 취약점 강조/특정 분야에 집중하세요" → 사용 add_hint 글쓰기 팁（플래너가 다음에 읽어볼게요）。
   - 사람들은 원한다"구체적인 목표를 즉시 측정하세요" → 사용 add_intent 우선순위가 높은 인텐트를 직접 주입（priority 8-10）。시스템은 완료된 작업을 자동으로 실행 상태로 되돌립니다.、하자 worker 이 뜻을 따라 실행하라，실행 후 완료상태로 복귀。
     **모든 임무 목표가 달성되었을 때**（graph_overview 내부 goals 둘 다 met）：보내기 전에 먼저 의도가 있는지부터 확인해보세요."새로운、달성할 결과"。암시적인 경우，추측한 목표를 한 문장으로 반복하세요，그리고**공식 대상으로 등록할지 묻는 질문**——사람들이 원하는 → 사용 set_goals 등록（그러면 작업이 정규 계획에 들어갑니다.、기획자가 독립적으로 진행합니다.）；사람들이 원하지 않아요 / 그냥 잠깐 확인해보고 싶었어요 → 만 add_intent 이것을 게시하세요，worker 작업을 실행한 후 완료된 상태로 돌아갑니다.（자체적으로는 계속되지 않습니다.）。이 의도가 당연히 일회성 검증이라면、새로운 대상을 의미하지 않습니다.，직접 add_intent 그렇죠，매번 물어볼 필요는 없어요。
   - 사람들은 원한다"달리기 의지를 위해(work)실시간 수정（더 이상 떠나지 마세요 X、집중 Y）" → 사용 steer_work（방해하지 마세요、진전이 없습니다，worker 다음 조치 이전에 유효）；먼저 사용해 보세요 get_worker_output 뭐하는 지 보세요。방향이 완전히 틀리면 대신 사용하세요. add_intent 새로운 마음을 품어보세요。
   - 사람들은 원한다"달성하기 위한 새로운 최종 목표를 추가하세요" → 사용 set_goals 보충대상。시스템은 작업 그래프에 대상을 기록하고**자동완성/일시 중지된 작업을 다시 실행 상태로 되돌리고 계속 실행합니다.**（기획자는 이를 바탕으로 달성 여부를 다시 판단하게 됩니다.），복원을 수동으로 클릭할 필요가 없습니다.。
   - 사람들은 원한다"추가됨/테스트 제약 조건 변경（허용됨/특정 유형의 작업을 금지합니다.，『현재 포트만 테스트』『폭파금지』『수동 정찰만 가능』）" → 사용 set_constraints 등록（type=allow 허용됨 / type=deny 금지됨）。다음 계획 단계에서 제약 조건이 주입됩니다. planner/worker 에 대한 프롬프트 단어；개요에서도 사용 가능「제약사항 관리」추가, 삭제, 수정。
3. 인간의 언어로 간결하게 답해주세요.，무엇을 했는지 설명해주세요。

현재 임무 목표：{{.Goal}}

결과를 꾸며내지 마세요；도구에서 반환된 실제 데이터를 바탕으로만 답변하세요.。`

func mainAgentSystem(goal, dataDir, workDir string) string {
	body := renderSystem("mainagent", mainAgentDefaultTmpl, MainVars{Goal: goal, DataDir: dataDir, Now: nowStr()})
	return body + artifactSpec(workDir)
}

// Chat handles one human message and returns the assistant reply. emit, if
// non-nil, receives each execution step (thinking / tool_use / tool_result /
// text / result) so the main-agent session shows its work — exactly like the
// worker/planner sessions — not just the final answer.
func (m *MainAgent) Chat(ctx context.Context, taskID int64, mainSeg int, as *db.AssetStore, ts *db.ExplorationStore, goal, message string, emit func(db.Activity), notify, resume func(), notifyGoal, notifyHint func([]string)) (string, error) {
	tsx := NewToolSet(ts, "human")
	tsx.SetFindingRecorder(m.findingRecorder)
	if as != nil {
		tsx.SetAssetStore(as, as.Companies())
	}
	tsx.SetTaskID(taskID)
	tsx.SetCoverageEnabled(as == nil || as.CoverageEnabled(taskID))
	tsx.SetNotify(notify)         // 범용 깨우기（전용 콜백 없이 쓰기 작업이 진행됩니다.，debounced）
	tsx.SetResumeTask(resume)     // set_goals 새 대상 추가 → 완료/일시 중지된 작업을 다시 시작합니다. running
	tsx.SetNotifyGoal(notifyGoal) // set_goals 새 대상 추가 → 주다 planner 하나만 기억하세요「사람들이 목표를 추가했습니다：…」트리거
	tsx.SetNotifyHint(notifyHint) // add_hint 새로운 팁 추가 → 주다 planner 하나만 기억하세요「명 추가됨 N 전략 팁：…」트리거
	tsx.steerWork = m.steerWork   // enable steer_work tool (nil = unavailable)
	// 도메인 도구 + 기본 기본 도구 세트（Read/Write/Edit/MultiEdit/LS/Glob/Grep/Bash）
	// 자산보상 기능이 꺼진 경우 제외 add_task_scope/list_untested_assets（허용되지 않음 prompt）。
	base := append(tsx.DropCoverageTools(tsx.MainAgentTools()), actool.DefaultTools()...)
	ctx = WithRunInfo(ctx, RunInfo{TaskID: taskID, ExplorationID: explorationID(ts)})
	tools, def, cleanup := AugmentTools(ctx, "mainagent", base)
	defer cleanup()
	// 이 작업의 작업 디렉터리 <workDir>/tasks/<taskID>，먼저 빌드해 보세요。
	mainDir := ensureRunDir(m.workDir, taskID, 0)
	ctx = intercept.WithReviewWorkingDirectory(ctx, mainDir)
	system, boundary := deferredSystem(mainAgentSystem(goal, m.workDir, mainDir), def)
	opts := agentcore.Options{
		Provider:        m.prov,
		SystemPrompt:    system,
		DynamicBoundary: boundary,
		Tools:           tools,
		DeferredTools:   def.Deferred,
		UnlockSet:       def.Unlock,
		PermissionMode:  permission.ModeBypass,
		EnableWebFetch:  true, // 가서 흔적 남기는 요원을 기록하라；에이전트 로딩 중 CA 확인 MITM 재계약했습니다 HTTPS 인증서
		WebFetchProxy:   m.proxyAddr,
		WebFetchCACert:  m.proxyCACert,
		// 인터넷 검색(선택사항)。ddgs 필요없어요 key；brave-free 필수 BraveKey；tavily 필수 TavilyKey。
		// WebSearchProxy 은 독립 수출 대리인입니다.(http/https/socks5)，트래픽이 기록되어 있음 MITM 상담원은 관련이 없습니다.；비어 있으면 직접 연결。
		EnableWebSearch:       m.webSearch.Enabled,
		WebSearchBackend:      m.webSearch.Backend,
		BraveSearchAPIKey:     m.webSearch.BraveKey,
		TavilySearchAPIKey:    m.webSearch.TavilyKey,
		DeepSeekSearchBaseURL: m.webSearch.DeepSeekBaseURL,
		DeepSeekSearchAPIKey:  m.webSearch.DeepSeekAPIKey,
		DeepSeekSearchModel:   m.webSearch.DeepSeekModel,
		WebSearchProxy:        m.webSearch.Proxy,
		BashEnv:               proxyEnv(m.proxyAddr, m.proxyCACert), // Bash 하위 명령은 기본적으로 프록시를 사용합니다.+신뢰 CA
		WorkingDir:            mainDir,                              // 이 작업의 작업 디렉터리 <workDir>/tasks/<taskID>
		ToolOutputDir:         cmdOutDir(mainDir),
		MaxTurns:              m.maxTurns,                             // 0 = unlimited (configurable in agent management)
		Compaction:            compactionConfig(m.compactionWindow()), // long chats stay within the window
		Todos:                 actool.NewTodoStore(),                  // 세션 수준 임시 작업（TodoWrite），순전히 기획용，퇴장 후 패배
		// 예산 달성(걸음수)→ SDK 마무리 주행:사용자에게 진행 요약을 출력합니다.。Prompt 및 마감 라운드 수는 백그라운드에서 편집할 수 있습니다.(기본값 10 휠)。
		Settlement:   wrapupSettlement("mainagent", nil),
		NonStreaming: m.nonStreaming(), // 그게 profile 비스트리밍 모드를 선택하세요. Provider.Complete
		MaxTokens:    m.maxTokens(),    // 0 = 상한선 없음,서버의 기본값에 따라 결정됩니다.
	}
	if m.tx != nil { // persist raw human↔AI conversation; one accumulating file per segment
		opts.Transcript = m.tx
		// Segment 0 keeps the legacy "exp%d-main" name so existing transcripts still
		// load; each new session (seg>=1) gets its own file for a clean context.
		opts.SessionID = fmt.Sprintf("exp%d-main", ts.ID())
		if mainSeg > 0 {
			opts.SessionID = fmt.Sprintf("exp%d-main-s%d", ts.ID(), mainSeg)
		}
	}
	// 실험적 기능:개봉 후, noa 컨텍스트 압축 인수(아카이브가 집중되어 있습니다. <workDir>/noa/<SessionID> 다음,지속됨)。
	// session id 그리고 transcript 같은 규칙(세분화된 인식),아카이브와 복원 정렬。
	noaSession := fmt.Sprintf("exp%d-main", ts.ID())
	if mainSeg > 0 {
		noaSession = fmt.Sprintf("exp%d-main-s%d", ts.ID(), mainSeg)
	}
	enableNoa(&opts, m.noaEnabledFn, m.workDir, noaSession, noaWarn(noaSession))
	ctx = attachSideCapture(ctx, &opts)
	s := agentcore.NewSession(opts)
	defer s.Close()
	// reload the prior conversation from the transcript so the agent has context
	// across turns (each Chat is a fresh session; without this it can't see earlier
	// messages). First turn: no file yet → Resume loads nothing and proceeds.
	if m.tx != nil {
		_ = s.Resume(opts.SessionID)
	}
	// C2: this session is fresh each turn; re-unlock skill-gated MCPs from prior
	// Skill() calls in the reloaded history so revealed tools stay callable.
	seedUnlockFromHistory(s.Messages(), def.UnlockSkill)
	text, _, err := captureRunSession(ctx, s, message, func(r db.Activity) {
		if emit != nil {
			r.Worker = "mainagent"
			emit(r)
		}
	})
	return text, err
}
