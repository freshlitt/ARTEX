package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"
)

// dingTalkChannel DingTalk 맞춤형 로봇 구현。
//
// 플랫폼 기능（은 여기에서 구현 선택을 결정합니다.）：
//   - 단일 로봇 전류 제한 20 글/분，초과전송은 자동으로 폐기됩니다（HTTP 그래도 가능해요 200），
//     따라서 전류 제한은 클라이언트 측에서 수행되어야 합니다.，또 만나요 DefaultRatePerMin。
//   - 세 가지 보안 설정 중 하나를 선택하세요.：서명 추가 / 맞춤 키워드 / IP 화이트리스트。서명은 의존하지 않는 유일한 방법입니다.
//     메시지 내용 계획，따라서 서명만 지원됩니다.（도 세 가지 모두에서 과도한 노출을 지원하지 않습니다. webhook）。
//   - 성공/실패하면 반환됩니다. HTTP 200，젠장 body 에 errcode 구별하세요——확인하지 않음 errcode
//     은 배달 실패를 성공으로 기록합니다.。
type dingTalkChannel struct{}

func (dingTalkChannel) Kind() string { return KindDingTalk }

func (dingTalkChannel) DefaultRatePerMin() int { return 20 }

// 딩딩 Webhook 주소에 access_token，자체가 자격 증명입니다.，그래서 종합마스크。
func (dingTalkChannel) SecretKeys() []string { return []string{"webhook", "secret"} }

// 골은 확정됐다 Webhook 주소 그 자체；주소 변경 시 새 주소에 대한 서명키를 동시에 다시 기재해야 합니다.。
func (dingTalkChannel) DestinationKeys() []string { return []string{"webhook"} }

func (dingTalkChannel) Validate(cfg map[string]any) error {
	hook := cfgString(cfg, "webhook")
	if hook == "" {
		return errors.New("없어짐 Webhook 주소")
	}
	if err := validateHTTPURL(hook); err != nil {
		return fmt.Errorf("Webhook 주소가 잘못되었습니다.: %w", err)
	}
	return nil
}

// Send 메시지를 한 번만 전달하세요.。백링크가 있고 단일링크일 때 사용 ActionCard（버튼 있음），그렇지 않으면 사용 markdown。
func (c dingTalkChannel) Send(ctx context.Context, cfg map[string]any, m Message) (int, error) {
	hook := cfgString(cfg, "webhook")
	if err := c.Validate(cfg); err != nil {
		return 0, Permanent(err)
	}
	endpoint, err := dingTalkSignedURL(hook, cfgString(cfg, "secret"), time.Now())
	if err != nil {
		return 0, Permanent(err)
	}

	title := markdownTitle(m)
	// 딩톡 markdown 텍스트에 대한 명확한 바이트 제한이 없습니다.，하지만 여전히 상한 보호를 수행합니다.，증거필드의 비정상적 확장 방지。
	text, kept := markdownBody(m, 20000)

	var payload any
	if !m.Batch && len(m.Items) == 1 && m.Items[0].DetailURL != "" {
		payload = map[string]any{
			"msgtype": "actionCard",
			"actionCard": map[string]any{
				"title":          title,
				"text":           text,
				"btnOrientation": "0",
				"singleTitle":    "자세히 보기",
				"singleURL":      m.Items[0].DetailURL,
			},
		}
	} else {
		payload = map[string]any{
			"msgtype":  "markdown",
			"markdown": map[string]any{"title": title, "text": text},
		}
	}

	raw, err := doJSON(ctx, "POST", endpoint, nil, payload)
	if err != nil {
		return 0, err
	}
	// DingTalk는 비즈니스 실수를 숨깁니다. 200 답변에。
	var res struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return 0, fmt.Errorf("DingTalk 응답을 구문 분석하지 못했습니다.: %w (%s)", err, snippet(raw))
	}
	if res.ErrCode != 0 {
		// 301000 서명 확인 실패、310000 키워드 불일치입니다——모두 구성 오류입니다.，
		// 재시도 자체가 복구되지 않음。
		return 0, Permanent(fmt.Errorf("DingTalk에서 오류가 발생했습니다. %d: %s", res.ErrCode, res.ErrMsg))
	}
	return kept, nil
}

// dingTalkSignedURL 공식 서명 규정에 따름 webhook 추가 timestamp 그리고 sign 매개변수。
//
// 규칙：서명할 문자열 = timestamp + "\n" + secret，HMAC-SHA256 님**핵심도 마찬가지다 secret**，
// 결과 base64 이후 URL 인코딩。timestamp 은 밀리초입니다.。secret 비어 있으면 그대로 반환합니다.，
// 서명이 활성화되지 않은 로봇을 지원하려면。
func dingTalkSignedURL(hook, secret string, now time.Time) (string, error) {
	if secret == "" {
		return hook, nil
	}
	ts := strconv.FormatInt(now.UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "\n" + secret))
	sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	u, err := url.Parse(hook)
	if err != nil {
		// 투명 전송 없음 err：url.Parse 의 오류 텍스트에는 전체 주소가 포함되어 있습니다.（포함 access_token）。
		return "", fmt.Errorf("분석 Webhook 주소 실패: %s", redactRequestTarget(hook))
	}
	q := u.Query()
	q.Set("timestamp", ts)
	q.Set("sign", sign)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// validateHTTPURL 인증주소가 있습니다、프로토콜 지원，및 리터럴 비교 IP 대상이 인트라넷 판단을 합니다.。
//
// 주의할 점 2가지：
//
//  1. **오류 메시지는 민감도를 낮추어야 합니다.**。url.Parse 제가 돌려드린 것은 *url.Error，그래요 Error() 와 함께
//     **원래 주소를 모두 입력하세요.**，이 기능을 갖춘 회사의 주소에는 자격 증명이 내장되어 있습니다.（딩톡 access_token、
//     치웨이 key、Telegram 님 bot token、페이슈 hook id）。예전엔 여기가 직설적이었는데 `return err`，
//     그래서「주소 형식이 잘못되었습니다.」이 오류는 자격 증명을 제거합니다.，은 테스트 인터페이스로 흐릅니다. 400 응답、
//     각 배달은 데이터베이스에 저장됩니다. last_error、서버 로그 및 전송 내역 인터페이스。
//
//  2. **리터럴 IP 인트라넷에서 직접 판단**，도메인 이름은 전화 접속 단계 결정을 위해 예약되어 있습니다.（blockInternalDial 이 마지막이에요
//     유효점，도 커버 가능 DNS 다시 바인딩）。이 작업을 한 번 수행하면 구성을 저장할 때 프롬프트가 표시됩니다.，
//     첫 번째 배송이 실패할 때까지 기다리는 대신。
//
// 금지 합의는 방어적입니다：file:///gopher:// 등을 만들게 됩니다. http.Client 예상치 못한 결과가 발생했습니다.
// 행동（비록 그랬지만 scheme 블록을 확인하세요，하지만 이 얼굴을 놓을 이유가 없다.）。
func validateHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("주소를 확인할 수 없습니다.（%s）", redactRequestTarget(raw))
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("지원만 가능 http/https，받음 %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("호스트 이름이 누락되었습니다.")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && isBlockedDialIP(ip) && !allowLocalTargets() {
		return fmt.Errorf("이 기계로의 배송을 거부합니다/링크 로컬 주소 %s（꼭 현지 서비스로 전달해야 하는 경우，설정 %s=1）", ip, AllowLocalTargetsEnv)
	}
	return nil
}
