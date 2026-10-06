package server

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

// 유휴 라운드(생각만 해도、텍스트도 없고 도구도 없습니다.)에 대한 인정과 지속，또 만나요 steerHooks.Stop。

func assistantThinking(text string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
		{Type: llm.BlockThinking, Thinking: text, Signature: "sig"},
	}}
}

func TestIsThinkingOnlyTurn(t *testing.T) {
	toolUse := llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
		{Type: llm.BlockThinking, Thinking: "먼저 포트를 스캔하세요."},
		{Type: llm.BlockToolUse, ID: "t1", Name: "run_nuclei"},
	}}
	cases := []struct {
		name string
		msgs []llm.Message
		want bool
	}{
		{"생각만 해도", []llm.Message{llm.UserText("시작"), assistantThinking("생각해 보세요")}, true},
		{"생각중+도구", []llm.Message{llm.UserText("시작"), toolUse}, false},
		{"생각중+문자", []llm.Message{assistantThinking("생각해 보세요"), {
			Role:    llm.RoleAssistant,
			Content: []llm.ContentBlock{{Type: llm.BlockThinking, Thinking: "x"}, llm.TextBlock("결론")},
		}}, false},
		{"텍스트에 공백 문자만 있습니다.", []llm.Message{{
			Role:    llm.RoleAssistant,
			Content: []llm.ContentBlock{{Type: llm.BlockThinking, Thinking: "x"}, llm.TextBlock("  \n ")},
		}}, true},
		{"완전히 비어 있음 assistant 라운드", []llm.Message{{Role: llm.RoleAssistant}}, true},
		// 도구 결과는 다음과 같습니다. user 역할，판결은 이전 판결로 추적되어야 합니다. assistant，근처의 오판 대신。
		{"마지막은 도구 결과입니다", []llm.Message{toolUse, {
			Role:    llm.RoleUser,
			Content: []llm.ContentBlock{{Type: llm.BlockToolResult, ToolUseID: "t1"}},
		}}, false},
		{"아니요 assistant 메시지", []llm.Message{llm.UserText("시작")}, false},
		{"빈 기록", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isThinkingOnlyTurn(c.msgs); got != c.want {
				t.Fatalf("isThinkingOnlyTurn = %v, want %v", got, c.want)
			}
		})
	}
}

// fakeHooks 은 프로그래밍 가능합니다. inner HookRunner，을 사용하여 확인합니다. steerHooks 예 inner 결정 존중。
type fakeHooks struct {
	prevent  bool
	blocking []string
	msg      string
}

func (f fakeHooks) PreToolUse(context.Context, string, []byte) (bool, string, []byte) {
	return false, "", nil
}
func (f fakeHooks) PostToolUse(context.Context, string, []byte, []byte, bool) {}
func (f fakeHooks) Stop(context.Context, []llm.Message) (bool, []string, string) {
	return f.prevent, f.blocking, f.msg
}

func TestSteerHooksStopNudgesEmptyTurn(t *testing.T) {
	empty := []llm.Message{assistantThinking("먼저 하위 도메인을 열거해야 합니다.")}

	t.Run("유휴 라운드에 계속 명령을 삽입합니다.", func(t *testing.T) {
		h := steerHooks{nudges: &atomic.Int64{}, limit: defaultEmptyTurnNudges, label: "worker-1 · #1"}
		prevent, blocking, _ := h.Stop(context.Background(), empty)
		if prevent {
			t.Fatal("아이들 라운드를 세게 멈춰선 안 된다.")
		}
		if len(blocking) != 1 || blocking[0] != emptyTurnNudge {
			t.Fatalf("blocking = %v, want [emptyTurnNudge]", blocking)
		}
	})

	t.Run("문자나 도구가 있으면 개입하지 마세요.", func(t *testing.T) {
		h := steerHooks{nudges: &atomic.Int64{}, limit: defaultEmptyTurnNudges}
		normal := []llm.Message{{
			Role:    llm.RoleAssistant,
			Content: []llm.ContentBlock{llm.TextBlock("스캔 완료，열려 있는 포트를 찾을 수 없습니다.")},
		}}
		if _, blocking, _ := h.Stop(context.Background(), normal); blocking != nil {
			t.Fatalf("노멀엔딩이 아이들링으로 오인되었습니다.: %v", blocking)
		}
		if n := h.nudges.Load(); n != 0 {
			t.Fatalf("개입이 없으면 계산되지 않습니다., got %d", n)
		}
	})

	t.Run("상한 도달 후 해제 및 종료", func(t *testing.T) {
		const limit = 5 // 사용자「빈 응답 재시도 횟수」더빙 5
		h := steerHooks{nudges: &atomic.Int64{}, limit: limit}
		for i := 1; i <= limit; i++ {
			if _, blocking, _ := h.Stop(context.Background(), empty); len(blocking) != 1 {
				t.Fatalf("아니요. %d 횟수는 여전히 할당량 내에 있어야 합니다., blocking = %v", i, blocking)
			}
		}
		if _, blocking, _ := h.Stop(context.Background(), empty); blocking != nil {
			t.Fatalf("상한을 초과했는데도 계속 주입중입니다.: %v", blocking)
		}
	})

	// 「빈 응답 재시도 횟수」일치 -1 = 이 레이어를 꺼주세요，emptyTurnNudgeLimit 은 다음으로 구문 분석됩니다. 0。
	t.Run("구성이 닫힐 때 개입하지 마십시오.", func(t *testing.T) {
		h := steerHooks{nudges: &atomic.Int64{}, limit: 0}
		if _, blocking, _ := h.Stop(context.Background(), empty); blocking != nil {
			t.Fatalf("닫혀 있고 계속 주입 중: %v", blocking)
		}
	})

	t.Run("inner 강제정지시 중첩하지 않기로 결정했습니다.", func(t *testing.T) {
		h := steerHooks{inner: fakeHooks{prevent: true, msg: "guard 종료를 거부합니다"}, nudges: &atomic.Int64{}, limit: defaultEmptyTurnNudges}
		prevent, blocking, msg := h.Stop(context.Background(), empty)
		if !prevent || msg != "guard 종료를 거부합니다" || blocking != nil {
			t.Fatalf("inner 의 하드스톱을 덮어썼습니다.: prevent=%v blocking=%v msg=%q", prevent, blocking, msg)
		}
		if n := h.nudges.Load(); n != 0 {
			t.Fatalf("양보하세요 inner 인 경우 할당량을 사용하면 안 됩니다., got %d", n)
		}
	})

	t.Run("inner 실행을 계속할 때 중첩이 없습니다.", func(t *testing.T) {
		h := steerHooks{inner: fakeHooks{blocking: []string{"guard 님이 계속하는 이유"}}, nudges: &atomic.Int64{}, limit: defaultEmptyTurnNudges}
		_, blocking, _ := h.Stop(context.Background(), empty)
		if len(blocking) != 1 || blocking[0] != "guard 님이 계속하는 이유" {
			t.Fatalf("inner 님의 계속되는 소식이 다시 작성되었습니다.: %v", blocking)
		}
	})

	t.Run("카운터가 설치되지 않은 경우 동작은 변경되지 않습니다.", func(t *testing.T) {
		h := steerHooks{limit: defaultEmptyTurnNudges} // 예를 들어 앞으로 다른 콜 포인트를 패스하는 것을 잊어버린 경우 nudges
		if _, blocking, _ := h.Stop(context.Background(), empty); blocking != nil {
			t.Fatalf("카운터가 없을 때는 주입하면 안 된다.: %v", blocking)
		}
	})
}
