package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

// Planner is the event-driven LLM planner (docs §4.3): each time the asset or
// exploration graph changes (debounced), it reads the exploration route, queries
// assets, judges whether the task goal is met, and emits 0..N exploration intents
// into the frontier. It is the sole intent generator.
type Planner struct {
	findingRecorder   FindingRecorder
	prov              llm.Provider
	model             string
	tx                *transcript.Store                      // raw LLM conversation persistence (nil = off)
	window            int                                    // context window in tokens (for compaction)
	windowFn          func() int                             // optional dynamic task-chain minimum
	maxTurns          int                                    // max agent turns per run (0 = unlimited)
	killWork          func(intentID int64) error             // engine callback to terminate a running work (nil = off)
	steerWork         func(intentID int64, msg string) error // engine callback to steer a running work mid-run (nil = off)
	proxyAddr         string                                 // recording proxy for WebFetch (empty = direct)
	proxyCACert       string                                 // recording proxy's CA cert path (HTTPS verify)
	webSearch         WebSearchOpts                          // web_search tool backend selection (off by default)
	workDir           string                                 // shared work dir (surfaced in prompt as artifact-output target)
	injectConstraints func() bool                            // resolver: inject task operation constraints into system prompt? (nil = yes)
	nonStreamingFn    func() bool                            // resolver: use non-streaming (Complete) path? (nil = streaming)
	noaEnabledFn      func() bool                            // resolver: use experimental noa compaction? (nil = off)
	maxTokensFn       func() int                             // resolver: per-reply output cap (nil/0 = send no cap)
	compactor         *Compactor                             // cold-node compaction (§7); nil = disabled

	// todos keeps ONE plan-scratchpad per task (keyed by exploration id) so the
	// planner's multi-step plan survives across wake-ups — each Plan() is a fresh
	// session, but the shared store lets it record a serial exploit chain once and
	// dispatch it step-by-step over rounds instead of front-loading it in parallel.
	todoMu sync.Mutex
	todos  map[int64]*actool.TodoStore
}

func NewPlanner(prov llm.Provider, model, workDir string, tx *transcript.Store, window, maxTurns int) *Planner {
	return &Planner{prov: prov, model: model, workDir: workDir, tx: tx, window: window, maxTurns: maxTurns, todos: map[int64]*actool.TodoStore{}}
}

func (p *Planner) SetCompactionWindowResolver(fn func() int) { p.windowFn = fn }

// SetCompactor wires the cold-node compactor (cold-digest §7). Called each
// planner wake-up to advance the round counter, maintain cold stamps, and
// (off the hot path) fold cold nodes into digests. nil = feature disabled.
func (p *Planner) SetCompactor(c *Compactor) { p.compactor = c }

// SetNonStreaming wires a resolver deciding whether runs use the non-streaming
// model path (true = non-streaming). nil/unset = streaming (default).
func (p *Planner) SetNonStreaming(fn func() bool) { p.nonStreamingFn = fn }

func (p *Planner) nonStreaming() bool { return p.nonStreamingFn != nil && p.nonStreamingFn() }

// SetNoaEnabled wires a resolver deciding whether runs use the experimental noa
// context-compression mechanism. nil/unset = off (built-in compaction). Read per
// run so the settings toggle takes effect without rebuilding the agent.
func (p *Planner) SetNoaEnabled(fn func() bool) { p.noaEnabledFn = fn }

// SetMaxTokens wires a resolver for the per-reply output cap. nil/unset or 0 =
// send no cap and let the endpoint decide. Read per run, like nonStreaming.
func (p *Planner) SetMaxTokens(fn func() int) { p.maxTokensFn = fn }

func (p *Planner) maxTokens() int {
	if p.maxTokensFn == nil {
		return 0
	}
	return p.maxTokensFn()
}

func (p *Planner) compactionWindow() int {
	if p.windowFn != nil {
		return p.windowFn()
	}
	return p.window
}

// SetProxy points the planner's WebFetch at the recording proxy plus the CA cert
// it trusts to verify HTTPS through it (empty addr = direct).
func (p *Planner) SetProxy(addr, caCert string) { p.proxyAddr, p.proxyCACert = addr, caCert }

// SetWebSearch selects the web_search backend for the planner (off by default).
func (p *Planner) SetWebSearch(o WebSearchOpts) { p.webSearch = o }

// SetConstraintInject wires a resolver deciding whether this task's operation
// constraints get injected into the planner system prompt. Read per round so the
// settings toggle takes effect without rebuilding the agent. nil = inject (default).
func (p *Planner) SetConstraintInject(fn func() bool) { p.injectConstraints = fn }

// wantConstraints reports whether constraint injection is enabled (default yes).
func (p *Planner) wantConstraints() bool { return p.injectConstraints == nil || p.injectConstraints() }

// todoFor returns the task's persistent planning todo store, creating it on first
// use. Shared across all of this task's planner wake-ups.
func (p *Planner) todoFor(expID int64) *actool.TodoStore {
	p.todoMu.Lock()
	defer p.todoMu.Unlock()
	s := p.todos[expID]
	if s == nil {
		s = actool.NewTodoStore()
		p.todos[expID] = s
	}
	return s
}

// SetKillWork wires the engine's per-work terminate callback so the planner's
// kill_work tool can stop a single running worker.
func (p *Planner) SetKillWork(fn func(intentID int64) error) { p.killWork = fn }

// SetSteerWork wires the engine's per-work steering callback so the planner's
// steer_work tool can inject a mid-run course-correction into a running worker.
func (p *Planner) SetSteerWork(fn func(intentID int64, msg string) error) { p.steerWork = fn }

// renderPlannerTodos formats the persistent planning todo for injection into the
// wake-up prompt (empty when there are no todos yet — first wake-up).
func renderPlannerTodos(items []actool.Todo) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n【당신의 계획은 완료될 예정입니다（항적 전체에 걸쳐 유지됨，지난번에 쓰셨네요）】：\n")
	for _, it := range items {
		mark := map[actool.TodoStatus]string{actool.TodoPending: "☐", actool.TodoInProgress: "▶", actool.TodoCompleted: "✔"}[it.Status]
		if mark == "" {
			mark = "☐"
		}
		b.WriteString(fmt.Sprintf("  %s %s\n", mark, it.Content))
	}
	b.WriteString("그대로 진행하세요：다만 그렇습니다【전제조건 단계 완료 / 상황에 따라 다르다 fact 이(가) 이미 존재합니다.】의 다음 의도；사용 TodoWrite 업데이트 목록（이 되었습니다. fact 만족스러운 걸음 표시 completed）。이미 목록에 있는 파이를 반복하지 마세요. pending/in_progress 단계。")
	return b.String()
}

// TriggerEvent describes what concretely caused this planning round to fire, so
// the planner looks first at the actual change instead of re-scanning the whole
// overview. Kind:
//
//	"done"    — a worker finished intent IntentID (its output conclusion is fetched).
//	"finding" — a worker reported a finding on intent IntentID (Detail = 요약).
//	"goal"    — the human (via 스승님 agent 님 set_goals) added one OR MORE goals in a
//	            single call (Goals = 이번에 새로 추가된 타겟 텍스트，1+ 글；set_goals 지원 배치).
//	"goal_deleted" — the human deleted a goal from 목표관리 개요 (Detail = 삭제된 대상 텍스트).
//	"goal_edited"  — the human edited a goal from 목표관리 개요 (OldGoal→NewGoal 문자).
//	"cancelled" — the human deleted intent IntentID (Detail = 삭제 이유). The intent is
//	            stopped (not deleted) and the reason is attached to it as a fact.
type TriggerEvent struct {
	Kind     string
	IntentID int64
	Detail   string
	Summary  string   // Kind=="cancelled" 헌신：삭제 전 캡처된 의도 요약（실제 삭제 후 노드가 더 이상 존재하지 않습니다.，더 이상 확인할 수 없습니다）
	Goals    []string // Kind=="goal" 헌신：이번에는 set_goals 새 대상 텍스트（1 하나 이상）
	OldGoal  string   // Kind=="goal_edited" 헌신：수정 전 대상 텍스트
	NewGoal  string   // Kind=="goal_edited" 헌신：대상 텍스트를 수정했습니다.
	Hints    []string // Kind=="hint" 헌신：이번에는 add_hint 새 프롬프트 텍스트（1 하나 이상）
}

// renderTriggers spells out the change(s) that fired this round: for a finished
// worker — which intent + its output conclusion; for a finding — which intent +
// what was found. Empty for time/heartbeat wakes. Reads the store (best-effort;
// a blank field never blocks the round).
func renderTriggers(ts *db.ExplorationStore, evs []TriggerEvent) string {
	if len(evs) == 0 || ts == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n【이번에 촉발된 실제 변화（먼저 여기를 보세요，그럼 방향을 정할지 결정하세요）】：")
	for _, ev := range evs {
		switch ev.Kind {
		case "goal":
			if len(ev.Goals) == 1 {
				b.WriteString(fmt.Sprintf("\n- 명（스승님 agent）새로운 대상이 추가되었습니다：%s —— 달성해야 할 새로운 목표，이에 따라 추가 탐색 방향을 추가해 주세요.（해당 의사가 없는 경우）。", ev.Goals[0]))
			} else {
				b.WriteString(fmt.Sprintf("\n- 명（스승님 agent）추가됨 %d 목표：%s —— 은 모두 달성해야 할 새로운 목표입니다.，해당 의도가 없는 대상에 대해서는 탐색 방향을 하나씩 추가해 주세요.。", len(ev.Goals), strings.Join(ev.Goals, "；")))
			}
		case "hint":
			if len(ev.Hints) == 1 {
				b.WriteString(fmt.Sprintf("\n- 명（스승님 agent）새로운 전략 팁을 추가했습니다.：%s —— 탐사 지도에 연결되었습니다，적당히 조절해주세요/추가 탐색 방향（해당 의사가 없는 경우）。", ev.Hints[0]))
			} else {
				b.WriteString(fmt.Sprintf("\n- 명（스승님 agent）추가됨 %d 전략 팁：%s —— 탐사 지도에 연결되었습니다，하나씩 적절하게 조정해주세요/추가 탐색 방향。", len(ev.Hints), strings.Join(ev.Hints, "；")))
			}
		case "goal_deleted":
			b.WriteString(fmt.Sprintf("\n- 사람이 이 대상을 삭제했습니다.：%s —— 대상이 제거되었습니다.，남은 대상도 그에 맞게 재심사해주세요/방향（더 이상 의도를 부여할 필요가 없습니다.）。", ev.Detail))
		case "goal_edited":
			b.WriteString(fmt.Sprintf("\n- 사람들이 타겟을 수정했어요，「%s」이 됩니다.「%s」—— 새로운 목표에 맞게 탐색 방향을 조정해주세요.（원래 방향이 더 이상 적용되지 않으면 파견을 중단하십시오）。", ev.OldGoal, ev.NewGoal))
		case "finding":
			b.WriteString(fmt.Sprintf("\n- 의도 #%d（%s）님 worker 이(가) 신고했습니다. finding：%s", ev.IntentID, intentSummary(ts, ev.IntentID), ev.Detail))
		case "cancelled":
			// 삭제 중에 의도한 내용이 캡처되었습니다. Summary（실제 삭제 후 노드가 더 이상 존재하지 않습니다.，intentSummary 찾을 수 없음）。
			sm := ev.Summary
			if sm == "" {
				sm = intentSummary(ts, ev.IntentID)
			}
			b.WriteString(fmt.Sprintf("\n- 의도 #%d 사용자가 삭제함，의도한 내용은：%s、삭제이유는：%s。인텐트가 삭제되었습니다.（더 이상 실행되지 않습니다.）；그에 맞춰 다시 계획을 세워주세요。", ev.IntentID, sm, ev.Detail))
		default: // "done"
			b.WriteString(fmt.Sprintf("\n- 의도 #%d（%s）님 worker 끝，출력 결론：%s", ev.IntentID, intentSummary(ts, ev.IntentID), workerOutput(ts, ev.IntentID)))
			if fids := factIDsYielded(ts, ev.IntentID); fids != "" {
				b.WriteString(fmt.Sprintf("；새로운 사실을 만들어내려는 의도 id：%s ", fids))
			}
		}
	}
	b.WriteString("\n（자세한 내용 확인 가능 node_detail / get_worker_output / list_findings 다시 확인해 보세요。）")
	return b.String()
}

// factIDsYielded lists the fact ids an intent produced this run as "#12、#15", so the
// planner can jump straight to the round's incremental facts. Empty (best-effort) when
// the intent yielded no facts or the lookup fails.
func factIDsYielded(ts *db.ExplorationStore, id int64) string {
	ids, err := ts.FactsYielded(id)
	if err != nil || len(ids) == 0 {
		return ""
	}
	parts := make([]string, len(ids))
	for i, fid := range ids {
		parts[i] = fmt.Sprintf("#%d", fid)
	}
	return strings.Join(parts, "、")
}

// intentSummary reads an intent node's one-line summary (best-effort, "?" on miss).
func intentSummary(ts *db.ExplorationStore, id int64) string {
	n, err := ts.GetNode(id)
	if err != nil || n == nil {
		return "?"
	}
	var p map[string]any
	if json.Unmarshal(n.Payload, &p) == nil {
		if s, ok := p["summary"].(string); ok && s != "" {
			return s
		}
	}
	return "?"
}

// workerOutput returns the finished worker's conclusion for an intent — the last
// 'result' (else 'text') activity's full detail, truncated. Same source get_worker_output uses.
func workerOutput(ts *db.ExplorationStore, id int64) string {
	acts, _, err := ts.ActivityList(&id, 0, 1000)
	if err != nil {
		return "(출력을 가져오지 못했습니다.)"
	}
	var pick *db.Activity
	for i := range acts {
		if acts[i].Kind == "result" {
			pick = &acts[i]
		} else if acts[i].Kind == "text" && pick == nil {
			pick = &acts[i]
		}
	}
	if pick == nil {
		return "(그게 work 아직 출력기록이 없습니다)"
	}
	out, _ := ts.ActivityDetail(pick.ID)
	if out == "" {
		out = pick.Summary
	}
	return truncOutput(out, 800)
}

// truncOutput caps a worker-output blob so the trigger context doesn't bloat the
// system prompt every round; full text is one get_worker_output call away.
func truncOutput(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + " …（잘림，완전체로 만나요 get_worker_output）"
}

// renderGraphOverview folds the pre-computed graph_overview snapshot into the
// wake-up prompt so the planner starts each round with the full situation in
// hand — saving the round-trip it would otherwise spend calling the tool. It is
// the exact same JSON graph_overview would return; deeper detail is still one
// tool call away (node_detail / list_facts / …).
func renderGraphOverview(data map[string]any) string {
	b, err := json.Marshal(data)
	if err != nil {
		return "" // fall back to the model calling graph_overview itself
	}
	return "\n\n【현재상황（graph_overview 프리페치，이 도구를 호출할 때의 반환과 동일합니다.；세부정보가 필요하고 필요에 따라 조정하세요. node_detail/list_facts 등）】：\n" + string(b)
}

// plannerDefaultTmpl is the built-in EDITABLE body (섹션 [A]) of the planner prompt,
// seeded into agent_prompts. Goal is a {{.Goal}} template var; the 중간제품 출력 프로토콜
// tail is code-owned (artifactSpec) and appended by plannerSystem after rendering.
const plannerDefaultTmpl = `귀하는 네트워크 보안 플랫폼 공인 침투 테스트 시스템입니다."기획자"，자주 잠에서 깨어난다（그림이 바뀌면 일어나요）。책임：상황 읽기 → 대상을 판단하라 → **다루지 않은 새로운 방향이 있는 경우에만 해당**보충탐사의향。당신은 기획자입니다、은 집행자가 아닙니다.：이번 라운드의 모든 제품은【생성/의도를 명확하게 말해주세요】또는【목표를 정하라】，물론이죠 plan 리가 일을 끝냈어。

미션목표：{{.Goal}}

**이번 라운드에는 여러 의도가 생산되어야 한다（이것을 먼저 생각해보세요）**：
- **확실한 결론（최우선 순위）**：언제까지나【목표 달성 실패】그리고【현재는 없습니다 open 또는 running 의도】（frontier_open=0 그리고 running_intents 이 비어 있습니다.），이번 라운드【필수】목표를 향해 전진하려는 의지를 하나 이상 만들어라——실행되지 않음 work 기다리면 된다、도 대기열 방향이 아닙니다.，출력 0 의도=임무 중단；알려진 방향도 오직 recent_done 내부，또한 다음에 따르면 done/exhausted/blocked 님의 판단은 하나 더 열어보거나 하나 더 보내는 것입니다.。
- 딱딱한 수익을 넘어서，**출력 0 의도는 당연한 결과，하지만 타당한 이유가 있을 거에요**（아니요"적은 세력이 더 안정적입니다."기본값）：①**덮었다**——당신이 생각한 방향이 다 맞았다. open/running 의 의도 처리（기존 의도를 재생성하기 위해 문구를 변경하는 것은 심각한 실수입니다.）；②**종속성을 기다리는 중입니다.**——다음 종속성이 현재 실행 중입니다. work 의 출력、그리고 아직 안 나왔네요（이때 하드코어로 인해 다운스트림이 프런트 엔드를 얻지 못하고 유휴 상태가 됩니다.，다음 기상 사진이 업데이트될 때까지 기다렸다가 보내야 합니다.）。
- 차례로：그렇죠【보장되지 않음、실행에 의존하지 않습니다. work】의 새로운 방향，또는 목표가 달성되지 않았으며 범위 내에서 아직 측정되지 않은 영역이 있습니다.，보낼 시간이다——하지 마세요 0 게으름의 기본은 의지다。

**각 기상별 의사결정 과정**：

1. **전체 상황은 이 팁 아래에 첨부되어 있습니다.**（그렇죠 graph_overview 의 귀환，더 이상 조정할 필요가 없습니다.）：task（원제목+대상/루트 노드）、자산수、goals+상태、open/running/recent_done 의도、sites_without_endpoints（엔드포인트가 없는 사이트，탐색할 수 있는 가능한 방향을 묻는 메시지를 표시합니다.）、facts（여러 가지 사실을 살펴보세요，과 취약점은 두 가지 범주로 나뉩니다.）、recent_facts（{id,summary,confidence?}）。
   - **범위**：노드 탐색（goals/의도/facts/findings）이 작업만 포함됩니다.；**자산 맵 글로벌 공유**（여러 작업에 동일한 사본，자산 수는 전 세계적으로 적용됩니다.、이 작업에만 해당되지 않음）——이 작업과 관련이 없는 자산은 무시합니다.。
   - **블러드라인**：각 의도 밴드 parents（업스트림：어떤 사실이 파생되는가?/의도）그리고 yields（다운스트림：어떤 사실이 생산됐나요?/찾음），recent_facts 각 스트립 from_intent；이러한 이해를 바탕으로"어떤 사실이 어느 방향에서 나오나요?、새로운 방향이 합성될 수 있을까?"。
   - **부정/의심스러운 관찰**（recent_facts 내부"포트가 닫혔습니다./주사 불가"등）네 worker 님의 관찰、결론은 아닙니다：편지를 받기 전 node_detail(id) 보세요 evidence——evidence 솔리드、confidence=observed 모든 수단을 동원한 경우에만 방향이 일시적으로 차단된 것으로 간주됩니다.；evidence 없어짐、그냥"같네요/한 번만 탐색하세요."、또는 confidence=inferred 님，언론【아직 확정되지 않았습니다】처리 중，범위 내이고 다른 취지의 의도가 없는 경우，기본적으로 확인 또는 반박을 위한 검토 의도가 전송됩니다.（**동일한 부정적인 방향은 최대 한 번만 검토할 수 있습니다.**；검토 후에도 여전히 부정적、그리고 증거는 합리적입니다，그냥 그 결론을 존중하세요、더 이상 보내지 않습니다.）。
   - **필요에 따라 조정하기 전에 자세한 내용이 필요합니다.**：list_facts（페이징，최신순，기본값 20，예 q 필터、before 페이지 넘기기，와 함께 total/has_more）、list_findings（모든 취약점）、node_detail(id)（완전한 증거/세부사항；목록/recent_facts 초록만 제공）、list_assets（pull：q 검색、type/company_id/task_id 필터、페이징，또는 id/ids 직접 접속）、asset_neighbors。자산의 글로벌 공유，기본적으로 전액을 가져오지 마세요.。

2. **대상을 판단하라（핵심 책임）**：goals 필드에 이미 대상 및 상태가 포함되어 있습니다.；누군가가 이 쌍을 발견했습니다/목표가 달성되지 않았다는 사실이 입증되었습니다.，조정 prove_goal(goal_id, evidence_id, reason) 마크 met。**마지막 미완성 목표를 표시할 때，전체 작업이 완료된 것으로 시스템이 자동으로 판단합니다.**——엔딩은 하나씩만 되네요 prove_goal 운전사，다른 건 없어요"클릭 한번으로 완료"뜻。
   - ⚠️ **정량적 합격 확인（사전 스탬프는 엄격히 금지되어 있습니다.）**：대상에 수량화 가능한 조건이 포함되어 있습니다.（적용 범위에 도달했습니다. X%、받아가세요 N  flag、특정 권한을 얻습니다.）시간，prove_goal 전【필수】위 내용을 확인하세요 graph_overview 의 실제 측정값（coverage.pct、findings_total 계산 등）：기준 미달【금지됨】prove_goal，차액을 보충하기 위한 재할당；허용되지 않음"일반적으로 달성됨/코어가 포착되었습니다" met。예：취재 요청 100% 그리고 실제 측정 coverage.pct=40% → 연결되지 않음，계속해서 보완적인 테스트 계획을 보내주세요。

3. **（선택사항，시작만 하세요、매우 가볍습니다.）탐지 및 이해**：사진이 거의 없을 때만 fact（recent_facts 기본적으로 비어 있음、임무는 이제 시작됐다）、상황만으로는 초기 의도를 특정할 수 없는 경우，전용 Bash 아주 적은 양의 목표를 수행하기 위해 기다려주십시오.、읽기 전용 감지（ 1–2 회 curl 홈페이지를 보세요/지문）。**유일한 합법적인 제품은 의도를 보다 정확하게 설명하는 것입니다.**——절대 취약점 발견은 아닙니다/확인/사용，도 엔드포인트가 아닙니다./목차/매개변수 열거 결과（그건 바로 worker 의 인생，의도를 적어서 보내주세요）。세 가지 하드 경계：
   - 이미 사진에 나와있어요 worker 제작 fact（facts>0 / recent_facts 비어있지 않음）→【금지됨】그럼 직접 감지해 보세요，모든 판단은 기존의 기준을 바탕으로 합니다. fact，이번 제품 라운드는"새로운 뜻을 보내주세요"또는"끝"；단서를 더 깊이 파고 싶음 → 세력은 양보할 생각이다 worker 확인해 보세요，본인은 아님 curl。
   - 처음에도 가장 탐색이 ≤3 처음부터 멈춤，처음 의도를 명확히 하기 위해；일단 자신을 찾으면"심층검증"대신"방향을 빠르게 판단"（끝점을 하나씩 열거합니다./목차、하나씩 해보세요 id、디코딩 체인、동일한 인터페이스를 반복적으로 프로브、어떤 주사라도/울트라 바이어스/취약점 테스트 검증——모두 worker 님의 수고가 컸습니다），즉시 중단하고 의사를 적어주세요。
   - 기존 사실에서/상황판단，전혀 감지할 필요가 없습니다.。

4. **어떤 새로운 방향을 추구할지 결정**：**여기요"구속"은 다음을 의미합니다.【기존 의도를 반복하지 마십시오.】，아니요"최대한 적게 보내세요"**——목표가 달성되지 않았을 때，기본 후속 질문은 다음과 같습니다."목표물에 접근하기 위해，더 깊은 건 또 뭐예요?、더 무자비해졌어、아직 보장되지 않음"，대신"결론이 날 수 있을까요?"。의도는【탐색 방향 열기】（은 고정형이 아닙니다./메뉴），알려진 사실과 결합、자산、목표자체판단방향，하나씩 open + running + recent_done 비교：
   - 이미 open/running 재정의 → 은 더 이상 생성되지 않습니다.（처리 중）。
   - 에 recent_done 에 등장 → **의도를 먼저 보세요 state（하나씩 가져오세요）멈추는 방법을 알려주세요，나중에 결정하세요**：
     · **done（정상적으로 완료되었습니다）**：덮었다 → 그대로 재할당하지 마세요.；막다른 골목인지 살펴보세요 yields 님으로부터 fact 결론、대신 state；나타남【재료 특성의 새로운 메커니즘】（새로운 사실/자산/매개변수/확실히 플레이스타일이 다르네요）인재 재배치，그리고 summary 지난번과 차이점을 적어주세요；문구 변경、"다시 시도하면 될지도 모르겠네요"포함되지 않음，재시도 금지。
     · **exhausted（예산 소진、중간에 잘렸어요，해당 부분만 다시 작성해주세요）/ blocked（모델 또는 네트워크 오류、기본적으로는 성공하지 못했습니다.）**：모든 것은 길의 끝이다.、정보가 불완전합니다.——먼저 사용해 보세요 get_worker_trace / get_worker_output 실제로 무엇을 하는지 살펴보겠습니다.、카드는 어디에 있나요?，그런 다음 다음 중에서 선택하세요.：돌파구에 가까워졌지만 예산 때문에 막혔습니다. → 파이"지난번에 이어서 계속"；순수 외부 결함 실패（blocked 창은）→ 같은 방향으로 직접 재전송；카드가 매번 같은 자리에 붙어있어요 → 플레이 방식을 바꿔보세요/방향。기본은 언제나 trace 의 실제 진전，아니요 state 그 자체。
   - 가리려는 의도 없이 완전히 새로운 방향 → 생성。
   - 알려진 모든 방향은 아직 open/running 의 의도 재정의 → 생성되지 않음、바로 끝（실행 중/줄서서 work，그들이 전진할 때까지 기다려라）；하지만 만약 그렇다면 recent_done 재정의、없음 open/running 그리고 목표는 달성되지 않았습니다 → 맨 위의 하드한 결론을 누르고 오픈하거나 갱신해야 합니다.。
   - **적용 범위보다 깊이를 우선시하세요**：coverage 이 하한값입니다./접수항목、탐사대상 자체는 아님；고가치 진입 발견（은 다음으로 이어질 수 있습니다. RCE/권한 상승/데이터 유출）이후，그 길에 의향을 보내는 것을 우선시하라【깊게 파고들어】，고르게 퍼뜨리기보다는、각 자산에 대한 간단한 테스트。
   - **경로를 다양하게 유지하세요、너무 일찍 수렴하지 마세요.**：목표가 달성되지 않았을 때，기존의 의도를 모두 같은 경로로 짜내면/입구，존재하며【본질적으로 다르다】의 방향이 밝혀졌습니다.（또 다른 입구/또 다른 유형의 자산/또 다른 익스플로잇 체인），분기방향을 우선시한다，같은 줄에 동의어 인텐트를 추가하는 대신（실제 차이점을 확인해보세요，문구를 읽지 마세요）；기존 의도에 엇갈린 방향이 가려지면，아직 생성되지 않았습니다.。이상은 2–3 서로 다른 메커니즘을 가진 경로가 공존합니다.（"업로드 링크에서"그리고"인증 우회"），특정 아이템 건네주기【목표가 가까워지고 있다】그래야만 증거에 자원을 집중할 수 있다。**하지만 다양성은 항상 상위에 복종합니다【운영상의 제약】**：제약 조건에 의해 제외된 입구 표면/포트/호스트/작동，근본적으로 다르다고 해서 절대 의도를 만들어내지 마세요.。

   **연쇄 공격 체인：차근차근，병렬로 나누지 마세요。** 직렬 체인에 대한 의존도가 높음（①→②→③，후자의 단계는 이전 단계의 실제 출력에 따라 달라집니다.）：한 번에 병렬로 발행하지 마십시오.（다운스트림이 아직 존재하지 않는 접두사를 가져올 수 없으면 반복만 됩니다./공회전）；사용 TodoWrite 체인 전체를 할 일로 표시（한걸음 한걸음），이번 라운드만 보내주세요"전제조건이 충족되었습니다."그 걸음（보통 첫 번째 단계는），출력될 때까지 기다려주세요 fact 다음에 일어나세요（프롬프트에 할 일 목록이 표시됩니다.）다음 단계를 보내고 만족한 기준을 추가합니다. completed。"똑같습니다"두 조각으로 나누지 마세요（"트리거포인트 확인"그리고"트리거 트리거 포인트"도 같은 단계입니다）；만【병렬、서로 의존하지 않음】의 치수（관련되지 않은 여러 끝점을 열거하는 등）여러 의도를 동시에 사용해야 합니다.。

5. **제출**：사용【한번】add_intent 일괄 제출 심사의 새로운 방향（intents 배열，대부분 4 가장 높은 값，여러번 조정하지 말고 하나씩）：
   - **summary**：자연어 한 문장으로 방향을 기술（테스트 대상 전체 주소 + 어떻게 해야 할까요? + 왜요?），정해진 분류 없음；중복제거는 주로 기존 의도와 비교하는 데 의존합니다.。
   - **asset_ids**：이 방향은 테스트가 필요합니다/공격의 대상 자산 id（전달해 보세요，0/1/여러개，님으로부터 list_assets）——방향이 특정 자산을 중심으로 돌아가는 한（사이트/인터페이스/매개변수/호스트）꼭 보내주세요，중복된 항목을 덮어쓰고 제거하는 데 사용됩니다.、에셋링크에 접속，여러 자산에 걸쳐 전달；순수 글로벌 정찰을 위해 이 항목을 공백으로 남겨두고 특정 자산을 지정하지 마십시오.。
   - **parent_ids**：이 방향의 포괄적인 결론은 어떤 업스트림 노드입니까?（선택사항，0/1/여러개）——여러 사실을 조합해 의도를 만들어내고 모두 합격，업스트림 인텐트에서 파생됨/찾아서 퍼뜨렸어요 id，최상위 새 방향을 비워 둡니다.。

중복없음、강요하지 않음；하지만 목표는 달성되지 않았습니다、또 다른 밝혀지고 깊어진 플레이가 있을 때，보낼 시간 되면 보내세요。단순하다、집중、효율적。`

func plannerSystem(goal, dataDir, workDir string) string {
	body := renderSystem("planner", plannerDefaultTmpl, PlannerVars{Goal: goal, DataDir: dataDir, Now: nowStr()})
	return body + artifactSpec(workDir)
}

// Plan runs one planning round. emit, if non-nil, receives the planner's execution
// steps (so users can see how it reads the situation and judges goals — the
// planner is the intent generator and was previously a black box). Returns whether
// the planner judged the goal met.
// triggers carries the concrete change(s) that fired this round — worker(s) done
// and/or finding(s) reported (may be several — the engine debounces a burst; empty
// for time/heartbeat wakes). They are spelled out at the top of the prompt so the
// planner looks first at the actual change (which intent, its output/finding).
func (p *Planner) Plan(ctx context.Context, taskID int64, as *db.AssetStore, ts *db.ExplorationStore, goal string, triggers []TriggerEvent, emit func(db.Activity)) (met bool, reason string, err error) {
	// cold-digest §2.3/§7: advance this task's planner-round counter, maintain the
	// cold_since_round stamps, and (if a threshold is hit) kick off background
	// compaction. Synchronous part is cheap (a few queries); the LLM compaction
	// runs in a detached goroutine so it never adds latency to this round.
	p.compactor.OnPlannerRound(ctx, ts)
	tsx := NewToolSet(ts, "planner")
	tsx.SetFindingRecorder(p.findingRecorder)
	if as != nil {
		tsx.SetAssetStore(as, as.Companies())
	}
	tsx.SetTaskID(taskID)
	tsx.SetCoverageEnabled(as == nil || as.CoverageEnabled(taskID))
	tsx.killWork = p.killWork   // enable kill_work tool (nil = unavailable)
	tsx.steerWork = p.steerWork // enable steer_work tool (nil = unavailable)
	if origin, _ := ts.OriginFactID(); origin > 0 {
		tsx.SetOwnerNode(origin) // planner-side anchors default to the task root (origin fact)
	}
	// 도메인 도구 + 기본 기본 도구 세트（Read/Write/Edit/MultiEdit/LS/Glob/Grep/Bash）
	// 자산보상 기능이 꺼진 경우 제외 add_task_scope/list_untested_assets（허용되지 않음 prompt）。
	base := append(tsx.DropCoverageTools(tsx.PlannerTools()), actool.DefaultTools()...)
	ctx = WithRunInfo(ctx, RunInfo{TaskID: taskID, ExplorationID: explorationID(ts)})
	tools, def, cleanup := AugmentTools(ctx, "planner", base)
	defer cleanup()
	// 주요상황（의향이 막 완성됐어요 + 미리 가져온 전체 이미지）변경【이번 라운드 user 입력】(아래 참조 input)，system
	// 정적 계획 텍스트만 남겨 둡니다.。move-out 하자 system 매 라운드 안정적、캐싱에 더 도움이 됨；가격은 한 라운드가 길어지면，그럴 수도 있는 상황이죠
	// 은(는) compaction 압축（planner 단일 휠은 일반적으로 짧습니다.，낮은 위험）。situational 은 아래에 표기됩니다. input。
	situational := renderTriggers(ts, triggers) + renderGraphOverview(tsx.graphOverviewData())
	// 작업 수준 deadline / 최종 모드( ctx 주사,또 만나요 taskclock.go)。최종 라운드에서 작업 시간이 초과되었습니다.
	// planner 마지막 말은 다음과 같습니다.【이번 라운드의 운영 지침】이번 라운드에 참여하세요 user 입력(수이 situational),마지막으로만 하자
	// 목표결정、새로운 의도는 없음。
	tc := taskClockFrom(ctx)
	if tc.Final {
		situational += "\n\n【미션 종료（이번 라운드의 특별 지시 사항，위의 정기 기획 과정을 다룹니다.）】：" + resolveTaskTimeoutWrapup("planner")
	}
	// 이 작업의 작업 디렉터리 <workDir>/tasks/<taskID>，먼저 빌드해 보세요。
	taskDir := ensureRunDir(p.workDir, taskID, 0)
	ctx = intercept.WithReviewContext(ctx, taskDir, intercept.ReviewBackground{})
	sysBody := plannerSystem(goal, p.workDir, taskDir)
	if p.wantConstraints() {
		sysBody += constraintBlock(ts) // 운영상의 제약(그렇다면)주입 시스템 프롬프트,프레임 탐색 경계
	}
	system, boundary := deferredSystem(sysBody, def)
	// planner 벽시계 예산이 없음;예 deadline 언제 MaxDuration 남은 부분까지 조여주세요,실행 중인 계획 휠을 사용하도록 하세요.
	// 때가 되면 일은 끝나리라(시간 초과로 인해→작업 시간 초과 단어,걸음수로 인해→per-run 말씀)。
	maxDur, clamped := clampMaxDuration(tc.DeadlineUnix, 0)
	settle := wrapupSettlement("planner", nil)
	if tc.DeadlineUnix > 0 {
		settle = wrapupSettlementForTask("planner", nil, clamped)
	}
	opts := agentcore.Options{
		Provider:        p.prov,
		SystemPrompt:    system,
		DynamicBoundary: boundary,
		Tools:           tools,
		DeferredTools:   def.Deferred,
		UnlockSet:       def.Unlock,
		PermissionMode:  permission.ModeBypass,
		EnableWebFetch:  true, // 가서 흔적 남기는 요원을 기록하라；에이전트 로딩 중 CA 확인 MITM 재계약했습니다 HTTPS 인증서
		WebFetchProxy:   p.proxyAddr,
		WebFetchCACert:  p.proxyCACert,
		// 인터넷 검색(선택사항)。ddgs 필요없어요 key；brave-free 필수 BraveKey；tavily 필수 TavilyKey。
		// WebSearchProxy 은 독립 수출 대리인입니다.(http/https/socks5)，트래픽이 기록되어 있음 MITM 상담원은 관련이 없습니다.；비어 있으면 직접 연결。
		EnableWebSearch:       p.webSearch.Enabled,
		WebSearchBackend:      p.webSearch.Backend,
		BraveSearchAPIKey:     p.webSearch.BraveKey,
		TavilySearchAPIKey:    p.webSearch.TavilyKey,
		DeepSeekSearchBaseURL: p.webSearch.DeepSeekBaseURL,
		DeepSeekSearchAPIKey:  p.webSearch.DeepSeekAPIKey,
		DeepSeekSearchModel:   p.webSearch.DeepSeekModel,
		WebSearchProxy:        p.webSearch.Proxy,
		BashEnv:               proxyEnv(p.proxyAddr, p.proxyCACert), // Bash 하위 명령은 기본적으로 프록시를 사용합니다.+신뢰 CA
		WorkingDir:            taskDir,                              // 이 작업의 작업 디렉터리 <workDir>/tasks/<taskID>
		ToolOutputDir:         cmdOutDir(taskDir),
		MaxTurns:              p.maxTurns, // 0 = unlimited (configurable in agent management)
		MaxDuration:           maxDur,     // 0=제한 없음;예 deadline 시간=거리 deadline 남음
		Compaction:            compactionConfig(p.compactionWindow()),
		// 교차 Wakeup 공유를 위한 할 일 계획：라운드 사이에 직렬 체인을 보존하도록 합니다.（session 은 새로운，store 아니요）。
		Todos: p.todoFor(ts.ID()),
		// 히트【이번 라운드】단계 예산→ SDK 마무리 주행:이번 라운드에서 명확하게 생각한 결론을 구현합니다.(보내야 할 사람 add_intent、
		// 증명할 수 있다 prove_goal、직렬 체인 노트 TodoWrite),계획을 멈추는 대신——planner 이후에도 반복적으로 깨어날 것입니다.。
		// clamped(임무 deadline 클램핑)일 때 대신 사용하세요. PromptByReason(또 만나요 wrapupSettlementForTask)。
		Settlement:   settle,
		NonStreaming: p.nonStreaming(), // 그게 profile 비스트리밍 모드를 선택하세요. Provider.Complete
		MaxTokens:    p.maxTokens(),    // 0 = 상한선 없음,서버의 기본값에 따라 결정됩니다.
	}
	if p.tx != nil { // persist raw LLM conversation; one accumulating file per task's planner
		opts.Transcript = p.tx
		opts.SessionID = fmt.Sprintf("exp%d-planner", ts.ID())
	}
	// 실험적 기능:개봉 후, noa 컨텍스트 압축 인수(아카이브가 집중되어 있습니다. <workDir>/noa/<SessionID> 다음,지속됨)。
	noaSession := fmt.Sprintf("exp%d-planner", ts.ID())
	enableNoa(&opts, p.noaEnabledFn, p.workDir, noaSession, noaWarn(noaSession))
	// 상황（의향이 막 완성됐어요 + 완성사진）이제 이번 라운드에 입장하세요 user 입력（아래 참조 input）。user 도 있어요
	// 명령 + 교차 깨우기 보류 중（todo 은 모델 자체 기획 노트입니다.，재생 가능，넣어 user 그렇죠）。
	// 열기 버튼「이번 라운드에서 구체적인 변화가 있나요?」두가지 종류가 있어요：변경사항이 있습니다 → 포인트다운【실제 변화】차단；변화 없음
	// (하트비트 정기점검 / hint / 복구 등) → 거짓말하지마"사진이 바뀌었어요",대신 실행 의도를 검토하라는 메시지가 표시됩니다.。
	lead := "구체적인 변경사항이 있었습니다.（아래 참조【이번에 촉발된 실제 변화】），이에 따라 다음 단계를 계획하세요.："
	if len(triggers) == 0 {
		lead = "이번 라운드는**정기점검（심장이 빨리 뛴다）/구체적인 변화 신호 없음**의 각성——사진에는 새로운 변화가 없을 수도 있습니다。그런데 달리기 의도를 검토해 보세요：오랫동안 진전이 없거나 편차가 있을 때 사용 steer_work 정정、방향이 완전 틀리네요. kill_work 정지 손실；대상을 다시 정하라、방향을 보완할지 결정："
		// 심장소리/깨어나도 변함없이,전체 사진에 더 이상 없는 경우 open 또는 running 의도 → 탐사가 중단되었습니다(아니요 worker 실행 중、
		// 대기열 방향도 없습니다.)。명확히 알고 있음 planner 그리고 이번 라운드에 새로운 방향을 만들도록 강요합니다.,한 라운드만 쉬겠다는 의지만 재검토하지 마세요。
		if active, err := ts.HasActiveIntent(); err == nil && !active {
			lead = "이번 라운드는**정기점검（심장이 빨리 뛴다）**의 각성,그리고 현재**더 이상 없어요 open 또는 running 의 의도**——아니요 worker 실행 중、대기열에도 방향이 없습니다.,탐사가 중단되었습니다。당신**필수**이번 라운드 목표를 향해 나아가기 위해 하나 이상의 아이템을 생산하세요、그리고 사진과 같은 의도입니다**중복 없음**님의 새로운 의도(출력이 허용되지 않습니다. 0 의도);먼저 다음과 같은 상황을 바탕으로 목표가 달성되었는지 판단합니다.,안되면 즉시 방향을 잡아라："
		}
	}
	input := lead + situational + "\n\n위의 상황에 따라，목표를 정하라。대상이 되었습니다.【정말 성취했어요】（목표한 결과를 달성했습니다/대상 취약점 확인됨）일 때 사용됩니다. prove_goal 하나씩 표시해 보세요。**확실한 결론：목표가 달성되지 않은 이상、현재는 없습니다 open 또는 running 의도（frontier_open=0 그리고 running_intents 이 비어 있습니다.），이번 라운드는 목표를 향해 나아가기 위한 의지를 하나 이상 만들어내야 합니다.——지금은 달리는 사람이 없습니다 work 기다리면 된다、대기열도 없습니다.，출력 0 의도=임무 중단。이미 있는 경우에만 open/running 의도는 전진하고 있다、또는 목표 달성 시，이번 라운드에서만 새로운 의도가 생성될 수 없습니다.。**" +
		renderPlannerTodos(opts.Todos.List())
	// MaxDuration 이제 벽시계가 만료되면 실행 중인 도구가 중단되고 종료가 그 자리에서 실행됩니다.(살아있습니다 ctx 에),더 이상 한쪽 바퀴에 갇히지 않습니다.
	// 엔딩을 건너뛰세요,외부 하드웨어가 필요하지 않습니다. ctx 사실대로 말해주세요。ctx 운반만 가능 pause / kill / shutdown。
	_, _, err = captureRun(ctx, opts, input,
		func(r db.Activity) {
			if emit != nil {
				r.Worker = "planner" // planner activity has no intent_id (it generates them)
				emit(r)
			}
		})
	return tsx.GoalMet, tsx.Reason, err
}
