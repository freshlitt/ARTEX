package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

// jsonResult marshals v to a JSON tool result.
func jsonResult(v any) (actool.Result, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}
	return actool.Text(string(b)), nil
}

// 이 문서는 P2「교차 작업 조정 도구 세트」(docs/러닝 스코어 정리 §2 P2)。이것들은 host 도구——필수
// 방문 Manager(어떤 작업이든 Store)、Engine(잠시 멈춤)、및 빌드 작업 프로세스,그래서 살아요 server 레이어。
// 읽기 도구「기존 per-task 도구」대상 작업으로 리디렉션 store 달려라(임시 구축 ToolSet
// 그리고 Call 해당 도구),따라서 정확히 동일한 논리를 재사용합니다.;제어 클래스(spawn/pause)직접 조정 Manager/Engine。
// 교통수단 같아요 seed 들어가세요 tools 테이블、언론 agent 바인딩(약정으로만 연결됨 agent 보이는 사람만 볼 수 있음)。

// hostTools is the runtime host-tool provider fed to ToolAugment: traffic tools
// (gated by capture) + cross-task orchestration tools + user-defined custom tools.
// The second return is the names of custom tools flagged `deferred` (schema
// withheld, routed via SearchExtraTools/ExecuteExtraTool). Per-agent binding still
// decides who actually sees any of them.
//
//nolint:unused // used as the hostTools provider in wireAgentAugment
func (s *Server) hostTools() ([]actool.CoreTool, map[string][]string) {
	tools := append(s.m.HostTools(), s.orchestrationTools()...)
	tools = append(tools, s.findingRetestTools()...)
	tools = append(tools, s.platformTools()...) // 플랫폼 운영 도구(재건 skill/도구/MCP，주다 Auto 사용)
	custom, err := s.customTools()
	if err != nil {
		log.Printf("[custom-tool] 로딩 실패: %v", err)
		return tools, nil
	}
	tools = append(tools, custom...)
	// deferred custom tools → name -> its bound agent keys. ToolAugment turns a
	// name into a deferred entry only for agents it's actually bound to (so we don't
	// advertise a tool the per-agent binding will drop from the callable set).
	deferred := map[string][]string{}
	rows, _ := s.m.pg.ListCustomTools()
	for _, t := range rows {
		if t.Deferred && t.Enabled {
			deferred[t.Key] = t.Agents
		}
	}
	return tools, deferred
}

// orchestrationTools returns the cross-task tool set. Bound per-agent via the
// tools table (default: no binding — opt-in for orchestration agents).
func (s *Server) orchestrationTools() []actool.CoreTool {
	return []actool.CoreTool{
		s.toolListTasks(),
		s.toolListLLMProfiles(),
		s.toolSpawnTask(),
		s.toolPauseTask(),
		s.toolGetTaskGraph(),
		s.toolListTaskFindings(),
		s.toolAddHint(),
		s.toolGetWorkerTrace(),
		s.toolListWorkerTraces(),
		s.toolSearchWorkerTraces(),
		s.toolGetTaskNodeDetail(),
		s.toolUpdateFindingReport(),
		s.toolGetFindingTraffic(),
		s.toolBindFindingTraffic(),
	}
}

// --- schema helpers ---

func strParam(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

// parseProfileID reads an LLM profile id from a tool arg that may arrive as a JSON
// number (5) or a numeric string ("5"); returns 0 when absent/unparseable.
func parseProfileID(raw json.RawMessage) int64 {
	if len(raw) == 0 {
		return 0
	}
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		v, _ := strconv.ParseInt(strings.TrimSpace(str), 10, 64)
		return v
	}
	return 0
}

func objSchema(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		req := make([]any, len(required))
		for i, r := range required {
			req[i] = r
		}
		m["required"] = req
	}
	return m
}

func roTool(name, desc string, schema map[string]any, run func(context.Context, json.RawMessage) (actool.Result, error)) actool.CoreTool {
	return actool.Build(actool.Spec{
		Name: name, Description: desc, Schema: schema,
		ReadOnly:   func(json.RawMessage) bool { return true },
		Concurrent: func(json.RawMessage) bool { return true },
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(ctx context.Context, in json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
			return run(ctx, in)
		},
	})
}

func wrTool(name, desc string, schema map[string]any, run func(context.Context, json.RawMessage) (actool.Result, error)) actool.CoreTool {
	return actool.Build(actool.Spec{
		Name: name, Description: desc, Schema: schema,
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(ctx context.Context, in json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
			return run(ctx, in)
		},
	})
}

// delegateToTask resolves the `task_id` in the input, builds a ToolSet bound to
// that task's store, strips task_id, and calls the chosen per-task tool — so the
// cross-task read reuses the exact in-task logic against another task.
func (s *Server) delegateToTask(ctx context.Context, in json.RawMessage, pick func(*agent.ToolSet) actool.CoreTool) (actool.Result, error) {
	var head struct {
		TaskID string `json:"task_id"`
	}
	_ = json.Unmarshal(in, &head)
	if strings.TrimSpace(head.TaskID) == "" {
		return actool.Errorf("task_id 이 필요합니다"), nil
	}
	t, ok := s.m.Task(head.TaskID)
	if !ok {
		return actool.Errorf("task 이 존재하지 않습니다: " + head.TaskID), nil
	}
	var m map[string]json.RawMessage
	_ = json.Unmarshal(in, &m)
	delete(m, "task_id")
	inner, _ := json.Marshal(m)
	tsx := agent.NewToolSet(t.Store, "orchestrator")
	if s.m.Assets() != nil {
		tsx.SetAssetStore(s.m.Assets(), s.m.Assets().Companies())
	}
	tsx.SetNotify(t.Notify)         // 범용 깨우기（전용 콜백 없이 쓰기 작업이 진행됩니다.；독서 도구는 no-op）
	tsx.SetNotifyHint(t.NotifyHint) // add_hint → 하나만 기억하세요「명 추가됨 N 전략 팁：…」트리거 및 깨우기 planner
	return pick(tsx).Call(ctx, inner, nil)
}

// --- tools ---

func (s *Server) toolListTasks() actool.CoreTool {
	return roTool("list_tasks",
		"모든 작업 나열(id/설명/대상/상태/실행시간/학부모 과제/LLM 구성)，편곡 agent 전체적인 상황 파악에 활용하세요、너무 오래 멈춰있는 작업을 확인하세요.、어느 것을 사용해야 하나요? LLM。실행시간：달리고 있다=생성→지금，최종 상태=생성→마지막 이벤트(초)。llm_profile：임무 planner/worker 사용된 구성 이름，(구성 활성화)=글로벌 활성화를 따르세요.。",
		objSchema(map[string]any{}),
		func(context.Context, json.RawMessage) (actool.Result, error) {
			lastAct, _ := s.m.PG().LastActivityAll()
			// id -> name to resolve each task's pinned LLM profile.
			profName := map[int64]string{}
			if profs, err := s.m.pg.ListProfiles(); err == nil {
				for _, p := range profs {
					profName[p.ID] = p.Name
				}
			}
			out := make([]map[string]any, 0)
			for _, t := range s.m.List() {
				status := s.deriveTaskStatus(t)
				end := lastAct[t.ExpID]
				if live := s.engine.LastActivity(t.ID); live > end {
					end = live
				}
				dur := int64(0)
				if status == "running" {
					dur = time.Now().Unix() - t.CreatedAt
				} else if end > t.CreatedAt {
					dur = end - t.CreatedAt
				}
				row := map[string]any{"id": t.ID, "description": t.Description, "goal": t.Goal, "status": status, "run_seconds": dur}
				if t.ParentRef != "" {
					row["parent_ref"] = t.ParentRef
				}
				llmState := t.llmStateSnapshot()
				if llmState.ProfileID == nil {
					row["llm_profile"] = "(구성 활성화)"
				} else if n, ok := profName[*llmState.ProfileID]; ok {
					row["llm_profile"] = n
				} else {
					row["llm_profile"] = fmt.Sprintf("#%d(삭제됨)", *llmState.ProfileID)
				}
				out = append(out, row)
			}
			return jsonResult(out)
		})
}

// toolListLLMProfiles lists the available LLM profiles (name/model/active) so an
// orchestration agent can pick one for spawn_task's llm_profile. Never leaks keys.
func (s *Server) toolListLLMProfiles() actool.CoreTool {
	return roTool("list_llm_profiles",
		"목록 사용 가능 LLM 구성(profile)：id、이름、모델、형식、현재 활성화된 구성인가요?。사용 id 주다 spawn_task 님 llm_profile_id 매개변수 지정 하위 작업 제외 LLM（정찰용 저가 모델 같은거요、강력한 모델 활용）。포함되지 않음 API Key。",
		objSchema(map[string]any{}),
		func(context.Context, json.RawMessage) (actool.Result, error) {
			profs, err := s.m.pg.ListProfiles()
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			out := make([]map[string]any, 0, len(profs))
			for _, p := range profs {
				out = append(out, map[string]any{
					"id": p.ID, "name": p.Name, "model": p.Model, "format": p.Format, "is_active": p.IsDefault,
				})
			}
			return jsonResult(map[string]any{"profiles": out})
		})
}

func (s *Server) toolSpawnTask() actool.CoreTool {
	return wrTool("spawn_task",
		"새 하위 작업을 생성하고 탐색 엔진을 시작합니다.，복귀 task_id。은 한 가지를 넣을 때 사용됩니다.(질문처럼/목표)독립적인 업무에 배정됨。parent_ref 선택사항：현재 계약과 관련된 상위 작업을 입력하세요. id 부자관계를 맺으세요。",
		objSchema(map[string]any{
			"description":            strParam("작업 설명(짧은 제목)"),
			"goal":                   strParam("미션목표(달성해야 할 것)"),
			"parent_ref":             strParam("선택사항：학부모 과제 id(부자관계를 맺으세요)"),
			"source_task_ids":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": fmt.Sprintf("선택사항：읽기 전용 상속 소스 작업 id 목록(대부분 %d )。하위 작업은 해당 작업에서 검색된 자산을 읽기 전용 참조할 수 있습니다./결론을 출발점으로 삼아；그리고 parent_ref 의 순수 부모-자식 포인터가 다릅니다.，내용 상속입니다.。", db.MaxTaskSourceCount)},
			"llm_profile_id":         map[string]any{"type": "integer", "description": "선택사항：이 하위 작업을 지정하세요. planner/worker 사용됨 LLM 구성 id(또 만나요 list_llm_profiles)；상위 작업을 상속하려면 비워 두세요.、전역 활성화 구성으로 돌아가기"},
			"timeout_seconds":        map[string]any{"type": "integer", "description": "선택사항：작업 수준 시간 초과(초)。지점 도달 후 우아한 엔딩을 발동하고 입장합니다. timeout 최종 상태；공백으로 두거나 0 = 시간 제한 없음"},
			"plan_heartbeat_seconds": map[string]any{"type": "integer", "description": "선택사항：planner 하트비트 트리거 간격(초)。마지막 기획이 끝났습니다/작업이 이 값에 도달하기 시작하고 해당 기간 동안 트리거가 없습니다. → 일련의 계획을 시작하세요(교착상태 + 일어나서 비행을 감독하다 worker)。공백으로 두거나 0 = 기본값 600(10min)；"},
			"seed_first_intent":      map[string]any{"type": "boolean", "description": "선택사항：간단한 작업에 사용 가능，작성 시 직접 시드 의도를 보내주세요.(내용=설명+대상)하자 worker 1라운드를 기다릴 필요가 없습니다. planner 직접 테스트 시작；기본값 false(기준을 따르고, 먼저 계획하고 실행하세요.)。"},
		}, "description", "goal"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Description          string          `json:"description"`
				Goal                 string          `json:"goal"`
				ParentRef            string          `json:"parent_ref"`
				SourceTaskIDs        []string        `json:"source_task_ids"`
				LLMProfileID         json.RawMessage `json:"llm_profile_id"`
				TimeoutSeconds       int             `json:"timeout_seconds"`
				PlanHeartbeatSeconds int             `json:"plan_heartbeat_seconds"`
				SeedFirstIntent      bool            `json:"seed_first_intent"`
			}
			_ = json.Unmarshal(in, &a)
			if strings.TrimSpace(a.Description) == "" {
				a.Description = "이름 없는 작업"
			}
			if strings.TrimSpace(a.Goal) == "" {
				return actool.Errorf("goal 이 필요합니다"), nil
			}
			if a.TimeoutSeconds < 0 {
				a.TimeoutSeconds = 0
			}
			// 읽기 전용 상속 소스 작업：최대수량 + 각 id 유효/중복 제거/존재합니다，확인 규칙 및 HTTP 일관된 빌드 작업。
			if len(a.SourceTaskIDs) > db.MaxTaskSourceCount {
				return actool.Errorf(fmt.Sprintf("가장 많이 선택된 관련 업무 %d ", db.MaxTaskSourceCount)), nil
			}
			sourceIDs := make([]int64, 0, len(a.SourceTaskIDs))
			seenSources := map[int64]bool{}
			for _, raw := range a.SourceTaskIDs {
				id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
				if err != nil || id <= 0 || seenSources[id] {
					return actool.Errorf("관련업무 id 잘못되었거나 중복되었습니다."), nil
				}
				if _, ok := s.m.Task(strconv.FormatInt(id, 10)); !ok {
					return actool.Errorf(fmt.Sprintf("관련업무 #%d 이 존재하지 않습니다", id)), nil
				}
				seenSources[id] = true
				sourceIDs = append(sourceIDs, id)
			}
			// LLM profile resolution: explicit id > inherit parent's pin > active(nil).
			var pin *int64
			if id := parseProfileID(a.LLMProfileID); id > 0 {
				if _, ok := s.loadProfileConfig(id); !ok {
					return actool.Errorf(fmt.Sprintf("LLM 구성 #%d 이 존재하지 않거나 설정되지 않았습니다. API Key", id)), nil
				}
				pin = &id
			} else if a.ParentRef != "" {
				if pt, ok := s.m.Task(a.ParentRef); ok {
					pin = pt.LLMProfileID
				}
			}
			var llmIDs []int64
			if pin != nil {
				llmIDs = []int64{*pin}
			}
			t, err := s.m.CreateTaskWithOptions(a.Description, a.Goal, db.TaskCreateOptions{
				SourceTaskIDs:        sourceIDs,
				LLMProfileIDs:        llmIDs,
				TimeoutSeconds:       a.TimeoutSeconds,
				PlanHeartbeatSeconds: a.PlanHeartbeatSeconds,
			})
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if a.ParentRef != "" {
				t.ParentRef = a.ParentRef
				if id, e := strconv.ParseInt(t.ID, 10, 64); e == nil {
					_ = s.m.PG().SetParentRef(id, a.ParentRef)
				}
			}
			// 시공 후 프로세스 공유,그리고 HTTP 작업 생성(server.go createTask)같은 문단 재사용 launchTask:
			// seed + 백그라운드에서 눈에 띄게 타겟 분해를 수행합니다.(아니요.0휠/LLM단계/하나씩goal) + engine.Run。
			// seed_first_intent 기본값 false(표준을 먼저 계획하고 실행해야 합니다.);간단한 작업을 활성화하고 직접 보낼 수 있습니다. work 테스트。
			s.launchTask(t, a.Description+" "+a.Goal, a.SeedFirstIntent)
			return actool.Text(fmt.Sprintf("task created: %s", t.ID)), nil
		})
}

func (s *Server) toolPauseTask() actool.CoreTool {
	return wrTool("pause_task", "지정된 작업을 일시 중지합니다.(그만해 planner/worker 루프)。",
		objSchema(map[string]any{"task_id": strParam("작업을 일시 중지합니다. id")}, "task_id"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				TaskID string `json:"task_id"`
			}
			_ = json.Unmarshal(in, &a)
			t, ok := s.m.Task(a.TaskID)
			if !ok {
				return actool.Errorf("task 이 존재하지 않습니다: " + a.TaskID), nil
			}
			if _, err := s.applyTaskControlWithCause(t, "pause", agent.AbortPausedByOrchestrator); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text("task paused: " + a.TaskID), nil
		})
}

func (s *Server) toolGetTaskGraph() actool.CoreTool {
	return roTool("get_task_graph", "지정된 작업의 탐색 지도 개요를 읽습니다.(마찬가지예요 graph_overview：자산수/frontier/찾음/취재 등)，사용 task_id 작업 지정。",
		objSchema(map[string]any{"task_id": strParam("임무 id")}, "task_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).GraphOverviewTool)
		})
}

func (s *Server) toolListTaskFindings() actool.CoreTool {
	return roTool("list_task_findings", "지정된 작업의 취약점 확인 읽기(포함 flag/PoC；각 스트립 id/task_id/intent_id/vulnclass/severity/요약/상태)，사용 task_id 작업 지정。",
		objSchema(map[string]any{"task_id": strParam("임무 id")}, "task_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).ListFindingsTool)
		})
}

func (s *Server) toolAddHint() actool.CoreTool {
	return wrTool("add_task_hint", "지정된 업무에 전략적 힌트를 주입(임무 planner 다음 인텐트 생성 라운드에서 읽혀질 것입니다.)。\n"+
		"★일괄 우선순위：팁을 여러개 넣어보세요 hints 배열은 한 번 제출됩니다.（복귀 ids 배열，그리고 hints 길이 동일, 순서 동일，실패한 항목 id=0）；단일항목 생략 hints 최상위 레벨로 직접 연결 text。",
		objSchema(map[string]any{
			"task_id":      strParam("임무 id"),
			"hints":        map[string]any{"type": "array", "description": "【이것을 먼저 사용하세요】프롬프트 배열，각 요소 필드는 최상위 수준과 동일합니다.（text/asset_ids/traffic_refs）。", "items": objSchema(map[string]any{"text": strParam("프롬프트 내용"), "asset_ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}, "traffic_refs": agent.HintTrafficSchema()})},
			"text":         strParam("[싱글] 프롬프트 내용"),
			"traffic_refs": agent.HintTrafficSchema(),
			"asset_ids":    map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "고정 자산 id（선택사항，0/1/여러개；이 작업 내의 자산 id）"},
		}, "task_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).AddHintTool)
		})
}

func (s *Server) toolGetWorkerTrace() actool.CoreTool {
	return roTool("get_task_worker_trace",
		"지정된 작업 중 하나를 살펴보세요. work(의도)실행과정：get_task_worker_trace(task_id, intent_id) 단계 요약 보기；또 가져와 step_ids=[...] 해당 단계의 전체 내용을 확인하세요.(최대 한 번 5 ,여러 번 패스하면 첫 번째만 반환됩니다. 5 )。",
		objSchema(map[string]any{
			"task_id":   strParam("임무 id"),
			"intent_id": map[string]any{"type": "integer", "description": "의도 id(이 작업에서는 work)"},
			"step_ids":  map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "선택사항：완전한 콘텐츠를 얻는 단계 id(최대 한 번 5 ,여러 번 패스하면 첫 번째만 반환됩니다. 5 ,나머지는 omitted_step_ids 목록에 있음)"},
		}, "task_id", "intent_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).GetWorkerTraceTool)
		})
}

func (s *Server) toolListWorkerTraces() actool.CoreTool {
	return roTool("list_task_worker_traces", "지정된 작업에서 실행된 작업을 나열합니다. work(의도) + 각 단계 수，은 무엇을 검색하는 데 사용됩니다. work 읽어볼 만한 가치가 있는(재사용 get_task_worker_trace)。",
		objSchema(map[string]any{"task_id": strParam("임무 id")}, "task_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).ListWorkerTracesTool)
		})
}

func (s *Server) toolSearchWorkerTraces() actool.CoreTool {
	return roTool("search_task_worker_traces", "키워드로 모든 업무 검색 work 실행과정(반환 히트 단계 요약 + intent_id)。",
		objSchema(map[string]any{"task_id": strParam("임무 id"), "q": strParam("키워드 검색")}, "task_id", "q"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).SearchWorkerTracesTool)
		})
}

func (s *Server) toolGetTaskNodeDetail() actool.CoreTool {
	return roTool("get_task_node_detail",
		"지정된 작업에서 탐색 그래프 노드의 전체 내용을 읽습니다.(찾음/사실/의도/대상：요약 + 세부사항/증거/PoC)。id 은 탐사 노드입니다. id( report_finding 복귀、또는 list_task_findings 에 id)。취약점 보고서를 작성하기 전에 취약점에 대한 완전한 증거를 얻으려면 이를 사용하십시오.。",
		objSchema(map[string]any{
			"task_id": strParam("임무 id"),
			"id":      map[string]any{"type": "integer", "description": "그래프 노드 탐색 id(비자산 id)"},
		}, "task_id", "id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).NodeDetailTool)
		})
}

// toolUpdateFindingReport writes/overwrites a finding's detailed Markdown report.
// finding_id is the id report_finding returned ("finding recorded: <id>", the
// finding node id). The write (SetFindingReportByNodeID) is keyed by node_id and
// task-agnostic, so this host tool needs no task_id / exploration store.
func (s *Server) toolUpdateFindingReport() actool.CoreTool {
	return wrTool("update_finding_report",
		"등록된 취약점에 대해 작성하세요./업데이트【상세보고】(Markdown 전문,전체 단락이 이전 내용을 덮어씁니다.)。finding_id 합격 report_finding 돌아온 사람 id(\"finding recorded: <id>\" 의 번호)。보고서 권장 사항에는 다음이 포함됩니다.:취약점 개요、영향과 피해、재생산 단계、증거/PoC、수리 제안。",
		objSchema(map[string]any{
			"finding_id":       map[string]any{"type": "integer", "description": "대상 취약점 id(report_finding 반환됨 id)"},
			"report":           strParam("세부보고서 전문,Markdown 형식"),
			"evidence_version": map[string]any{"type": "integer", "description": "get_finding_traffic 증거자료 반환 version；보고서에서 새로운 증거 변경 사항이 포함되지 않도록 방지하는 데 사용됩니다."},
		}, "finding_id", "report"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				EvidenceVersion *int64          `json:"evidence_version"`
				FindingID       json.RawMessage `json:"finding_id"`
				Report          string          `json:"report"`
			}
			_ = json.Unmarshal(in, &a)
			nodeID := parseProfileID(a.FindingID) // 재사용「숫자 또는 숫자 문자열」분석
			if nodeID <= 0 {
				return actool.Errorf("finding_id 유효하지 않음"), nil
			}
			n, err := s.m.pg.SetFindingReportVersionByNodeID(ctx, nodeID, a.Report, a.EvidenceVersion)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if n == 0 {
				return actool.Errorf(fmt.Sprintf("찾을 수 없음 finding_id=%d 해당 취약점 레코드(먼저 사용해 보세요 report_finding 등록)", nodeID)), nil
			}
			return actool.Text(fmt.Sprintf("finding %d report updated (%d chars)", nodeID, len(a.Report))), nil
		})
}

// deriveTaskStatus mirrors listTasks' status derivation for the list_tasks tool.
func (s *Server) deriveTaskStatus(t *Task) string {
	lifecycle := t.lifecycleSnapshot()
	switch {
	case isTerminalStatus(lifecycle.Status):
		return lifecycle.Status
	case lifecycle.Paused || s.engine.IsPaused(t.ID):
		return "paused"
	case s.engine.ReadyFor(t) && s.engine.Started(t.ID):
		return "running"
	}
	return "created"
}

// orchestrationToolSeeds seeds the cross-task tools into the tools table so they
// are bindable per-agent (default: bound to nobody — opt-in for orchestration
// agents). First-insert only, like the traffic seeds.
func (s *Server) seedOrchestrationTools() {
	// task-op + platform tools default-bind to the built-in Auto agent (이렇게 탄생했습니다
	// 운영 플랫폼)。SeedTool 첫 번째 삽입이 적용됩니다.;오래된 도서관에는 seed 이 노를 젓습니다. seedAutoDefaultBindings 다시 묶음。
	autoAgents, _ := json.Marshal([]string{"auto"})
	for _, t := range s.orchestrationTools() {
		schema, _ := json.Marshal(t.InputSchema())
		bindings := autoAgents
		if t.Name() == "bind_finding_traffic" {
			bindings = json.RawMessage(`["reporter"]`)
		}
		_ = s.m.PG().SeedTool(t.Name(), t.Description(), schema, bindings)
	}
	for _, t := range s.platformTools() {
		schema, _ := json.Marshal(t.InputSchema())
		_ = s.m.PG().SeedTool(t.Name(), t.Description(), schema, autoAgents)
	}
	s.refreshBuiltinToolSchemas()
	s.seedAutoDefaultBindings()
	s.seedPlannerDefaultBindings()
	s.seedPlannerListAssetsBinding()
	s.seedCompanyScopeRebind()
	s.seedWorkerReadToolsUnbind() // list_facts/list_companies/list_worker_traces 님으로부터 worker 기본적으로 바인딩 해제(일회용)
	s.seedWorkerReadbackRebind()  // 이전 마이그레이션을 실수로 삭제하는 문제 수정：넣어보세요 search_all_worker_traces/get_worker_trace/node_detail 화장하고 다시 묶으세요 worker(일회용)
	s.seedAutoReportFindingBinding()
	s.unbindGoalMetDefault()
	s.reseedGoalsPrompt()             // goals 프롬프트 단어 추가됨「펌핑 운전 제약」단계 → 이전 라이브러리에 새 기본 버전을 추가합니다.(일회용)
	s.reseedMainAgentPrompt()         // mainagent 프롬프트 단어 추가됨「목표 달성 후 add_intent 타겟 빌드 여부를 물었습니다」(일회용)
	s.reseedPlannerPrompt()           // planner 프롬프트 단어:다시 작성「0 의도」정당한 이유 + 정량적 합격 확인 추가(일회용)
	s.reseedWorkerPrompt()            // worker 프롬프트 단어:부정적인 결론에 대한 증거 임계값을 추가합니다.(일회용)
	s.seedReporterAgent()             // 프리셋「보고서 작성」agent + 도구 바인딩 + finding 트리거(일회용)
	s.upgradeReporterTriggerMessage() // 기존 데이터베이스 마이그레이션:하자 reporter 답글 evidence_version(일회용)
	s.seedFindingTrafficTools()       // 선택적 증거 매개변수 및 읽기 전용 증거 도구를 추가합니다.，사용자 구성 유지
	s.seedFindingWorkflowTools()
	// 참고：pentest 의 기본 도구 바인딩은 마이그레이션할 필요가 없습니다.——BuiltinToolSeeds 설정
	// list_assets/insert_assets/report_finding/list_findings/list_companies 님과 함께
	// pentest 함께해요 seed 알았어（프로젝트에 아직 오래된 라이브러리가 없습니다.，마이그레이션 없음）。
}

// refreshBuiltinToolSchemas propagates code schema/description changes on the
// orchestration + platform tools into already-seeded rows ONCE per version flag —
// SeedTool is first-insert-only, so a new param (e.g. spawn_task 님 llm_profile) never
// reaches an old DB otherwise. Preserves each tool's agent binding + enabled flag.
// Bump the flag whenever these tools' schemas/descriptions change in code.
func (s *Server) refreshBuiltinToolSchemas() {
	const flag = "tool_schema_refresh_v7_list_facts_paging"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	tools := append(s.orchestrationTools(), s.platformTools()...)
	for _, t := range tools {
		schema, _ := json.Marshal(t.InputSchema())
		if err := s.m.pg.RefreshToolDefaults(t.Name(), t.Description(), schema); err != nil {
			log.Printf("[tools] refresh %s schema failed: %v", t.Name(), err)
		}
	}
	// 동시에 일부 부품이 내장되어 있습니다. agent 도구가 코드 기본값으로 브러시되었습니다.：
	//   - goal_met：오래된 도서관 seed 의 설명 테이프“이번 계획 라운드를 종료합니다.”의 오해，할게요 planner 이렇게 생각해보세요
	//     “빈라운드 종료”뜻、달리기 시작하자마자 전체 미션이 완료된 것으로 착각했습니다.。
	//   - insert_assets：새로운 related 매개변수를 입력하세요.(자산이 현재 작업과 관련이 있는지 표시、보험가입 여부를 결정합니다.)，
	//     SeedTool 첫 번째 삽입only，오래된 도서관은 seed 님 schema 그렇지 않으면 이 새 매개변수가 수신되지 않습니다.。
	//   - list_facts：페이징으로 변경，새로운 limit/before/q 매개변수를 입력하세요.；오래된 도서관은 seed 비어 있음 schema 그렇지 않으면
	//     공구 관리 페이지에 표시됩니다.「매개변수 없음」，모델은 이러한 매개변수 설명을 가져올 수 없습니다.。
	refreshBuiltin := map[string]bool{"goal_met": true, "insert_assets": true, "list_facts": true}
	for _, sd := range agent.BuiltinToolSeeds() {
		if !refreshBuiltin[sd.Key] {
			continue
		}
		schema, _ := json.Marshal(sd.Schema)
		if err := s.m.pg.RefreshToolDefaults(sd.Key, sd.Desc, schema); err != nil {
			log.Printf("[tools] refresh %s desc failed: %v", sd.Key, err)
		}
	}
	_ = s.m.pg.SetSetting(flag, "true")
	log.Printf("[tools] 상쾌하다 orchestration/platform 도구 schema 코드 기본값으로(일회용)")
}

// unbindGoalMetDefault removes goal_met's default "planner" binding ONCE (guarded by
// a settings flag), so existing DBs match the new default of NO agent. goal_met bypasses
// per-goal prove_goal to declare the whole task done — powerful/risky and redundant with
// the prove_goal→auto-complete path — so it ships unbound; users can re-bind it per agent
// in the UI. A user's own binding to another agent is untouched (we only strip planner).
func (s *Server) unbindGoalMetDefault() {
	const flag = "goal_met_unbind_default_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.RemoveAgentFromTool("planner", "goal_met"); err != nil {
		log.Printf("[tools] goal_met 바인딩 해제 planner 실패: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// reseedGoalsPrompt 넣어보세요 goals 대상 디스어셈블러의 프롬프트 단어가 새로 고쳐집니다.【현재 코드 기본값】——기본 텍스트가 추가되었기 때문에
// 「연산 제약조건 먼저 추출(set_constraints)대상을 다시 제거하십시오.」이번 단계는,그리고 SeedPromptIfEmpty 첫 번째 삽입only,오래된 도서관
// 기존 version 1 이 단계를 받을 수 없습니다。여기서 버전관리를 이용하세요【새 버전 추가】하고 잘라내세요.(ResetPromptToDefault),
// 이전 버전이 역사에 남아있습니다.,사용자가 이를 사용자 정의한 경우 버전 기록에서 검색할 수 있습니다.。settings flag 경비병 → 한번만 해주세요;
// 기본값은 나중에 변경됩니다. bump 이거 flag。새 라이브러리를 처리할 필요가 없습니다.(SeedPromptIfEmpty 이미 seed 최신 기본값)。
func (s *Server) reseedGoalsPrompt() {
	const flag = "goals_prompt_constraint_step_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // 성공, 실패 상관없이 한 번만 시도해보세요.
	a, err := s.m.pg.GetAgentByKey("goals")
	if err != nil || a == nil {
		return // 새 라이브러리가 아직 구축되지 않았습니다. agent 알았어,seedPrompts 이 직접 seed 최신 기본값,이 마이그레이션은 필요하지 않습니다.
	}
	tmpl := agent.BuiltinPromptSeeds()["goals"]
	if tmpl == "" {
		return
	}
	// 새로운 도서관 seedPrompts 이미 seed 최신 기본값 → 현재 버전이 코드 기본값과 동일합니다.,중복 버전을 추가할 필요가 없습니다.。
	if cur, err := s.m.pg.CurrentPrompt(a.ID); err == nil && cur == tmpl {
		return
	}
	if _, err := s.m.pg.ResetPromptToDefault(a.ID, tmpl); err != nil {
		log.Printf("[prompts] goals 프롬프트 단어를 새 기본값으로 새로 고치지 못했습니다.: %v", err)
		return
	}
	log.Printf("[prompts] goals 프롬프트 단어에 새로운 기본 버전이 추가되었습니다.(펌핑운전 제약단계 추가,일회용)")
}

// reseedMainAgentPrompt 넣어보세요 mainagent 프롬프트 단어가 브러싱되어 있어요【현재 코드 기본값】——기본 텍스트가 추가되었습니다.「전체 대상
// 도착 후 add_intent 직접투자를 하려는 경우,공식 대상으로 등록되어 있는지 물어보세요.」이 가이드,그리고 SeedPromptIfEmpty 첫 번째 삽입
// only,이전 라이브러리의 기존 버전을 받을 수 없습니다.。버전관리를 이용하세요【새 버전 추가】하고 잘라내세요.(ResetPromptToDefault),이전 버전은 아직
// 역사에 남겨두세요,사용자가 이를 사용자 정의한 경우 버전 기록에서 검색할 수 있습니다.。settings flag 경비병 → 한번만 해주세요。새 라이브러리를 처리할 필요가 없습니다.
// (SeedPromptIfEmpty 이미 seed 최신 기본값)。그리고 reseedGoalsPrompt 완전 동형。
func (s *Server) reseedMainAgentPrompt() {
	const flag = "mainagent_prompt_goalless_intent_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // 성공, 실패 상관없이 한 번만 시도해보세요.
	a, err := s.m.pg.GetAgentByKey("mainagent")
	if err != nil || a == nil {
		return // 새 라이브러리가 아직 구축되지 않았습니다. agent 알았어,seedPrompts 이 직접 seed 최신 기본값,이 마이그레이션은 필요하지 않습니다.
	}
	tmpl := agent.BuiltinPromptSeeds()["mainagent"]
	if tmpl == "" {
		return
	}
	// 새로운 도서관 seedPrompts 이미 seed 최신 기본값 → 현재 버전이 코드 기본값과 동일합니다.,중복 버전을 추가할 필요가 없습니다.。
	if cur, err := s.m.pg.CurrentPrompt(a.ID); err == nil && cur == tmpl {
		return
	}
	if _, err := s.m.pg.ResetPromptToDefault(a.ID, tmpl); err != nil {
		log.Printf("[prompts] mainagent 프롬프트 단어를 새 기본값으로 새로 고치지 못했습니다.: %v", err)
		return
	}
	log.Printf("[prompts] mainagent 프롬프트 단어에 새로운 기본 버전이 추가되었습니다.(목표에 합류한 후 목표를 세워달라고 요청하세요.,일회용)")
}

// reseedPlannerPrompt 넣어보세요 planner 프롬프트 단어가 브러싱되어 있어요【현재 코드 기본값】——기본 텍스트가 간소화되고 재구성되었습니다.,그리고 넣어「구속」으로 다운그레이드됨
// 중복된 항목만 제거、새로운「적용 범위보다 깊이를 우선시하세요」「확실한 결론:목표가 달성되지 않았으며, 실행 의지가 없어 출력되어야 합니다.」、부정적인 결론 검토에 한계 추가。
// 기본값에 큰 변화가 있을 때마다 bump 다음은 flag(현재 v2)기존의 기존 데이터베이스를 다시 새로 고치도록 하세요.。SeedPromptIfEmpty 첫 번째 삽입only,이전 라이브러리의 기존 버전을 받을 수 없습니다.,그러니 버전관리를 이용하세요
// 【새 버전 추가】하고 잘라내세요.(ResetPromptToDefault),이전 버전이 역사에 남아있습니다.,사용자가 사용자 정의한 경우 버전에서 녹음할 수 있습니다.
// 검색。settings flag 경비병 → 한번만 해주세요。새 라이브러리를 처리할 필요가 없습니다.(SeedPromptIfEmpty 이미 seed 최신 기본값)。그리고
// reseedGoalsPrompt 완전 동형。
func (s *Server) reseedPlannerPrompt() {
	const flag = "planner_prompt_compact_realistic_v2"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // 성공, 실패 상관없이 한 번만 시도해보세요.
	a, err := s.m.pg.GetAgentByKey("planner")
	if err != nil || a == nil {
		return // 새 라이브러리가 아직 구축되지 않았습니다. agent 알았어,seedPrompts 이 직접 seed 최신 기본값,이 마이그레이션은 필요하지 않습니다.
	}
	tmpl := agent.BuiltinPromptSeeds()["planner"]
	if tmpl == "" {
		return
	}
	// 새로운 도서관 seedPrompts 이미 seed 최신 기본값 → 현재 버전이 코드 기본값과 동일합니다.,중복 버전을 추가할 필요가 없습니다.。
	if cur, err := s.m.pg.CurrentPrompt(a.ID); err == nil && cur == tmpl {
		return
	}
	if _, err := s.m.pg.ResetPromptToDefault(a.ID, tmpl); err != nil {
		log.Printf("[prompts] planner 프롬프트 단어를 새 기본값으로 새로 고치지 못했습니다.: %v", err)
		return
	}
	log.Printf("[prompts] planner 프롬프트 단어에 새로운 기본 버전이 추가되었습니다.(간소화된 재구성+다운그레이드 억제 및 중복 제거+깊이 우선+부정적인 리뷰 상한,일회용)")
}

// reseedWorkerPrompt 넣어보세요 worker 프롬프트 단어가 브러싱되어 있어요【현재 코드 기본값】——기본 텍스트 record_fact 문단이 삭제되었습니다「부정적인 결론
// 관찰 쓰기+잠정읽기 방법」문장 전체、그리고 넣어 confidence(observed/inferred)그리고「본래의 의도와 수단이 소진되었는지 여부」디커플링(기획자들은 오해를 받기 쉽습니다.),
// 동시에 facts 어레이 스트라이핑이 다음과 같이 강화되었습니다.「서로 완전히 독립되어 있음、병합할 수 없습니다.」에 대한 드문 예외。bump flag 에게 v3 기존의 기존 데이터베이스를 다시 새로 고치도록 하세요.。
// SeedPromptIfEmpty 첫 번째 삽입only,이전 라이브러리의 기존 버전을 받을 수 없습니다.,그러니 버전관리를 이용하세요【새 버전 추가】하고 잘라내세요.,이전 버전은 아직 기록에 남아 있으므로 검색할 수 있습니다.。
// settings flag 경비병 → 한번만 해주세요。새 라이브러리를 처리할 필요가 없습니다.。그리고 reseedGoalsPrompt 완전 동형。
func (s *Server) reseedWorkerPrompt() {
	const flag = "worker_prompt_compact_v4"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // 성공, 실패 상관없이 한 번만 시도해보세요.
	a, err := s.m.pg.GetAgentByKey("worker")
	if err != nil || a == nil {
		return // 새 라이브러리가 아직 구축되지 않았습니다. agent 알았어,seedPrompts 이 직접 seed 최신 기본값,이 마이그레이션은 필요하지 않습니다.
	}
	tmpl := agent.BuiltinPromptSeeds()["worker"]
	if tmpl == "" {
		return
	}
	// 새로운 도서관 seedPrompts 이미 seed 최신 기본값 → 현재 버전이 코드 기본값과 동일합니다.,중복 버전을 추가할 필요가 없습니다.。
	if cur, err := s.m.pg.CurrentPrompt(a.ID); err == nil && cur == tmpl {
		return
	}
	if _, err := s.m.pg.ResetPromptToDefault(a.ID, tmpl); err != nil {
		log.Printf("[prompts] worker 프롬프트 단어를 새 기본값으로 새로 고치지 못했습니다.: %v", err)
		return
	}
	log.Printf("[prompts] worker 프롬프트 단어에 새로운 기본 버전이 추가되었습니다.(컨텍스트 세그먼트를 확인하고 수렴 list_assets/list_findings,제거 list_facts/node_detail/asset_neighbors,일회용)")
}

// reporterToolCallMessage 무조건 한 번씩 읽어야 한다. get_finding_traffic 또 다른 보고서 작성。
// 이 도구는 읽기 전용입니다.、「캡처 스위치에 의존하지 않습니다.」,자동바인딩이 꺼졌든 안됐든 수동바인딩의 증거물을 읽을 수 있습니다.。여기라면
// 은 다음과 같이 작성됩니다.「자동 바인딩이 활성화된 경우에만 읽기」,기본 폐쇄 구성에서 reporter 통과되지 않습니다 evidence_version,
// SetFindingReportVersionByNodeID 그냥 누르세요 legacy 의미적 글쓰기 -1,취약점 세부정보 및 Markdown 수출
// 영원히 여기 있어줘「증거가 변경되었습니다，보고서 업데이트 예정」,그리고 UI 에 이를 지울 진입점이 없습니다.。
const reporterToolCallMessage = "방금 언급한 취약점이 있었습니다. report_finding 등록。꼭 읽어보시고 돌려주세요 JSON 님 finding_id（독립적인 취약점 기록 ID）그리고 finding_node_id（노드 탐색 ID），" +
	"먼저 사용해 보세요 get_finding_traffic(finding_id) 현재 증거 목록과 그 내용을 읽어 보십시오. version（빈 목록이 정상입니다.，평소대로 보고서를 작성하세요.）；" +
	"런닝 가이드에서 자동 바인딩을 활성화한 경우，이 취약점을 읽기 전에 해당 취약점의 트래픽을 확인하고 상관관계를 확인하십시오.。사용된 노드 세부정보 finding_node_id。" +
	"마지막 통화 update_finding_report(finding_id=finding_node_id, report, evidence_version=실제 읽은 버전) 저장，" +
	"evidence_version 반드시 통과해야 함，그렇지 않으면 보고서가 영구적으로 업데이트 보류로 표시됩니다.。두 숫자를 섞지 마세요。"

// 이전 버전 트리거 메시지(0.3.8 및 이전 버전)。여전히 문자 그대로 동일한 레코드만 마이그레이션으로 덮어쓰게 됩니다.，사용자가 변경한 내용은 그대로 유지됩니다.。
const reporterToolCallMessageV1 = "방금 언급한 취약점이 있었습니다. report_finding 등록。트리거 컨텍스트에서 제거해 주세요. finding_id" +
	"（공구반환 \"finding recorded: <id>\" 의 번호）및 작업 id，귀하의 책임에 따라 취약점에 대한 자세한 보고서를 작성하십시오.，" +
	"마지막 통화 update_finding_report(finding_id, report) 저장。"

// upgradeReporterTriggerMessage Lao Kuli는 여전히 기본 카피라이터입니다. reporter 새 버전으로 새로 고치라는 메시지 트리거。
// seedReporterAgent 수락됨 reporter_agent_seed_v1 지키고 새로만 만들어라 agent 시간 쓰기 트리거，그래서
// 업그레이드된 라이브러리에서 새 카피라이팅을 얻을 수 없습니다. —— 도구 schema  seedFindingTrafficTools 완료
// evidence_version，그런데 아무것도 안 나와요 reporter 가서 써보세요。일회용，변경되지 않은 복사본만 덮어쓰게 됩니다.。
func (s *Server) upgradeReporterTriggerMessage() {
	const flag = "reporter_trigger_evidence_version_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // 한 번만 시도해보세요
	triggers, err := s.m.pg.ListTriggersFor("reporter")
	if err != nil {
		log.Printf("[reporter] 트리거를 읽지 못했습니다.: %v", err)
		return
	}
	for _, t := range triggers {
		if !t.OnToolCall || t.ToolCallMessage != reporterToolCallMessageV1 {
			continue // 사용자가 변경했거나 변경하지 않았습니다. finding 트리거，움직이지 않는다。
		}
		t.ToolCallMessage = reporterToolCallMessage
		if err := s.m.pg.UpdateTrigger(t); err != nil {
			log.Printf("[reporter] 업그레이드 트리거 메시지 실패: %v", err)
			return
		}
		log.Printf("[reporter] 트리거 메시지를 읽고 반환할 수 있도록 업그레이드되었습니다. evidence_version")
	}
}

// seedReporterAgent 프리셋 1「보고서 작성」맞춤형 agent(builtin=false，사용 가능 UI 편집/삭제)：
// 바인딩 update_finding_report + 작업 쿼리 도구，그리고 하나 걸어두세요「report_finding 호출 시 트리거됨」님
// 트리거 —— 취약점이 등록될 때마다 호출되어 자세한 보고서가 작성됩니다.。일회용(settings flag 경비병)：삭제 후 사용자가 다시 생성되지 않습니다.。
// 의존성：orchestration 도구가 이미 이 기능 위에 있습니다. SeedTool 저장，바인딩이 가능하도록。
func (s *Server) seedReporterAgent() {
	const flag = "reporter_agent_seed_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // 성공, 실패 상관없이 한 번만 시도해보세요.

	if exist, _ := s.m.pg.GetAgentByKey("reporter"); exist != nil {
		return // key 이미 점유 중입니다.(사용자가 제작함)——보장되지 않음
	}
	a, err := s.m.pg.CreateAgent("reporter", "보고서 작성",
		"취약점 상세 보고서 작성：취약점이 발견되면 자동으로 트리거됩니다.，증거 및 집행과정을 확인 후 작성하세요. Markdown 신고하고 답장하세요。")
	if err != nil {
		log.Printf("[reporter] 생성 agent 실패: %v", err)
		return
	}
	if err := s.m.pg.SeedPromptIfEmpty(a.ID, agent.ReporterDefaultPrompt); err != nil {
		log.Printf("[reporter] seed prompt 실패: %v", err)
	}
	// 트리거 실행 정책：parallel + none —— 하나의 취약점, 하나의 보고서、여러개 finding 동시에 직접 작성。
	// merge 반드시 none：그렇지 않으면(기본값 all)파도 finding 이 하나의 실행으로 병합됩니다.，병렬은 의미가 없습니다。
	// maxParallel=5：동시에 최대 5 보고회，너무 많은 순간을 피하세요 LLM 전화주세요。
	if err := s.m.pg.SetAgentTriggerBehavior("reporter", "parallel", "none", 5); err != nil {
		log.Printf("[reporter] 트리거 실행 정책을 설정하지 못했습니다.: %v", err)
	}
	// 바인딩에 필요한 도구：보고서 작성 + 증거를 읽어보세요/실행과정/상황。
	if err := s.m.pg.AddAgentToToolBinding("reporter", []string{
		"update_finding_report", "get_task_node_detail", "list_task_findings",
		"get_task_worker_trace", "list_task_worker_traces", "search_task_worker_traces",
		"get_task_graph",
	}); err != nil {
		log.Printf("[reporter] 바인딩 도구 실패: %v", err)
	}
	// 트리거：report_finding 호출 시 트리거됨（공구반환 "finding recorded: <id>" 가지고 가세요 finding_id，
	// 임무 id 도 트리거 메시지에 있습니다.）。
	if _, err := s.m.pg.CreateTrigger(&db.AgentTrigger{
		AgentKey:        "reporter",
		Enabled:         true,
		OnToolCall:      true,
		ToolNames:       []string{"report_finding"},
		ToolCallMessage: reporterToolCallMessage,
	}); err != nil {
		log.Printf("[reporter] 트리거를 생성하지 못했습니다.: %v", err)
	}
	log.Printf("[reporter] 프리셋「보고서 작성」agent + finding 트리거")
}

// seedAutoReportFindingBinding adds "auto" to report_finding's binding ONCE so
// conversation-context agents can call it without requiring an intent_id.
func (s *Server) seedAutoReportFindingBinding() {
	const flag = "auto_report_finding_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.AddAgentToToolBinding("auto", []string{"report_finding"}); err != nil {
		log.Printf("[auto] report_finding 기본 바인딩 실패: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedPlannerDefaultBindings adds "planner" to report_finding's binding ONCE
// (guarded by a settings flag), so existing DBs — whose report_finding row was
// seeded as worker-only — also let the planner record findings. Fresh DBs already
// get it via PlannerTools(); this only backfills without overriding a user unbind.
func (s *Server) seedPlannerDefaultBindings() {
	const flag = "planner_report_finding_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.AddAgentToToolBinding("planner", []string{"report_finding"}); err != nil {
		log.Printf("[planner] report_finding 기본 바인딩 실패: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedPlannerListAssetsBinding adds "planner" to list_assets's binding ONCE
// (guarded by a settings flag), so existing DBs — whose list_assets row was seeded
// as auto/pentest-only — also let the planner query the asset store by DSL. Fresh
// DBs already get it via PlannerTools(); this only backfills without overriding a
// user unbind.
func (s *Server) seedPlannerListAssetsBinding() {
	const flag = "planner_list_assets_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.AddAgentToToolBinding("planner", []string{"list_assets"}); err != nil {
		log.Printf("[planner] list_assets 기본 바인딩 실패: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedCompanyScopeRebind changes add_company_scope's default binding ONCE on
// existing DBs (guarded by a settings flag): the tool moves off worker and onto
// planner — defining a company's asset scope is a planning/main/auto concern, not
// something a worker does mid-exploration. Fresh DBs already get planner via
// PlannerTools() and lack worker via WorkerTools(); this only backfills old rows.
// One-shot + flag-guarded so a user who later re-binds worker isn't overridden.
func (s *Server) seedCompanyScopeRebind() {
	const flag = "company_scope_rebind_v1" // worker→planner 기본 바인딩 전환
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.AddAgentToToolBinding("planner", []string{"add_company_scope"}); err != nil {
		log.Printf("[planner] add_company_scope 기본 바인딩 실패: %v", err)
		return
	}
	if err := s.m.pg.RemoveAgentFromTool("worker", "add_company_scope"); err != nil {
		log.Printf("[worker] add_company_scope 바인딩을 해제하지 못했습니다.: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedWorkerReadToolsUnbind strips the read-context tools off worker's default
// binding ONCE on existing DBs (guarded by a settings flag): a worker executes one
// intent and writes back — reading facts/companies and listing all workers' traces is
// a planning/main concern, not the executor's. Fresh DBs already lack these via
// WorkerTools(); this only backfills old rows without overriding a user who
// deliberately re-binds worker. Each RemoveAgentFromTool is per-tool +
// membership-guarded, so planner/mainagent bindings of the same tool are untouched.
//
// NOTE: search_all_worker_traces / get_worker_trace / node_detail are intentionally NOT
// unbound — worker owns them for cross-work look-back + node drill-down (see WorkerTools).
// They used to be in this list back when worker lacked them; seedWorkerReadbackRebind
// repairs DBs whose old run stripped them.
func (s *Server) seedWorkerReadToolsUnbind() {
	const flag = "worker_readtools_unbind_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	for _, k := range []string{
		"list_facts", "list_companies", "list_worker_traces",
	} {
		if err := s.m.pg.RemoveAgentFromTool("worker", k); err != nil {
			log.Printf("[worker] %s 님으로부터 worker 바인딩을 해제하지 못했습니다.: %v", k, err)
			return // 실수해도 뒤쳐지지 않습니다. flag，다음에 다시 시도해보세요
		}
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedWorkerReadbackRebind re-binds the cross-work look-back / drill-down tools onto
// worker ONCE (guarded by a settings flag): an earlier seedWorkerReadToolsUnbind wrongly
// stripped search_all_worker_traces / get_worker_trace / node_detail from worker after
// they had been added to WorkerTools(), so any DB that ran that migration lost them.
// Fresh DBs already have them via WorkerTools() and this is a harmless no-op there.
// One-shot + flag-guarded so a user who later deliberately unbinds them isn't overridden.
func (s *Server) seedWorkerReadbackRebind() {
	const flag = "worker_readback_rebind_v2" // v2: 추가 node_detail
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.AddAgentToToolBinding("worker", []string{
		"search_all_worker_traces", "get_worker_trace", "node_detail",
	}); err != nil {
		log.Printf("[worker] 검토/세부정보 도구 리바인딩 실패: %v", err)
		return // 실수해도 뒤쳐지지 않습니다. flag，다음에 다시 시도해보세요
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedAutoDefaultBindings adds "auto" to the task-op + platform tools' bindings
// ONCE (guarded by a settings flag), so existing DBs whose tool rows were seeded
// before Auto existed still give Auto its default toolset — without re-adding it
// after a user deliberately unbinds.
func (s *Server) seedAutoDefaultBindings() {
	const flag = "auto_default_bindings_v3" // v3: 이전 자산 도구 이름을 바꿉니다.，가입하세요 insert_assets/add_company_scope
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	keys := make([]string, 0, len(platformToolKeys)+12)
	for _, t := range s.orchestrationTools() {
		keys = append(keys, t.Name())
	}
	keys = append(keys, platformToolKeys...)
	// 자산 도구：Auto 항상 운영 플랫폼을 확인하세요./자산 등록、소속사 범위。
	keys = append(keys, "insert_assets", "add_company_scope", "list_assets")
	if err := s.m.pg.AddAgentToToolBinding("auto", keys); err != nil {
		log.Printf("[auto] 기본 바인딩 실패: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}
