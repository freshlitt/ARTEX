package agent

import (
	"encoding/json"
	"fmt"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

const findingIDGuidance = "\n\n**취약점 번호 지정 규칙**：finding_id 은 독립적인 취약점 레코드입니다. ID；finding_node_id 은 탐사 노드입니다. ID。list_findings / list_task_findings / node_detail / get_task_node_detail 님 id 탐사 노드로 예약됨 ID，은 동일한 곳에서 반환되어야 합니다. finding_id 독립번호 읽기。get_finding_traffic / bind_finding_traffic 독립적으로 사용 finding_id。늙었다 update_finding_report 님 finding_id 매개변수가 여전히 전달됩니다. finding_node_id。넣지 마세요 report_finding 첫 번째 줄의 숫자는 증거 도구입니다.，잘못된 숫자를 만난 후 다른 숫자를 추측하지 마세요.。"

// The server supplies the persisted setting. A missing setting/host is off.
// Consulted at assembly and again on writes so an already-running session
// cannot keep binding after the user switches the feature off.
var FindingTrafficBindingEnabled func() bool

func findingTrafficBindingEnabled() bool {
	return FindingTrafficBindingEnabled != nil && FindingTrafficBindingEnabled()
}

// Applied after ToolResolve: user descriptions and prompts remain intact, while
// all actual reporters (including Planner and custom chat agents) see the same
// API contract. Disabled/unbound tools are never reintroduced here.
func findingWorkflowTools(agentKey string, tools []actool.CoreTool) ([]actool.CoreTool, string) {
	if !findingTrafficBindingEnabled() {
		out := make([]actool.CoreTool, 0, len(tools))
		for _, tool := range tools {
			if tool.Name() == "bind_finding_traffic" {
				continue
			}
			if agentKey == "reporter" && (tool.Name() == "traffic_search" || tool.Name() == "traffic_get" || tool.Name() == "traffic_blob") {
				continue
			}
			switch tool.Name() {
			case "report_finding", "add_hint", "add_task_hint":
				// Work on a copy: toggling back on must restore the original schema.
				raw, _ := json.Marshal(tool.InputSchema())
				var schema map[string]any
				if json.Unmarshal(raw, &schema) == nil {
					stripTrafficParameters(schema)
					tool = DecorateTool(tool, tool.Description(), schema)
				}
			}
			out = append(out, tool)
		}
		return out, ""
	}
	out := append([]actool.CoreTool(nil), tools...)
	has := map[string]bool{}
	for i, tool := range out {
		has[tool.Name()] = true
		note := ""
		switch tool.Name() {
		case "report_finding":
			note = "\n기본적으로 보고됨 Agent 보고서 작성 전 트래픽 확인 및 바인딩。기자는 evidence 에 예약되어 있습니다.、주요 출력、기존 실제 트래픽 ID 및 그 용도，제보용 Agent 실행기록과 대조 확인；바인딩을 위해 추가 패킷을 확인할 필요가 없습니다.。명시적 즉시 바인딩과 호환 가능：traffic_refs 또는 evidence_hint_id 검증된 인용을 제출할 수 있습니다.，후자는 지정된 작업을 읽습니다. hint 에 대한 구조화된 참조；하나라도 유효하지 않으면 모든 보고서가 실패합니다.。TCP/이러한 선택적 매개 변수가 필요하지 않은 패키지는 없습니다.。복귀 finding_id 그리고 finding_node_id 는 각각 독립적인 기록 및 탐색 노드를 나타냅니다.。"
		case "add_hint", "add_task_hint":
			note = "\n확인된 취약점을 넘겨줄 때，해당 프롬프트에서 traffic_refs 은 확인된 트래픽을 유지합니다. ID、목적、설명 및 순서（상단 레이어에 단일 스트립을 놓습니다.，일괄 릴리스 대응 hints 요소），그리고 text 은 그것이 보여주는 특정 취약점을 설명합니다.。발신자는 문자만 넘겨주고 기존 트래픽 참조를 버릴 수는 없습니다.。검증되지 않은 후보는 증거로 인정할 수 없습니다.。"
		case "get_finding_traffic", "bind_finding_traffic", "list_findings", "list_task_findings", "node_detail", "get_task_node_detail", "update_finding_report":
			note = findingIDGuidance
		}
		if note != "" {
			out[i] = DecorateTool(tool, tool.Description()+note, tool.InputSchema())
		}
	}
	guidance := ""
	if has["report_finding"] || has["add_task_hint"] || has["add_hint"] {
		guidance = "\n\n**교통 증거 인계（선택사항）**：자동 바인딩은 기본적으로 보고됩니다. Agent 라이브러리에 취약점 저장 후、보고서 작성 전 완료。기자는 다음 장소에 있어야 합니다. evidence 확인 명령 유지、주요 출력、이미 실제 트래픽이 발생함 ID 및 그 용도，미션 중 intent_id，신고가 용이함 Agent 추적성；바인딩을 위해 패키지를 확인할 필요가 없습니다.。Auto / Planner 대리신고 시 기존 집행자의 참고문헌을 버리지 마세요.。add_hint / add_task_hint 가능 traffic_refs 인계；명시적 인스턴트 바인딩은 여전히 호환됩니다. report_finding 님 traffic_refs / evidence_hint_id。TCP 아니면 패키지가 없을때 정상적으로 등록하세요，짐작이 안가네요 ID，보충 패킷에 대해서만 반복 탐지도 불가능합니다.。"
		if has["add_task_hint"] && !has["add_hint"] {
			guidance += "\n플랫폼 대화에 작업 컨텍스트가 없는 경우，직접 전화하지 마세요 report_finding；합격 add_task_hint 기존 해당 업무에 넘겨주세요，업무별 Agent 등록，함께 사용하세요 list_task_findings 결과를 확인해보세요。"
		}
		if has["prove_goal"] || has["goal_met"] {
			guidance += "\n목표가 완료되었다고 판단하기 전에，기존 증거 보고서를 먼저 작성하세요./인계。증거인계가 완료되기도 전에 텍스트 허점이 등록됐다고 작업을 종료하지 마세요.、취소 Worker；패킷이 없으면 대기 또는 강제 패킷 캡처가 필요하지 않습니다.。"
		}
	}
	if has["update_finding_report"] && has["bind_finding_traffic"] && has["get_finding_traffic"] {
		guidance += "\n\n**보고하기 전에 트래픽을 자동으로 연관시킵니다.（켜짐）**：이번에 발생한 취약점에 대한 트래픽 확인 및 바인딩 업무는 귀하의 몫입니다，또 다른 보고서 작성。시작하세요 report_finding 복귀 JSON 또는 get_task_node_detail / list_task_findings 명확하게 해라 finding_id 그리고 finding_node_id。취약점 세부정보 읽기、해당 의도의 실행기록 및 기존 증거 목록，기자가 전달한 진실을 우선으로 생각합니다 ID。이 검증이 HTTP 및 트래픽 도구를 사용할 수 있습니다.，사용 traffic_search 전형후보，재사용 traffic_get 항목별 요청항목을 확인합니다./응답이 취약점을 지원합니다.；도메인 이름과 시간은 필터링에만 사용됩니다.，소유권 증명 없음。확인된 증거를 재발순으로 활용 bind_finding_traffic(finding_id, traffic_refs) 관련，선택 baseline / proof / verification / supporting 그리고 목적을 설명해주세요。은 이 취약점만 조작할 수 있습니다.，취약점을 재현하거나 대상을 다시 조사하지 마십시오.。바인딩 성공 후 리콜 get_finding_traffic 최신 정보 받기 version，필수 텍스트 읽기，그럼 실제 내용을 읽어보세요 version  evidence_version 패스 update_finding_report（그래요 finding_id 매개변수가 계속 사용됩니다. finding_node_id）。이미 바인딩이 있는 경우 다시 추가할 필요가 없습니다.。TCP、수집되지 않음、도구를 사용할 수 없거나 정확히 일치하는 항목이 없는 경우，자동 바인딩 건너뛰기，텍스트 기반/정상적으로 보고서를 작성하도록 증거를 주문하고 이유를 설명하세요.，트래픽을 모으기 위해 추측하지 마세요.。바인딩 실패는 성공을 선언하지 않습니다.；기존 증거를 유지하고 신고서에 구속 해제 이유를 설명하세요.。"
	}
	if guidance != "" || has["get_finding_traffic"] || has["update_finding_report"] {
		guidance += findingIDGuidance
	}
	return out, guidance
}

func stripTrafficParameters(schema map[string]any) {
	props, _ := schema["properties"].(map[string]any)
	delete(props, "traffic_refs")
	delete(props, "evidence_hint_id")
	if required, ok := schema["required"].([]any); ok {
		kept := required[:0]
		for _, key := range required {
			if key != "traffic_refs" && key != "evidence_hint_id" {
				kept = append(kept, key)
			}
		}
		schema["required"] = kept
	}
	if hints, ok := props["hints"].(map[string]any); ok {
		if items, ok := hints["items"].(map[string]any); ok {
			stripTrafficParameters(items)
		}
	}
}

// HintTrafficSchema is shared by the task-local and cross-task hint tools.
func HintTrafficSchema() map[string]any {
	return map[string]any{"type": "array", "description": "선택사항：검증되었으며 이 팁의 특정 취약점에 해당하는 트래픽 참조입니다.，순서를 유지하세요；인계 후 report_finding 양도 가능 evidence_hint_id 은 이러한 참조를 전달합니다.。", "items": obj(map[string]any{"traffic_id": str("실제 트래픽 ID"), "role": str("baseline / proof / verification / supporting"), "note": str("이 흐름은 어떤 결론을 뒷받침합니까?")}, "traffic_id")}
}

func (t *ToolSet) findingRefsFromHint(hintID int64, explicit []db.TrafficRef) ([]db.TrafficRef, error) {
	if hintID <= 0 {
		return db.NormalizeTrafficRefs(explicit)
	}
	n, err := t.ts.GetNode(hintID) // local store only: inherited hints cannot supply evidence
	if err != nil {
		return nil, err
	}
	if n == nil || n.Kind != db.KindHint {
		return nil, fmt.Errorf("evidence_hint_id=%d 은 이 작업의 프롬프트 노드여야 합니다.（상속 힌트는 바인딩에 직접 사용할 수 없습니다.）", hintID)
	}
	var payload struct {
		Refs []db.TrafficRef `json:"traffic_refs"`
	}
	if err := json.Unmarshal(n.Payload, &payload); err != nil {
		return nil, err
	}
	return db.NormalizeTrafficRefs(append(append([]db.TrafficRef{}, explicit...), payload.Refs...))
}
