package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	acperm "github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

// goalsDefaultTmpl is the built-in EDITABLE body (섹션 [A]) of the goals-decomposer
// prompt, seeded into agent_prompts. No template vars are used today.
const goalsDefaultTmpl = `당신은 침투 테스트 대상 분해자입니다。귀하의 책임은**달성해야 할 최종 결과**，공격 단계를 계획하는 대신。

**첫걸음（대상을 나누기 전에 하세요）：추출 작업 제약 조건**
님으로부터「미션목표 / 작업 설명」은 연산자 쌍을 식별합니다.【어떻게 하면 될까요?、허용되지 않는 작업은 무엇입니까?】의 명확한 규정，전화주세요 set_constraints 항목별로 항목을 등록하세요.（설명의 경우、대상이 연산 제약 조건을 포함하지 않는 경우 연산 제약 조건을 추출할 필요가 없습니다.）：
- type=deny：금지된 조작（「포트 스캐닝 없음」「프로덕션 환경에 대해서는 쓰지 마십시오./삭제 작업」「폭파금지」「특정 하위 도메인을 건드리지 마세요.」）。
- type=allow：명시적으로 허용됨/제한된 작동 범위（「수동 정찰만 허용됩니다.」「특정 도메인 이름에만 해당」）。
- 제약 ≠ 대상，도요 ≠ 공격 단계：운용행위의 경계를 규정한 것이다.。
- **제약 조건은 다음과 같습니다.【독립형、구체적인 목표를 적어보세요】**：넣어보세요「현재 목표/현재 포트/현재IP/현재 도메인 이름/이 사이트」이런**대명사**작업 대상으로 바꾸기/**특정 값**。제약 조건은 실행 단계에서 프롬프트에 별도로 주입됩니다.，문맥에서 벗어나 대명사가 누구를 가리키는지 판단하는 것은 불가능합니다.。
  예：목표는 https://abc.example.net → 쓰기「테스트만 허용 abc.example.net」대신「현재 타겟만 테스트 가능」；「대상 포트만 테스트 443，다른 포트를 스캔하지 마십시오」대신「현재 포트만 테스트」。원문에 다음과 같은 내용만 나온다면「현재 목표」그런데 목적지 주소는 확실해요，주소만 입력해주세요。
- **대상만 등록하세요/설명에【명확하게 쓰거나 강조하세요】제약，어떠한 가공도 엄격히 금지됩니다.**；유형이 확실하지 않을 때 사용됩니다. deny（좀 더 보수적으로）。
- 대상이 된다면/설명에는 실제로 작동상의 제약이 없습니다.，그러면**하지 마세요**전화주세요 set_constraints。
제약조건 등록 완료（그렇다면）이후，그런 다음 다음과 같은 대상 분할을 수행합니다.。

**대상 = 최종 결과물/검증 가능한 결과**

**대상이 아닌 내용（하위대상 기재 금지）**：
- 정보수집、정찰、엔드포인트 검사
- 취약점 분석 및 검증 프로세스
- 공격 단계、사용방법
- 결과 확인 단계

**분할 원리**：
- 사용자 설명의 궁극적인 목표는 단 하나입니다. → 1번 출력
- 여러개 있어요**서로 독립적**의 최종 결과물 → 별도로 나열됨
- 명확한 취약점 클래스의 주석에 대응 가능 vulnclass；정보수집/비즈니스 로직 클래스 대상을 비워 두세요.
- 사용자가 언급하지 않은 목표를 고안하는 것은 엄격히 금지됩니다.

전화주세요 set_goals 결과 제출。`

// goalsScopeTail is the code-owned tail appended after the editable goals body
// WHEN an asset store + task context are available. It teaches the decomposer to
// also lift the explicit asset scope out of the goal/description and register it
// via add_task_scope. Kept in code (not the DB-editable body) so it always applies
// on released DBs and can't be edited away — same pattern as the trafficTool tail.
const goalsScopeTail = `

**추가 책임：테스트 자산 범위 등록**
대상을 분할하는 것 외에，아직부터 시작해야 합니다.「미션목표 / 작업 설명」으로 식별됨**명시적으로 지정된 테스트 자산 범위**，전화주세요 add_task_scope 등록（이 작업에 대한 권한 범위，은 자산 테스트 커버리지의 분모이기도 합니다.）。**최소 범위 원칙：사용자가 명시적으로 클릭한 대상만 등록，임의로 확대하지 마세요。**
- 목표는 URL 또는 호스트 이름이 포함된 주소（ https://xxx.example.com/path、app.example.com）→ 어느 쪽이든**전체 호스트 이름**，kind=subdomain，value=전체 호스트 이름。
  예：대상 https://a1b2c3.lab.example.net/path → kind=subdomain，value=a1b2c3.lab.example.net（**아니요** example.net）。
  **절대금지**하위 도메인이 있는 호스트 이름을 루트 도메인 이름으로 줄입니다.——보세요 xxx.example.com 그냥 전체등록만 해주세요 example.com 사용자 대상을 넘어 범위를 확장하겠습니다.，최소 범위의 원칙을 위반합니다.。
- 사용자가 제공하는 경우에만**베어 루트 도메인 이름、이며 하위 도메인을 포함하지 않습니다.**（직접 써보면 example.com），아니면 명시적으로 말하세요“전체 사이트 / 모든 하위 도메인 / 전체 도메인 이름” → 전용 kind=root_domain，value=example.com。
- 순수 IP 또는 네트워크 세그먼트 → kind=ip / cidr，value=IP 또는 CIDR。
- **하지 마세요**등록업체 범위（company）——작업이 방금 생성되었습니다.、이 회사는 일반적으로 자산 시스템에 존재하지 않습니다.，등록할 수 없습니다，회사 차원의 범위는 후속조치로 넘기겠습니다. plan 스테이지 처리。
기타 규칙：
- 회원가입만 가능**대상/설명에 명확하게 적어주세요**의 범위；언급되지 않은 도메인 이름을 발명하거나 유추하는 것은 엄격히 금지됩니다./IP。
- reason 간략한 설명은 어떤 문장에서 나왔나요?，감사에 편리함。
- 대상이 된다면/설명에 명확한 자산 범위가 없습니다.，그러면**하지 마세요**전화주세요 add_task_scope。
먼저 사용해 보세요 add_task_scope 등록범위（그렇다면），다시 전화해 set_goals 대상 제출。`

// GoalSpec is one decomposed objective.
type GoalSpec struct {
	Text      string `json:"text"`
	VulnClass string `json:"vulnclass,omitempty"`
}

// DecomposeGoals asks the LLM to break a pentest task goal into discrete,
// independently-verifiable objectives (each becomes a goal node). Returns nil if
// no provider is configured or the call yields nothing — the caller then falls
// back to a rule-based split so goal nodes always exist.
//
// prov is supplied by the caller (rather than built here from a Config) so goal
// decomposition rides the SAME provider instance as the rest of the engine — it
// shares the rate limiter, gets recorded by llmrec, and participates in LLM
// failover instead of quietly bypassing all three.
//
// desc is the task's free-text description (배경：대상 범위/flag 수량/전투 지시사항 등).
// It is fed alongside the goal so the decomposer no longer splits blind — the
// prompt still forbids inventing anything the two texts don't state.
//
// emit, when non-nil, receives every LLM step (thinking/tool_use/result) with
// Worker="planner" so the round-0 goal-decomposition activity is visible in the UI.
//
// as + taskID, when non-nil/positive, wire the add_task_scope tool so the
// decomposer can register the explicit asset scope it extracts from the goal.
//
// ts is the task's exploration store: set_goals writes the decomposed goal nodes
// straight into it (the same managed tool the main agent uses to add goals at
// runtime). The returned specs are read back from the store so callers can emit
// per-goal activity and detect the "LLM produced nothing" case for their fallback.
func DecomposeGoals(ctx context.Context, prov llm.Provider, dataDir, goalText, desc string, as *db.AssetStore, ts *db.ExplorationStore, taskID int64, emit func(db.Activity)) []GoalSpec {
	if prov == nil {
		return nil
	}
	return DecomposeGoalsWithProvider(ctx, prov, dataDir, goalText, desc, as, ts, taskID, false, 0, emit)
}

// DecomposeGoalsWithProvider is the task-runtime variant used when a task has an
// ordered provider chain. It preserves the same tools and write behavior while
// letting the caller own provider selection/failover. maxTokens is the profile's
// per-reply output cap (0 = send none).
func DecomposeGoalsWithProvider(ctx context.Context, prov llm.Provider, dataDir, goalText, desc string, as *db.AssetStore, ts *db.ExplorationStore, taskID int64, nonStreaming bool, maxTokens int, emit func(db.Activity)) []GoalSpec {
	if prov == nil {
		return nil
	}
	// 타겟 분해는 일회성 통화입니다：끊지 마세요 transcript store，그래서 agentcore 안 가요 ctx 끊으세요
	// session id（존재할 뿐이다 writer 그냥 끊으세요，또 만나요 agentcore.Prompt）。그리고 눌러주세요 session-id 머리
	// 프롬프트 캐시 만들기/고정 라우팅을 위한 게이트웨이（opencode zen 누락 x-opencode-session 직접 400
	// MissingSessionID）내가 읽은 내용은 ctx 이 값은——보상 안하면 그걸로 끝「대화는 평범하네요、분해 400」。
	// 안정된 것을 명시적으로 걸어두세요 id：동일한 탐색의 분해를 공유하도록 요청했습니다.（캐시 타격에 도움이 됨），으로 명명되었으며
	// planner/worker 충돌 없음，할 수 있습니다 llmrec.parseSession 올바른 귀속。
	if ts != nil {
		ctx = transcript.WithSessionID(ctx, fmt.Sprintf("exp%d-goals", ts.ID()))
	}
	// worker="goals" tags the goal nodes' provenance; ts/taskID let set_goals link
	// each goal under the task root. This is the catalog's real set_goals tool, so a
	// web-edited description/schema on it applies here too.
	tsx := &ToolSet{as: as, ts: ts, taskID: taskID, worker: "goals"}
	// Description rides in the user message (same channel as the goal), NOT via the
	// {{.EngagementDescription}} template var — else a prompt that references the var
	// would inject the description twice. System prompt stays pure static instructions.
	sys := renderSystem("goals", goalsDefaultTmpl, GoalsVars{DataDir: dataDir, Now: nowStr()})
	// set_constraints 상시 가능(의존하지 않음 asset store):본문이 포함되어 있습니다「연산 제약 조건을 먼저 추출한 후 대상을 분할합니다.」이번 단계는
	// (사용 가능 agent 문구를 변경하려면 페이지를 편집하세요.),여기에 도구를 연결하면 됩니다。
	tools := []actool.CoreTool{tsx.setGoals(), tsx.setConstraints()}
	// Wire add_task_scope only when we have a real asset store + task to write to.
	// The scope-extraction tail is appended in lockstep so the prompt never asks for
	// a tool that isn't present.
	if as != nil && taskID > 0 {
		tools = append(tools, tsx.addTaskScope())
		sys += goalsScopeTail
	}
	userMsg := "미션목표：\n" + goalText
	if d := strings.TrimSpace(desc); d != "" {
		userMsg += "\n\n작업 설명（배경정보，목표 범위를 포함할 수 있음/flag 수량/전투 설명；참고용，언급되지 않은 내용은 꾸며내지 마세요.）：\n" + d
	}
	// Use captureRun so every LLM step is emitted as an activity record (visible in
	// the plan tab under the round-0 marker). Falls back gracefully when emit is nil.
	captureEmit := func(r db.Activity) {
		if emit != nil {
			r.Worker = "planner"
			emit(r)
		}
	}
	captureRun(ctx, agentcore.Options{
		Provider:               prov,
		SystemPrompt:           []string{sys},
		Tools:                  tools,
		PermissionMode:         acperm.ModeBypass,
		DisableBackgroundTasks: true,
		// 3 단계(그리기 제약조건 → 등록범위 → 철거 대상)각각 하나의 도구 호출이 필요합니다.,끝나기 전에 조정이 누락되지 않도록 충분한 라운드를 제공하십시오. set_goals。
		MaxTurns:     8,
		NonStreaming: nonStreaming, // 그게 profile 비스트리밍 모드를 선택하세요. Provider.Complete
		MaxTokens:    maxTokens,    // 0 = 상한선 없음,서버의 기본값에 따라 결정됩니다.
	}, userMsg, captureEmit)
	// set_goals persisted the goals directly; read them back so the caller sees what
	// was written (empty slice ⇒ the LLM produced nothing ⇒ caller falls back).
	if ts == nil {
		return nil
	}
	nodes, _ := ts.ListByKind(db.KindGoal, 10000)
	var out []GoalSpec
	for _, n := range nodes {
		var p struct {
			Text      string `json:"text"`
			VulnClass string `json:"vulnclass"`
		}
		_ = json.Unmarshal(n.Payload, &p)
		if strings.TrimSpace(p.Text) != "" {
			out = append(out, GoalSpec{Text: p.Text, VulnClass: p.VulnClass})
		}
	}
	return out
}
