package agent

import (
	"strings"

	"github.com/Autumn-27/norma/harness"
)

// 마무리 프롬프트 단어(wrap-up / settlement prompt):언제 agent 왜냐하면【걸음이 지쳤습니다.(MaxTurns)】또는
// 【시간 초과(run_seconds/MaxDuration)】종료 시,SDK 님 settlement 단계에서 이 프롬프트를 삽입합니다.,
// 하자 agent 먼저 식별되었으나 다시 기록되지 않은 내용을 저장합니다.、또 다른 요약 출력,배드엔딩은 피하세요。
//
// 각 agent 의 종료 프롬프트 단어는 백그라운드에서 요청 시 덮어쓸 수 있습니다.(저장 agents.wrapup_prompt),공백으로 남겨둘 경우 여기를 사용하세요.
// 기본 내장。만【프롬프트 단어 텍스트】편집 가능;어떤 도구가 비활성화되어 있습니까?、스스로 주어진 예산의 라운드 수를 확정하는 것이 코드 고정 전략입니다.。

// WrapupOverride, if set, returns the stored wrap-up prompt for an agent key and
// whether a non-empty one exists. Wired by the server to the agents table (like
// PromptOverride for system prompts). nil / empty → the built-in default is used.
var WrapupOverride func(agentKey string) (string, bool)

// WrapupMaxTurnsOverride, if set, returns the admin-configured turn budget for the
// wrap-up phase of an agent and whether a positive one exists. Wired to the agents
// table. nil / ≤0 → the built-in per-agent default (wrapupTurnDefaults) is used.
var WrapupMaxTurnsOverride func(agentKey string) (int, bool)

// 기본 닫기 프롬프트 단어 내장,언론 agent key 색인。worker 역사적으로 하드 코딩된 재사용 settleWrapUpPrompt
// (은 다음에 정의되어 있습니다. worker.go),planner/mainagent 각각 하나의 버전이 있습니다.;놓쳤어요(맞춤형 agent)일반적인 접근 방식을 취하세요.。
var wrapupDefaults = map[string]string{
	"worker":    settleWrapUpPrompt,
	"planner":   plannerWrapUpDefault,
	"mainagent": mainAgentWrapUpDefault,
}

// wrapupTurnDefaults: 각각 agent 최종 무대【그 자체】님의 라운드 예산이 기본적으로 내장되어 있습니다.(을(를) 백그라운드로 설정할 수 있습니다. >0 재정의)。
// 모두 주어진다 10 휠,최종 단계에서 충분한 단계가 있는지 확인하십시오.。놓치고 떠났어요 genericWrapupTurns。
var wrapupTurnDefaults = map[string]int{
	"worker":    10,
	"planner":   10,
	"mainagent": 10,
}

const genericWrapupTurns = 10

const plannerWrapUpDefault = "이번 라운드에 계획된 단계 수가 곧 소진됩니다.——참고만【이번 라운드】끝,시스템은 상황 변화에 따라 계속 계획을 세울 수 있도록 사용자를 다시 깨울 것입니다.,작업이 종료되지 않았습니다.,여기서 전체 계획을 마무리할 필요는 없습니다.。이번 라운드에서 명확하게 생각한 결론을 구현해 주세요.、이번 라운드를 헛되이 보내지 마세요,하지만 역시【결말만을 위해서 작정하지마】(이번 라운드 0 의도는 여전히 완전히 정상적인 결과입니다.)：(1) 결정된 경우【이제 배포해야지】의 탐색방향,한번만 사용하세요 add_intent 일괄 제출(생각한 것을 머뭇거리지 마세요.);(2) 누군가가 이 쌍을 발견했습니다/목표가 달성되었다는 사실이 입증되었습니다.,조정 prove_goal 태그 met(놓치지 마세요);(3) 단계별로 나누어야 하는 연쇄 익스플로잇 체인이 식별된 경우,사용 TodoWrite 참고하세요,다음에 일어나서 보내면 편해요。완료 후 즉시 이번 라운드를 종료합니다.,요약 텍스트를 출력할 필요가 없습니다.。"

const mainAgentWrapUpDefault = "걸음이 곧 끝나가네요,이 대화가 곧 종료됩니다。새로운 탐색을 시작하지 마십시오/작동。부탁드려요**일반 텍스트 한 문장만 사용하세요.**사용자에게 현재 진행 상황을 요약합니다.、주요 결론,및 다음 단계 제안。"

const genericWrapUpDefault = "예산 소진으로 종료 예정입니다.。완료되었으나 아직 라이브러리에 올라오지 않은 결과를 다시 작성해주세요.,다시**일반 텍스트 한 문장만 사용하세요.**당신이 한 일을 요약해보세요、어떤 핵심 결론을 얻었습니까?(이 문장은 이 실행의 결과로 표시됩니다.)。"

// WrapupDefault returns the built-in default wrap-up prompt for an agent key —
// used by the admin UI as the "restore default" value and empty-field placeholder.
func WrapupDefault(agentKey string) string {
	if d, ok := wrapupDefaults[agentKey]; ok {
		return d
	}
	return genericWrapUpDefault
}

// WrapupTurnsDefault returns the built-in wrap-up turn budget for an agent key —
// used by the admin UI as the "0 = default N" hint.
func WrapupTurnsDefault(agentKey string) int {
	if n, ok := wrapupTurnDefaults[agentKey]; ok {
		return n
	}
	return genericWrapupTurns
}

// resolveWrapup returns the effective wrap-up prompt: the DB override (if set and
// non-empty) over the built-in default.
func resolveWrapup(agentKey string) string {
	if WrapupOverride != nil {
		if t, ok := WrapupOverride(agentKey); ok && strings.TrimSpace(t) != "" {
			return t
		}
	}
	return WrapupDefault(agentKey)
}

// resolveWrapupTurns returns the effective wrap-up turn budget: a positive DB
// override over the built-in per-agent default.
func resolveWrapupTurns(agentKey string) int {
	if WrapupMaxTurnsOverride != nil {
		if v, ok := WrapupMaxTurnsOverride(agentKey); ok && v > 0 {
			return v
		}
	}
	return WrapupTurnsDefault(agentKey)
}

// wrapupSettlement builds the settlement config for an agent's run. Prompt and the
// turn budget are admin-editable per agent; disabled tools are code-owned policy so
// a user can't edit away the "stop probing" guardrail. Resolved fresh each run
// (reads DB live), so edits apply on the next run without a restart.
func wrapupSettlement(agentKey string, disabledTools []string) *harness.Settlement {
	return &harness.Settlement{
		Prompt:        resolveWrapup(agentKey),
		DisabledTools: disabledTools,
		MaxTurns:      resolveWrapupTurns(agentKey),
	}
}

// ---------- 작업 수준 시간 초과 종료 단어（또 만나요 docs/작업 수준 제한 시간 및 종료 설계.md）----------
//
// 그리고 per-run 끝나는 단어는【2개 세트】：per-run 네"이번에는 당신이 run 님의 예산이 소진되었습니다."；작업 시간 초과는 다음과 같습니다.
// "전체 미션 완료、끝나가는 중"。의미가 반대되는 경우가 많습니다.（특히 planner：per-run 님이 말했어요"계획을 멈추지 마세요"，
// 작업 시간 초과가 발생했습니다."그 시점에서 계획을 중단하세요、최종 결정하세요"）。전용 worker/planner 구성。

// WrapupTaskTimeoutOverride / …TurnsOverride：작업 시간 초과 종료 단어 및 라운드 숫자 DB 재정의
// （wire 에게 agents.task_timeout_wrapup_prompt / _max_turns，만 worker/planner）。
var (
	WrapupTaskTimeoutOverride      func(agentKey string) (string, bool)
	WrapupTaskTimeoutTurnsOverride func(agentKey string) (int, bool)
)

var taskTimeoutWrapupDefaults = map[string]string{
	"worker":  workerTaskTimeoutDefault,
	"planner": plannerTaskTimeoutDefault,
}

const workerTaskTimeoutDefault = "**전체 작업이 시간 초과 제한에 도달했습니다.，끝나가는 중**（이번에는 당신이 아닙니다 run 의 예산，이것으로 전체 탐색이 끝났습니다.）。이번이 마지막 기회다：(1) 확인했으나 아직 답장하지 않은 내용을 적어주세요【모두】라이브러리 삭제——새로운 자산 insert_assets、결론 탐색/사실 record_fact、취약점 확인 report_finding；(2) 새 명령을 시작하지 마십시오./탐지；(3) **마지막으로 일반 텍스트 한 문장을 사용하세요.**이 의도에 대한 주요 결론을 요약하세요.。"

const plannerTaskTimeoutDefault = "**전체 작업이 시간 초과 제한에 도달했습니다.，끝나가는 중**（이번 회차는 아님，전체 작업이 종료되었습니다.）。현재 기준으로 작성해주세요【모두】사실 및 조사 결과，최종 목표 결정：달성이 입증된 목표 조정 prove_goal 태그 met（놓치지 마세요）。**새로운 인텐트를 생성하지 마세요.**（이때 해당 의도는 더 이상 실행되지 않습니다.）。판결이 완료되면 종료됩니다，요약 텍스트를 출력할 필요가 없습니다.。"

// TaskTimeoutWrapupDefault 반환 agent 의 작업 시간 초과에는 기본 종료 단어가 내장되어 있습니다.（배경 공간용/기본값 복원）。
func TaskTimeoutWrapupDefault(agentKey string) string {
	return taskTimeoutWrapupDefaults[agentKey] // 구성되지 않음(mainagent/chat)은 빈 문자열을 반환합니다.
}

// resolveTaskTimeoutWrapup：DB 재정의(비어있지 않음) > 기본 내장。빈 문자열은 agent 작업 시간 초과 단어가 없습니다.
// （아니요 worker/planner），이 시점에서 발신자는 롤백해야 합니다. per-run 말씀。
func resolveTaskTimeoutWrapup(agentKey string) string {
	if WrapupTaskTimeoutOverride != nil {
		if t, ok := WrapupTaskTimeoutOverride(agentKey); ok && strings.TrimSpace(t) != "" {
			return t
		}
	}
	return TaskTimeoutWrapupDefault(agentKey)
}

func resolveTaskTimeoutTurns(agentKey string) int {
	if WrapupTaskTimeoutTurnsOverride != nil {
		if v, ok := WrapupTaskTimeoutTurnsOverride(agentKey); ok && v > 0 {
			return v
		}
	}
	return resolveWrapupTurns(agentKey) // 기본적으로 사용됨 per-run 라운드 횟수
}

// wrapupSettlementForTask builds settlement for a worker/planner run that is aware
// of the task deadline. See §5 of the design doc:
//   - clamped=true  → 이번에는 run 임무 deadline 클램핑：왜냐하면 Timeout 종료=임무 완수→작업 시간 초과 단어；
//     왜냐하면 MaxTurns 종료=핀치 창 내의 단계 수가 먼저 소진되었습니다.、미션은 몇 분 남았나요?→물러서라 per-run 말씀。
//   - clamped=false → 아직 작업은 이르다：2종 reason 둘 다 사용 per-run 말씀（즉, 변질된다. wrapupSettlement）。
//
// 줘 harness 님 PromptByReason 마지막에 눌러주세요【실제】reason 현장선정，없음 build 시간 불일치。
func wrapupSettlementForTask(agentKey string, disabledTools []string, clamped bool) *harness.Settlement {
	perRun := resolveWrapup(agentKey)
	st := &harness.Settlement{
		Prompt:        perRun, // 사실대로 말해주세요(예, 아니오 clamped 두가지 종류가 있어요 reason 의 값)
		DisabledTools: disabledTools,
		MaxTurns:      resolveWrapupTurns(agentKey),
	}
	if clamped {
		if tt := resolveTaskTimeoutWrapup(agentKey); tt != "" {
			st.PromptByReason = map[harness.TerminalReason]string{
				harness.ReasonTimeout:  tt,     // 임무 완수
				harness.ReasonMaxTurns: perRun, // 먼저 단계 수가 소진되었습니다.、미션 남은 시간
			}
			st.MaxTurns = resolveTaskTimeoutTurns(agentKey)
		}
	}
	return st
}
