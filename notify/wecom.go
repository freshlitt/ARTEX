package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// weComMarkdownLimit 는 Qiweiqun 로봇입니다 markdown content 의 하드 캡（바이트，비문자）。
// 6개 채널 중 가장 엄격한 제한 사항입니다.，도요 TruncateBytes 존재의 주된 이유。
const weComMarkdownLimit = 4096

// weComChannel 기업 위챗 그룹 로봇 구현。
//
// 플랫폼 기능：
//   - 한명만 합격 URL 에 key 인증，서명을 지원하지 않습니다.——그래서 webhook 주소자체가 전부 자격증명입니다。
//   - markdown content 상한 4096 **바이트**，글이 너무 길면 전체가 거절되었습니다.（잘림 아님）。중국어 3 바이트/단어，
//     은 본문에 써야 할 단어가 천 단어도 안 된다는 뜻이에요.，클라이언트에서 잘라야 함。
//   - 전류 제한 20 글/분，또한 클라이언트 전류 제한에 따라 달라집니다.。
type weComChannel struct{}

func (weComChannel) Kind() string { return KindWeCom }

func (weComChannel) DefaultRatePerMin() int { return 20 }

// 기업용 WeChat 전용 Webhook 자격 증명 1개（URL 에 key），그리고 서명을 지원하지 않습니다.——
// 주소 전체가 모두 자격증명입니다.，다른 필드에는 마스킹이 필요하지 않습니다.。
func (weComChannel) SecretKeys() []string { return []string{"webhook"} }

// 치웨이 전용 Webhook 필드，목적지이자 크리덴셜이다.，그럼 안돼요「주소 변경 후에도 남은 자격증명」할 말이 없다.。
func (weComChannel) DestinationKeys() []string { return []string{"webhook"} }

func (weComChannel) Validate(cfg map[string]any) error {
	hook := cfgString(cfg, "webhook")
	if hook == "" {
		return errors.New("없어짐 Webhook 주소")
	}
	if err := validateHTTPURL(hook); err != nil {
		return fmt.Errorf("Webhook 주소가 잘못되었습니다.: %w", err)
	}
	return nil
}

func (c weComChannel) Send(ctx context.Context, cfg map[string]any, m Message) (int, error) {
	if err := c.Validate(cfg); err != nil {
		return 0, Permanent(err)
	}
	// 요약 배치가 매우 길 수 있습니다.（50 글 × 항목당 한 줄씩 + 접두사），4096 바이트가 쉽게 초과됩니다.。
	// 플랫폼에 의존하여 오류를 보고하는 대신 여기서 잘림이 수행됩니다.：거부는 전체 배치가 손실됨을 의미합니다.，그리고 배송 전에 최소한 처음 몇 개의 항목을 자릅니다.。
	content, kept := markdownBody(m, weComMarkdownLimit)
	payload := map[string]any{
		"msgtype":  "markdown",
		"markdown": map[string]any{"content": content},
	}
	raw, err := doJSON(ctx, "POST", cfgString(cfg, "webhook"), nil, payload)
	if err != nil {
		return 0, err
	}
	var res struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return 0, fmt.Errorf("기업 WeChat 응답을 구문 분석하지 못했습니다.: %w (%s)", err, snippet(raw))
	}
	if res.ErrCode != 0 {
		// 45009 인터페이스 호출이 한도를 초과했습니다.——플랫폼의 현재 제한 창이 스크롤됩니다.，백오프 후 재시도는 유효합니다.，
		// 그래서 명시적으로 재시도 가능으로 분류됩니다.。여기로 가서 클라이언트에 대한 설명을 해주세요 rate_per_min 너무 공격적이어서 그럴 자격이 없습니다.，
		// 다시 시도하는 것은 은폐일 뿐이다，실제 해결 방법은 이 채널의 전류 제한을 낮추는 것입니다.。
		if res.ErrCode == 45009 {
			return 0, fmt.Errorf("기업 WeChat 현재 제한 %d: %s", res.ErrCode, res.ErrMsg)
		}
		// 93000 네 webhook key 유효하지 않음——영구 장애，재시도 자체가 복구되지 않음。
		return 0, Permanent(fmt.Errorf("Enterprise WeChat에서 오류를 반환함 %d: %s", res.ErrCode, res.ErrMsg))
	}
	return kept, nil
}
