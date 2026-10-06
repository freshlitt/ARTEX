package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Autumn-27/norma/harness"
)

// runTrace retains the latest tool call so an interrupted run can identify the
// operation that was still in flight.
type runTrace struct {
	startedAt time.Time
	id        string
	name      string
	input     string
	at        time.Time
	pending   bool
}

func (t *runTrace) start(id, name, input string) {
	t.id, t.name, t.input, t.at, t.pending = id, name, input, time.Now(), true
}

func (t *runTrace) done(id string) {
	if id == t.id {
		t.pending = false
	}
}

var reasonHint = map[harness.TerminalReason]string{
	harness.ReasonCompleted:         "모델님은 라운드를 정상적으로 종료하였습니다，하지만 텍스트 요약이 남지 않았습니다.；사실과 자산은 이번 라운드의 도구 통화 기록에 따릅니다.",
	harness.ReasonMaxTurns:          "걸음 수 상한에 도달했습니다.(MaxTurns)：SDK 폐쇄가 수행되었으며 사실과 자산이 다시 기록되었습니다.，의도는 다음과 같이 표시됩니다. exhausted，기획자가 방향을 바꾸고 계속하기 위해，실패로 처리하는 대신",
	harness.ReasonTimeout:           "단일 실행을 위한 벽시계 예산에 도달했습니다.(MaxDuration)：이 시점에서 실행 중인 도구가 중단되고 그 자리에서 완료됩니다.，확인된 사실과 자산을 다시 작성하십시오.，의도는 다음과 같이 표시됩니다. exhausted",
	harness.ReasonModelError:        "모델 또는 API 통화가 실패했습니다.（네트워크、인증、전류 제한、공급업체 5xx 등），의도가 다음으로 표시됨 blocked——전송 계층 오류로 인해 이러한 의도는 기본적으로 실현되지 않았습니다.；실행과정을 확인하세요（get_worker_trace）방법을 재할당하거나 변경하기로 결정하기 전에",
	harness.ReasonBlockingLimit:     "컨텍스트 길이가 하드 제한에 도달했습니다.，요청이 전송되기 전에 차단되었습니다.；의도 세분성을 좁히지 않으면 압축 도구가 반환되어야 합니다.",
	harness.ReasonPromptTooLong:     "프롬프트 단어가 너무 길어서 컨텍스트 압축 재시도가 소진되었습니다.，실행을 계속할 수 없습니다.",
	harness.ReasonImageError:        "현재 모델은 이번 라운드에서 다중 모드 콘텐츠를 지원하지 않습니다.；비전을 지원하는 모델로 전환하거나 도구가 이미지를 반환하는 것을 피하십시오",
	harness.ReasonStopHookPrevented: "Stop 훅이 이번 라운드의 끝을 막는다，그러면 계속하지 못했습니다.；작업을 확인해주세요 Guard 규칙이 너무 엄격한가요?",
	harness.ReasonHookStopped:       "도구 또는 후크가 실행을 적극적으로 중지합니다.，예를 들어 범위를 벗어난 대상 또는 비활성화된 명령；마지막 내용을 확인해주세요 tool_result 에 대한 차단 지침",
	harness.ReasonAbortedStreaming:  "모델 출력 스트리밍 생성 단계 중에 실행이 취소되었습니다.",
	harness.ReasonAbortedTools:      "도구 실행 단계 중에 실행이 취소되었습니다.",
}

// terminalText renders a terminal event with no final text into a compact summary
// and a Markdown detail block.
func terminalText(ctx context.Context, term *harness.Terminal, tr *runTrace) (string, string) {
	reason := term.Reason
	aborted := reason == harness.ReasonAbortedStreaming || reason == harness.ReasonAbortedTools
	// Prompt may return ctx.Err directly without a terminal event. Preserve the
	// cancellation cause instead of falling back to an empty/unknown terminal reason.
	if reason == "" && ctx.Err() != nil {
		aborted = true
	}

	var sum string
	if aborted {
		_, short, _, ok := AbortReason(ctx)
		if !ok {
			short = "취소 사유를 가져오지 못했습니다."
		}
		stage := "실행 중"
		switch reason {
		case harness.ReasonAbortedStreaming:
			stage = "모델 출력 단계"
		case harness.ReasonAbortedTools:
			stage = "도구 실행 단계"
		}
		sum = "（작업이 중단되었습니다.：" + short + "；멈추세요" + stage + progressSuffix(term, tr) + "，완료되지 않음）"
	} else if reason == harness.ReasonMaxTurns || reason == harness.ReasonTimeout {
		sum = "（운영 예산 한도 도달(" + string(reason) + ")，사실반론을 마쳤습니다" + progressSuffix(term, tr) + "；이번에는 텍스트 요약이 없습니다.）"
	} else {
		hint := terminalReasonHint(reason)
		sum = "（텍스트 요약 없음，최종 상태 " + terminalReasonLabel(reason) + "：" + firstLine(hint, 80) + "）"
	}

	var b strings.Builder
	b.WriteString(sum)
	b.WriteString("\n\n")
	displayReason := terminalReasonLabel(reason)
	fmt.Fprintf(&b, "- **최종 상태**: `%s` - %s\n", displayReason, terminalReasonHint(reason))
	if aborted {
		code, _, why, ok := AbortReason(ctx)
		if ok {
			fmt.Fprintf(&b, "- **중단 이유** (`%s`): %s\n", code, why)
		} else {
			b.WriteString("- **중단 이유**: 구할 수 없습니다；취소 당사자가 통과하지 못했을 수도 있습니다. context.WithCancelCause 추가 사유\n")
		}
	}
	if term.Err != nil {
		fmt.Fprintf(&b, "- **근본적인 오류**: `%v`\n", term.Err)
	}
	if aborted && strings.TrimSpace(term.Text) != "" {
		b.WriteString("- **이전에 생성된 출력의 일부를 취소합니다.**:\n\n")
		b.WriteString(term.Text)
		b.WriteString("\n\n")
	}
	if term.Turns > 0 {
		fmt.Fprintf(&b, "- **실행됨**: %d 휠 모델 라운드\n", term.Turns)
	}
	if !tr.startedAt.IsZero() {
		fmt.Fprintf(&b, "- **이 실행에는 시간이 걸립니다.**: %s\n", roundDur(time.Since(tr.startedAt)))
	}
	if u := term.Usage; u.InputTokens+u.OutputTokens+u.CacheReadTokens+u.CacheWriteTokens > 0 {
		fmt.Fprintf(&b, "- **누적 token**: 입력 %d / 출력 %d / 캐시 읽기 %d / 캐시 쓰기 %d\n",
			u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens)
	}
	if tr.name == "" {
		b.WriteString("- **공구콜**: 이 실행은 도구 호출이 실행되기 전에 종료되었습니다.\n")
	} else if tr.pending {
		fmt.Fprintf(&b, "- **중단 시 실행 중인 도구**: `%s`（달리고 있다 %s，**반환된 결과가 없습니다.**）\n\n  ```json\n  %s\n  ```\n",
			tr.name, roundDur(time.Since(tr.at)), firstLine(tr.input, 300))
	} else {
		fmt.Fprintf(&b, "- **중단 전 마지막 도구**: `%s`（정상적으로 돌아왔습니다）\n", tr.name)
	}
	return sum, b.String()
}

func terminalReasonLabel(reason harness.TerminalReason) string {
	if reason == "" {
		return "context_canceled"
	}
	return string(reason)
}

func terminalReasonHint(reason harness.TerminalReason) string {
	if hint := reasonHint[reason]; hint != "" {
		return hint
	}
	if reason == "" {
		return "달리고 있다 context 취소됨，하지만 맨 아래 레이어는 생성되지 않습니다. Terminal 이벤트"
	}
	return "최종 상태를 알 수 없음；harness 추가될 수 있음 TerminalReason，추가해주세요 reasonHint"
}

func progressSuffix(term *harness.Terminal, tr *runTrace) string {
	var parts []string
	if term.Turns > 0 {
		parts = append(parts, fmt.Sprintf("%d 휠", term.Turns))
	}
	if !tr.startedAt.IsZero() {
		parts = append(parts, roundDur(time.Since(tr.startedAt)))
	}
	if len(parts) == 0 {
		return ""
	}
	return "，달리고 있다 " + strings.Join(parts, " / ")
}

func roundDur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return d.Round(100 * time.Millisecond).String()
	case d < time.Hour:
		return d.Round(time.Second).String()
	default:
		return d.Round(time.Minute).String()
	}
}
