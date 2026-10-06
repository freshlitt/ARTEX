package notify

import (
	"fmt"
	"strings"
)

// 이 문서는「Markdown 부서」채널（딩톡、기업 위챗）공유 메시지 렌더링。
// 페이슈 카드 JSON、Telegram 사용 HTML、이메일의 경우 HTML，각각은 어댑터에서 렌더링됩니다.。

// maxAssetsShown 은 메시지에 나열된 최대 자산 수입니다.。취약점으로 인해 수십 개의 자산이 고정될 수 있습니다.，
// 칼럼 전체가 뉴스로 가득 차서 정보 가치가 없습니다——아니요. 4 이후에는 누구도 도메인 이름을 사용하지 않습니다. IM 안을 보세요。
const maxAssetsShown = 3

// maxSummaryRunes 은 요약이 압축되는 문자 수입니다.。IM 메시지는「세부사항으로 이동하라는 메시지가 표시됩니다.」，
// 보고서 자체는 아님，전체 콘텐츠가 플랫폼에 있습니다.。
const maxSummaryRunes = 120

// markdownReservedBytes 메시지 헤더용으로 예약됨（요약 행 + 레벨분포 + 가능한 잘림 힌트）
// 그리고 꼬리（플랫폼 링크）。전체적으로 포장할 때 이 부분을 예산에서 빼주세요，머리와 꼬리가 잘리지 않도록 주의해주세요——
// 머리와 꼬리가 잘려지면，독자 여러분「이건 어느 배치인가요?、표시되지 않는 항목은 몇 개입니까?」보이지도 않네요。
const markdownReservedBytes = 320

// markdownEscape 탈출 markdown 메타문자。
//
// 왜 해야 하는가：취약점 제목、요약、유형、모든 자산 표시 이름의 출처는 다음과 같습니다.**신뢰할 수 없는 출처**——
// 제목과 요약은 모델 출력에서 따왔습니다.（모델은 측정 대상의 응답을 읽습니다.），자산 url 검색 중
// 전체 내용을 받아보세요 URL（제어 가능한 대상이 있는 쿼리 문자열）。이스케이프되지 않은 경우，제목은
//
//	로그인 포트 SQL 주사\n[긴급：계정을 확인하려면 여기를 클릭하세요.](http://attacker.tld)
//
// 의 취약점은 보안 엔지니어가 식별합니다./Feishuli 렌더링**클릭 가능한 외부 링크**；그리고
// `![](http://attacker.tld/beacon)` 은 렌더링 중에 클라이언트에 의해 당겨집니다.，알림과 같습니다.
// 「이 취약점이 발견되었습니다.」그리고 독자 유출 IP。무해한 내용이라도，굵은 글씨 또는
// 인용 블록은 접는 선에서 다음과 같은 심각한 취약점을 짜낼 수도 있습니다.。
//
// 이스케이프 컬렉션 재정의 헤더/링크/강조/목록/견적/취소선은 구조를 변경하거나 클릭 가능한 항목을 생성합니다.
// 요소의 문자。`\` 을 먼저 처리해야 합니다.，그렇지 않으면 추가된 백슬래시가 다시 이스케이프됩니다.。
func markdownEscape(s string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		"`", "\\`",
		"*", `\*`,
		"_", `\_`,
		"[", `\[`,
		"]", `\]`,
		"(", `\(`,
		")", `\)`,
		"!", `\!`,
		"#", `\#`,
		">", `\>`,
		"|", `\|`,
		"~", `\~`,
	)
	return replacer.Replace(s)
}

// markdownText 신뢰할 수 없는 텍스트를 한 줄로 압축하고 이스케이프 처리하세요.， markdown 텍스트 사용법。
// 한 줄은 이스케이프 외에 나머지 절반입니다.：줄 바꿈만으로도 새 목록 항목이나 인용 블록이 위조될 수 있습니다.，
// 그리고 이스케이프 문자는 이를 차단할 수 없습니다.。
func markdownText(s string, maxRunes int) string {
	return markdownEscape(OneLine(s, maxRunes))
}

// markdownTitle 메시지 제목 반환（IM 플랫폼의 제목 표시줄/카드제목），내용은**이스케이프 처리되지 않은 원본 텍스트**。
//
// 여기서는 벗어날 수 없습니다.：이 제목은 4개의 컨텍스트 렌더러에서 공유됩니다.——markdown 문자、Telegram 님
// HTML、페이슈 카드 plain_text、및 일반 Webhook 님 JSON 이메일 제목으로。
// 이스케이프 규칙이 다릅니다.（markdown 탈출하다 HTML 은 백슬래시를 눈에 띄게 남깁니다.，닥쳐 JSON 오염시키겠다
// 데이터），따라서 이스케이프는 해당 출력 끝에서 수행되어야 합니다.，또 만나요 writeItem / feishuItemLines /
// telegramEscape。공유 기능에 추가되면 markdown 탈출，결과 Telegram 메시지에
// 등장 `\(1\)` 눈에 보이는 백슬래시。
func markdownTitle(m Message) string {
	if m.Batch {
		return fmt.Sprintf("취약점 요약 · 합계 %d 글", len(m.Items))
	}
	if len(m.Items) == 0 {
		return "취약점 알림"
	}
	it := m.Items[0]
	return fmt.Sprintf("[%s] %s", SeverityLabel(it.Severity), OneLine(it.Title(), 0))
}

// markdownBody 메시지 텍스트 렌더링，본문으로 돌아가서**실제로 작성된 항목 수**。
//
// 반환 값 kept 은 이번 배송으로 실제 배송된 상품의 개수입니다.，따라서 발신자는 첫 번째만 추가합니다. kept 다음으로 표시된 기사
// 배달됨——최대 채널 길이로 인해 차단된 항목은 다음 배치를 위해 예약되어야 합니다.，따라가는 대신
// 표시 성공。바로 이것이다.「침묵이 사라졌다」의 출처：메시지가 잘렸습니다.，그런데 배송기록을 보니 모두 배송이 되었네요.，
// 후반전을 보내지 않은 곳은 어디에도 없습니다.。
//
// maxBytes<=0 은 제한 없음을 의미합니다.。
func markdownBody(m Message, maxBytes int) (string, int) {
	if !m.Batch {
		if len(m.Items) == 0 {
			return "", 0
		}
		var b strings.Builder
		writeItem(&b, m.Items[0], "", true)
		// 너무 길어도 한통의 메시지가 전달됩니다.（궁극적인 잘림）：취약점의 일부 정보
		// 아예 메시지를 보내지 않는 것보다는 낫다。
		return TruncateBytes(b.String(), maxBytes), 1
	}

	footer := ""
	if m.HomeURL != "" {
		footer = fmt.Sprintf("\n[플랫폼 전체 보기](%s)\n", m.HomeURL)
	}
	kept := packItemCount(m.Items, maxBytes, markdownReservedBytes, footer, byteSize, func(it Item, idx int) string {
		var b strings.Builder
		writeItem(&b, it, fmt.Sprintf("%d. ", idx+1), false)
		return b.String()
	})

	items := m.Items[:kept]
	var b strings.Builder
	b.WriteString(markdownBatchIntro(m, items, len(m.Items)))
	for i, it := range items {
		writeItem(&b, it, fmt.Sprintf("%d. ", i+1), false)
	}
	b.WriteString(footer)
	return TruncateBytes(b.String(), maxBytes), kept
}

// markdownBatchIntro 요약 메시지의 시작 부분을 렌더링합니다.：시간 창、인원 및 레벨 분포。
// 이걸로，요약을 받은 사람들은 플랫폼을 클릭하지 않고도 이 배치를 즉시 처리해야 하는지 여부를 판단할 수 있습니다.。
//
// items 네**실제 설치**에 대한 항목，total 은 이 배치의 총 개수입니다.。둘이 다른 경우에는 명확히 명시해야 한다.
// 「다음 메시지는 몇 개나 더 있나요?」——그렇지 않으면 독자들은 메시지 헤더에 적힌 숫자가 전부라고 생각할 것입니다.，
// 그리고 한 번도 발행된 적이 없는 후속 항목은 인터페이스에 전혀 존재하지 않습니다.。
func markdownBatchIntro(m Message, items []Item, total int) string {
	var b strings.Builder
	if m.WindowMinutes > 0 {
		fmt.Fprintf(&b, "**근처 %d 분 추가됨 %d 취약점**", m.WindowMinutes, total)
	} else {
		fmt.Fprintf(&b, "**새로운 %d 취약점**", total)
	}
	if extra := total - len(items); extra > 0 {
		fmt.Fprintf(&b, "(이 메시지에 %d건, 나머지는 %d건 다음 메시지로 계속)", len(items), extra)
	}
	// 은 레벨별 분포를 제공합니다.，심각한 내용이 있는지 독자들이 한눈에 알 수 있도록 하라.。통계만**이 글에는 실제로 다음과 같은 내용이 포함되어 있습니다.**님
	// 항목，보장「심각해요 3」은 아래의 집계 가능한 항목과 일치합니다.。
	counts := map[string]int{}
	for _, it := range items {
		counts[it.Severity]++
	}
	var parts []string
	for _, sev := range []string{"critical", "high", "medium", "low"} {
		if n := counts[sev]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", SeverityLabel(sev), n))
		}
	}
	if len(parts) > 0 {
		b.WriteString("\n" + strings.Join(parts, " · "))
	}
	b.WriteString("\n\n")
	return b.String()
}

// writeItem 단일 취약점 항목 렌더링。
//
// prefix 목록을 요약하는 데 사용되는 일련 번호；single=true 타임렌더링 풀버전（요약 및 링크 다시 포함），
// 요약 목록에는 한 줄의 요약만 렌더링됩니다.——그렇지 않으면 50 요약하면 긴 문서가 됩니다。
//
// 모든 외부 콘텐츠（제목/유형/자산/요약）둘 다 합격 markdownText：
// 한 줄 + 탈출。백링크는 관리자가 구성했습니다. public_base_url 로 표기，신뢰할 수 없는 내용은 아닙니다，
// 클릭 가능한 링크여야 합니다.，그럼 그대로 출력해 보세요。
func writeItem(b *strings.Builder, it Item, prefix string, single bool) {
	line := fmt.Sprintf("%s**%s · %s**", prefix, SeverityLabel(it.Severity), markdownText(it.Title(), 0))
	if !single {
		// 요약 모드：단일 라인 렌더링，자산 및 요약 압축이 이어집니다.。
		var extras []string
		if a := assetLine(it.Assets, maxAssetsShown); a != "" {
			extras = append(extras, markdownText(a, 0))
		}
		if it.Summary != "" {
			extras = append(extras, markdownText(it.Summary, 60))
		}
		if len(extras) > 0 {
			line += " — " + strings.Join(extras, " · ")
		}
		b.WriteString(line + "\n")
		return
	}
	b.WriteString(line + "\n")
	if it.IsStatusChange() {
		fmt.Fprintf(b, "**상태변화**：%s → %s\n",
			markdownText(StatusLabel(it.FromStatus), 0), markdownText(StatusLabel(it.ToStatus), 0))
	}
	if it.VulnClass != "" && it.VulnClass != it.Title() {
		fmt.Fprintf(b, "**유형**：%s\n", markdownText(it.VulnClass, 0))
	}
	if a := assetLine(it.Assets, maxAssetsShown); a != "" {
		fmt.Fprintf(b, "**자산**：%s\n", markdownText(a, 0))
	}
	if it.Summary != "" {
		if s := markdownText(it.Summary, maxSummaryRunes); s != "" {
			fmt.Fprintf(b, "**요약**：%s\n", s)
		}
	}
	if it.DetailURL != "" {
		fmt.Fprintf(b, "[자세히 보기](%s)\n", it.DetailURL)
	}
}
