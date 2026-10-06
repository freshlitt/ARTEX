package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// feishuChannel 페이슈 구현（포함 Lark）맞춤형 로봇，인터랙티브 카드 걷기。
//
// 플랫폼 기능：
//   - 서명 알고리즘 및 DingTalk**다르다**，그리고 실수하기 쉽죠，또 만나요 feishuSign 댓글。
//   - DingTalk처럼 비즈니스 오류를 HTTP 200 님 body 내부（code != 0）。
//   - 카드 header 색상 템플릿 지원，레벨 매핑을 사용하여 색상 지정，메시지 목록을 통해 심각도를 한눈에 확인할 수 있습니다.。
type feishuChannel struct{}

func (feishuChannel) Kind() string { return KindFeishu }

// Feishu 맞춤형 로봇 예약 5 회/초，변환됨 100 회/분。
func (feishuChannel) DefaultRatePerMin() int { return 100 }

// Webhook 주소의 마지막 부분은 로봇의 고유 식별자입니다.，자격 증명。
func (feishuChannel) SecretKeys() []string { return []string{"webhook", "secret"} }

// 같은 이유：변경 Webhook 주소는 새 주소에 대한 서명 키를 다시 확인해야 합니다.。
func (feishuChannel) DestinationKeys() []string { return []string{"webhook"} }

func (feishuChannel) Validate(cfg map[string]any) error {
	hook := cfgString(cfg, "webhook")
	if hook == "" {
		return errors.New("없어짐 Webhook 주소")
	}
	if err := validateHTTPURL(hook); err != nil {
		return fmt.Errorf("Webhook 주소가 잘못되었습니다.: %w", err)
	}
	return nil
}

func (c feishuChannel) Send(ctx context.Context, cfg map[string]any, m Message) (int, error) {
	if err := c.Validate(cfg); err != nil {
		return 0, Permanent(err)
	}
	card, kept := feishuCard(m)
	payload := map[string]any{
		"msg_type": "interactive",
		"card":     card,
	}
	// 서명 매개변수는 메시지와 동일한 레이어에 있습니다.，및 구성만 됨 secret 일 때 나타납니다.。
	if secret := cfgString(cfg, "secret"); secret != "" {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		payload["timestamp"] = ts
		payload["sign"] = feishuSign(ts, secret)
	}
	raw, err := doJSON(ctx, "POST", cfgString(cfg, "webhook"), nil, payload)
	if err != nil {
		return 0, err
	}
	var res struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		// Feishu의 일부 버전 hook 이 필드 이름 집합을 사용하십시오.，모두 호환 가능。
		StatusCode    int    `json:"StatusCode"`
		StatusMessage string `json:"StatusMessage"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return 0, fmt.Errorf("Feishu 응답을 구문 분석하지 못했습니다.: %w (%s)", err, snippet(raw))
	}
	if res.Code != 0 {
		return 0, Permanent(fmt.Errorf("Feishu가 오류를 반환했습니다. %d: %s", res.Code, res.Msg))
	}
	if res.StatusCode != 0 {
		return 0, Permanent(fmt.Errorf("Feishu가 오류를 반환했습니다. %d: %s", res.StatusCode, res.StatusMessage))
	}
	return kept, nil
}

// feishuSign Feishu 공식 규정에 따라 서명을 계산합니다.。
//
// 여기서 문제가 발생하기 매우 쉽습니다.：공식 샘플은
//
//	hmac.new(string_to_sign.encode(), digestmod=sha256)
//
// 그렇죠 **key = timestamp + "\n" + secret，message 이 비어 있습니다.**，직관보다는
// 「key=secret, message=stringToSign」——그게 바로 딩톡의 알고리즘이에요。양쪽의 알고리즘은 정반대입니다，
// 다른 회사의 구현을 따를 경우 필연적으로 서명 검증이 실패하게 됩니다.（신고 19021）。
func feishuSign(timestamp, secret string) string {
	stringToSign := timestamp + "\n" + secret
	mac := hmac.New(sha256.New, []byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// feishuSeverityTemplate 취약성 수준을 카드에 매핑 header 컬러 템플릿。
// 레벨을 알 수 없는 경우 grey——필요없어요 blue，그러지 않으려면 low 혼란。
func feishuSeverityTemplate(severity string) string {
	switch severity {
	case "critical":
		return "red"
	case "high":
		return "orange"
	case "medium":
		return "yellow"
	case "low":
		return "blue"
	default:
		return "grey"
	}
}

// feishuMaxCardBytes 은 카드 내용의 보수적인 상한선입니다.。페이슈는 카드 사이즈 제한이 있어요.，한도를 초과하면 전체 라인이 거부되었습니다.；
// 공식 상한치보다 현저히 낮은 값을 취함，넣어보세요 JSON 포장 오버헤드도 포함됩니다.。
const feishuMaxCardBytes = 24000

// feishuCard 대화형 카드 만들기，반납카드와**실제로 작성된 항목 수**。
// kept 같은 목적입니다 markdownBody：실제로 카드에 들어가는 상품만 배송완료로 표시되어야 합니다.。
func feishuCard(m Message) (map[string]any, int) {
	elements := []any{}
	kept := 0
	if m.Batch {
		// 먼저 전체를 포장한 후 머리를 조립하세요.：헤더를 작성해야 합니다.「나머지는 N 다음 메시지에 계속됩니다」，
		// N 실제 로드된 항목 수에서 나와야 합니다.。
		kept = packItemCount(m.Items, feishuMaxCardBytes, markdownReservedBytes, "", byteSize, func(it Item, idx int) string {
			return feishuBatchLine(it, idx+1)
		})
		items := m.Items[:kept]
		elements = append(elements, feishuMarkdownDiv(markdownBatchIntro(m, items, len(m.Items))))
		for i, it := range items {
			elements = append(elements, feishuMarkdownDiv(feishuBatchLine(it, i+1)))
		}
		if m.HomeURL != "" {
			elements = append(elements, feishuButton("플랫폼 전체 보기", m.HomeURL))
		}
	} else if len(m.Items) > 0 {
		kept = 1
		it := m.Items[0]
		elements = append(elements, feishuMarkdownDiv(feishuItemLines(it)))
		if it.DetailURL != "" {
			elements = append(elements, feishuButton("자세히 보기", it.DetailURL))
		}
	}

	card := map[string]any{
		"config":   map[string]any{"wide_screen_mode": true},
		"header":   map[string]any{"title": map[string]any{"tag": "plain_text", "content": markdownTitle(m)}},
		"elements": elements,
	}
	if len(m.Items) > 0 {
		card["header"].(map[string]any)["template"] = feishuSeverityTemplate(m.Items[0].Severity)
	}
	return card, kept
}

func feishuMarkdownDiv(content string) map[string]any {
	return map[string]any{"tag": "div", "text": map[string]any{"tag": "lark_md", "content": content}}
}

func feishuButton(label, url string) map[string]any {
	return map[string]any{
		"tag": "action",
		"actions": []any{map[string]any{
			"tag":  "button",
			"text": map[string]any{"tag": "lark_md", "content": label},
			"url":  url,
			"type": "primary",
		}},
	}
}

// feishuItemLines 단일 취약점 렌더링 lark_md 문자。
//
// lark_md 그리고 markdown 은 같은 계열의 텍스트 형식입니다.，은 링크와 강조도 구문 분석합니다.，그래서 밖에서는
// 필드를 통과해야 합니다. markdownText（한 줄 + 탈출）——그렇지 않으면 취약점 제목은 다음과 같습니다.
// Feishuli가 클릭 가능한 외부 링크가 되었습니다.。
func feishuItemLines(it Item) string {
	out := fmt.Sprintf("**%s · %s**", SeverityLabel(it.Severity), markdownText(it.Title(), 0))
	if it.IsStatusChange() {
		out += fmt.Sprintf("\n**상태변화**：%s → %s",
			markdownText(StatusLabel(it.FromStatus), 0), markdownText(StatusLabel(it.ToStatus), 0))
	}
	if it.VulnClass != "" && it.VulnClass != it.Title() {
		out += fmt.Sprintf("\n**유형**：%s", markdownText(it.VulnClass, 0))
	}
	if a := assetLine(it.Assets, maxAssetsShown); a != "" {
		out += fmt.Sprintf("\n**자산**：%s", markdownText(a, 0))
	}
	if it.Summary != "" {
		if s := markdownText(it.Summary, maxSummaryRunes); s != "" {
			out += fmt.Sprintf("\n**요약**：%s", s)
		}
	}
	return out
}

// feishuBatchLine 렌더링 요약 카드의 항목。
func feishuBatchLine(it Item, index int) string {
	line := fmt.Sprintf("**%d. %s · %s**", index, SeverityLabel(it.Severity), markdownText(it.Title(), 0))
	if a := assetLine(it.Assets, maxAssetsShown); a != "" {
		line += " — " + markdownText(a, 0)
	}
	return line
}
