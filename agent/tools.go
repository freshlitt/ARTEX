package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/db"
	acperm "github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

// compactIntents distills intents to {id, summary, state, asset_ids, parents,
// yields} so the planner sees both the direction and its LINEAGE — parents (the
// upstream nodes it derived from: facts/intents/findings) and yields (the facts/
// findings it produced) — without pulling full payloads. parentsOf/yieldsOf are
// built from the exploration edges in graph_overview.
func compactIntents(ns []*db.Node, parentsOf, yieldsOf map[int64][]int64) []map[string]any {
	out := make([]map[string]any, 0, len(ns))
	for _, n := range ns {
		var p map[string]any
		_ = json.Unmarshal(n.Payload, &p)
		m := map[string]any{"id": n.ID, "summary": p["summary"], "state": n.State}
		if n.Inherited {
			m["source_task_id"] = n.SourceTaskID
			m["inherited"] = true
		}
		// asset_ids is the structured "which assets this direction covers" signal for
		// dedup; fall back to legacy payload keys (target_ids plural, then target_id
		// single) so intents stored before the rename still surface their anchors.
		if tg, ok := p["asset_ids"]; ok && tg != nil {
			m["asset_ids"] = tg
		} else if tg, ok := p["target_ids"]; ok && tg != nil {
			m["asset_ids"] = tg
		} else if tg, ok := p["target_id"]; ok && tg != nil && tg != "" {
			m["asset_ids"] = []any{tg}
		}
		if ps := parentsOf[n.ID]; len(ps) > 0 {
			m["parents"] = ps // 업스트림：이 의도는 어느 노드에서 파생됩니까?（여러 사실이 함께 작용하여 의도를 만들 수 있습니다.）
		}
		if ys := yieldsOf[n.ID]; len(ys) > 0 {
			m["yields"] = ys // 다운스트림：이러한 의도로 인해 어떤 사실이 발생하게 되었나요?/찾음
		}
		out = append(out, m)
	}
	return out
}

// ToolSet exposes the PG-backed dual graph (asset + exploration) to an LLM agent.
// One ToolSet is created per planner/worker run; per-run signals live here.
type ToolSet struct {
	findingRecorder FindingRecorder
	as              *db.AssetStore   // asset store (optional; nil = asset tools not available)
	cs              *db.CompanyStore // company store (optional)
	ts              *db.ExplorationStore
	worker          string
	taskID          int64 // PG tasks.id; 0 when unknown (tests / orchestrator cross-task reads)
	// coverageDisabled mirrors tasks.coverage_enabled=false. Stored inverted so the
	// zero value (all existing ToolSet constructions) means ENABLED — matching the
	// DB default (true). When true: graphOverviewData drops the coverage block, the
	// auto-scope hook (insertAssets) is skipped, and add_task_scope/list_untested_assets
	// are filtered out of the agent's tool list. The scope field stays regardless.
	coverageDisabled bool
	// ownerNode is the exploration node that writes attach to: assets this run
	// touches get anchored to it as lineage/provenance (NOT visibility — the asset
	// graph is global and shared). Worker = its claimed intent; planner = begin root.
	ownerNode int64
	GoalMet   bool
	Reason    string
	writes    WriteCounts
	// killWork, if set, terminates a running work by intent id (engine callback,
	// wired by the planner). nil = the kill_work tool reports unavailable.
	killWork func(intentID int64) error
	// steerWork, if set, queues a mid-run course-correction for the work running an
	// intent id (engine callback, wired by the planner): the worker injects it before
	// its next tool call and re-plans, without being killed. nil = tool unavailable.
	steerWork func(intentID int64, msg string) error
	// enrich, if set, receives async auto-completion triggers (DNS resolve for a
	// domain, HTTP probe for a site). nil = no engine enrichment.
	enrich EnrichTrigger
	// notify, if set, wakes the task's planner after a graph change that should be
	// re-planned promptly (currently: a new hint). nil = no wake (the hint is still
	// stored and read on the next round triggered by other events). debounced.
	notify func()
	// notifyFinding, if set, wakes the task's planner when this run reports a finding,
	// carrying (intentID, summary) so the round can spell out which intent found what.
	// Wired for workers; nil elsewhere → falls back to notify (bare wake).
	notifyFinding func(intentID int64, summary string)
	// resumeTask, if set, revives the task after a graph change that should make a
	// stopped task run again (currently: set_goals adds a goal). It flips a terminal/
	// paused task back to running and (re)starts the engine loops — a plain notify()
	// can't, because the planner's terminal gate swallows wakes. Wired ONLY for the
	// main agent (human steering); nil for the goals decomposer and workers.
	resumeTask func()
	// notifyGoal, if set, wakes the planner AND records ONE "명 추가됨 N 목표：…" trigger
	// for a whole set_goals call (batch-aware — one call, one trigger, not one per goal)
	// so the next round spells out the added goals (instead of the planner having to
	// spot new open goals in the overview). Wired ONLY for the main agent; nil for the
	// goals decomposer (round-0 has no running planner to inform) and workers → those
	// fall back to the bare notify.
	notifyGoal func(texts []string)
	// notifyHint, if set, wakes the planner AND records ONE "명 추가됨 N 전략 팁：…"
	// trigger for a whole add_hint call (batch-aware — one call, one trigger) so the next
	// round is told the round was fired by a new hint and spells the hint out, instead of
	// the planner having to spot it folded into the graph overview. Wired for the main
	// agent + cross-task orchestration; nil elsewhere → falls back to the bare notify.
	notifyHint func(texts []string)
}

// SetNotifyGoal wires the goal-add trigger callback (see ToolSet.notifyGoal). Set only
// by the main-agent chat, so runtime-added goals are announced to the planner by name.
func (t *ToolSet) SetNotifyGoal(fn func([]string)) { t.notifyGoal = fn }

// SetNotifyHint wires the hint-add trigger callback (see ToolSet.notifyHint). Set by
// the main-agent chat and cross-task orchestration, so a runtime-added hint fires a
// planner round announced by name instead of a bare wake.
func (t *ToolSet) SetNotifyHint(fn func([]string)) { t.notifyHint = fn }

// SetResumeTask wires the task-revive callback (see ToolSet.resumeTask). Set only by
// the main-agent chat, so runtime-added goals can pull a finished task back to running.
func (t *ToolSet) SetResumeTask(fn func()) { t.resumeTask = fn }

// SetNotify wires the planner-wake callback (see ToolSet.notify). Set by callers
// that hold the task handle (main-agent chat, cross-task orchestration).
func (t *ToolSet) SetNotify(fn func()) { t.notify = fn }

// SetNotifyFinding wires the finding-wake callback (see ToolSet.notifyFinding).
func (t *ToolSet) SetNotifyFinding(fn func(int64, string)) { t.notifyFinding = fn }

// EnrichTrigger is the enrichment engine seen from the tool layer (see package
// enrich). Kept as an interface here to avoid coupling agent → enrich.
type EnrichTrigger interface {
	ResolveDomain(id int64, host string)
	ProbeSite(id int64, url string)
}

// WriteCounts breaks down what a worker persisted this run, by node kind, so the
// engine can log an accurate "wrote back" summary instead of lumping assets and
// findings under "facts" (record_fact → Facts, insert_assets → Assets,
// report_finding → Findings; each element of a batch counts once).
type WriteCounts struct {
	Facts    int
	Assets   int
	Findings int
}

// Total is every node persisted this run, regardless of kind — the
// "explored but persisted nothing" signal (Total == 0).
func (w WriteCounts) Total() int { return w.Facts + w.Assets + w.Findings }

// String renders the per-kind breakdown for logs, e.g. "사실1 자산25 취약점0".
func (w WriteCounts) String() string {
	return fmt.Sprintf("사실%d 자산%d 취약점%d", w.Facts, w.Assets, w.Findings)
}

// Writes reports what this run wrote back, split by node kind (so the engine can
// tell "explored but persisted nothing" apart from a completed intent, and log an
// honest breakdown instead of calling assets/findings "facts").
func (t *ToolSet) Writes() WriteCounts { return t.writes }

func NewToolSet(ts *db.ExplorationStore, worker string) *ToolSet {
	return &ToolSet{ts: ts, worker: worker}
}

// SetTaskID sets the PG task id on this ToolSet so that report_finding can
// dual-write to the standalone findings table (which survives task deletion).
func (t *ToolSet) SetTaskID(id int64) { t.taskID = id }

// SetCoverageEnabled records whether this task has the asset-coverage feature on
// (default enabled). Passing false makes graphOverviewData omit the coverage block
// and DropCoverageTools filter the two coverage-only tools out of the agent's tool
// list. It does NOT stop scope accumulation: insertAssets' auto-scope hook runs
// either way, because task_scope is the task's range boundary (the filter basis for
// asset queries), not merely a coverage denominator.
func (t *ToolSet) SetCoverageEnabled(enabled bool) { t.coverageDisabled = !enabled }

// CoverageDisabled reports whether the coverage feature is off for this task.
func (t *ToolSet) CoverageDisabled() bool { return t.coverageDisabled }

// coverageOnlyTools are the LLM tools that only make sense when asset coverage is
// on. When the feature is off they are filtered out of the agent's tool list so
// they neither pollute the prompt nor let the model build a disabled denominator.
// add_task_scope is deliberately NOT here: task_scope is the task's range boundary
// (the filter basis for asset queries), not merely a coverage denominator, so the
// agents that own범위 정의 keep it either way — in lockstep with insertAssets'
// auto-scope hook, which also runs regardless of the switch.
var coverageOnlyTools = map[string]bool{"list_untested_assets": true}

// DropCoverageTools returns tools with the coverage-only ones removed when this
// task has the feature disabled; otherwise it returns tools unchanged.
func (t *ToolSet) DropCoverageTools(tools []actool.CoreTool) []actool.CoreTool {
	if !t.coverageDisabled {
		return tools
	}
	out := tools[:0:0]
	for _, tool := range tools {
		if coverageOnlyTools[tool.Name()] {
			continue
		}
		out = append(out, tool)
	}
	return out
}

// Cross-task reuse: exported accessors returning the per-task tool logic bound to
// THIS ToolSet's store. Host-side orchestration tools build a ToolSet for an
// arbitrary task, then Call these — so cross-task reads/hint reuse the exact
// same logic as the in-task tools. (readTool ignores ToolContext, so Call(…,nil)
// is safe; add_hint is a writeTool but also doesn't deref the context here.)
func (t *ToolSet) GraphOverviewTool() actool.CoreTool      { return t.graphOverview() }
func (t *ToolSet) ListFindingsTool() actool.CoreTool       { return t.listFindings() }
func (t *ToolSet) GetWorkerTraceTool() actool.CoreTool     { return t.getWorkerTrace() }
func (t *ToolSet) ListWorkerTracesTool() actool.CoreTool   { return t.listWorkerTraces() }
func (t *ToolSet) SearchWorkerTracesTool() actool.CoreTool { return t.searchAllWorkerTraces() }
func (t *ToolSet) NodeDetailTool() actool.CoreTool         { return t.nodeDetail() }
func (t *ToolSet) AddHintTool() actool.CoreTool            { return t.addHint() }

// SetEnrich wires the async enrichment engine (DNS/HTTP auto-completion).
func (t *ToolSet) SetEnrich(e EnrichTrigger) { t.enrich = e }

// SetOwnerNode sets the exploration node that writes anchor to (worker: its
// intent node; planner/main: the begin root). Assets created/referenced while
// ownerNode is set are anchored to it as lineage (not visibility).
func (t *ToolSet) SetOwnerNode(id int64) { t.ownerNode = id }

// anchorOwner records a lineage edge from this run's owner node to an asset
// (no-op if unset). Provenance only — the asset graph is global and shared, so
// this no longer affects which assets a task can read.
func (t *ToolSet) anchorOwner(assetID int64) {
	if t.ts != nil && t.ownerNode > 0 && assetID > 0 {
		_ = t.ts.Anchor(t.ownerNode, assetID)
	}
}

// pid parses an id that may arrive as a JSON number or string ("" / 0 → 0).
func pid(raw json.RawMessage) int64 {
	if len(raw) == 0 {
		return 0
	}
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		v, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		return v
	}
	return 0
}

// pidList parses a list of ids (number|string), dropping zeros/invalids.
func pidList(raw []json.RawMessage) []int64 {
	var out []int64
	for _, r := range raw {
		if v := pid(r); v > 0 {
			out = append(out, v)
		}
	}
	return out
}

func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}
func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func intp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}
func idp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func readTool(name, desc string, schema map[string]any, run func(context.Context, json.RawMessage) (actool.Result, error)) actool.CoreTool {
	return actool.Build(actool.Spec{
		Name: name, Description: desc, Schema: schema,
		ReadOnly:    func(json.RawMessage) bool { return true },
		Concurrent:  func(json.RawMessage) bool { return true },
		Permissions: func(context.Context, json.RawMessage, acperm.Context) acperm.Decision { return acperm.Allowed() },
		Run: func(ctx context.Context, in json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
			return run(ctx, in)
		},
	})
}

func writeTool(name, desc string, schema map[string]any, run func(context.Context, json.RawMessage) (actool.Result, error)) actool.CoreTool {
	return actool.Build(actool.Spec{
		Name: name, Description: desc, Schema: schema,
		Permissions: func(context.Context, json.RawMessage, acperm.Context) acperm.Decision { return acperm.Allowed() },
		Run: func(ctx context.Context, in json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
			return run(ctx, in)
		},
	})
}

// readExpTool / writeExpTool build a domain tool whose handler dereferences the
// task-bound ExplorationStore. Two ToolSets carry a nil store: the catalog's
// seed-only shell (never called) and the server-level one behind buildDomainReg,
// which the tools table can bind to ANY agent — including ones that never run
// inside a task (auto/pentest/reporter/맞춤형 agent/부가 질문). Refusing there
// keeps a mis-bound tool a bad tool call; without the guard it was a nil deref,
// and tool handlers run on the harness's own goroutine, so the panic is out of
// reach of every recover() in the server and kills the whole process.
func (t *ToolSet) readExpTool(name, desc string, schema map[string]any, run func(context.Context, json.RawMessage) (actool.Result, error)) actool.CoreTool {
	return readTool(name, desc, schema, t.needExploration(name, run))
}

func (t *ToolSet) writeExpTool(name, desc string, schema map[string]any, run func(context.Context, json.RawMessage) (actool.Result, error)) actool.CoreTool {
	return writeTool(name, desc, schema, t.needExploration(name, run))
}

// needExploration wraps a handler so it only runs with an exploration store.
// Tools that degrade more usefully than "unavailable" (report_finding points at
// add_task_hint, set_goals/set_constraints at the task itself) keep their own
// bespoke guard instead.
func (t *ToolSet) needExploration(name string, run func(context.Context, json.RawMessage) (actool.Result, error)) func(context.Context, json.RawMessage) (actool.Result, error) {
	return func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
		if t.ts == nil {
			return actool.Errorf(name + " 작업 컨텍스트가 필요합니다.（탐험지도）：현재 agent 작업 내에서 실행되지 않음，임무 탐사 지도를 획득할 수 없습니다.，이 도구를 사용할 수 없습니다.。작업 내에서 활용해주세요，또는 task_id 의 교차 작업 읽기 도구（get_task_node_detail / list_task_findings / get_task_graph 등）。"), nil
		}
		return run(ctx, in)
	}
}

func jsonResult(v any) (actool.Result, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}
	return actool.Text(string(b)), nil
}

// --- read tools (planner + worker) ---

func (t *ToolSet) graphOverview() actool.CoreTool {
	return t.readExpTool("graph_overview",
		"(링크 다이어그램 살펴보기)탐사상황 증류 요약：자산수、인터페이스가 없는 사이트、frontier、찾음、hints(인간/스승님 agent 의 전략적 팁，인텐트 생성 시 반드시 포함되어야 함)。계획할 때 먼저 조정하세요.。",
		obj(map[string]any{}),
		func(context.Context, json.RawMessage) (actool.Result, error) {
			return jsonResult(t.graphOverviewData())
		})
}

// graphOverviewData computes the distilled situational snapshot shared by the
// graph_overview tool and the planner's wake-up prompt (which pre-injects it so
// the model needn't spend a turn calling the tool — every plan round starts with
// an empty context and always needs this first).
func (t *ToolSet) graphOverviewData() map[string]any {
	out := map[string]any{}
	// goals summary folded in so the planner needn't call list_goals each round.
	goals, _ := t.ts.ListByKind(db.KindGoal, 100)
	gsum := make([]map[string]any, 0, len(goals))
	for _, g := range goals {
		var p map[string]any
		_ = json.Unmarshal(g.Payload, &p)
		gsum = append(gsum, map[string]any{"id": g.ID, "state": g.State, "text": p["text"]})
	}
	out["goals"] = gsum
	// hints: 인간/스승님 agent 합격 add_hint 위 사진 걸기 전략적인 팁；folded in so the
	// planner reads them every round when generating intents (그렇지 않으면 쓰기만 하고 읽지는 않습니다.).
	hints, _ := t.ts.ListByKind(db.KindHint, 50)
	hsum := make([]map[string]any, 0, len(hints))
	for _, h := range hints {
		var p map[string]any
		_ = json.Unmarshal(h.Payload, &p)
		hint := map[string]any{"id": h.ID, "state": h.State, "text": p["text"]}
		if findingTrafficBindingEnabled() && p["traffic_refs"] != nil {
			hint["traffic_refs"] = p["traffic_refs"]
		}
		hsum = append(hsum, hint)
	}
	out["hints"] = hsum
	// lineage from the exploration edges: an intent's parents (what it
	// derived_from — possibly several facts combined) and its yields (the
	// facts/findings it produced). factFrom maps a fact → the intent that
	// produced it. This is the relationship layer the flat lists lacked.
	edges, _ := t.ts.Edges(5000)
	parentsOf := map[int64][]int64{}
	yieldsOf := map[int64][]int64{}
	factFrom := map[int64]int64{}
	for _, e := range edges {
		switch e.Rel {
		case db.RelDerivedFrom, db.RelSpawns: // upstream: derived_from (fact/finding/intent→intent) or spawns (origin fact→goal, legacy begin→intent)
			parentsOf[e.To] = append(parentsOf[e.To], e.From)
		case db.RelYields: // intent --yields--> fact/finding
			yieldsOf[e.From] = append(yieldsOf[e.From], e.To)
			factFrom[e.To] = e.From
		}
	}
	// cold-digest §6: members folded into an active digest are shown via cold_digests
	// (below), not the flat recent_* lists. `covered` maps member id → its digest id.
	// §6 render-time revival check: a covered member that has become hot again (a new
	// intent derived from it) must reappear this round — so `hidden` folds a member out
	// only when it is covered AND still cold.
	covered, _ := t.ts.CoveredMembers()
	// Render-time hot set (ancestor of a live intent / fact under a live intent).
	// §6 revival check: a covered member that revived (now hot) must NOT stay folded
	// — hidden() only folds a member out when it is covered AND still cold. Computed
	// every round (cheap for real graph sizes); nil map degrades safely.
	var hotAtRender map[int64]bool
	if cg, _, err := loadColdGraph(t.ts); err == nil {
		hotAtRender = cg.hotSet()
	}
	hidden := func(id int64) bool { _, c := covered[id]; return c && !hotAtRender[id] }
	const openIntentsCap = 30
	fr, _ := t.ts.Frontier(openIntentsCap) // priority DESC, id ASC —— 우선순위가 가장 높은 것 N 글；실제 합계 보기 frontier_open
	out["open_intents"] = compactIntents(fr, parentsOf, yieldsOf)
	all, _ := t.ts.ListByKind(db.KindIntent, 300)
	var running, recentDone []*db.Node
	for _, n := range all {
		switch n.State {
		case "running":
			running = append(running, n)
		case "done", "blocked", "exhausted":
			if hidden(n.ID) {
				continue // in a cold_digest and still cold — shown via cold_digests (§6.2)
			}
			recentDone = append(recentDone, n) // 최신순（all 언론 id 내림차순）；접힌 것은 없어졌습니다.，출력을 최신으로 잘라냅니다. N
		}
	}
	out["running_intents"] = compactIntents(running, parentsOf, yieldsOf)
	// done_intents_total：의도가 종료되었습니다.（done/blocked/exhausted）합계，그리고 recent_done_intents
	// 병렬 명명——후자는 최신 창 잘린 보기입니다.。나란히 있는 두 개의 키는 자체적으로 설명됩니다.："내가 보는 것은 N/합계"，
	// 하자 planner 중복된 항목을 제거할 때"표시되지 않음"그대로 받아들이세요"전송되지 않음"，프롬프트 단어에서는 따로 설명할 필요가 없습니다.。
	if dt, err := t.ts.CountFinishedIntents(); err == nil {
		out["done_intents_total"] = dt
	}
	// frontier_open：열린 의도의 실제 총 개수（open_intents 우선순위가 가장 높은 것뿐이에요 N 스트립 잘린 보기）。
	if fo, err := t.ts.CountOpenIntents(); err == nil {
		out["frontier_open"] = fo
	} else {
		out["frontier_open"] = len(fr)
	}
	// findings (confirmed vulns) and facts (worker exploration results) are
	// now distinct node kinds. recent_facts surfaces fact summaries (esp.
	// negative results) so the planner sees them in one call; full content
	// via node_detail(id).
	vulnNodes, _ := t.ts.ListByKind(db.KindFinding, 1000)
	factNodes, _ := t.ts.ListByKind(db.KindFact, 1000) // newest first
	out["findings_total"] = len(vulnNodes)             // 확인된 총 취약점 개수（타겟 결정을 위해 봐주세요）；자세히 알아보겠습니다. finding_list（최신창）
	out["facts"] = len(factNodes)                      // 사실을 살펴보세요/결론 수（부정적인 결론을 담고 있습니다.）
	// findings 미션에서 가장 가치있는 제품입니다 → 최신 창으로 개요（≤10 글，vulnNodes 눌림 id 내림차순은 최신순을 의미합니다.），
	// 하자 planner 라운드별로 대상을 판단할 때 가장 최근에 확인된 취약점을 한눈에 확인하세요；전액/이전에 사용됨 list_findings 받아。
	// 각 항목만 남겨주세요 {id, summary, from_intent?}：from_intent 은 이 취약점을 생성하려는 의도입니다.。
	// evidence/assets/vulnclass/severity/state 등은 아직 사용 가능합니다. list_findings / node_detail(id) 받아。
	const findingListCap = 10
	findingList := make([]map[string]any, 0, findingListCap)
	for _, n := range vulnNodes {
		if len(findingList) >= findingListCap {
			break
		}
		var fp map[string]any
		_ = json.Unmarshal(n.Payload, &fp)
		m := map[string]any{"id": n.ID, "summary": fp["summary"]}
		if from := factFrom[n.ID]; from > 0 {
			m["from_intent"] = from // 이 취약점은 어떤 의도로 발생한 것입니까?
		}
		findingList = append(findingList, m)
	}
	out["finding_list"] = findingList
	// recent_facts：접지 않는 사실의 최신 창（≤N，factNodes 언론 id 내림차순은 최신순을 의미합니다.）。접어서
	// digest 아직도 추워요（hidden）가자 cold_digests，여기서는 반복하지 않음。각 {id, summary, from_intent?,
	// confidence?}；evidence 및 기타 세부 정보가 사용됩니다. node_detail(id)。이전에 사용됨 list_facts 번역하다。
	const recentFactsCap = 20
	recentFacts := make([]map[string]any, 0, recentFactsCap)
	for _, n := range factNodes {
		if len(recentFacts) >= recentFactsCap {
			break
		}
		if hidden(n.ID) {
			continue // 접어서 digest 아직도 추워요 —— 또 만나요 cold_digests
		}
		m := compactNode(n)
		if from := factFrom[n.ID]; from > 0 {
			m["from_intent"] = from // 이 사실로 인해 어떤 의도가 생겼습니까?
		}
		// confidence 개요 가져오기：어떤 결론이 나올지 기획자들이 한눈에 알 수 있게 해주세요 inferred（특히 부정적인 결론
		// 당연하게 여기지 마세요）；evidence 더 길게，맡겨주세요 node_detail(id)。
		var fp map[string]any
		if json.Unmarshal(n.Payload, &fp) == nil {
			if c, ok := fp["confidence"].(string); ok && c != "" {
				m["confidence"] = c
			}
		}
		recentFacts = append(recentFacts, m)
	}
	out["recent_facts"] = recentFacts
	// recent_done_intents：접히지 않은 종료 의도의 최신 창（≤N，recentDone 눌림 id 내림차순）。
	// 미리보기 done_intents_total 수 + node_detail(id)。
	const recentDoneCap = 12
	if len(recentDone) > recentDoneCap {
		recentDone = recentDone[:recentDoneCap]
	}
	out["recent_done_intents"] = compactIntents(recentDone, parentsOf, yieldsOf)
	// cold-digest §6.1: 차가운 부분을 접어주세요 digest body，최근 회원 시간 기준 내림차순으로 정렬 N；가로채는 건 더 오래됐어요 digest
	// 알몸만 id（아직 사용 가능합니다 expand_digest 펼치기），Cold Zone의 유일한 출구가 무한히 늘어나는 것을 방지。
	const coldDigestsCap = 15
	if cds, more := coldDigestsRecent(t.ts, coldDigestsCap); len(cds) > 0 {
		out["cold_digests"] = cds // [{id, body, member_count}] —— 직접 읽어보세요 body (§6.1)
		if len(more) > 0 {
			out["cold_digests_more"] = more // 잘린 것이 더 오래되었습니다. digest 님 id；사용 expand_digest(id) 펼치기
		}
	}
	// the original task (root) so the planner always has it, not just the
	// decomposed goals.
	if description, goal, err := t.ts.Root(); err == nil {
		out["task"] = map[string]any{"description": description, "goal": goal}
	}
	// Direct source tasks are a live, read-only blackboard view. Keep their
	// summaries in a separate field so their intents never enter this task's
	// frontier or get mistaken for locally claimable work.
	out["related_tasks"] = t.relatedTaskOverviews()
	// coverage：대략적인 자산 테스트 적용 범위 참조——범위(task_scope)내의 자산에서，은(는) fact 감동받았어요
	// 비율 + by_type(유형별 합계/테스트됨)。측정되지 않는 특정 자산에 따라 다릅니다. agent 필요에 따라 조정하세요. list_untested_assets 판단은 알아서 하세요。작업 컨텍스트에만。
	// 자산보상 기능이 꺼진 경우(coverageDisabled)：예약만 가능 host_count(대상 호스트 수에 대한 정보를 감지합니다.)，
	// 삭제 denominator/tested/pct/by_type/note 및 기타 적용 범위 측정항목，컨텍스트를 오염시키지 마세요.、유도도 아니고
	// 숨김 add_task_scope/list_untested_assets。
	if t.as != nil && t.ts != nil && t.taskID > 0 {
		{
			m := map[string]any{}
			if !t.coverageDisabled {
				if cov, err := t.as.TaskCoverageWithSources(t.taskID); err == nil {
					m["denominator"] = cov.Denominator
					m["tested"] = cov.Tested
					m["by_type"] = cov.ByType
					m["note"] = "coverage자산 테스트 범위（인터페이스 및 기타 관련 자산 포함），대략적인 추정、참고용：현재 작업과 직접 관련된 작업이 포함되어 있습니다. scope、팩트앵커；관련 scope 읽기 전용。컨테이너 자산/열거 수가 많으면 값이 낮아집니다.，이를 토대로 테스트가 완료되었다고 가정하지 마시기 바랍니다.；가능 add_task_scope 이 작업의 범위를 보완합니다.、list_untested_assets 테스트되지 않은 자산을 살펴보세요【보통 전화 안함list_untested_assets，그냥 업무대로 진행하세요】；"
					if cov.Denominator == 0 {
						m["pct"] = nil
						m["status"] = "범위가 고정되지 않았습니다."
					} else {
						m["pct"] = cov.Pct
					}
				}
			}
			if hosts, err := t.as.HostsByTaskWithSources(t.taskID); err == nil {
				// 총 호스트 수만 제공，더 이상은 안돼 host 목록이 타일로 되어있습니다. graph_overview（대규모 임무에서는 그게 매 라운드죠
				// 많은 수의 문자열이 반복적으로 운반됩니다.，계획 결정에 대한 제한된 가치）；요청 시 특정 호스트 list_assets 확인。
				m["host_count"] = len(hosts)
			}
			if len(m) > 0 {
				out["coverage"] = m
			}
		}
	}
	return out
}

func inheritedMap(m map[string]any, sourceTaskID int64) map[string]any {
	m["source_task_id"] = sourceTaskID
	m["inherited"] = true
	return m
}

const (
	relatedOverviewTotalTextRunes      = 48_000
	relatedOverviewMaxTextPerSource    = 8_000
	relatedOverviewMaxGoalsPerSource   = 8
	relatedOverviewMaxHintsPerSource   = 6
	relatedOverviewMaxFactsPerSource   = 12
	relatedOverviewMaxFindingsPerTask  = 6
	relatedOverviewMaxIntentsPerTask   = 8
	relatedOverviewMaxScopePerSource   = 12
	relatedOverviewMaxDigestsPerSource = 6
)

// overviewTextBudget bounds inherited prompt text while preserving a fair slice
// for every direct source. Full evidence remains available through the on-demand
// read tools, so truncation here does not discard persisted blackboard data.
type overviewTextBudget struct {
	remaining int
	truncated bool
}

func relatedOverviewBudgetForSources(sourceCount int) int {
	if sourceCount <= 0 {
		return 0
	}
	if sourceCount > db.MaxTaskSourceCount {
		sourceCount = db.MaxTaskSourceCount
	}
	perSource := relatedOverviewTotalTextRunes / sourceCount
	if perSource > relatedOverviewMaxTextPerSource {
		perSource = relatedOverviewMaxTextPerSource
	}
	return perSource
}

func (b *overviewTextBudget) take(value any, fieldLimit int) string {
	var text string
	switch value := value.(type) {
	case string:
		text = strings.TrimSpace(value)
	case nil:
		return ""
	default:
		text = strings.TrimSpace(fmt.Sprint(value))
	}
	if text == "" {
		return ""
	}
	if b.remaining <= 0 || fieldLimit <= 0 {
		b.truncated = true
		return ""
	}
	runes := []rune(text)
	limit := fieldLimit
	if limit > b.remaining {
		limit = b.remaining
	}
	if len(runes) > limit {
		b.truncated = true
		if limit == 1 {
			text = "…"
		} else {
			text = string(runes[:limit-1]) + "…"
		}
		runes = []rune(text)
	}
	b.remaining -= len(runes)
	return text
}

func recentTerminalIntents(store *db.ExplorationStore, limit int) []*db.Node {
	if limit <= 0 {
		return []*db.Node{}
	}
	const batch = 300
	cursor := int64(0)
	out := make([]*db.Node, 0, limit)
	for len(out) < limit {
		page, more, err := store.ListByKindPage(db.KindIntent, cursor, batch)
		if err != nil || len(page) == 0 {
			break
		}
		for _, intent := range page {
			switch intent.State {
			case "done", "blocked", "exhausted", "stopped":
				out = append(out, intent)
			}
			if len(out) >= limit {
				break
			}
		}
		if !more {
			break
		}
		cursor = page[len(page)-1].ID
	}
	return out
}

// relatedTaskOverviews distills persistent blackboard state from direct source
// tasks. It intentionally reads each source's local store methods, never its own
// related sources, so inheritance is one level only.
func (t *ToolSet) relatedTaskOverviews() []map[string]any {
	sources, err := t.ts.DirectSourceStores()
	if err != nil {
		return []map[string]any{}
	}
	if len(sources) > db.MaxTaskSourceCount {
		sources = sources[:db.MaxTaskSourceCount]
	}
	perSourceTextBudget := relatedOverviewBudgetForSources(len(sources))
	out := make([]map[string]any, 0, len(sources))
	for _, source := range sources {
		ts := source.Store
		// §2 cross-task: render the source task's OWN folded view — fold out the
		// members it has already folded, and surface its cold_digests read-only.
		hidden := hiddenMembersFor(ts)
		budget := overviewTextBudget{remaining: perSourceTextBudget}
		item := map[string]any{
			"source_task_id": source.Task.TaskID,
			"inherited":      true,
			"task": map[string]any{
				"description": budget.take(source.Task.Description, 800),
				"goal":        budget.take(source.Task.Goal, 800),
				"status":      source.Task.Status,
			},
		}
		stats, statsErr := ts.Stats()

		edges, _ := ts.Edges(5000)
		parentsOf := map[int64][]int64{}
		yieldsOf := map[int64][]int64{}
		factFrom := map[int64]int64{}
		for _, edge := range edges {
			switch edge.Rel {
			case db.RelDerivedFrom, db.RelSpawns:
				parentsOf[edge.To] = append(parentsOf[edge.To], edge.From)
			case db.RelYields:
				yieldsOf[edge.From] = append(yieldsOf[edge.From], edge.To)
				factFrom[edge.To] = edge.From
			}
		}

		goals, _ := ts.ListByKind(db.KindGoal, relatedOverviewMaxGoalsPerSource)
		goalSummary := make([]map[string]any, 0, len(goals))
		for _, goal := range goals {
			var payload map[string]any
			_ = json.Unmarshal(goal.Payload, &payload)
			goalSummary = append(goalSummary, inheritedMap(map[string]any{
				"id": goal.ID, "state": goal.State, "text": budget.take(payload["text"], 400),
			}, source.Task.TaskID))
		}
		item["goals"] = goalSummary

		hints, _ := ts.ListByKind(db.KindHint, relatedOverviewMaxHintsPerSource)
		hintSummary := make([]map[string]any, 0, len(hints))
		for _, hint := range hints {
			var payload map[string]any
			_ = json.Unmarshal(hint.Payload, &payload)
			hintSummary = append(hintSummary, inheritedMap(map[string]any{
				"id": hint.ID, "state": hint.State, "text": budget.take(payload["text"], 400),
			}, source.Task.TaskID))
		}
		item["hints"] = hintSummary

		facts, _ := ts.ListByKind(db.KindFact, relatedOverviewMaxFactsPerSource)
		findings, _ := ts.ListByKind(db.KindFinding, relatedOverviewMaxFindingsPerTask)
		intentNodes, _ := ts.ListByKind(db.KindIntent, 300)
		terminalIntent := make(map[int64]bool, len(intentNodes))
		for _, intent := range intentNodes {
			terminalIntent[intent.ID] = inheritedIntentSummaryState(intent.State)
		}
		item["facts"] = len(facts)
		item["findings"] = len(findings)
		if statsErr == nil {
			item["facts"] = stats[db.KindFact]
			item["findings"] = stats[db.KindFinding]
			if stats[db.KindGoal] > len(goals) || stats[db.KindHint] > len(hints) ||
				stats[db.KindFact] > len(facts) || stats[db.KindFinding] > len(findings) {
				budget.truncated = true
			}
		}
		recentFindings := make([]map[string]any, 0, len(findings))
		for _, finding := range findings {
			entry := inheritedMap(compactFinding(finding), source.Task.TaskID)
			entry["summary"] = budget.take(entry["summary"], 400)
			recentFindings = append(recentFindings, entry)
		}
		item["recent_findings"] = recentFindings
		recentFacts := make([]map[string]any, 0, len(facts))
		for _, fact := range facts {
			if hidden(fact.ID) {
				continue // folded into this source's cold_digests — shown there (§2/§6.2)
			}
			m := inheritedMap(compactNode(fact), source.Task.TaskID)
			m["summary"] = budget.take(m["summary"], 400)
			if from := factFrom[fact.ID]; from > 0 && terminalIntent[from] {
				m["from_intent"] = from
			}
			var payload map[string]any
			if json.Unmarshal(fact.Payload, &payload) == nil {
				if confidence, ok := payload["confidence"].(string); ok && confidence != "" {
					m["confidence"] = confidence
				}
			}
			recentFacts = append(recentFacts, m)
		}
		item["recent_facts"] = recentFacts

		recentDoneRaw := recentTerminalIntents(ts, relatedOverviewMaxIntentsPerTask)
		recentDone := recentDoneRaw[:0] // in-place filter: drop this source's folded intents (§2)
		for _, intent := range recentDoneRaw {
			if hidden(intent.ID) {
				continue
			}
			recentDone = append(recentDone, intent)
		}
		for _, intent := range recentDone {
			intent.Inherited = true
			intent.SourceTaskID = source.Task.TaskID
		}
		intentResults := compactIntents(recentDone, parentsOf, yieldsOf)
		for i, intent := range recentDone {
			intentResults[i]["summary"] = budget.take(intentResults[i]["summary"], 400)
			acts, _, err := ts.ActivityPageForTerminalIntent(intent.ID, 0, 20)
			if err != nil {
				continue
			}
			var resultSummary, textFallback string
			for _, activity := range acts {
				switch activity.Kind {
				case "result":
					resultSummary = activity.Summary
				case "text":
					textFallback = activity.Summary
				}
			}
			if resultSummary == "" {
				resultSummary = textFallback
			}
			if resultSummary != "" {
				intentResults[i]["result_summary"] = budget.take(resultSummary, 800)
			}
		}
		item["recent_intent_results"] = intentResults
		// §2 cross-task: the source task's folded cold region, read-only, newest-member
		// first & capped like the current task's. Members (and overflow digests) are
		// resolvable via expand_digest(id)/node_detail(id), which search source tasks.
		if cds, more := coldDigestsRecent(ts, relatedOverviewMaxDigestsPerSource); len(cds) > 0 {
			for _, cd := range cds {
				cd["inherited"] = true
				cd["source_task_id"] = source.Task.TaskID
			}
			item["cold_digests"] = cds
			if len(more) > 0 {
				item["cold_digests_more"] = more // 잘린 것이 더 오래되었습니다. digest 님 id；expand_digest(id) 펼치기
			}
		}
		if statsErr == nil {
			item["node_stats"] = stats
		}

		if t.as != nil {
			if scopeRows, err := t.as.ListTaskScope(source.Task.TaskID); err == nil && len(scopeRows) > 0 {
				scopeCount := len(scopeRows)
				if len(scopeRows) > relatedOverviewMaxScopePerSource {
					scopeRows = scopeRows[:relatedOverviewMaxScopePerSource]
					budget.truncated = true
				}
				scope := make([]map[string]any, 0, len(scopeRows))
				for _, row := range scopeRows {
					entry := map[string]any{"kind": row.Kind, "source": budget.take(row.Source, 300)}
					switch {
					case row.Domain != "":
						entry["value"] = budget.take(row.Domain, 400)
					case row.Net != "":
						entry["value"] = budget.take(row.Net, 400)
					case row.Value != "":
						entry["value"] = budget.take(row.Value, 400)
					case row.CompanyID != nil:
						entry["company_id"] = *row.CompanyID
					}
					scope = append(scope, entry)
				}
				item["asset_scope"] = scope
				item["asset_scope_count"] = scopeCount
			}
			if coverage, err := t.as.TaskCoverage(source.Task.TaskID, source.Task.ExplorationID); err == nil {
				item["asset_coverage"] = map[string]any{
					"denominator": coverage.Denominator,
					"tested":      coverage.Tested,
					"pct":         coverage.Pct,
					"by_type":     coverage.ByType,
				}
			}
		}
		if budget.truncated {
			item["summary_truncated"] = true
		}
		out = append(out, item)
	}
	return out
}

func inheritedIntentSummaryState(state string) bool {
	switch state {
	case "done", "blocked", "exhausted", "stopped":
		return true
	default:
		return false
	}
}

// compactNode distills any exploration node to id + summary + state, dropping the
// big detail/evidence (fetch that on demand via node_detail).
func compactNode(n *db.Node) map[string]any {
	var p map[string]any
	_ = json.Unmarshal(n.Payload, &p)
	m := map[string]any{"id": n.ID, "state": n.State, "summary": p["summary"]}
	if n.Inherited {
		inheritedMap(m, n.SourceTaskID)
	}
	return m
}

// compactFinding is compactNode plus the vuln-specific vulnclass/severity.
func compactFinding(n *db.Node) map[string]any {
	var p map[string]any
	_ = json.Unmarshal(n.Payload, &p)
	m := map[string]any{"id": n.ID, "state": n.State, "summary": p["summary"]}
	if n.Inherited {
		inheritedMap(m, n.SourceTaskID)
	}
	if vc, ok := p["vulnclass"]; ok && vc != nil && vc != "" {
		m["vulnclass"] = vc
	}
	if sv, ok := p["severity"]; ok && sv != nil && sv != "" {
		m["severity"] = sv
	}
	return m
}

func (t *ToolSet) listFindings() actool.CoreTool {
	return t.readExpTool("list_findings", "이 작업과 직접 관련된 작업을 나열합니다.【취약점 확인】(컴팩트：id+task_id+intent_id+vulnclass+severity+요약+상태)。관련 태스크 항목 밴드 source_task_id/inherited=true 및 읽기 전용。여기에는 취약점만 포함되어 있습니다.；일반적인 사실 탐구용 list_facts，자세한 내용은 node_detail(id)。",
		obj(map[string]any{}),
		func(context.Context, json.RawMessage) (actool.Result, error) {
			f, _ := t.ts.ListByKindWithSources(db.KindFinding, 500)
			if err := t.ts.PopulateFindingTrafficIDs(f); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			intentOf, _ := t.ts.FindingIntentsWithSources() // finding id -> 프로듀싱했어요 intent id
			taskID := t.taskID
			if taskID <= 0 {
				taskID, _ = t.ts.TaskID()
			}
			out := make([]map[string]any, 0, len(f))
			for _, n := range f {
				m := compactFinding(n)
				if n.FindingID > 0 {
					m["finding_id"], m["finding_node_id"], m["traffic_count"] = n.FindingID, n.ID, n.TrafficCount
				}
				if n.Inherited {
					m["task_id"] = n.SourceTaskID
				} else {
					m["task_id"] = taskID
				}
				if iid, ok := intentOf[n.ID]; ok {
					m["intent_id"] = iid
				}
				out = append(out, m)
			}
			return jsonResult(out)
		})
}

// factsPageSize is the default page size for list_facts. Facts pile up on long
// tasks; returning all of them at once (the old behaviour) could blow up the
// context, so default to the newest page and let the agent page/filter for more.
const factsPageSize = 20

func (t *ToolSet) listFacts() actool.CoreTool {
	return t.readExpTool("list_facts", "이 작업과 직접 관련된 작업을 페이지에 나열합니다.【사실을 살펴보세요/결론】，최신순(컴팩트：id+요약+상태，초록이 너무 길면 잘립니다.，사용 전문 node_detail(id))。모든 매개변수는 선택사항입니다.：limit(기본값 20，상한 100)、before(커서，이전 페이지를 올려서 돌아왔습니다. next_before 이전 페이지 가져오기；생략/0=최신페이지)、q(추상 키워드로 필터링)。복귀 {facts, total, has_more, next_before}：total 은 필터링 후의 총 개수입니다.，has_more=true 일 때 사용됩니다. next_before 페이지를 계속 넘기세요。관련 태스크 항목 밴드 source_task_id/inherited=true 및 읽기 전용。허점을 봐라 list_findings。",
		obj(map[string]any{
			"limit":  intp("항목 개수를 반환합니다.，기본값 20，상한 100"),
			"before": intp("페이징 커서：반품만 가능 id 이 값보다 작은 오래된 사실；생략 또는 0 = 최신페이지"),
			"q":      str("사실 요약 키워드로 필터링（대소문자를 구분하지 않습니다.）；생략 = 필터링 없음"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Limit  int    `json:"limit"`
				Before int64  `json:"before"`
				Q      string `json:"q"`
			}
			_ = json.Unmarshal(in, &a)
			limit := a.Limit
			if limit <= 0 {
				limit = factsPageSize
			}
			if limit > 100 {
				limit = 100
			}
			f, hasMore, total, err := t.ts.ListByKindPageWithSources(db.KindFact, a.Before, limit, strings.TrimSpace(a.Q))
			if err != nil {
				return actool.Result{}, err
			}
			out := make([]map[string]any, 0, len(f))
			for _, n := range f {
				out = append(out, compactFact(n))
			}
			res := map[string]any{"facts": out, "total": total, "has_more": hasMore}
			if hasMore && len(f) > 0 {
				res["next_before"] = f[len(f)-1].ID // 다음 페이지로 다시 보내주세요(나이가 많은)
			}
			return jsonResult(res)
		})
}

// factSummaryMax caps a fact summary in list_facts output. Facts carry one-line
// conclusions, but nothing enforces brevity; a runaway summary must not bloat a
// whole page. Full text stays available via node_detail(id).
const factSummaryMax = 160

// compactFact is compactNode with the summary rune-capped for list_facts, so a
// page of facts stays bounded regardless of how long any single summary grew.
func compactFact(n *db.Node) map[string]any {
	m := compactNode(n)
	if s, ok := m["summary"].(string); ok && len([]rune(s)) > factSummaryMax {
		m["summary"] = string([]rune(s)[:factSummaryMax]) + "…"
		m["summary_truncated"] = true
	}
	return m
}

func (t *ToolSet) nodeDetail() actool.CoreTool {
	return t.readExpTool("node_detail", "언론 id 이 작업 또는 해당 작업과 직접 관련된 작업을 가져옵니다.【그래프 노드 탐색】전체 내용。노드 밴드 상속 source_task_id/inherited=true 및 읽기 전용。만 list_facts/list_findings/graph_overview 반환된 탐사 노드 id；에셋을 활용해주세요 list_assets/asset_neighbors。",
		obj(map[string]any{"id": idp("그래프 노드 탐색 id(비자산 id)")}, "id"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				ID json.RawMessage `json:"id"`
			}
			_ = json.Unmarshal(in, &a)
			id := pid(a.ID)
			if id <= 0 {
				return actool.Errorf("id 필수"), nil
			}
			n, err := t.ts.GetNodeWithSources(id)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if n == nil {
				return actool.Errorf(fmt.Sprintf("탐색 노드를 찾을 수 없습니다. %d。자산을 확인하고 싶다면，이용해주세요 list_assets / asset_neighbors（자산과 탐색 노드가 다릅니다. id 공간，자산 id 을(를) 전달할 수 없습니다. node_detail）。", id)), nil
			}
			if err := t.ts.PopulateFindingTrafficIDs([]*db.Node{n}); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return jsonResult(n) // full payload incl. detail / evidence, plus explicit finding IDs
		})
}

// --- planner write tools ---

// intentItem 네 add_intent 배치/단일 탐색 방향。
type intentItem struct {
	Summary   string            `json:"summary"`
	AssetIDs  []json.RawMessage `json:"asset_ids"`
	ParentIDs []json.RawMessage `json:"parent_ids"`
	Priority  int               `json:"priority"`
}

// addOneIntent 인텐트 노드를 생성하고 업스트림 혈통에 연결，복귀 id。
// 제약：의도는 확인된 지식에만 고정될 수 있습니다.——각 parent_id 이(가) 이미 존재해야 합니다. fact/finding
// 노드（다른 의도는 있을 수 없어/대상/프롬프트）。최상위 새 방향을 비워 둡니다. parent_ids，비밀로 해주세요 origin fact。
// 이렇게요"모든 의도는 연결되어 있습니다. fact 노드、그리고 무작정 계획하기보다는 발견 중심입니다."생성 경로에서 강제 실행됨。
func (t *ToolSet) addOneIntent(it intentItem) (int64, error) {
	if strings.TrimSpace(it.Summary) == "" {
		return 0, fmt.Errorf("summary 은 비워둘 수 없습니다.")
	}
	// 먼저 앵커 포인트를 확인하세요.（노드를 구축하기 전，고아 의도를 남기는 나쁜 앵커를 피하세요.）。
	parents := pidList(it.ParentIDs)
	for _, pidv := range parents {
		n, err := t.ts.GetNodeWithSources(pidv)
		if err != nil || n == nil {
			return 0, fmt.Errorf("parent_id %d 이 작업에 존재하지 않거나 해당 작업과 직접 관련되어 있습니다.：parent_ids 이(가) 이미 존재해야 합니다.【사실(fact)/찾음(finding)】노드 id；새로운 최상위 방향을 지정하려면 공백으로 남겨두세요. parent_ids", pidv)
		}
		if n.Kind != db.KindFact && n.Kind != db.KindFinding {
			return 0, fmt.Errorf("parent_id %d 네 %q 노드，은 인텐트 앵커로 사용할 수 없습니다.：의도는 확인된 것에만 고정될 수 있습니다.【사실(fact)/찾음(finding)】에，의욕이 안 붙는다/대상/프롬프트；새로운 최상위 방향을 지정하려면 공백으로 남겨두세요. parent_ids", pidv, n.Kind)
		}
	}
	priority := it.Priority
	if priority == 0 {
		priority = 5
	}
	anchors := pidList(it.AssetIDs)
	// 자산 가로채기：바인딩하려는 자산이 시스템 자산 차단 규칙에 부합하는 경우，의사표시 금지。
	if t.as != nil && len(anchors) > 0 {
		hits, err := t.as.CheckAssetsIntercept(t.taskID, anchors)
		if err != nil {
			return 0, fmt.Errorf("자산 차단 확인 실패：%w", err)
		}
		if len(hits) > 0 {
			var b strings.Builder
			fmt.Fprintf(&b, "의도「%s」바인딩된 자산이 테스트 범위 확인에 실패했습니다.，관련 자산 테스트를 중단해주세요：", it.Summary)
			for _, h := range hits {
				fmt.Fprintf(&b, "\n - %s", h.Describe())
			}
			return 0, fmt.Errorf("%s", b.String())
		}
	}
	payload := map[string]any{"summary": it.Summary}
	if len(anchors) > 0 {
		payload["asset_ids"] = anchors
	}
	id, err := t.ts.AddIntent(payload, priority, anchors, "planner")
	if err != nil {
		return 0, err
	}
	// upstream lineage: link each (validated) fact/finding parent → this intent, so
	// "multiple facts combine into one new intent" is expressible.
	for _, parent := range parents {
		_ = t.ts.Link(parent, db.RelDerivedFrom, id)
	}
	// a top-level intent (no explicit parent) connects to the origin fact, so every
	// intent still traces back to a fact node — at task start the only fact is the
	// origin, and the first intents derive from it.
	if len(parents) == 0 {
		if origin, _ := t.ts.OriginFactID(); origin > 0 {
			_ = t.ts.Link(origin, db.RelDerivedFrom, id)
		}
	}
	return id, nil
}

func (t *ToolSet) addIntent() actool.CoreTool {
	return t.writeExpTool("add_intent", "생성【방향 탐색】쓰기 frontier，탐사링크에 접속합니다。탐구의 길을 열어주려는 의도，은 고정형이 아닙니다.——사용 summary 탐색할 수 있는 한 문장의 무료 설명/확인/사용법。\n"+
		"★일괄 우선순위：한 라운드에서 선별된 여러 가지 새로운 방향이 투입됩니다. intents 배열은 한 번 제출됩니다.（한 명씩 전화하는 것보다 왕복이 절약됩니다.）。복귀 ids 배열，그리고 intents 길이 동일, 순서 동일（실패한 항목 id=0，자세히 보기 errors）。단일항목 생략 intents 최상위 레벨로 직접 연결 summary。",
		obj(map[string]any{
			"intents":    map[string]any{"type": "array", "description": "【이것을 먼저 사용하세요】추가할 탐색방향 배열，순서대로 처리。각 요소 필드는 아래의 최상위 필드와 동일합니다.（summary/asset_ids/parent_ids/priority）。복귀 ids 이 배열과 길이가 같습니다.、같은 순서。", "items": map[string]any{"type": "object"}},
			"summary":    str("[싱글] 이 탐구 방향을 한 문장으로 설명해보세요.：어떻게 해야 할까요?+왜요?。방향만 명확하게 적어주세요，자산에 의존하지 않음 id。"),
			"asset_ids":  map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "이 방향은 테스트가 필요합니다/공격당함【대상자산 id】（**전달해 보세요**，0/1/여러개；네 list_assets 반환된 자산 id，은 탐사 노드가 아닙니다. id）：이 탐색 방향의 대상 자산은 무엇입니까?（사이트/인터페이스/매개변수/호스트 등）。특정 자산을 중심으로 방향이 돌아가는 한 반드시 업로드해야 합니다.——그렇죠「이번 탐사의 목표는 무엇인가요?」에 대한 구조화된 태그，중복된 항목을 덮어쓰고 제거하는 데 사용됩니다.、인텐트를 자산 링크에 연결합니다.。순수 전역 정찰 시에만 해당、구체적인 대상 자산이 없는 경우에만 공백으로 남겨두세요.。"},
			"parent_ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "상류 앵커 포인트 id（선택사항，0/1/여러개）：이 방향으로 어떤 방향이 있나요?【확인된 사실(fact)/찾음(finding)】종합적인 결과。**기존 내용만 작성하시면 됩니다. fact/finding 노드 id,사진을 채울 수 없습니다/대상/팁**——의도는 확증된 지식에 기초해야 합니다.,막연한 계획보다는 발견 중심。여러 사실을 함께 전달하여 새로운 의도를 생성할 수 있습니다.;새로운 최상위 정찰 방향은 공백으로 남겨두세요.（작업 시작점에 자동으로 매달립니다. origin fact）。"},
			"priority":   intp("우선순위 0-10，기본값5"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Intents    []intentItem `json:"intents"`
				intentItem              // 단일 모드：최상위 수준 summary/asset_ids/parent_ids/priority
			}
			_ = json.Unmarshal(in, &a)
			batch := len(a.Intents) > 0
			items := a.Intents
			if !batch {
				items = []intentItem{a.intentItem}
			}

			ids := make([]int64, len(items))
			errs := map[string]string{}
			createdAny := false
			for i, it := range items {
				id, err := t.addOneIntent(it)
				if err != nil {
					errs[strconv.Itoa(i)] = err.Error()
					continue
				}
				ids[i] = id
				createdAny = true
			}

			// 인간의 책의 주인 agent 직접 투자 의향 → 작업이 완료되면 done（없음 open 대상 goalless 지점），넣어보세요
			// 물러서라 running，worker 그래야만 이 뜻이 이루어질 수 있다.。resumeTask 메인으로만 agent 님 Chat 접속
			// (SetResumeTask)；planner 님 ToolSet 입니다 nil，그래서 planner 직접 조정해보세요 add_intent 이 기간
			// no-op，은 정상적인 생산 의도에 영향을 미치지 않습니다.。위와 같이 인텐트 노드가 구축되었습니다.(open)，부활 시 실수로 배수되지 않습니다.。
			if createdAny && t.resumeTask != nil {
				t.resumeTask()
			}

			if !batch { // 싱글：반품 원본을 보관하세요.
				if e, bad := errs["0"]; bad {
					return actool.Errorf(e), nil
				}
				return actool.Text(fmt.Sprintf("intent created: %d", ids[0])), nil
			}
			out := map[string]any{"ids": ids}
			if len(errs) > 0 {
				out["errors"] = errs
			}
			return jsonResult(out)
		})
}

func (t *ToolSet) listGoals() actool.CoreTool {
	return t.readExpTool("list_goals", "대상 노드와 이 작업의 상태를 나열합니다.（open/met），달성 여부를 확인하는 데 사용됩니다.。",
		obj(map[string]any{}),
		func(context.Context, json.RawMessage) (actool.Result, error) {
			g, _ := t.ts.ListByKind(db.KindGoal, 100)
			return jsonResult(g)
		})
}

func (t *ToolSet) proveGoal() actool.CoreTool {
	return t.writeExpTool("prove_goal", "발견을 판단할 때/특정 목표가 달성되었다는 사실이 입증되었습니다.：증거 노드를 대상 노드에 연결합니다.，그리고 타겟을 표시하세요 met。",
		obj(map[string]any{
			"goal_id":     idp("대상 노드 id"),
			"evidence_id": idp("의 발견을 증명하다/팩트 노드 id"),
			"reason":      str("이 증거가 이 목표를 달성하는 이유는 무엇입니까?"),
		}, "goal_id", "evidence_id"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				GoalID     json.RawMessage `json:"goal_id"`
				EvidenceID json.RawMessage `json:"evidence_id"`
				Reason     string          `json:"reason"`
			}
			_ = json.Unmarshal(in, &a)
			goal, ev := pid(a.GoalID), pid(a.EvidenceID)
			if goal == 0 || ev == 0 {
				return actool.Errorf("goal_id 그리고 evidence_id 필수"), nil
			}
			goalNode, err := t.ts.GetNode(goal)
			if err != nil || goalNode == nil || goalNode.Kind != db.KindGoal {
				return actool.Errorf("goal_id 은 이 작업의 대상 노드여야 합니다.（연결된 작업 대상이 읽기 전용입니다.）"), nil
			}
			evidenceNode, err := t.ts.GetNodeWithSources(ev)
			if err != nil || evidenceNode == nil || (evidenceNode.Kind != db.KindFact && evidenceNode.Kind != db.KindFinding) {
				return actool.Errorf("evidence_id 본 업무의 사실이거나 직접적으로 관련된 업무여야 합니다./취약점 노드"), nil
			}
			_ = t.ts.Link(ev, db.RelProves, goal)
			_ = t.ts.SetNodeState(goal, "met")
			// 각 대상이 표시됨 met，이 작업인지 확인해 보세요.【모든 목표】모두 met；그렇다면，자동결정
			// 작업 완료（세트 GoalMet），명시적인 모델 조정에 의존할 필요가 없습니다. goal_met。
			if goals, err := t.ts.ListByKind(db.KindGoal, 1000); err == nil && len(goals) > 0 {
				allMet := true
				for _, g := range goals {
					if g.State != "met" {
						allMet = false
						break
					}
				}
				if allMet {
					t.GoalMet = true
					t.Reason = fmt.Sprintf("모두 %d 목표를 달성했습니다 met（드디어 goal %d 트리거）", len(goals), goal)
					return actool.Text(fmt.Sprintf("goal %d marked met；이번 임무의 모든 목표가 달성되었습니다.，작업 완료가 자동으로 결정됩니다.", goal)), nil
				}
			}
			return actool.Text(fmt.Sprintf("goal %d marked met", goal)), nil
		})
}

func (t *ToolSet) goalMet() actool.CoreTool {
	return writeTool("goal_met", "【전체 작업을 즉시 종료합니다.】——작업을 확정한 경우에만【모든 목표가 정말로 달성되었습니다、종합엔딩】그때만이라도（관심이 과제이다【종합】완료；목표 중 하나만 달성했습니다./어떤 분 flag/특정 취약점【포함되지 않음】——그럴때 사용하세요 prove_goal 대상을 표시하면 됩니다）。⚠️사용되지 않습니다.“이번 계획 라운드를 종료합니다.”님：이번 라운드를 보낼 새로운 의향은 없습니다.、또는 대기 중 worker 출력，둘 다【그냥 이번 라운드를 직접 끝내세요，이 도구를 조정하지 마십시오】（0 이런 의도는 지극히 정상입니다）。일반적인 판단이 먼저 사용됩니다. prove_goal 목표를 하나씩 증명해 보세요；goal_met 그냥 하나씩 증명을 우회하세요、전체적인 상황에서 직접적으로 끝내자는 뜻이다.。",
		obj(map[string]any{"reason": str("달성 이유（목표가 진정으로 달성되었다는 증거여야 합니다.，그럴 리가 없어.“이번 라운드에는 새로운 방향이 없습니다”이번 라운드를 종료하는 이유가 바로 이렇습니다.）")}, "reason"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct{ Reason string }
			_ = json.Unmarshal(in, &a)
			t.GoalMet = true
			t.Reason = a.Reason
			return actool.Text("acknowledged: goal marked met"), nil
		})
}

// --- worker write tools ---

func (t *ToolSet) addFinding() actool.CoreTool {
	return writeTool("report_finding", "확인된 취약점을 기록합니다.，사용 evidence 명령 출력 제공、로그 등 검증 가능한 증거。작업 컨텍스트가 현재 컨텍스트로 전달됩니다. intent_id。반환됨 finding_id 은 독립적인 취약점 레코드입니다. ID，finding_node_id 은 탐사 노드입니다. ID（첫 번째 줄에는 노드 번호가 유지됩니다.）。", obj(map[string]any{
		"vulnclass": str("취약점 카테고리"), "name": str("취약점 이름"), "severity": str("critical|high|medium|low"), "summary": str("조사 결과 요약"),
		"intent_id": idp("현 업무의 의도 id"), "asset_ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "영향을 받는 자산 id"},
		"evidence":         str("증거/PoC 문자"),
		"evidence_hint_id": idp("선택사항：이 작업의 이 취약점에 해당하는 프롬프트 노드 ID，자동으로 구조를 운반합니다. traffic_refs；상속 팁이나 기타 취약점 팁을 참조할 수 없습니다."),
		"traffic_refs": map[string]any{"type": "array", "description": "선택사항；HTTP/HTTPS 먼저 취약점을 하나씩 검색하여 검증합니다./응답은 취약점 결론을 뒷받침합니다.，그럼 반복되는 순서대로 진실을 채워주세요 ID。TCP 기다리지 마세요 HTTP 취약점、정확한 기록이 수집되지 않거나 찾을 수 없는 경우 생략 또는 합격 []，신고를 막지 마세요；사용 가능 evidence 이유를 설명하고 기타 검증 가능한 증거를 제출하세요.。추측하지 마세요 ID、도메인 이름별/시간 추정 상관 관계 또는 보충 패킷의 반복 감지만。목적 baseline 일반제어 / proof 취약점 증명 / verification 보충검증 / supporting 뒷받침하는 증거。",
			"items": obj(map[string]any{"traffic_id": str("traffic_search 실제 트래픽을 반환했습니다. ID"), "role": map[string]any{"type": "string", "enum": []string{"baseline", "proof", "verification", "supporting"}}, "note": str("이 트래픽은 취약점 결론을 어떻게 뒷받침합니까?")}, "traffic_id")},
	}, "vulnclass", "severity", "summary"), func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
		var a struct {
			VulnClass, Name, Severity, Summary, Evidence string
			IntentID                                     json.RawMessage   `json:"intent_id"`
			AssetIDs                                     []json.RawMessage `json:"asset_ids"`
			TrafficRefs                                  []db.TrafficRef   `json:"traffic_refs"`
			EvidenceHintID                               json.RawMessage   `json:"evidence_hint_id"`
		}
		if err := json.Unmarshal(in, &a); err != nil {
			return actool.Errorf(err.Error()), nil
		}
		if t.ts == nil {
			return actool.Errorf("report_finding 작업 컨텍스트가 필요합니다.；플랫폼 대화를 통과해주세요 add_task_hint 해당 작업에 취약점을 넘겨주세요，프롬프트에서 기존 항목을 수행합니다. traffic_refs，업무별 Agent 등록。등록된 취약점이 있습니다. bind_finding_traffic 다시 묶음。"), nil
		}
		// Auto-binding off: ignore the evidence params instead of rejecting the call.
		// stripTrafficParameters already removes them from the advertised schema, but
		// models routinely emit fields anyway — failing here would discard a confirmed
		// finding over a stray parameter. The success path below reports evidence_status
		// "not_bound" with the "폐쇄됨，페이지에서 수동으로 연결할 수 있습니다." note, which is what the caller needs.
		if !findingTrafficBindingEnabled() {
			a.TrafficRefs, a.EvidenceHintID = nil, nil
		}
		if len(a.EvidenceHintID) > 0 && pid(a.EvidenceHintID) <= 0 {
			return actool.Errorf("evidence_hint_id 은 유효한 프롬프트 노드여야 합니다. ID；핸드오버 메시지가 없을 경우 생략"), nil
		}
		refs, err := t.findingRefsFromHint(pid(a.EvidenceHintID), a.TrafficRefs)
		if err != nil {
			return actool.Errorf(err.Error()), nil
		}
		input := db.RecordFindingInput{TaskID: t.taskID, ExplorationID: t.ts.ID(), IntentID: pid(a.IntentID), VulnClass: a.VulnClass, Name: a.Name, Severity: a.Severity, Summary: a.Summary, Evidence: a.Evidence, Worker: t.worker, AssetIDs: pidList(a.AssetIDs)}
		var recorded *db.RecordedFinding
		if t.findingRecorder != nil {
			recorded, err = t.findingRecorder.Record(ctx, input, refs)
		} else if len(refs) > 0 {
			return actool.Errorf("교통 증거 저장이 불가능합니다.；등록되지 않은 취약점"), nil
		} else {
			recorded, err = t.ts.RecordFinding(ctx, input)
		}
		if err != nil {
			return actool.Errorf(err.Error()), nil
		}
		if t.notifyFinding != nil {
			iid := input.IntentID
			if iid <= 0 {
				iid = t.ownerNode
			}
			t.notifyFinding(iid, a.Summary)
		} else if t.notify != nil {
			t.notify()
		}
		t.writes.Findings++
		// Keep the first line's node-ID contract for existing reporter triggers.
		for i := range recorded.Traffic.Bindings {
			recorded.Traffic.Bindings[i].Snapshot.ReqHead = ""
			recorded.Traffic.Bindings[i].Snapshot.RespHead = ""
		}
		result := struct {
			*db.RecordedFinding
			EvidenceStatus string `json:"evidence_status"`
			EvidenceNote   string `json:"evidence_note,omitempty"`
		}{RecordedFinding: recorded, EvidenceStatus: "bound"}
		if len(recorded.Traffic.Bindings) == 0 {
			result.EvidenceStatus = "not_bound"
			result.EvidenceNote = "취약점이 저장되었습니다.，언바운드 트래픽。TCP/패키지가 없으면 상황은 정상적으로 계속될 수 있습니다.；인증이 되었다면 HTTP 교통，이용 가능하세요 bind_finding_traffic 또는 취약점 페이지를 패치하세요.，증거인계 완료。취약점을 재현하지 마십시오。"
			if !findingTrafficBindingEnabled() {
				result.EvidenceNote = "취약점이 저장되었습니다.。Agent 자동 바인딩 트래픽이 꺼졌습니다.，페이지의 트래픽을 수동으로 연관시킬 수 있습니다.。"
			}
		}
		raw, _ := json.Marshal(result)
		return actool.Text(fmt.Sprintf("finding recorded: %d\n%s", recorded.NodeID, raw)), nil
	})
}

// recordFact writes a general exploration RESULT/conclusion (not a vuln, not a
// new asset) into the EXPLORATION graph, chained to the intent that produced it.
// This is the home for observations and — importantly — negative results
// ("port closed", "param not injectable", "no login found"). Such conclusions
// must NOT be stuffed into the asset graph via upsert_asset.
// factItem 네 record_fact 배치/한가지 사실。
type factItem struct {
	Summary    string            `json:"summary"`
	Detail     string            `json:"detail"`
	Evidence   string            `json:"evidence"`   // 핵심 증거 한 줄（명령+키 출력 라인），결론을 지지하라、이후 확인이 용이함
	Confidence string            `json:"confidence"` // observed（직접보기）| inferred（현상으로 추론）
	IntentID   json.RawMessage   `json:"intent_id"`
	AssetIDs   []json.RawMessage `json:"asset_ids"`
}

// recordOneFact 메시지를 작성하세요 fact 인텐트에 연결된 노드（intent→yields→fact）。defaultIntent 입니다
// 일괄 처리 시 기본 인텐트（이 기사에는 나와 있지 않습니다. intent_id 일 때 사용됩니다.）。
func (t *ToolSet) recordOneFact(it factItem, defaultIntent int64) (int64, error) {
	if strings.TrimSpace(it.Summary) == "" {
		return 0, fmt.Errorf("summary 은 비워둘 수 없습니다.")
	}
	payload := map[string]any{"summary": it.Summary}
	if it.Detail != "" {
		payload["detail"] = it.Detail
	}
	if e := strings.TrimSpace(it.Evidence); e != "" {
		payload["evidence"] = e
	}
	if c := strings.TrimSpace(it.Confidence); c != "" {
		payload["confidence"] = c
	}
	intent := pid(it.IntentID)
	if intent <= 0 {
		intent = defaultIntent
	}
	if intent > 0 {
		node, err := t.ts.GetNode(intent)
		if err != nil || node == nil || node.Kind != db.KindIntent {
			return 0, fmt.Errorf("intent_id 이 작업의 의도가 틀림없습니다（연결된 작업 의도가 읽기 전용입니다.）")
		}
	}
	// a fact is its OWN node kind (distinct from a vuln finding).
	id, err := t.ts.AddNode(db.KindFact, payload, 5, "confirmed", t.worker, pidList(it.AssetIDs))
	if err != nil {
		return 0, err
	}
	if intent > 0 {
		_ = t.ts.Link(intent, db.RelYields, id) // chain: intent -> fact
	}
	t.writes.Facts++
	return id, nil
}

func (t *ToolSet) recordFact() actool.CoreTool {
	return t.writeExpTool("record_fact", "탐색【사실/결론】탐사지도 작성，제작 의도와 연결되어（intent_id）。탐사 결과를 기록하는 데 사용됩니다.——지문 포함/열거 등【긍정적인 결론】，그리고'포트가 닫혔습니다.'/'매개변수를 삽입할 수 없습니다.'/'로그인 입구를 찾을 수 없습니다.'등【부정적인 결론】。\n"+
		"⚠️하나의 탐색에 대한 다중 관찰【사실을 하나로 요약하면】，여러개로 쪼개지 마세요，하나의 사실로 묶일 수 있다면 하나의 사실로 표현해 보세요.：summary=이 결론에 대한 결론 문장，detail=관련사항（여러 특정 항목을 포함할 수 있습니다.）。예：지문 의도→사실 {summary:'인식됨 X 사이트의 기술스택과 대응특성', detail:'nginx 1.25 / Vue3 / 200 / title=.. / body_len=..'}，상태 코드 대신、지문、제목은 하나씩 적어주세요。의도는 일반적으로 단 하나의 사실만을 생성합니다.，너무 많이 분해하면 맵이 무한 확장됩니다.。\n"+
		"★facts 배열은 한 번에 여러 항목을 쓰는 데 사용됩니다.【서로 다르다】의 결론（각 항목은 생략 가능 intent_id，기본적으로 최상위 레이어를 사용합니다. intent_id）。복귀 ids 배열，그리고 facts 길이 동일, 순서 동일。\n"+
		"⚠️도구 출력에 그냥 적어주세요【정말 그렇군요】의 결론，가정하지 마세요。evidence 그리고 confidence 부정확한 결론이 지도를 오염시키는 것을 방지하기 위해 사용됩니다.：\n"+
		"  · evidence=이 결론을 지지하라【한 줄】핵심 증거（명령+그것을 가장 잘 증명하는 출력 한두 줄），**간결하게 해주세요**——자세한 내용은 detail，여기에 출력의 큰 부분을 붙여넣지 마십시오.。\n"+
		"  · confidence=observed（출력에서 바로 확인하실 수 있습니다）| inferred（현상으로 추론）。\n"+
		"  · **부정적인 결론**（주사 불가/포트가 닫혔습니다./출입구 등이 발견되지 않음）쓰기 전용\"관찰 + 잠정읽기 방법\"——실제로 본 것을 말해주세요，방향을 포기할지 말지는 전체적인 상황을 고려해 기획자들이 결정한다.；꼭 드려요 evidence，수단이 무한하거나 증거가 약하다（한 번만 탐험해보세요、같네요）마크 inferred，정말 지쳐서 재능마크가 바로 보이네요 observed。",
		obj(map[string]any{
			"facts":      map[string]any{"type": "array", "description": "【결론이 여러 개인 경우에 사용됩니다.】사실 배열，요소 필드는 아래의 최상위 필드와 동일합니다.（summary/detail/evidence/confidence/intent_id/asset_ids）；생략 intent_id 그런 다음 최상위 레이어를 사용하십시오. intent_id。복귀 ids 이 배열과 길이가 같습니다.、같은 순서。", "items": map[string]any{"type": "object"}},
			"summary":    str("이번 탐구의 결론에 대하여【결론】（그렇죠 detail 요약）"),
			"intent_id":  idp("이 사실을 생산하려는 의도 id（당신이 받은 의도；각 항목을 일괄적으로 기본값으로 사용）"),
			"detail":     str("이 사실과 관련된 내용：이 탐구에서 얻은 여러 관찰과 사실을 여기에 적어보세요."),
			"evidence":   str("【한 줄】핵심 증거：명령 + 결론을 가장 잘 증명하는 출력 한두 줄。간결하게 해주세요，출력의 큰 부분에 집착하지 마세요.（자세한 내용은 게시하겠습니다. detail）。"),
			"confidence": str("observed（출력에서 바로 확인하실 수 있습니다）| inferred（현상으로 추론）。부정적인 결론은 사실대로 표시해야 합니다.。"),
			"asset_ids":  map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "관련자산 id（선택사항，0/1/여러개）：이 사실과 관련된 자산은 무엇입니까?"},
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Facts    []factItem `json:"facts"`
				factItem            // 단일 모드 + 배치 기본값 intent_id
			}
			_ = json.Unmarshal(in, &a)
			batch := len(a.Facts) > 0
			items := a.Facts
			if !batch {
				items = []factItem{a.factItem}
			}
			defaultIntent := pid(a.factItem.IntentID) // 최상위 수준 intent_id = 배치 기본값

			ids := make([]int64, len(items))
			errs := map[string]string{}
			for i, it := range items {
				id, err := t.recordOneFact(it, defaultIntent)
				if err != nil {
					errs[strconv.Itoa(i)] = err.Error()
					continue
				}
				ids[i] = id
			}

			if !batch { // 싱글：반품 원본을 보관하세요.
				if e, bad := errs["0"]; bad {
					return actool.Errorf(e), nil
				}
				return actool.Text(fmt.Sprintf("fact recorded: %d", ids[0])), nil
			}
			out := map[string]any{"ids": ids}
			if len(errs) > 0 {
				out["errors"] = errs
			}
			return jsonResult(out)
		})
}

type hintItem struct {
	Text        string            `json:"text"`
	AssetIDs    []json.RawMessage `json:"asset_ids"`
	TrafficRefs []db.TrafficRef   `json:"traffic_refs"`
}

// addOneHint 하나만 걸어보세요 hint 노드(active/human)탐사지도 바로가기,고정 가능한 자산,복귀 id。
func (t *ToolSet) addOneHint(it hintItem) (int64, error) {
	if len(it.TrafficRefs) > 0 && !findingTrafficBindingEnabled() {
		return 0, fmt.Errorf("Agent 자동 바인딩 트래픽이 꺼졌습니다.，저장 및 운반되지 않음 traffic_refs 의 팁；시스템 설정에서 활성화할 수 있습니다.，아니면 그냥 문자로 넘겨주세요")
	}
	if strings.TrimSpace(it.Text) == "" {
		return 0, fmt.Errorf("text 은 비워둘 수 없습니다.")
	}
	var anchors []int64
	for _, raw := range it.AssetIDs {
		if tid := pid(raw); tid > 0 {
			anchors = append(anchors, tid)
		}
	}
	refs, err := db.NormalizeTrafficRefs(it.TrafficRefs)
	if err != nil {
		return 0, err
	}
	payload := map[string]any{"text": it.Text}
	if len(refs) > 0 {
		payload["traffic_refs"] = refs
	}
	// 일어나세요 planner 여기서는 하나씩 하지 마세요—— addHint 전체 배치가 작성된 후 한 번 트리거됩니다.（프롬프트 텍스트 가져오기），
	// 일단 피하세요 add_hint 여러 프롬프트가 화면을 하나씩 새로 고칩니다. planner 에 대한 트리거 라인。
	return t.ts.AddNode(db.KindHint, payload, 0, "active", "human", anchors)
}

type goalItem struct {
	Text      string `json:"text"`
	VulnClass string `json:"vulnclass"`
}

// addOneGoal 하나만 걸어보세요 goal 노드(open)탐사지도 바로가기:작업 루트에 연결(origin fact,rel spawns)。
// origin 받아 t.worker(기본값 system):goals 디스어셈블러가 작성한 메모 "goals"、스승님 agent 런타임 참고 사항
// "human"。일어나세요 planner  setGoals 전체 배치를 작성한 후 함께 하세요.(아래 참조),이곳은 재고입고만 담당하는 곳입니다。
func (t *ToolSet) addOneGoal(it goalItem) (int64, error) {
	text := strings.TrimSpace(it.Text)
	if text == "" {
		return 0, fmt.Errorf("text 은 비워둘 수 없습니다.")
	}
	payload := map[string]any{"text": text}
	if vc := strings.TrimSpace(it.VulnClass); vc != "" {
		payload["vulnclass"] = vc
	}
	origin := t.worker
	if origin == "" {
		origin = "system"
	}
	id, err := t.ts.AddNode(db.KindGoal, payload, 0, "open", origin, nil)
	if err != nil {
		return 0, err
	}
	if of, _ := t.ts.OriginFactID(); of > 0 && id > 0 {
		_ = t.ts.Link(of, db.RelSpawns, id) // goals descend from the task root (origin fact)
	}
	return id, nil
}

// setGoals 주다【이번 임무는】새로운 탐사 대상 추가(goal 노드)。대상 디스어셈블러의 제출 도구이기도 합니다.,님도 마스터이십니다
// agent 런타임 시 대상을 보완하기 위한 도구——동일한 관리 도구,사용 가능 web 변경 설명 끝/schema、언론 agent 바인딩。
func (t *ToolSet) setGoals() actool.CoreTool {
	return writeTool("set_goals",
		"주다【이번 임무는】새로운 탐사 대상 추가(goal)。대상=최종 결과물/검증 가능한 결과,공격 단계나 정찰 이동이 아닙니다.。\n"+
			"★일괄 우선순위:여러 대상을 넣었습니다. goals 배열은 한 번 제출됩니다.,복귀 ids 길이도 같고 순서도 같습니다(실패한 항목 id=0,자세히 보기 errors)。단일항목 생략 goals 최상위 레벨로 직접 연결 text。\n"+
			"vulnclass 선택사항:해당 취약점 클래스( SQLi/IDOR),비즈니스 로직 클래스 대상을 비워 두세요.。목표 달성 여부는 시스템에 의해 결정됩니다. met,이 도구는 새 항목을 추가하는 역할만 담당합니다.。",
		obj(map[string]any{
			"goals":     map[string]any{"type": "array", "description": "【이것을 먼저 사용하세요】추가할 타겟 어레이,순서대로 처리。각 요소:text(필수,독립적으로 검증 가능한 최종 목표)+ vulnclass(선택사항)。복귀 ids 이 배열과 길이가 같습니다.、같은 순서。", "items": map[string]any{"type": "object"}},
			"text":      str("[싱글] 독립적으로 검증 가능한 최종 목표"),
			"vulnclass": str("[싱글] 해당 취약점 클래스(클리어된 경우), SQLi/IDOR;비즈니스 로직 대상은 비워둘 수 있습니다."),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.ts == nil {
				return actool.Errorf("set_goals 활성화되지 않음: ExplorationStore 초기화되지 않음"), nil
			}
			var a struct {
				Goals    []goalItem `json:"goals"`
				goalItem            // 단일 모드:최상위 수준 text/vulnclass
			}
			_ = json.Unmarshal(in, &a)
			batch := len(a.Goals) > 0
			items := a.Goals
			if !batch {
				items = []goalItem{a.goalItem}
			}

			ids := make([]int64, len(items))
			errs := map[string]string{}
			var addedTexts []string
			for i, it := range items {
				id, err := t.addOneGoal(it)
				if err != nil {
					errs[strconv.Itoa(i)] = err.Error()
					continue
				}
				ids[i] = id
				addedTexts = append(addedTexts, strings.TrimSpace(it.Text))
			}
			if len(addedTexts) > 0 {
				// 일어나세요 planner(전체 배치 1회)。우선순위 notifyGoal:한번 set_goals 하나만 기억하세요「명 추가됨
				// N 목표:…」트리거,화면을 하나씩 새로 고치지 마세요;디스어셈블러/worker 해당 콜백이 없습니다. → 순수 반환 notify(디스어셈블러
				// round-0 리안 notify 역시 대답이 없었다,즉, 조작이 없습니다.,왜냐면 이때는 planner 아직 시작하지 않았습니다)。
				switch {
				case t.notifyGoal != nil:
					t.notifyGoal(addedTexts)
				case t.notify != nil:
					t.notify()
				}
				// 스승님 agent 런타임 중에 새 대상 추가 → 완료/일시 중지된 작업을 다시 시작합니다. running 계속 달려라(최종 국문 회의
				// 평범한 것을 삼키다 notify,명시적으로 부활해야 함)。만 mainagent 이 콜백을 수락했습니다.;디스어셈블러/worker 입니다 nil。
				if t.resumeTask != nil {
					t.resumeTask()
				}
			}

			if !batch { // 싱글:반품 원본을 보관하세요.
				if e, bad := errs["0"]; bad {
					return actool.Errorf(e), nil
				}
				return actool.Text(fmt.Sprintf("goal added: %d", ids[0])), nil
			}
			out := map[string]any{"ids": ids}
			if len(errs) > 0 {
				out["errors"] = errs
			}
			return jsonResult(out)
		})
}

type constraintItem struct {
	Text string `json:"text"`
	Type string `json:"type"` // allow | deny
}

// addOneConstraint 작업 제약 조건을 삭제합니다. task_constraints。origin 받아 t.worker(기본값 system):
// 디스어셈블러가 씁니다. "goals"、스승님 agent 쓰기 "human"。
func (t *ToolSet) addOneConstraint(it constraintItem) (int64, error) {
	text := strings.TrimSpace(it.Text)
	if text == "" {
		return 0, fmt.Errorf("text 은 비워둘 수 없습니다.")
	}
	kind := strings.TrimSpace(strings.ToLower(it.Type))
	if kind == "" {
		kind = "deny" // 기본적으로 처리가 금지되어 있습니다.:유형이 표시되지 않은 경우 더 보수적입니다.
	}
	if kind != "allow" && kind != "deny" {
		return 0, fmt.Errorf("type 반드시 allow 또는 deny")
	}
	return t.ts.AddConstraint(kind, text, t.worker)
}

// setConstraints 주다【이번 임무는】작업 제약 조건 추가(allow=무엇이 허용됩니까? / deny=금지사항은 무엇인가요?)。은 타겟이자 동시에
// 디스어셈블러 round-0 제약조건 추출을 위한 제출 도구,님도 마스터이십니다 agent 런타임 시 제약 조건을 보완하기 위한 도구——동일한 관리 도구,사용 가능 web
// 변경 설명 끝/schema、언론 agent 바인딩。제약조건이 주입됩니다. planner/worker 의 시스템에서 탐사 경계를 제한하라는 메시지가 표시됩니다.。
func (t *ToolSet) setConstraints() actool.CoreTool {
	return writeTool("set_constraints",
		"주다【이번 임무는】작업 제약 조건 추가,탐사 경계의 틀을 잡는 데 사용됩니다.:type=allow(허용되는 작업)또는 deny(금지된 조작)。\n"+
			"제약=예『예/할 수 없는 작업은 무엇입니까?』규정(『현재 포트만 테스트,다른 포트를 스캔하지 마십시오』『프로덕션 라이브러리에 대한 쓰기 작업은 금지됩니다.』『수동 정찰만 허용됩니다.』),대상이 아님、공격스텝도 아니고。\n"+
			"★일괄 우선순위:여러 항목을 입력하세요. constraints 배열은 한 번 제출됩니다.,복귀 ids 길이도 같고 순서도 같습니다(실패한 항목 id=0,자세히 보기 errors)。단일항목 생략 constraints 최상위 레벨로 직접 연결 text/type。\n"+
			"작업대상만 등록/설명에【명확하게 써라】제약,꾸며내지 마세요;유형이 확실하지 않을 때 사용됩니다. deny(좀 더 보수적으로)。",
		obj(map[string]any{
			"constraints": map[string]any{"type": "array", "description": "【이것을 먼저 사용하세요】추가할 제약 배열,순서대로 처리。각 요소:text(필수,제약)+ type(allow|deny)。복귀 ids 이 배열과 길이가 같습니다.、같은 순서。", "items": map[string]any{"type": "object"}},
			"text":        str("[싱글] 연산제약 내용"),
			"type":        str("[싱글] allow(허용됨)또는 deny(금지됨);기본 프레스 deny 처리 중"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.ts == nil {
				return actool.Errorf("set_constraints 활성화되지 않음: ExplorationStore 초기화되지 않음"), nil
			}
			var a struct {
				Constraints    []constraintItem `json:"constraints"`
				constraintItem                  // 단일 모드:최상위 수준 text/type
			}
			_ = json.Unmarshal(in, &a)
			batch := len(a.Constraints) > 0
			items := a.Constraints
			if !batch {
				items = []constraintItem{a.constraintItem}
			}
			ids := make([]int64, len(items))
			errs := map[string]string{}
			for i, it := range items {
				id, err := t.addOneConstraint(it)
				if err != nil {
					errs[strconv.Itoa(i)] = err.Error()
					continue
				}
				ids[i] = id
			}
			if !batch { // 싱글:간단하게 반환하세요.
				if e, bad := errs["0"]; bad {
					return actool.Errorf(e), nil
				}
				return actool.Text(fmt.Sprintf("constraint added: %d", ids[0])), nil
			}
			out := map[string]any{"ids": ids}
			if len(errs) > 0 {
				out["errors"] = errs
			}
			return jsonResult(out)
		})
}

func (t *ToolSet) addHint() actool.CoreTool {
	return t.writeExpTool("add_hint", "인간을 넣어라/스승님 agent 님의 전략팁이 탐사지도와 연결되어 있습니다，플래너는 다음에 인텐트를 생성할 때 이를 읽습니다.。\n"+
		"★일괄 우선순위：팁을 여러개 넣어보세요 hints 배열은 한 번 제출됩니다.（한 명씩 전화하는 것보다 왕복이 절약됩니다.）。복귀 ids 배열，그리고 hints 길이 동일, 순서 동일（실패한 항목 id=0，자세히 보기 errors）。단일항목 생략 hints 최상위 레벨로 직접 연결 text。",
		obj(map[string]any{
			"hints":        map[string]any{"type": "array", "description": "【이것을 먼저 사용하세요】추가할 프롬프트 배열，순서대로 처리。각 요소 필드는 아래의 최상위 필드와 동일합니다.（text/asset_ids/traffic_refs）。복귀 ids 이 배열과 길이가 같습니다.、같은 순서。", "items": obj(map[string]any{"text": str("프롬프트 내용"), "asset_ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}, "traffic_refs": HintTrafficSchema()})},
			"text":         str("[싱글] 프롬프트 내용，'사후인증 인터페이스 파헤치기에 집중'"),
			"traffic_refs": HintTrafficSchema(),
			"asset_ids":    map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "고정 자산 id（선택사항，0/1/여러개）"},
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Hints    []hintItem `json:"hints"`
				hintItem            // 단일 모드：최상위 수준 text/asset_ids
			}
			_ = json.Unmarshal(in, &a)
			batch := len(a.Hints) > 0
			items := a.Hints
			if !batch {
				items = []hintItem{a.hintItem}
			}

			ids := make([]int64, len(items))
			errs := map[string]string{}
			var addedTexts []string
			for i, it := range items {
				id, err := t.addOneHint(it)
				if err != nil {
					errs[strconv.Itoa(i)] = err.Error()
					continue
				}
				ids[i] = id
				addedTexts = append(addedTexts, strings.TrimSpace(it.Text))
			}
			if len(addedTexts) > 0 {
				// 일어나세요 planner（전체 배치 1회）。우선순위 notifyHint：한번 add_hint 하나만 기억하세요「명 추가됨
				// N 전략 팁：…」트리거，하자 planner 클리어"이번 라운드에 새로 추가되었습니다. hint 트리거"및 프롬프트 내용 보기；
				// 콜백을 받지 못한 경우 순수로 돌아갑니다. notify（bare wake，hint 그래도 스스로 읽을 수 있도록 그림에 접혀 있습니다.）。
				switch {
				case t.notifyHint != nil:
					t.notifyHint(addedTexts)
				case t.notify != nil:
					t.notify()
				}
			}

			if !batch { // 싱글：반품 원본을 보관하세요.
				if e, bad := errs["0"]; bad {
					return actool.Errorf(e), nil
				}
				return actool.Text(fmt.Sprintf("hint added: %d", ids[0])), nil
			}
			out := map[string]any{"ids": ids}
			if len(errs) > 0 {
				out["errors"] = errs
			}
			return jsonResult(out)
		})
}

// killWorkTool lets the planner terminate a single running work (by intent id).
func (t *ToolSet) killWorkTool() actool.CoreTool {
	return t.writeExpTool("kill_work", "실행 중인 인텐트 종료(work)。편차를 멈추는 데 사용됩니다./무의미한 탐색；종료된 인텐트는 다음과 같이 표시됩니다. stopped，더 이상 자동으로 회수되지 않습니다.。먼저 사용해 보세요 get_worker_output 뭐하는지 보고 결정하세요。",
		obj(map[string]any{"intent_id": idp("종료 의사 id（= work 처리）")}, "intent_id"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.killWork == nil {
				return actool.Errorf("kill_work 현재는 이용할 수 없습니다."), nil
			}
			var a struct {
				IntentID json.RawMessage `json:"intent_id"`
			}
			_ = json.Unmarshal(in, &a)
			id := pid(a.IntentID)
			if id <= 0 {
				return actool.Errorf("intent_id 필수"), nil
			}
			node, err := t.ts.GetNode(id)
			if err != nil || node == nil || node.Kind != db.KindIntent {
				return actool.Errorf("intent_id 이 작업의 의도가 틀림없습니다（연결된 작업 의도가 읽기 전용입니다.）"), nil
			}
			if err := t.killWork(id); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text(fmt.Sprintf("인텐트 전송됨 %d 님 work 종료 신호를 보냅니다.", id)), nil
		})
}

// steerWorkTool lets the planner inject a mid-run course-correction into a running
// work WITHOUT killing it: the message reaches the worker before its next tool call,
// which re-plans its next step (already-gathered context is kept). For in-intent
// nudges ("그만하세요 X、집중 Y"); if the whole direction is wrong use kill_work + a new intent.
func (t *ToolSet) steerWorkTool() actool.CoreTool {
	return t.writeExpTool("steer_work", "실행 의도를 부여(work)수정지침 실시간 주입，방해하지마、진전이 없습니다：worker 은 다음 단계 전에 귀하의 지시를 받고 그에 따라 조정합니다.。'더 이상 떠나지 마세요 X、집중 Y'이런【의도】정정；방향이 완전히 틀리면 대신 사용해야 합니다. kill_work 새로운 마음을 품어보세요。먼저 사용하는 것이 좋습니다 get_worker_output 뭐하는 지 보세요。",
		obj(map[string]any{
			"intent_id": idp("편차 수정 의지 id（= work 처리）"),
			"message":   str("주다 worker 님의 수정요령，무엇을 중지해야 할지 명확히 하세요.、무엇을 의지해야 할까요?"),
		}, "intent_id", "message"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.steerWork == nil {
				return actool.Errorf("steer_work 현재는 이용할 수 없습니다."), nil
			}
			var a struct {
				IntentID json.RawMessage `json:"intent_id"`
				Message  string          `json:"message"`
			}
			_ = json.Unmarshal(in, &a)
			id := pid(a.IntentID)
			if id <= 0 {
				return actool.Errorf("intent_id 필수"), nil
			}
			node, err := t.ts.GetNode(id)
			if err != nil || node == nil || node.Kind != db.KindIntent {
				return actool.Errorf("intent_id 이 작업의 의도가 틀림없습니다（연결된 작업 의도가 읽기 전용입니다.）"), nil
			}
			if strings.TrimSpace(a.Message) == "" {
				return actool.Errorf("message 필수"), nil
			}
			if err := t.steerWork(id, a.Message); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text(fmt.Sprintf("인텐트 전송됨 %d 님 work 수정 지시사항 주입（다음 단계가 적용됩니다.）", id)), nil
		})
}

// getWorkerOutput returns a work's final (or정지 시점 기준) conclusion text by intent id.
func (t *ToolSet) getWorkerOutput() actool.CoreTool {
	return t.readExpTool("get_worker_output", "이 작업을 가져오거나 작업의 의도와 직접 연관시키세요.(work)의 최종 출력 결론。관련 작업 결과 밴드 source_task_id/inherited=true 및 읽기 전용。정상적으로 종료하고 요약을 반환합니다.；종료됨(stopped)/이상해요 work 중단 시점까지의 마지막 출력을 반환합니다.。",
		obj(map[string]any{"intent_id": idp("의도 id（= work 처리）")}, "intent_id"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				IntentID json.RawMessage `json:"intent_id"`
			}
			_ = json.Unmarshal(in, &a)
			id := pid(a.IntentID)
			if id <= 0 {
				return actool.Errorf("intent_id 필수"), nil
			}
			intentNode, err := t.ts.GetNodeWithSources(id)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if intentNode == nil || intentNode.Kind != db.KindIntent {
				return actool.Errorf("intent_id 본 업무 또는 이와 직접적으로 관련된 업무에 속하지 않습니다."), nil
			}
			acts, _, err := t.ts.ActivityListWithSources(id, 0, 1000)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			var chosen, fallback *db.Activity
			for i := range acts {
				switch acts[i].Kind {
				case "result":
					chosen = &acts[i]
					fallback = &acts[i]
				case "text":
					fallback = &acts[i]
				}
			}
			pick := chosen
			if pick == nil {
				pick = fallback
			}
			if pick == nil {
				if intentNode.Inherited {
					return jsonResult(inheritedMap(map[string]any{
						"intent_id": id, "final_text": "（그게 work 아직 출력이 없습니다）",
					}, intentNode.SourceTaskID))
				}
				return actool.Text("（그게 work 아직 출력이 없습니다）"), nil
			}
			detail, _ := t.ts.ActivityDetailWithSources(pick.ID)
			if detail == "" {
				detail = pick.Summary
			}
			result := map[string]any{
				"intent_id": id, "final_text": detail,
				"summary": pick.Summary, "is_error": pick.IsError,
			}
			if intentNode.Inherited {
				inheritedMap(result, intentNode.SourceTaskID)
			}
			return jsonResult(result)
		})
}

// traceSteps renders summary-only trace rows, re-truncating each summary to 100
// chars — the stored summary is capped at 200 for the UI transcript; the trace
// tools want it tighter since a whole work's step list is many rows.
func traceSteps(acts []db.Activity) []map[string]any {
	steps := make([]map[string]any, 0, len(acts))
	for i := range acts {
		step := map[string]any{
			"step_id": acts[i].ID, "kind": acts[i].Kind, "tool": acts[i].Tool,
			"is_error": acts[i].IsError, "summary": firstLine(acts[i].Summary, 100),
		}
		if acts[i].Inherited {
			inheritedMap(step, acts[i].SourceTaskID)
		}
		steps = append(steps, step)
	}
	return steps
}

// getWorkerTrace exposes a work's execution PROCESS (not just its final output):
// list step summaries, keyword-search within one work, or pull full detail of a
// few specific steps. Thinking steps are excluded everywhere.
func (t *ToolSet) getWorkerTrace() actool.CoreTool {
	return t.readExpTool("get_worker_trace",
		"인텐트 보기(work)님【실행과정】（은(와) 다릅니다 get_worker_output 최종 결론만 말씀해 주세요）。세 가지 용도：\n"+
			"① 보내기만 하세요 intent_id → 이거 돌려줘 work 각 단계의 요약 흐름（summary≤100단어，포함 step_id；동작의 개요만，전체 출력이 포함되어 있지 않습니다.）；\n"+
			"② intent_id + q → 키워드에 맞는 단계의 요약만 반환합니다.（요약 및 전체 출력 모두 검색；그래도 줘라 summary，내용을 보려면 사용하세요③）；\n"+
			"③ intent_id + step_ids → 이 단계의 전체 내용을 반환하세요.(detail)；한 번에 최대 가져오기 5 ，초과는 이전에만 반환됩니다. 5 이(가) 있습니다. notice/omitted_step_ids 접수가 안됐다고 알려줬어요。\n"+
			"일반적인 프로세스：먼저①/②의심스러운 단계 찾기 step_id，재사용③전체 출력을 가져옵니다.。아무 생각 없이(thinking)단계。작업 내역과 직접 연계 지원 trace；결과는 source_task_id/inherited=true 및 읽기 전용。",
		obj(map[string]any{
			"intent_id": idp("의도 id（= work 처리）"),
			"q":         str("키워드：요약만 반환/그것을 달성하기 위한 단계의 완전한 출력（선택사항；그리고 step_ids 상호 배타적）"),
			"step_ids":  map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "전체 콘텐츠를 받고 싶습니다. step_id（님으로부터①/②복귀；한 번에 최대 가져오기 5 ，여러 번 패스하면 첫 번째만 반환됩니다. 5 ，나머지는 omitted_step_ids 목록에 있음）"},
			"limit":     intp("요약 스트림/검색 반품 상한（선택사항）"),
		}, "intent_id"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				IntentID json.RawMessage   `json:"intent_id"`
				Q        string            `json:"q"`
				StepIDs  []json.RawMessage `json:"step_ids"`
				Limit    int               `json:"limit"`
			}
			_ = json.Unmarshal(in, &a)
			id := pid(a.IntentID)
			if id <= 0 {
				return actool.Errorf("intent_id 필수"), nil
			}
			intentNode, nodeErr := t.ts.GetNodeWithSources(id)
			if nodeErr != nil {
				return actool.Errorf(nodeErr.Error()), nil
			}
			if intentNode == nil || intentNode.Kind != db.KindIntent {
				return actool.Errorf("intent_id 본 업무 또는 이와 직접적으로 관련된 업무에 속하지 않습니다."), nil
			}
			// ③ detail drill-down by step ids, thinking excluded by the store.
			if len(a.StepIDs) > 0 {
				// Dedup + drop invalid ids first so garbage/duplicates don't eat into
				// the per-call cap. detail is returned in full (untruncated), so the
				// cap bounds one tool result; over the cap we serve the first N and
				// tell the model exactly which ids were deferred, instead of erroring
				// and forcing it to re-plan the call.
				const maxStepIDs = 5
				var ids []int64
				seen := make(map[int64]bool)
				for _, raw := range a.StepIDs {
					if v := pid(raw); v > 0 && !seen[v] {
						seen[v] = true
						ids = append(ids, v)
					}
				}
				var omitted []int64
				if len(ids) > maxStepIDs {
					omitted = append(omitted, ids[maxStepIDs:]...)
					ids = ids[:maxStepIDs]
				}
				acts, err := t.ts.ActivityByIDsWithSources(ids)
				if err != nil {
					return actool.Errorf(err.Error()), nil
				}
				steps := make([]map[string]any, 0, len(acts))
				for i := range acts {
					if acts[i].NodeID == nil || *acts[i].NodeID != id || acts[i].Inherited != intentNode.Inherited ||
						(acts[i].Inherited && acts[i].SourceTaskID != intentNode.SourceTaskID) {
						continue
					}
					step := map[string]any{
						"step_id": acts[i].ID, "kind": acts[i].Kind, "tool": acts[i].Tool,
						"is_error": acts[i].IsError, "detail": acts[i].Detail,
					}
					if acts[i].Inherited {
						inheritedMap(step, acts[i].SourceTaskID)
					}
					steps = append(steps, step)
				}
				result := map[string]any{"intent_id": id, "steps": steps, "returned_step_ids": ids}
				if len(omitted) > 0 {
					// returned_step_ids/omitted_step_ids let the model decide programmatically
					// whether another call is worth it; the notice states the same in prose.
					result["omitted_step_ids"] = omitted
					result["notice"] = fmt.Sprintf(
						"매번 최대 가져오기 %d 단계의 전체 내용，이번에는 예전으로 돌아왔습니다 %d （%v），받지 못함 %d 개별적으로 %v。"+
							"이 내용들이 충분히 자리 잡았다면，그러면 나머지 단계를 수행할 필요가 없습니다.；꼭 계속해야 할 때，이것을 사용하세요 step_id 다시 조정해 보세요。",
						maxStepIDs, len(ids), ids, len(omitted), omitted)
				}
				if intentNode.Inherited {
					inheritedMap(result, intentNode.SourceTaskID)
				}
				return jsonResult(result)
			}
			// ①/② summary stream, optionally keyword-filtered; 100-char summaries.
			var acts []db.Activity
			var err error
			if strings.TrimSpace(a.Q) != "" {
				acts, err = t.ts.ActivityTraceSearchWithSources(id, a.Q, a.Limit)
			} else {
				acts, err = t.ts.ActivityTraceWithSources(id, a.Limit)
			}
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			result := map[string]any{"intent_id": id, "steps": traceSteps(acts)}
			if intentNode.Inherited {
				inheritedMap(result, intentNode.SourceTaskID)
			}
			return jsonResult(result)
		})
}

// searchAllWorkerTraces keyword-searches EVERY work's process in this task — for
// finding what a worker saw but never wrote back as a fact. Returns only matching
// summaries (≤100 chars), each tagged with its intent_id for follow-up drill-down.
func (t *ToolSet) searchAllWorkerTraces() actool.CoreTool {
	return t.readExpTool("search_all_worker_traces",
		"【일반적으로 권장되지 않음，대부분의 정보는 시스템에서 주어지기 때문에】에【이 작업의 다른 사용자 work 실행과정】키워드를 클릭하세요(q)검색——은 특정 항목을 검색하는 데 사용됩니다. worker 봤어、하지만 포함되지 않았습니다 fact 물건（특정 경로/token/오류 보고 등）。"+
			"이 의도에 대한 본인의 발걸음은 자동으로 제외되었습니다.（그것은 이미 당신의 맥락에 있습니다.）。"+
			"히트단계 요약만 반환(summary≤100단어)，각 스트립 intent_id；적절하게 사용하세요 get_worker_trace(intent_id, step_ids=[...]) 전체 콘텐츠 받기。",
		obj(map[string]any{
			"q":     str("키워드（전혀 work 단계 요약+출력 검색 완료）"),
			"limit": intp("반환 상한，기본값 100（선택사항）"),
		}, "q"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Q     string `json:"q"`
				Limit int    `json:"limit"`
			}
			_ = json.Unmarshal(in, &a)
			if strings.TrimSpace(a.Q) == "" {
				return actool.Errorf("q 필수"), nil
			}
			// 발신자 본인의 의사를 배제하는 조치（worker 자신의 것 trace 은(는) 이미 해당 컨텍스트에 있습니다.）。
			acts, err := t.ts.ActivityTraceSearchAllWithSources(t.ownerNode, a.Q, a.Limit)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			hits := make([]map[string]any, 0, len(acts))
			for i := range acts {
				var intent int64
				if acts[i].NodeID != nil {
					intent = *acts[i].NodeID
				}
				hit := map[string]any{
					"intent_id": intent, "step_id": acts[i].ID, "worker": acts[i].Worker,
					"kind": acts[i].Kind, "tool": acts[i].Tool, "is_error": acts[i].IsError,
					"summary": firstLine(acts[i].Summary, 100),
				}
				if acts[i].Inherited {
					inheritedMap(hit, acts[i].SourceTaskID)
				}
				hits = append(hits, hit)
			}
			return jsonResult(map[string]any{"query": a.Q, "hits": hits})
		})
}

// listWorkerTraces gives a worker (which has no graph_overview and can't see the
// intent graph) a lightweight index of the works in this task — intent_id +
// one-line summary + state — so it can DISCOVER which works to inspect via
// get_worker_trace. Without this a worker only knows intent_ids that come back
// from search_all_worker_traces hits. Excludes still-open intents (not yet run →
// no process to inspect).
func (t *ToolSet) listWorkerTraces() actool.CoreTool {
	return t.readExpTool("list_worker_traces",
		"【일반적으로 권장되지 않음，대부분의 정보는 시스템에서 주어지기 때문에】이 작업 목록【이미 실행됨 work（의도）】색인：intent_id + 한문장 방향(summary) + 상태。"+
			"당신(worker)탐사 지도가 보이지 않아요，이를 사용하여 무엇을 알아보세요. work 읽어볼 만한 가치가 있는——재사용 get_worker_trace(intent_id) 단계 보기、get_worker_trace(intent_id, step_ids=[...]) 세부정보 보기。"+
			"실행된 것만 나열(running/done/exhausted/blocked/stopped)，아직 달리지 않은 분은 포함되지 않습니다. open。주의：당신의 임무 경계는 여전히 당신이 받은 의도입니다.，다른거 보세요 work 재사용 관찰용으로만 사용/업무 중복 방지。",
		obj(map[string]any{
			"q":     str("언론 summary 키워드 필터링（선택사항）"),
			"limit": intp("반환 상한，기본값 50（선택사항）"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Q     string `json:"q"`
				Limit int    `json:"limit"`
			}
			_ = json.Unmarshal(in, &a)
			limit := a.Limit
			if limit <= 0 {
				limit = 50
			}
			all, err := t.ts.ListByKindWithSources(db.KindIntent, 500)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			q := strings.ToLower(strings.TrimSpace(a.Q))
			out := make([]map[string]any, 0, limit)
			for _, n := range all {
				if n.Inherited && n.State == "running" {
					continue
				}
				switch n.State {
				case "running", "done", "exhausted", "blocked", "stopped": // has run → has a process
				default:
					continue
				}
				var p map[string]any
				_ = json.Unmarshal(n.Payload, &p)
				summary, _ := p["summary"].(string)
				if q != "" && !strings.Contains(strings.ToLower(summary), q) {
					continue
				}
				item := map[string]any{"intent_id": n.ID, "summary": summary, "state": n.State}
				if n.Inherited {
					inheritedMap(item, n.SourceTaskID)
				}
				out = append(out, item)
				if len(out) >= limit {
					break
				}
			}
			return jsonResult(map[string]any{"works": out})
		})
}

// PlannerTools is the read + intent-generation + goal-judgement tool set.
func (t *ToolSet) PlannerTools() []actool.CoreTool {
	return []actool.CoreTool{
		t.graphOverview(), t.listFindings(), t.listFacts(), t.nodeDetail(),
		// cold-digest §6.1: restore folded cold nodes (digest body → members → detail).
		t.expandDigest(),
		t.getWorkerOutput(), t.getWorkerTrace(), t.searchAllWorkerTraces(), t.listGoals(), t.addIntent(), t.proveGoal(), t.goalMet(),
		t.killWorkTool(), t.steerWorkTool(),
		// report_finding：기획 상황 분석 및 판단 과정에서 취약점을 확인한 경우，직접 등록 가능（그리고 worker 동일한 도구）。
		t.addFinding(),
		// list_companies：업체 목록 보기 + scope + 자산수（받아가세요 company_id / 소유권 범위를 이해합니다.）。
		t.listCompanies(),
		// list_assets：기획하실 때 눌러주세요 DSL 전체 자산 라이브러리 검색（협력 list_untested_assets 님"범위 내에서 측정되지 않음"관점，
		// 보충"도메인 이름별/지문/포트/상태코드 및 기타 조건을 전체 데이터베이스에서 확인 가능"의 능력）。
		t.listAssets(),
		// add_company_scope：계획할 때 도메인 이름을 넣을 수 있습니다/IP/CIDR/ICP/키워드는 회사의 자산 범위에 포함됩니다.（히트 자산 자동 청구）。
		t.addCompanyScope(),
		// add_task_scope：루트 도메인 전체를 주도적으로 변경하십시오./회사 전체/특정 하위 도메인/IP 이 작업의 테스트 범위에 포함됩니다.(적용 범위 분모)。
		t.addTaskScope(),
		// list_untested_assets：요청 시 본 업무 범위 내 미측정 자산 확인(유형+페이징)，스스로 결정하여 보충검사를 받아보세요。
		t.listUntestedAssets(),
	}
}
