package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// telegramTextLimit 네 Telegram sendMessage 님 text 필드 상한（문자수）。
const telegramTextLimit = 4096

// telegramChannel 구현 Telegram Bot API。
//
// 플랫폼 기능：
//   - 인증은 올인 URL path 내부（/bot<token>/sendMessage），서명이 필요하지 않습니다.。
//   - 사용 HTML 대신 패턴을 구문 분석합니다. MarkdownV2：MarkdownV2 이스케이프가 필요합니다. `_*[]()~`>#+-=|{}.!`
//     합계 18 문자，하나의 메시지가 누락되면 전체 메시지가 거부됩니다.；HTML 그냥 탈출해라 & < > 셋。
//   - 비즈니스 오류도 숨겨져 있습니다. HTTP 200 내부，젠장 ok 현장심사。
type telegramChannel struct{}

func (telegramChannel) Kind() string { return KindTelegram }

// Telegram 단일 채팅 약속 1 글/초、그룹 20 글/분。보수적인 가치를 취하라。
func (telegramChannel) DefaultRatePerMin() int { return 20 }

// Bot Token 은 완전한 자격 증명입니다.；chat_id 받는 사람만요，비밀은 아니야（받으셨나요? Token 메시지도 보낼 수 없어요）。
func (telegramChannel) SecretKeys() []string { return []string{"bot_token"} }

// base_url 결정 Token 어느쪽으로 보내나요? API 끝점（나만의 역세대를 구축한다면），바꾸려면 입장을 다시 밝혀야 합니다 Token。
func (telegramChannel) DestinationKeys() []string { return []string{"base_url"} }

func (telegramChannel) Validate(cfg map[string]any) error {
	if cfgString(cfg, "bot_token") == "" {
		return errors.New("없어짐 Bot Token")
	}
	if cfgString(cfg, "chat_id") == "" {
		return errors.New("없어짐 Chat ID")
	}
	if base := cfgString(cfg, "base_url"); base != "" {
		if err := validateHTTPURL(base); err != nil {
			return fmt.Errorf("API 주소가 잘못되었습니다.: %w", err)
		}
	}
	return nil
}

func (c telegramChannel) Send(ctx context.Context, cfg map[string]any, m Message) (int, error) {
	if err := c.Validate(cfg); err != nil {
		return 0, Permanent(err)
	}
	endpoint, err := telegramEndpoint(cfg)
	if err != nil {
		return 0, Permanent(err)
	}
	text, kept := telegramHTML(m)
	payload := map[string]any{
		"chat_id":                  cfgString(cfg, "chat_id"),
		"text":                     text,
		"parse_mode":               "HTML",
		"disable_web_page_preview": false,
	}
	raw, err := doJSON(ctx, "POST", endpoint, nil, payload)
	if err != nil {
		return 0, err
	}
	var res struct {
		OK          bool   `json:"ok"`
		ErrorCode   int    `json:"error_code"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return 0, fmt.Errorf("분석 Telegram 응답 실패: %w (%s)", err, snippet(raw))
	}
	if res.OK {
		return kept, nil
	}
	// 429 은 전류 제한입니다.，백오프 후 재시도는 유효합니다.；나머지는（400 매개변수가 잘못되었습니다.、401 token 틀렸어요、403 블랙리스트에 등록됨、
	// 404 chat 이 존재하지 않습니다）모두 설정 문제입니다，재시도 자체가 복구되지 않음。
	if res.ErrorCode == 429 {
		return 0, fmt.Errorf("Telegram 전류 제한: %s", res.Description)
	}
	return 0, Permanent(fmt.Errorf("Telegram 반환 오류 %d: %s", res.ErrorCode, res.Description))
}

// telegramEndpoint 철자 sendMessage 주소。base_url 비워두면 공식을 사용합니다. API，
// 비어 있지 않은 경우 자체 빌드에 사용됩니다. Bot API 안티세대（국내망 공통 요구사항）。
func telegramEndpoint(cfg map[string]any) (string, error) {
	base := cfgString(cfg, "base_url")
	if base == "" {
		base = "https://api.telegram.org"
	}
	base = strings.TrimSuffix(base, "/")
	token := cfgString(cfg, "bot_token")
	raw := base + "/bot" + token + "/sendMessage"
	u, err := url.Parse(raw)
	if err != nil {
		// 투명 전송 없음 err：주소에 다음이 포함됩니다. Bot Token，그리고 이때 연결이 addr 에코되어서는 안 됩니다.。
		return "", fmt.Errorf("접합 API 주소 실패（API 주소：%s）", redactRequestTarget(base))
	}
	return u.String(), nil
}

// telegramHTML 렌더링 HTML 문자，실제로 작성된 항목 수와 텍스트를 반환합니다.（또 만나요 Channel.Send）。
func telegramHTML(m Message) (string, int) {
	var b strings.Builder
	b.WriteString("<b>" + telegramEscape(markdownTitle(m)) + "</b>\n")
	if m.Batch {
		// Telegram 상한은**문자수**，그래서 포장도 문자로 측정됩니다.（runeSize）。
		footer := ""
		if m.HomeURL != "" {
			footer = fmt.Sprintf("\n\n<a href=\"%s\">플랫폼 전체 보기</a>", telegramEscapeAttr(m.HomeURL))
		}
		kept := packItemCount(m.Items, telegramTextLimit, telegramReservedRunes, footer, runeSize, func(it Item, idx int) string {
			return telegramBatchLine(it, idx+1)
		})
		items := m.Items[:kept]
		b.Reset()
		b.WriteString("<b>" + telegramEscape(telegramBatchTitle(m, items, len(m.Items))) + "</b>")
		for i, it := range items {
			b.WriteString("\n" + telegramEscape(telegramBatchLine(it, i+1)))
		}
		b.WriteString(footer)
		return TruncateHTML(b.String(), telegramTextLimit), kept
	}
	if len(m.Items) == 0 {
		return b.String(), 0
	}
	it := m.Items[0]
	if it.IsStatusChange() {
		b.WriteString(fmt.Sprintf("\n<b>상태변화</b>：%s → %s",
			telegramEscape(StatusLabel(it.FromStatus)), telegramEscape(StatusLabel(it.ToStatus))))
	}
	if it.VulnClass != "" && it.VulnClass != it.Title() {
		b.WriteString("\n<b>유형</b>：" + telegramEscape(it.VulnClass))
	}
	if a := assetLine(it.Assets, maxAssetsShown); a != "" {
		b.WriteString("\n<b>자산</b>：" + telegramEscape(a))
	}
	if s := OneLine(it.Summary, maxSummaryRunes); s != "" {
		b.WriteString("\n<b>요약</b>：" + telegramEscape(s))
	}
	if it.DetailURL != "" {
		b.WriteString(fmt.Sprintf("\n\n<a href=\"%s\">자세히 보기</a>", telegramEscapeAttr(it.DetailURL)))
	}
	return TruncateHTML(b.String(), telegramTextLimit), 1
}

// telegramReservedRunes 메시지 제목 및 가능한 잘림 프롬프트용으로 예약됨（문자별）。
const telegramReservedRunes = 160

// telegramBatchLine 렌더링 요약 항목（이스케이프되지 않음，호출자가 균일하게 이스케이프했습니다.）。
func telegramBatchLine(it Item, idx int) string {
	if a := assetLine(it.Assets, maxAssetsShown); a != "" {
		return fmt.Sprintf("%d. %s · %s — %s", idx, SeverityLabel(it.Severity), it.Title(), a)
	}
	return fmt.Sprintf("%d. %s · %s", idx, SeverityLabel(it.Severity), it.Title())
}

// telegramBatchTitle 요약 메시지의 헤더 라인을 렌더링합니다.。막대 개수는**이 글에는 실제로 다음과 같은 내용이 포함되어 있습니다.**수，
// ——그렇지 않으면 독자들은 메시지 헤더에 적힌 숫자가 전부라고 생각할 것입니다.。
func telegramBatchTitle(m Message, items []Item, total int) string {
	title := fmt.Sprintf("취약점 요약 · 합계 %d 글", total)
	if extra := total - len(items); extra > 0 {
		title += fmt.Sprintf("（표시하기 전 %d 글，나머지는 %d 다음으로 계속）", len(items), extra)
	}
	if m.WindowMinutes > 0 {
		title = fmt.Sprintf("근처 %d 분 · %s", m.WindowMinutes, title)
	}
	return title
}

// telegramEscape 탈출 HTML 텍스트 내용。
// Telegram 이 세 엔터티만 인식합니다.，탈출 후 &amp; 과 같은 기존 엔터티는 두 번 이스케이프됩니다.——바로 이것이다.
// 예상되는 동작：우리가 보여주고 싶은 것은 원작의 캐릭터다.，사용자에게 주입을 시키는 것이 아닙니다. HTML。
func telegramEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// telegramEscapeAttr 탈출 HTML 속성 값。텍스트 이스케이프 외에 따옴표 처리——
// URL 안의 따옴표는 조기 마감됩니다 href 속성，다음 내용을 주입점으로 바꿔보세요。
func telegramEscapeAttr(s string) string {
	s = telegramEscape(s)
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}
