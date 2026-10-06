// Package agent wires real LLM-driven planner and work agents (on top of the
// agent-core SDK) to the dual SQLite graph. See docs/ARTEX-건축설계.md
// §4.3 (planner) and §4.4 (work agent).
//
// Provider configuration is read from the environment so the system runs with
// any Anthropic- or OpenAI-format endpoint. If no key is configured, FromEnv
// returns ok=false and the exploration engine stays idle (an LLM is required).
package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/Autumn-27/artex/llmrec"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/compaction"
	"github.com/Autumn-27/norma/llm"
	acperm "github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/transcript"
)

// Config describes the LLM backend resolved from the environment.
type Config struct {
	Format  llm.Format
	BaseURL string
	APIKey  string
	Model   string
	// Proxy routes all LLM requests through the given proxy URL (http/https/socks5,
	// optionally with user:pass@ credentials). Empty means direct — it does NOT
	// fall back to the standard *_PROXY environment variables.
	Proxy string
	// RatePerSecond / RatePerMinute cap the shared request rate across ALL agents
	// using the provider (0 = that window unlimited).
	RatePerSecond float64
	RatePerMinute float64
	// ContextWindowK is the model's context window in K tokens (user-configured),
	// used to size compaction thresholds. 0 = default; see CompactionWindow.
	ContextWindowK int
	// ThinkingType 독립적인 사고 조절「스위치」필드(thinking.type):
	//   "" = 보내지 않음(기본값,이 필드를 지원하지 않는 모델과 호환됩니다.); "disabled" = 명시적으로 종료;
	//   "enabled" = 켜세요. 그리고 ReasoningEffort 완전히 분리됨——일부 인터페이스를 사용할 수 없습니다. thinking 필드、
	//   강도 매개변수만으로도 사고를 활성화할 수 있습니다.,따라서 둘 다 독립적으로 설정할 수 있습니다..
	ThinkingType string
	// ReasoningEffort 독립적인 사고 조절「힘」필드:
	//   "" = 보내지 않음(기본값); "low"/"medium"/"high"/"xhigh"/"max" = 대응강도.
	//   OpenAI 최상위 수준으로 매핑 reasoning_effort;Anthropic 이(가) 매핑되었습니다. output_config.effort.
	ReasoningEffort string
	// Stream 이것을 조절하세요 profile 스트리밍 사용 여부(SSE)인터페이스。true(기본값)= 스트리밍;false = 맞아요·
	// 비스트리밍(보내기 stream:false,한꺼번에 가져가세요 JSON,가자 Provider.Complete)。비스트리밍 우회 가능
	// 일부 게이트웨이가 불량함 SSE 구현(빈 프레임、사고 영역의 프레임 손실),실시간 작업 진행이 손실되는 대가입니다/실시간
	// token 수。이(가) 매핑되었습니다. agentcore.Options.NonStreaming = !Stream。
	Stream bool
	// MaxTokens 은 단일 응답의 출력 상한입니다.(token)。0 = 이 필드를 보내지 마십시오.,서버의 기본값에 따라 결정됩니다.
	// (역사적 행동)。그리고 ContextWindowK 다르다:후자는 모델의 전체 용량입니다.,압축 임계값을 계산하기 위해 로컬에서만 사용됩니다.,
	// 이 요청에 표시되지 않습니다.;이 값은 각 요청과 함께 전송됩니다.。이(가) 매핑되었습니다. agentcore.Options.MaxTokens。
	MaxTokens int
	// MaxTokensField 선택 MaxTokens 어떤 요청 필드 이름을 사용할 것인가?,전용 format=openai 유효:
	//   "" = max_tokens(기본값); "max_completion_tokens" = 새 필드。
	// OpenAI 추론 모델(o 시리즈/GPT-5)후자만 인식,받음 max_tokens 이 직접 보고하겠습니다
	// unsupported_parameter;대부분의 호환 게이트웨이는 전자만 인식합니다.,따라서 자동 추론이 이루어지지 않습니다.,단말기 클릭은 사용자의 몫으로 남겨두세요。
	MaxTokensField string
	// SessionHeaderKey,비어 있지 않은 경우,매번 하자 LLM 커스텀 지참 요청 HTTP 머리,헤더 이름은 이 값입니다、
	// 헤더 값은【 session id】(chat 대화=conv-<id>,worker=exp<x>-worker-i<intent>
	// 등,또 만나요 WorkerSessionID)。은 일부 버튼에 사용됩니다. session-id 헤더 프롬프트 캐시/고정 라우팅을 위한 게이트웨이。
	// 비어 있음 = 보내지 않음。값은 다음과 같이 지정됩니다. transcript.WithSessionID 요청에 따라 중단됩니다. context 에, RoundTripper
	// 읽고 작성하세요.,그래서 같은 공유 provider 세션마다 다른 헤더 값을 내보낼 수도 있습니다.。
	SessionHeaderKey string
	// Retry 은 구성을 구문 분석한 후 재시도 매개변수입니다.(profile 재정의 → 글로벌 전략 → 기본 내장,
	// server 측면분석)。세 레이어의 의미 보기 RetryConfig;값이 0입니다. = 내장된 기본값을 완전히 사용。
	Retry RetryConfig
}

// RetryConfig 랜덤이에요 LLM 재시도 매개변수 구성。각 레이어「회」통일된 의미론:
// 0 = 내장된 기본 시간 사용;음수 = 이 레이어를 닫고 다시 시도해 보세요.;>0 = 이 값을 사용하세요。각 레이어「간격」:
// 0 = 레이어의 원래 인덱스를 사용하여 백오프합니다.;>0 = 대신 이 고정 간격을 사용하십시오.。
type RetryConfig struct {
	// ConnectAttempts/ConnectInterval:SDK 연결 다시 시도(연결 재설정/시간 초과/429/5xx,흐름이 시작되기 전에),
	// 은 다음에 직접 매핑됩니다. llm.Config.MaxRetries / RetryInterval。기본값 3 회、0.5s 시작 인덱스(캡 8s)。
	ConnectAttempts int
	ConnectInterval time.Duration
	// EmptyAttempts/EmptyInterval:SDK 빈 응답 재시도(완료되었으나 아무것도 없음 content block,만 openai
	// 형식),이(가) 매핑되었습니다. llm.Config.EmptyResponseRetries / EmptyResponseInterval。
	// 기본값 2 회、동일한 지수 기울기。
	EmptyAttempts int
	EmptyInterval time.Duration
	// StreamAttempts/StreamInterval:마찬가지예요 provider 안전창 재시도——이 프로젝트는 SDK 위에 추가함
	// 1층,에서만「아직 호출자에게 출력이 전달되지 않았습니다.」재생이 중단되었습니다./과부하/흐름 내에서 429。SDK 안보이네요,
	//  server/task_llm.go 소비。기본값 2 회、0.5s 시작 인덱스(캡 4s)。
	StreamAttempts int
	StreamInterval time.Duration
}

// compaction window resolution bounds (in K tokens). Below the floor the
// threshold math (window − summary reserve − buffer) would go non-positive and
// compaction would fire every turn; above the cap it would never fire.
const (
	defaultWindowK = 200  // unset → assume a 200K window (Claude default)
	minWindowK     = 32   // floor so effectiveWindow stays comfortably positive
	maxWindowK     = 1000 // cap at 1M tokens (user request)
)

// CompactionWindow returns the model context window in TOKENS for compaction
// thresholds, resolved from the user-configured size (ContextWindowK). 0/unset →
// a 200K default; otherwise clamped to [32K, 1M] so compaction stays effective.
func (c Config) CompactionWindow() int {
	k := c.ContextWindowK
	if k <= 0 {
		k = defaultWindowK
	}
	if k < minWindowK {
		k = minWindowK
	}
	if k > maxWindowK {
		k = maxWindowK
	}
	return k * 1000
}

// compactionConfig builds the agent-core compaction config for a context window
// in tokens. agentcore.NewSession wires the summarizer (same provider) when this
// is set on Options.Compaction.
func compactionConfig(windowTokens int) *compaction.Config {
	if windowTokens <= 0 {
		windowTokens = defaultWindowK * 1000
	}
	return &compaction.Config{ContextWindow: windowTokens}
}

// FromEnv resolves the LLM provider config:
//
//	ARTEX_LLM_PROVIDER = anthropic|openai (default: inferred from keys)
//	ARTEX_LLM_MODEL    = model id        (default: per provider)
//	ARTEX_LLM_BASE_URL = endpoint        (optional)
//	ARTEX_LLM_PROXY    = proxy URL        (optional; http/https/socks5)
//	ANTHROPIC_API_KEY / OPENAI_API_KEY         = credentials
func FromEnv() (Config, bool) {
	prov := os.Getenv("ARTEX_LLM_PROVIDER")
	anthKey := os.Getenv("ANTHROPIC_API_KEY")
	oaiKey := os.Getenv("OPENAI_API_KEY")

	if prov == "" {
		switch {
		case anthKey != "":
			prov = "anthropic"
		case oaiKey != "":
			prov = "openai"
		default:
			return Config{}, false
		}
	}

	c := Config{
		BaseURL: os.Getenv("ARTEX_LLM_BASE_URL"),
		Model:   os.Getenv("ARTEX_LLM_MODEL"),
		Proxy:   strings.TrimSpace(os.Getenv("ARTEX_LLM_PROXY")),
		// 기본 스트리밍;ARTEX_LLM_STREAM=false/0/off 비스트리밍 모드를 명시적으로 종료합니다.。
		Stream: !isFalsy(os.Getenv("ARTEX_LLM_STREAM")),
	}
	switch prov {
	case "openai":
		c.Format = llm.FormatOpenAI
		c.APIKey = oaiKey
		if c.Model == "" {
			c.Model = "gpt-4o"
		}
	case "openai-responses":
		c.Format = llm.FormatOpenAIResponses
		c.APIKey = oaiKey
		if c.Model == "" {
			c.Model = "gpt-5"
		}
	default:
		c.Format = llm.FormatAnthropic
		c.APIKey = anthKey
		if c.Model == "" {
			c.Model = "claude-opus-4-8"
		}
	}
	if c.APIKey == "" {
		return Config{}, false
	}
	return c, true
}

// ConfigFrom builds a Config from UI-provided strings (provider defaults to
// anthropic; model defaults per provider). Inputs are trimmed and the base URL
// is normalized to the API base the provider expects (the provider appends the
// endpoint path itself), so a full endpoint URL is tolerated.
func ConfigFrom(provider, model, baseURL, apiKey, proxy string) Config {
	c := Config{
		Model:   strings.TrimSpace(model),
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		APIKey:  strings.TrimSpace(apiKey),
		Proxy:   strings.TrimSpace(proxy),
		Stream:  true, // 기본 스트리밍;발신자 누르기 profile 재정의
	}
	switch strings.TrimSpace(provider) {
	case "openai":
		c.Format = llm.FormatOpenAI
		// provider appends "/chat/completions"; tolerate a full endpoint URL.
		c.BaseURL = strings.TrimRight(strings.TrimSuffix(c.BaseURL, "/chat/completions"), "/")
		if c.Model == "" {
			c.Model = "gpt-4o"
		}
	case "openai-responses":
		c.Format = llm.FormatOpenAIResponses
		// provider appends "/responses"; tolerate a full endpoint URL.
		c.BaseURL = strings.TrimRight(strings.TrimSuffix(c.BaseURL, "/responses"), "/")
		if c.Model == "" {
			c.Model = "gpt-5"
		}
	default:
		c.Format = llm.FormatAnthropic
		// provider appends "/v1/messages".
		c.BaseURL = strings.TrimRight(strings.TrimSuffix(c.BaseURL, "/v1/messages"), "/")
		if c.Model == "" {
			c.Model = "claude-opus-4-8"
		}
	}
	return c
}

// isFalsy reports whether an env-var string explicitly requests "off". Empty or
// unrecognized → false (so an unset var keeps the streaming default).
func isFalsy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "0", "false", "off", "no":
		return true
	}
	return false
}

// Provider returns the short provider name ("anthropic"/"openai").
func (c Config) Provider() string {
	switch c.Format {
	case llm.FormatOpenAI:
		return "openai"
	case llm.FormatOpenAIResponses:
		return "openai-responses"
	}
	return "anthropic"
}

// NewProvider builds an llm.Provider from the config. When a rate is set, the
// limiter lives on the single provider instance — so planner + all workers +
// main agent (which share this provider) are bounded by one shared rate limit.
func (c Config) NewProvider() (llm.Provider, error) {
	client, err := quotaAwareHTTPClient(c.Proxy, c.SessionHeaderKey)
	if err != nil {
		return nil, err
	}
	lc := llm.Config{
		Format:     c.Format,
		BaseURL:    c.BaseURL,
		APIKey:     c.APIKey,
		Model:      c.Model,
		HTTPClient: client,
	}
	// 사고 전환과 강도의 두 분야가 별도로 투명하게 전달됩니다.(비어 있음 = 이 필드는 전송되지 않습니다.)。둘을 분리하기:
	// 보내기만 가능 thinking.type、보내기만 effort、두파、아니면 아예 보내지 마세요。
	lc.ThinkingType = c.ThinkingType
	lc.ReasoningEffort = c.ReasoningEffort
	// 출력 상한 필드명 선택(비어 있음 = 사용 max_tokens)。상한「값」여기는 없어요:매 라운드마다 바뀌어요
	// agentcore.Options.MaxTokens 가자,provider 어느 키에 넣을지 결정하세요。
	lc.MaxTokensField = c.MaxTokensField
	// 재시도 매개변수 및 SDK 동일한 의미(회 0=기본값/부정=닫기,간격 0=지수 백오프/>0=고정됨),투명한 전달 그대로。
	lc.MaxRetries = c.Retry.ConnectAttempts
	lc.RetryInterval = c.Retry.ConnectInterval
	lc.EmptyResponseRetries = c.Retry.EmptyAttempts
	lc.EmptyResponseInterval = c.Retry.EmptyInterval
	if c.RatePerSecond > 0 || c.RatePerMinute > 0 {
		lc.RateLimit = &llm.RateLimit{PerSecond: c.RatePerSecond, PerMinute: c.RatePerMinute}
	}
	return llm.NewProvider(lc)
}

// IsQuotaExhaustedMessage deliberately recognizes only explicit balance,
// billing, credit, or quota-exhaustion signals. Generic 429/rate-limit text,
// authentication failures, network errors, and server failures are excluded.
var nonFailoverHTTPStatus = regexp.MustCompile(`(?:status(?:\s+code)?|http(?:\s+status)?)\s*[=:]?\s*(?:401|403|5\d\d)\b`)
var transientQuotaLimit = regexp.MustCompile(`(?i)(?:\b(?:rpm|tpm|rpd|qps)\b|quota[_\s-]*metric|rate[_\s-]*limit|too many requests|(?:requests?|tokens?)\s+(?:per|/)\s*(?:second|minute)|(?:per|/)\s*(?:second|minute)\s+(?:requests?|tokens?)|generate[_\s-]*requests[_\s-]*per[_\s-]*(?:minute|second)|tokens?[_\s-]*per[_\s-]*(?:minute|second))`)

func IsQuotaExhaustedMessage(message string) bool {
	message = strings.ToLower(message)
	// Authentication/authorization and provider-side 5xx failures never rotate,
	// even when a gateway happens to echo a quota-looking phrase in the body.
	if nonFailoverHTTPStatus.MatchString(message) {
		return false
	}
	// Provider APIs frequently describe an ordinary rate limit as "quota
	// exceeded", especially Google-style responses containing a quota metric.
	// These limits recover with time and must stay on the current provider.
	if transientQuotaLimit.MatchString(message) {
		return false
	}
	markers := []string{
		"insufficient_quota", "quota_exceeded", "quota exceeded", "quota exhausted",
		"exceeded your current quota", "billing_hard_limit_reached",
		"billing hard limit", "billing_not_active", "credit balance", "insufficient credit",
		"insufficient balance", "balance is too low", "payment required", "status 402",
		"잔고가 부족해요", "할당량이 부족합니다.", "할당량이 소진되었습니다.", "체납",
		"\u4f59\u989d\u4e0d\u8db3", "\u989d\u5ea6\u4e0d\u8db3", "\u989d\u5ea6\u5df2\u7528\u5c3d", "\u6b20\u8d39",
	}
	for _, marker := range markers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	// gRPC RESOURCE_EXHAUSTED is overloaded for both account quota and ordinary
	// request-rate limiting. Preserve it as an explicit exhaustion signal only
	// when the same error does not identify a transient rate limit.
	return strings.Contains(message, "resource_exhausted") &&
		!strings.Contains(message, "rate limit") &&
		!strings.Contains(message, "too many requests")
}

// quotaAwareTransport preserves Norma's normal retry behavior except for a 429
// whose body explicitly says the account quota/balance is exhausted. Norma's
// retry loop treats every 429 as transient; normalizing only that response to
// 402 lets a task router fail over immediately while retaining the original
// response body for provider-specific classification and audit logs.
type quotaAwareTransport struct {
	base http.RoundTripper
	// sessionHeaderKey, when non-empty, is the HTTP header name each request
	// carries; its value is the session id read from the request context. Empty
	// disables it. See Config.SessionHeaderKey.
	sessionHeaderKey string
}

func (t quotaAwareTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Custom session-id header: name is user-configured, value is THIS run's
	// session id (norma stashes it on the context via transcript.WithSessionID).
	// Stable across a session's turns and distinct across sessions — exactly what
	// a session-keyed prompt cache wants. Skipped when no session id is present.
	if t.sessionHeaderKey != "" {
		if sid := transcript.SessionIDFrom(req.Context()); sid != "" {
			req.Header.Set(t.sessionHeaderKey, sid)
		}
	}
	// When LLM recording is on, the Recorder puts a Capture on the context so the
	// raw wire bodies can be persisted. This is the only layer that still sees
	// them: norma builds the request body internally and decodes the SSE response
	// before either reaches the recorder.
	capt := llmrec.CaptureFrom(req.Context())
	capt.SetRequest(requestBodySnapshot(req))

	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}
	// Tee rather than read: a 200 is an SSE stream that must keep streaming. The
	// 429 branch below reads through this wrapper, so its body lands in the
	// capture before being replaced.
	resp.Body = capt.TeeResponse(resp.StatusCode, resp.Body)

	if resp.StatusCode != http.StatusTooManyRequests {
		return resp, nil
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	if readErr != nil {
		return resp, nil
	}
	if IsQuotaExhaustedMessage(string(body)) {
		resp.StatusCode = http.StatusPaymentRequired
		resp.Status = "402 Payment Required"
	}
	return resp, nil
}

// requestBodySnapshot copies an outgoing request body without consuming it.
// norma builds every model request from a *bytes.Reader, so net/http populates
// GetBody and the copy has no effect on what gets sent.
func requestBodySnapshot(req *http.Request) string {
	if req.GetBody == nil {
		return ""
	}
	rc, err := req.GetBody()
	if err != nil {
		return ""
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		return ""
	}
	return string(b)
}

func quotaAwareHTTPClient(proxy, sessionHeaderKey string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	proxy = strings.TrimSpace(proxy)
	if proxy == "" {
		transport.Proxy = nil // 비워두세요=직접 연결,롤백 없음 HTTP_PROXY/HTTPS_PROXY 환경변수
	} else {
		proxyURL, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("llm: invalid proxy %q: %w", proxy, err)
		}
		switch proxyURL.Scheme {
		case "http", "https", "socks5":
		case "":
			return nil, fmt.Errorf("llm: proxy %q missing scheme (use http://, https:// or socks5://)", proxy)
		default:
			return nil, fmt.Errorf("llm: unsupported proxy scheme %q (use http, https or socks5)", proxyURL.Scheme)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	return &http.Client{Transport: quotaAwareTransport{base: transport, sessionHeaderKey: strings.TrimSpace(sessionHeaderKey)}}, nil
}

// logTestConnection prints the raw HTTP status code(s) and response body of a
// connection test to the server log, so "테스트하려면 클릭하세요." leaves a diagnosable trail of
// exactly what the gateway returned — 401 bodies, quota text, empty frames — not
// just the collapsed ok/err the UI shows. Bodies are clipped to keep a chatty
// SSE stream from flooding the log.
func logTestConnection(c Config, capt *llmrec.Capture) {
	attempts := capt.Attempts()
	if len(attempts) == 0 {
		log.Printf("[llm-test] %s / %s @ %s — 님이 보내지 않았습니다. HTTP 요청(구성 분석 또는 연결 설정에 실패했습니다.)",
			c.Provider(), c.Model, c.BaseURL)
		return
	}
	for i, a := range attempts {
		log.Printf("[llm-test] %s / %s @ %s — 시도해 보세요 %d/%d HTTP %d\n응답 본문: %s",
			c.Provider(), c.Model, c.BaseURL, i+1, len(attempts), a.Status, clipBody(a.Body))
	}
}

// clipBody trims a wire body for logging. 4K is plenty to show an error JSON or
// the head of an SSE stream while bounding a runaway response.
func clipBody(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(비어 있음)"
	}
	const max = 4096
	if len(s) > max {
		return s[:max] + fmt.Sprintf("…(잘림,합계 %d 바이트)", len(s))
	}
	return s
}

// TestConnection makes a minimal real completion to verify the provider/model/
// endpoint/key actually work. Returns the round-trip latency and the model's
// reply text.
func TestConnection(ctx context.Context, c Config) (time.Duration, string, error) {
	prov, err := c.NewProvider()
	if err != nil {
		return 0, "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// 원본을 받아가세요 wire 메시지:연결 테스트에서 가장 눈여겨 봐야 할 것은 게이트웨이가 반환하는 내용이다.(상태 코드+응답 본문),
	// 그리고 norma 응답을 다음으로 디코딩합니다. StreamEvent 이 내용은 나중에 사라질 예정입니다.。quotaAwareTransport 갈게요
	// context 에서 찾았습니다. Capture 그리고 매번 채워주세요 HTTP 시도한 상태 코드는 다음과 같습니다. body。
	ctx, capt := llmrec.NewCapture(ctx)
	defer logTestConnection(c, capt)
	// 연결 테스트는 원샷 경로입니다.,불합격 agentcore 의 세션 루프,그럼 아무도 안가네 context 끊으세요
	// session id。일치함 SessionHeaderKey 의 끝점( opencode zen 필수사항
	// x-opencode-session 머리,직접 누락 400 MissingSessionID),결과는 다음과 같습니다."대화는 평범하네요、
	// 테스트하려면 클릭하세요. 400"격차。일회성 무작위 보충 자료입니다. session id,테스트와 실제 대화를 동시에 진행하세요.
	// 헤어 로직 세트;할당되지 않음 SessionHeaderKey 의 엔드포인트는 읽지 않습니다.,부작용 없음。
	ctx = transcript.WithSessionID(ctx, "conntest-"+transcript.NewSessionID())
	start := time.Now()
	// MaxTokens 충분히 주세요：추론 모델( deepseek-v4-pro)답변을 하기 전에 긴 문단을 작성합니다.
	// 생각중(실제 측정에서 나온 한 문장 "ping" 태울 수도 있습니다 ~2900 token)。그렇다면 32,모델은 늘 꼼짝 못해요"생각단계"
	// 출력 상한에 도달했습니다.(finish=length)、잘림,연결 테스트가 계속 작동합니다.(err=nil)하지만 다음과 같이 표시됩니다.
	// "중단됨/length/resume" 엉망이네。그러려면 예산을 충분히 줘라 OK 깨끗하게 토한 후(finish=stop)。
	// EscalateMaxTokens 유지하세요 false:잘림으로 인해 재시도하지 마세요,피하세요 resume 사이클 빈 연소。
	reply, err := agentcore.Run(ctx, agentcore.Options{
		Provider:       prov,
		SystemPrompt:   []string{"접속 테스트 입니다。두 글자를 직접 출력 OK 그렇죠，생각하지 마세요、설명하지 마세요、다른 건 없어요。"},
		PermissionMode: acperm.ModeBypass,
		MaxTurns:       1,
		MaxTokens:      8192,
		NonStreaming:   !c.Stream, // 이것을 사용하세요 profile 의 연결 테스트를 위한 실제 트랜시버 모드
	}, "ping")
	lat := time.Since(start)
	if err != nil {
		return lat, "", err
	}
	// err==nil 부족해요：요청이 승인되었으나 모델은 한마디도 내뱉지 않는 것이 사실입니다.(예산을 다 태워버릴 생각、
	// 보안정책에 의해 해당 텍스트가 삼켜졌습니다.、호환성 레이어 핸들 content 졌다)。세션의 이 구성은"답장이 없습니다",
	// 테스트가 성공했다고 보고되었습니다.——이것이 바로 이 프로젝트가 없애고 싶은 격차입니다.。표시되는 텍스트가 없으면 실패합니다.。
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return lat, "", fmt.Errorf("모델에 답글 내용이 없습니다.（요청이 통과되었습니다，그러나 텍스트가 반환되지 않았습니다.）")
	}
	return lat, reply, nil
}
