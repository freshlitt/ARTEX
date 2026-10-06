package db

import (
	"encoding/json"
	"time"
)

// LLM 재시도 전략：레이어 5 재시도「회 + 간격」글로벌 구성，또 만나요 docs/LLM디자인 재시도.md。
// 존재합니다 settings 테이블 중 하나 JSON 값 —— 전체 기계의 작동 매개변수입니다.，테이블을 열 가치가 없습니다.；
// 내장된 기본 포켓 읽기，그럼 열쇠가 존재하지 않는군요(새로운 도서관/구성되지 않음)동작은 하드코딩된 상수 시대와 완전히 일치합니다.。

const settingLLMRetryPolicy = "llm_retry_policy"

// RetryRule is one layer's knob pair. The zero value means "unset":
//
//	Attempts   0 = 내장된 기본 시간 사용; -1 = 이 레이어를 닫고 다시 시도해 보세요.; >0 = 이 값을 사용하세요
//	IntervalMS 0 = 이 레이어의 원래 간격 전략을 사용합니다.(일반적으로 지수 백오프); >0 = 대신 고정된 밀리초 간격을 사용하세요.
//
// -1 네「명시적으로 끄기」대신「0 회」，왜냐하면 0 이 되었습니다.「구성되지 않음」점유。
type RetryRule struct {
	Attempts   int `json:"attempts"`
	IntervalMS int `json:"interval_ms"`
}

// Interval returns the configured fixed interval, or 0 when unset (caller keeps
// its own default ladder).
func (r RetryRule) Interval() time.Duration {
	if r.IntervalMS <= 0 {
		return 0
	}
	return time.Duration(r.IntervalMS) * time.Millisecond
}

// Or returns the rule with each unset field filled in from fallback. Used to
// layer a profile override on top of the global policy field by field, so a
// profile that only pins the interval still inherits the global count.
func (r RetryRule) Or(fallback RetryRule) RetryRule {
	if r.Attempts == 0 {
		r.Attempts = fallback.Attempts
	}
	if r.IntervalMS == 0 {
		r.IntervalMS = fallback.IntervalMS
	}
	return r
}

// retry knob bounds. A count above the cap turns a blip into a token bonfire;
// an interval above an hour outlives any transient failure worth waiting out.
const (
	maxRetryAttempts   = 20
	maxRetryIntervalMS = 3600_000 // 1h
)

// Clamped returns the rule with out-of-range values pulled back into the sane
// band (attempts within [-1, 20], interval within [0, 1h]).
func (r RetryRule) Clamped() RetryRule {
	if r.Attempts < -1 {
		r.Attempts = -1
	}
	if r.Attempts > maxRetryAttempts {
		r.Attempts = maxRetryAttempts
	}
	if r.IntervalMS < 0 {
		r.IntervalMS = 0
	}
	if r.IntervalMS > maxRetryIntervalMS {
		r.IntervalMS = maxRetryIntervalMS
	}
	return r
}

// Clamped bounds a profile's override the same way the global policy is bounded,
// so a hand-crafted API payload can't land a value the CHECK constraint rejects.
func (o RetryOverride) Clamped() RetryOverride {
	o.Connect, o.Empty, o.Stream = o.Connect.Clamped(), o.Empty.Clamped(), o.Stream.Clamped()
	return o
}

// LLMRetryPolicy holds the5층 retry configuration. Connect/Empty/Stream are the
// per-request layers (a profile may override them, see LLMProfile.Retry);
// Breaker and Intent are process-wide by nature and live only here.
type LLMRetryPolicy struct {
	// Connect：SDK 연결 다시 시도(연결 재설정/시간 초과/429/5xx，흐름이 시작되기 전에)。기본값 3 회、지수 백오프。
	Connect RetryRule `json:"connect"`
	// Empty：SDK 빈 응답 재시도(완료되었으나 아무것도 없음 content block，만 openai 형식)。기본값 2 회、지수 백오프。
	Empty RetryRule `json:"empty"`
	// Stream：마찬가지예요 provider 안전창 재시도(출력이 전달되기 전 중단 재생)。기본값 2 회、0.5s 시작 인덱스(캡 4s)。
	Stream RetryRule `json:"stream"`
	// Breaker：폴링 회로 차단기。Attempts=여러 번 연속해서 순간적으로 오류가 발생하면 퓨즈가 작동됩니다.(기본값 3，-1=퓨즈가 없으면 순간적으로 고장이 납니다.，
	// 잔액 부족 등 하드 장애/열쇠가 실패하면 즉시 날아가게 됩니다.)；IntervalMS=고정 냉각 시간(0=기본값 1/5/30min 그라데이션)。
	Breaker RetryRule `json:"breaker"`
	// Intent：worker 에게 model_error 엔딩 후 의도 전체를 재실행。기본값 2 회、고정됨 3s。
	Intent RetryRule `json:"intent"`
}

// Clamped returns the policy with every rule clamped.
func (p LLMRetryPolicy) Clamped() LLMRetryPolicy {
	p.Connect, p.Empty, p.Stream = p.Connect.Clamped(), p.Empty.Clamped(), p.Stream.Clamped()
	p.Breaker, p.Intent = p.Breaker.Clamped(), p.Intent.Clamped()
	return p
}

// LLMRetryPolicy reads the global retry policy. A missing or unparseable value
// yields the zero policy — i.e. every layer on its built-in default.
func (d *DB) LLMRetryPolicy() LLMRetryPolicy {
	var p LLMRetryPolicy
	if d == nil {
		return p
	}
	raw, ok, err := d.GetSetting(settingLLMRetryPolicy)
	if err != nil || !ok || raw == "" {
		return p
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return LLMRetryPolicy{}
	}
	return p.Clamped()
}

// SetLLMRetryPolicy persists the global retry policy (values are clamped first).
func (d *DB) SetLLMRetryPolicy(p LLMRetryPolicy) error {
	raw, err := json.Marshal(p.Clamped())
	if err != nil {
		return err
	}
	return d.SetSetting(settingLLMRetryPolicy, string(raw))
}
