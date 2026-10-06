package server

import (
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
)

// 서버측 재시도 전략 분석，또 만나요 docs/LLM디자인 재시도.md。5층에：
//   - 지안롄 / 빈 응답 / 마찬가지예요 provider 보안창 네「끝점을 따라가세요」님，각 LLM 구성을 재정의할 수 있습니다.
//     전역 기본값（profile 항목을 공백으로 두면 전역 항목을 상속받습니다.，전역 구성이 없으면 내장된 기본값을 사용합니다.）；
//   - 붕괴 / 다시 뛰고 싶은 마음 은 프로세스 수준입니다.，전역 복사본이 하나만 있습니다.。
//
// 글로벌 정책을 한 번 읽어보세요. DB 한 줄 settings，콜포인트가 모두 저주파 경로에 있습니다.（빌드 provider、work 종료、
// 구성 저장），캐싱 레이어를 추가할 가치가 없습니다.；퓨즈 매개변수는 예외입니다.——실패한 경로에서 매번 읽습니다.，그래서
//  applyRetryPolicy 푸시 Registry 저장。

// retryPolicy reads the global policy; a nil DB yields the zero policy (all
// layers on their built-in defaults).
func (s *Server) retryPolicy() db.LLMRetryPolicy {
	if s.m == nil || s.m.pg == nil {
		return db.LLMRetryPolicy{}
	}
	return s.m.pg.LLMRetryPolicy()
}

// resolveRetry layers one profile's override on top of the global policy and
// converts the result into the form agent.Config carries. Rules combine field by
// field, so a profile that only pins an interval still inherits the global count.
func resolveRetry(o db.RetryOverride, pol db.LLMRetryPolicy) agent.RetryConfig {
	connect := o.Connect.Or(pol.Connect)
	empty := o.Empty.Or(pol.Empty)
	stream := o.Stream.Or(pol.Stream)
	return agent.RetryConfig{
		// 여기서는 횟수가 유지됩니다.「0=기본값 / 부정=닫기」의 원래 의미:SDK 님 MaxRetries /
		// EmptyResponseRetries 완전히 동형입니다.,그냥 맡기고 분석해보세요.。
		ConnectAttempts: connect.Attempts, ConnectInterval: connect.Interval(),
		EmptyAttempts: empty.Attempts, EmptyInterval: empty.Interval(),
		StreamAttempts: stream.Attempts, StreamInterval: stream.Interval(),
	}
}

// applyProfileRetry fills cfg.Retry for a profile read from the DB.
func (s *Server) applyProfileRetry(cfg *agent.Config, p *db.LLMProfile) {
	if p == nil {
		return
	}
	cfg.Retry = resolveRetry(p.Retry, s.retryPolicy())
}

// 붕괴(폴링 냉각)의 기본값,그리고 llmpool 내장된 일관성 —— 여기에서만「사용자 지정 값」해당사항만 적용됨。
// 인텐트 재실행의 기본값은 다음을 참조하세요. engine.go 님 modelErrorRetries / modelErrorRetryBackoff。

// applyRetryPolicy pushes the process-wide layers of the policy into the objects
// that consume them on a hot path: the circuit-breaker registry. Called at
// startup and whenever the policy is saved.
func (s *Server) applyRetryPolicy() {
	pol := s.retryPolicy()
	if s.llmHealth != nil {
		s.llmHealth.SetPolicy(pol.Breaker.Attempts, pol.Breaker.Interval())
	}
}

// modelErrorRetryPolicy resolves the intent-level replay knobs (layer ⑤): how
// many times a model_error work is re-run and how long to back off between runs.
func (e *Engine) modelErrorRetryPolicy() (retries int, backoff time.Duration) {
	retries, backoff = modelErrorRetries, modelErrorRetryBackoff
	if e == nil || e.m == nil || e.m.pg == nil {
		return retries, backoff
	}
	rule := e.m.pg.LLMRetryPolicy().Intent
	if rule.Attempts != 0 {
		retries = max(rule.Attempts, 0)
	}
	if d := rule.Interval(); d > 0 {
		backoff = d
	}
	return retries, backoff
}

// emptyTurnNudgeLimit resolves how many empty-turn continuations one work may
// inject (see steerHooks.Stop). It deliberately reuses layer ②'s knob —— 「빈 응답
// 재시도 횟수」:둘은 같은 뜻입니다。SDK 저 파이프층「단일 콘텐츠 블록이 아님」，방법은
// 같은 요청을 그대로 다시 보냅니다.;여기서 관리함「생각뿐이다、글도 없고 도구도 없고」，방법은 다음과 같은 명령을 추가하는 것입니다.
// 기존 생각을 모델화하고 계속 이어가기(컨텍스트 형태에 따라 결정되는 이러한 종류의 유휴 상태에는 있는 그대로 재전송하는 것이 의미가 없습니다.)。짧은 판단력
// 차이점은 SDK 에게「혹시 있나요? yield 지난 행사」이 우선합니다，그리고 증분에 대해 생각하는 것 자체가 하나의 사건입니다.——하지만 사용자 구성은
// 「빈 응답을 여러 번 재시도」제가 표현하고 싶은 건「모델이 실질적인 콘텐츠를 생산하지 못하면 다시 시도하십시오.」，두 레벨이 하나의 개수를 공유함
// 이런 마음으로만。
//
// 특정 정책 대신 글로벌 정책을 읽으십시오. profile 취재:하나 run 중간에 장애조치(failover)로 인해 변경될 수 있습니다. profile，그리고 이건
// 은 전체 의도의 총액입니다.，엔드포인트를 변경해도 변경되어서는 안 됩니다.。의미론 및 SDK 님 emptyRetries() 동형:
// 0 = 기본값 defaultEmptyTurnNudges;-1(부정) = 공회전을 끄고 계속 실행;>0 = 이 값을 사용하세요。
func (e *Engine) emptyTurnNudgeLimit() int {
	if e == nil || e.m == nil || e.m.pg == nil {
		return defaultEmptyTurnNudges
	}
	switch n := e.m.pg.LLMRetryPolicy().Empty.Attempts; {
	case n == 0:
		return defaultEmptyTurnNudges
	case n < 0:
		return 0
	default:
		return n
	}
}
