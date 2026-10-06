package agent

import (
	"context"
	"errors"
	"fmt"
)

// AbortCause names why an agent run's context was cancelled. Every cancellation
// site should attach one so the activity trace can report the real initiator.
type AbortCause struct {
	Code  string
	Short string
	Text  string
}

func (c *AbortCause) Error() string { return c.Text }

func cause(code, short, text string) *AbortCause {
	return &AbortCause{Code: code, Short: short, Text: text}
}

// Causef builds a cause that includes runtime-specific detail.
func Causef(code, short, format string, args ...any) *AbortCause {
	return &AbortCause{Code: code, Short: short, Text: fmt.Sprintf(format, args...)}
}

var (
	// Task-level execution context.
	AbortPausedByUser = cause("paused_by_user", "사용자가 작업을 일시 중지했습니다.",
		"사용자는 임무 제어 인터페이스를 사용합니다.（POST /api/tasks/{id}/control，action=pause）작업을 일시 중지했습니다.。이번에는 Planner/Worker 작업이 적극적으로 취소되었습니다.；실행 중인 인텐트가 반환됩니다. frontier(open)，작업 재개 후 다시 획득하여 처음부터 실행")
	AbortPausedByOrchestrator = cause("paused_by_orchestrator", "편곡 Agent 작업을 일시 중지했습니다.",
		"편곡 Agent 전화했어요 pause_task 도구가 이 작업을 일시 중지합니다.。이번에는 Planner/Worker 작업이 적극적으로 취소되었습니다.；실행 중인 인텐트가 반환됩니다. frontier(open)，복구 후 재실행")
	AbortTaskDeleted = cause("task_deleted", "작업이 삭제되었습니다.",
		"작업을 삭제하는 중입니다.（DELETE /api/tasks/{id}），장벽 삭제가 취소되었습니다. 작업이 실행 중입니다. Planner、Worker 그리고 주님 Agent；이 작업의 결과는 다시 사용되지 않습니다.")
	AbortPausedOnReload = cause("paused_on_reload", "백엔드가 작업의 일시 중지 상태를 재개했습니다.",
		"백엔드가 시작되면 데이터베이스의 지속 상태에 따라 작업 일시 중단이 재개됩니다.。이 실행이 취소되었습니다.；일반적인 상황에서는 복구 단계에서 실행 중인 프로세스가 없습니다. Agent")
	AbortGoalMet = cause("goal_met", "기획자는 미션 목표가 달성되었다고 판단한다.",
		"기획자는 작업 목표가 달성되었다고 판단하고 작업을 다음과 같이 설정합니다. done，그럼 실행 중인 것을 취소하세요. Worker；이러한 의도는 다음과 같이 표시됩니다. stopped，실패 대신")
	AbortSettleDrainTimeout = cause("settle_drain_timeout", "작업 시간 초과 완료를 위한 대기 시간이 소진되었습니다.",
		"임무 도착 timeout 그럼 실행을 기다리세요 Worker 우아한 엔딩，하지만 90 초 drain 그레이스는 아직 부족해요，그러니 강제 취소를 수행하세요.；의도는 다음과 같이 표시됩니다. exhausted，마감 단계에서 이미 작성된 사실과 자산은 유지됩니다.")

	// Per-work context.
	AbortKilledByPlanner = cause("killed_by_planner", "기획자는 이 의도를 종료했습니다.",
		"플래너 콜 kill_work 이 주도적으로 이 의도를 종료시켰습니다.，일반적으로 방향이 벗어났거나 더 이상 가치가 없다는 의미입니다.；의도는 다음과 같이 표시됩니다. stopped，은 자동으로 다시 획득되지 않습니다.")
	AbortWorkPausedByUser = cause("work_paused_by_user", "사용자가 일시중지했습니다. Worker 의도",
		"사용자가 실행을 일시 중지했습니다. Worker。통화가 취소되었습니다.，의도가 다음으로 변경됨 paused；의향등록、사실、모든 취약점 및 활동 기록이 유지됩니다.，복구 후 처음부터 다시 실행")
	AbortWorkCancelledByUser = cause("work_cancelled_by_user", "사용자가 삭제했습니다. Worker 의도",
		"사용자가 실행 중인 항목을 삭제했습니다. Worker。통화가 취소되었습니다.；Worker 글쓰기 영역 종료 후，사용자가 선택한 삭제 모드에 따라 서버가 인텐트를 처리합니다.——거짓 삭제는 삭제된 것으로만 표시하고 모든 출력을 유지합니다.，진정한 삭제는 의도와 그에 의해서만 지원되는 다운스트림 노드를 계단식으로 제거합니다.")
	AbortWorkFinished = cause("work_finished", "Worker 정상적으로 종료되어 출시되었습니다 context",
		"Worker 정상적으로 종료되었습니다，엔진은 detachWork 풀어주세요 context 리소스。방해가 아닙니다；인터럽트 메시지에 나타나는 경우，취소 이벤트와 마감 이벤트 사이에 경합이 있음을 나타냅니다.")
	AbortPausedRaceGuard = cause("paused_race_guard", "작업이 일시 중지된 동안 새 실행 시작을 거부합니다.",
		"작업이 일시정지된 상태일 때，엔진이 새로운 실행 실행을 거부했습니다. context，은 방지하는 데 사용됩니다. claim 과 일시 중지 사이의 경쟁 조건으로 인해 발생 Worker 계속 시작하세요；받은 의도를 돌려드립니다. frontier")

	// Main Agent and standalone conversation contexts.
	AbortChatStoppedByUser = cause("chat_stopped_by_user", "사용자가 이 대화를 중단했습니다.",
		"사용자가 중지를 클릭했습니다.，주도적으로 이번 라운드를 종료하세요. Agent 또는 대화 Agent 달려라。생성된 활동기록은 그대로 유지됩니다.，다음 메시지를 계속 보내실 수 있습니다")
	AbortChatPausedWithTask = cause("chat_paused_with_task", "작업이 일시 중지되고 기본 작업이 중단되었습니다. Agent 대화",
		"사용자가 작업을 일시 중지할 때，메인을 실행 중 Agent 대화도 동시에 취소되었습니다。생성된 활동기록은 그대로 유지됩니다.；이 메시지 라운드는 작업을 재개한 후에 자동으로 재생되지 않습니다.")
	AbortChatTurnFinished = cause("chat_turn_finished", "이번 대화는 정상적으로 종료되어 공개되었습니다. context",
		"이번 대화는 정상적으로 종료되었습니다，서버가 라운드를 공개하고 있습니다. context 리소스。방해가 아닙니다；인터럽트 메시지에 나타나는 경우，취소 이벤트와 마감 이벤트 사이에 경합이 있음을 나타냅니다.")

	// Process-level and per-run hard backstop.
	AbortShutdown = cause("shutdown", "백엔드 프로세스가 종료됩니다.",
		"백엔드 프로세스가 수신되었습니다. SIGINT 또는 SIGTERM，다시 시작하는 중、업데이트 또는 종료。모두 실행 중 Agent 취소됩니다.；다시 시작해도 남음 running 의도가 다음으로 재설정됩니다. open 그리고 다시 실행")
	AbortRunHardTimeout = cause("run_hard_timeout", "단일 실행에 대한 하드 시간 초과가 트리거되었습니다.",
		"한 번의 실행으로 부드러운 벽시계 예산과 추가 은혜를 초과합니다.，모델 요청이나 도구가 오랫동안 반환되지 않았다는 의미입니다.，결과적으로 일반 라운드 경계 엔딩을 실행할 수 없습니다.。중단 이전에 반환되지 않은 마지막 도구 호출을 집중적으로 확인하십시오.")
)

// AbortReason resolves the named cause attached to a cancelled run context.
func AbortReason(ctx context.Context) (code, short, text string, ok bool) {
	c := context.Cause(ctx)
	if c == nil {
		return "", "", "", false
	}
	var ac *AbortCause
	if errors.As(c, &ac) {
		return ac.Code, ac.Short, ac.Text, true
	}
	switch {
	case errors.Is(c, context.DeadlineExceeded):
		return "deadline_exceeded", "업스트림 context 도착 deadline",
			"업스트림 context 도착 deadline，그런데 세팅파티가 실패했어요 WithTimeoutCause 추가 사유: " + c.Error(), true
	case errors.Is(c, context.Canceled):
		return "canceled_no_cause", "취소 당사자가 명시적인 사유를 첨부하지 않았습니다.",
			"업스트림 context 취소됨，그런데 취소당은 불합격됐어요 context.WithCancelCause 추가 사유；부탁드려요 agent/cancelcause.go 사유등록 및 취소포인트 접속", true
	default:
		return "other", firstLine(c.Error(), 80), c.Error(), true
	}
}
