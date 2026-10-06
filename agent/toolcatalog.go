package agent

import (
	"context"
	"encoding/json"

	actool "github.com/Autumn-27/norma/tool"
)

// 이 문서「내장 도구」순수 코드에서 열거 가능한 코드로、할 수 있습니다 DB 디렉터리 포함：
//   - BuiltinToolSeeds()：셋을 실행하라 agent 의 내장 도구 세트는 다음으로 확장됩니다. seed 기록（key +
//     설명 + 매개변수 schema + 기본 바인딩 agent），서버가 멱등성 시드를 시작하려면 tools 테이블。
//   - ToolResolve 후크：실행시 누르세요 DB 에 tools 조립된 도구에서 이 작업을 수행해도 됩니다.「언론 agent 필터 +
//     설명 재정의/schema + 주입 매개변수 기본값」。key/handler 아직은 코드 수준，DB 변경만 가능「산문 및 기본값」。
// handler（Call 행동）은 항상 코드에서 옵니다.——DB 변경할 수 없습니다，모델에 표시되는 설명 및 기본 입력 매개변수만 변경할 수 있습니다.。

// ToolSeed 은 기본 제공 도구의 시드 가능한 스냅샷입니다.：key 그렇죠 CoreTool.Name()（그리고 handler 묶여 죽음，
// UI 읽기 전용），Desc/Schema 코드의 도구 정의에서 가져옴，Agents 은 코드가 기본적으로 제공하는 것입니다. agent。
type ToolSeed struct {
	Key    string         // = CoreTool.Name()，기본 키，변경할 수 없습니다.
	Desc   string         // 최상위 설명（사용 가능 UI 재정의）
	Schema map[string]any // 매개변수 JSON-Schema（구조 읽기 전용，description/default 사용 가능 UI 변경）
	Agents []string       // 기본 바인딩 agent key（worker/planner/mainagent）
}

// builtinToolsByAgent 하나만 사용하세요「읽기 전용 빈 쉘」ToolSet（nil stores）각 실행 구성 agent 님
// 도메인 도구 세트。도구 생성자는 클로저만 삽입합니다. Spec、생성 중 역참조 없음 store，그래서 nil 보안——
// 이 도구는 여기서는 읽기용으로만 사용됩니다. Name()/Description()/InputSchema()，절대 안됨 Call。
//
// 일부러【포함되지 않음】SDK 일반 도구 actool.DefaultTools()（Read/Write/Edit/MultiEdit/LS/Glob/
// Grep/Bash）：각각 agent 다들 영구적으로 가지고 있어요、아니요「누구에게 묶여 있는가」님의 선택，그리고 설명은 대부분 Prompt()
// 내부（이 표에는 다음 내용만 포함됩니다. Description()，은 보도의 절반만 오해하게 만들 것입니다.）。아니요 seed → 없음 DB 알았어 → ToolResolve
// 있는 그대로 릴리스、보장되지 않음，동작은 이전과 동일합니다.。만 artex 자체 도메인 도구를 테이블에서 관리 가능。
func builtinToolsByAgent() map[string][]actool.CoreTool {
	ts := NewToolSet(nil, "")
	return map[string][]actool.CoreTool{
		"mainagent": ts.MainAgentTools(),
		"planner":   ts.PlannerTools(),
		"worker":    ts.WorkerTools(),
		// goals（타겟 디스어셈블러）기본 바운드 set_goals + set_constraints：그들에게 의지하여 목표물을 철거하세요、
		// 추출된 작업 제약 조건을 라이브러리에 씁니다.。그리고 mainagent 동일한 관리 도구 공유，web 단말기 설명을 변경할 수 있습니다./schema、언론 agent 확인。
		"goals": {ts.setGoals(), ts.setConstraints()},
		// auto 취약점 보고는 기본적으로 바인딩됩니다. + 자산 관리 도구，다른 도메인 도구는 다음에서 사용할 수 있습니다. UI 필요에 따라 확인하세요.。
		// 새로운 도서관이 왔습니다 seed 쓰기；라오 쿠유 seedAutoDefaultBindings 마이그레이션。
		"auto": {ts.addFinding(), ts.insertAssets(), ts.addCompanyScope(), ts.listAssets(), ts.listCompanies()},
		// pentest（독립적 침투 agent）기본 바운드：자산 확인 / 자산 삽입 / 취약점 보고 / 취약점 확인 / 회사를 확인해보세요。
		// 새로운 도서관이 왔습니다 seed 쓰기；라오 쿠유 seedPentestDefaultBindings 마이그레이션。
		"pentest": {ts.listAssets(), ts.insertAssets(), ts.addFinding(), ts.listFindings(), ts.listCompanies()},
	}
}

// defaultUnbound：이것들은 system 도구는 평소와 같이 디렉토리에 입력됩니다.（web 표시됨、수동으로 누를 수 있음 agent 확인），하지만
// 기본값【아무것도 묶지 마세요 agent】——ToolResolve null에 바인딩된 도구는 모든 도구에 유효합니다. agent 모두 삭제,명시적이어야 합니다. opt-in。
// 아직도 특정 곳에 머물고 있는 이유 agent 님 base 도구 세트에서( goal_met 에 PlannerTools):먼저 보자 seed 예
// 구성하여 얻으세요. desc/schema,두 번째는 사용자가 수동으로 다시 바인딩한 후의 런타임입니다. base 거기 있어요、ToolResolve 그래야만 지킬 수 있지。
//
// goal_met：하나씩 바이패스 prove_goal、글로벌에서 직접 발표【전체 미션이 완료되었습니다】,무거운 무게와 오판의 위험,다시
// 「prove_goal 마지막 목표를 표시하세요 → 자동종료」반복,따라서 기본값은 아무 것도 제공하지 않는 것입니다. agent,필요하면 수동으로 바인딩하세요.。
var defaultUnbound = map[string]bool{"goal_met": true}

// BuiltinToolSeeds 각각 넣어주세요 agent 의 병합을 위한 내장 도구 세트 seed 목록：같은 이름의 도구（ list_assets
// 여러개 agent 모두）하나로 합쳐진다，Agents 노조를 잡아라；defaultUnbound 의 도구는 강제로 비어 있게 바인딩됩니다.。
func BuiltinToolSeeds() []ToolSeed {
	byAgent := builtinToolsByAgent()
	order := []string{"mainagent", "goals", "planner", "worker", "auto", "pentest"}

	type acc struct {
		tool   actool.CoreTool
		agents []string
	}
	m := map[string]*acc{}
	var keys []string
	for _, ak := range order {
		for _, t := range byAgent[ak] {
			a, ok := m[t.Name()]
			if !ok {
				a = &acc{tool: t}
				m[t.Name()] = a
				keys = append(keys, t.Name())
			}
			a.agents = append(a.agents, ak)
		}
	}

	out := make([]ToolSeed, 0, len(keys))
	for _, k := range keys {
		a := m[k]
		agents := a.agents
		if defaultUnbound[k] {
			agents = []string{} // 디렉토리를 입력하세요、수동으로 묶을 수 있음，그러나 기본값은 아무것도 제공하지 않는 것입니다. agent（저장 [] 대신 null，다른 도구와 일관성）
		}
		out = append(out, ToolSeed{
			Key:    k,
			Desc:   a.tool.Description(),
			Schema: a.tool.InputSchema(),
			Agents: agents,
		})
	}
	return out
}

// ToolResolve, if set, post-processes an agent's fully-assembled tool list against
// the DB tools table: it drops tools not bound to this agent (or globally disabled)
// and wraps the rest so the model sees the DB-overridden description/schema and
// 기본 입력 매개변수 get injected. Tools with no matching DB row (MCP/skill/host tools like
// traffic) pass through untouched. nil = tools unchanged. Wired in server/assembly.go.
var ToolResolve func(ctx context.Context, agentKey string, tools []actool.CoreTool) []actool.CoreTool

// DecorateTool wraps t so Description()/InputSchema() report the DB overrides and
// Call() injects scalar parameter defaults (from schema's "default" props) whenever
// the model omitted them. Name/Prompt/permission/scheduler flags delegate to t, so
// the tool's identity and handler are unchanged. Empty desc/schema fall back to t's.
func DecorateTool(t actool.CoreTool, desc string, schema map[string]any) actool.CoreTool {
	if desc == "" {
		desc = t.Description()
	}
	if len(schema) == 0 {
		schema = t.InputSchema()
	}
	return &overriddenTool{CoreTool: t, desc: desc, schema: schema}
}

// overriddenTool is a CoreTool decorator: it embeds the original (so all behavioral
// methods — Prompt/IsReadOnly/IsConcurrencySafe/CheckPermissions/Name — delegate)
// and overrides only the model-facing description/schema plus default injection.
type overriddenTool struct {
	actool.CoreTool
	desc   string
	schema map[string]any
}

func (o *overriddenTool) Description() string         { return o.desc }
func (o *overriddenTool) InputSchema() map[string]any { return o.schema }

func (o *overriddenTool) Call(ctx context.Context, in json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	return o.CoreTool.Call(ctx, injectDefaults(in, o.schema), tc)
}

// injectDefaults fills scalar parameter defaults declared in the (possibly edited)
// schema into the input JSON whenever the model omitted the field or left it empty/
// null. Structure (names/types/required) is untouched — only기본값 are merged in.
func injectDefaults(in json.RawMessage, schema map[string]any) json.RawMessage {
	defs := scalarDefaults(schema)
	if len(defs) == 0 {
		return in
	}
	m := map[string]json.RawMessage{}
	if len(in) > 0 {
		if err := json.Unmarshal(in, &m); err != nil {
			return in // non-object input: don't touch it
		}
	}
	changed := false
	for k, dv := range defs {
		if cur, ok := m[k]; !ok || isEmptyJSON(cur) {
			m[k] = dv
			changed = true
		}
	}
	if !changed {
		return in
	}
	b, err := json.Marshal(m)
	if err != nil {
		return in
	}
	return b
}

// scalarDefaults extracts properties[k]["default"] for scalar params (string/
// integer/number/boolean). Array/object defaults are skipped: merging them is
// ambiguous and not worth the surprise.
func scalarDefaults(schema map[string]any) map[string]json.RawMessage {
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		return nil
	}
	out := map[string]json.RawMessage{}
	for name, raw := range props {
		p, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		dv, ok := p["default"]
		if !ok || dv == nil {
			continue
		}
		switch p["type"] {
		case "string", "integer", "number", "boolean":
			if b, err := json.Marshal(dv); err == nil {
				out[name] = b
			}
		}
	}
	return out
}

func isEmptyJSON(raw json.RawMessage) bool {
	s := string(raw)
	return s == "null" || s == `""`
}
