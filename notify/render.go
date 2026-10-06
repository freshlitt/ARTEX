package notify

import (
	"strings"
	"unicode/utf8"
)

const ellipsis = "…"

// TruncateBytes 넣어보세요 s 이하로 잘림 max 바이트，결과가 합법적임을 보장합니다 UTF-8 및 문자를 자르지 마십시오.。
//
// 왜 글자의 경계에 따라 잘라야 할까요?：치웨이쿤 로봇 markdown 예 4096 **바이트**하드캡（아니요
// 문자수），그리고 중국어로 한마디 3 바이트。바이트 단위로 직접 자르면 한자가 반으로 잘립니다.，잘못된 출력
// UTF-8——플랫폼 측 또는 전체 항목이 거부됩니다.，아니면 왜곡된 사각형으로 표시됩니다.。여기서 접근 방식은 예산 위치부터 시작하는 것입니다.
// 가까운 곳으로 돌아가세요 rune 시작 바이트（utf8.RuneStart 연속 바이트 결정 0b10xxxxxx）。
//
// max<=0 은 제한 없음을 의미합니다.。잘린 후 줄임표 추가，그렇지 않으면 max 너무 작아서 줄임표에 들어갈 수 없습니다.。
func TruncateBytes(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	budget := max - len(ellipsis)
	suffix := ellipsis
	if budget < 0 {
		// max 은 줄임표보다 짧습니다.：줄임표 삭제，순수 잘림，결과를 초과하는 것을 피하십시오 max。
		budget = max
		suffix = ""
	}
	cut := budget
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + suffix
}

// OneLine 여러 줄의 텍스트를 한 줄로 압축합니다.：공백 모두 축소，문자수만큼 잘림。
//
//	IM 메시지의 헤더 라인——초록에서 줄 바꿈이 자주 발생합니다.，양식에 직접 삽입/제목 때문에 레이아웃이 깨집니다.。
//
// max<=0 길이에 제한이 없음을 나타냅니다.。
func OneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	return TruncateRunes(s, max)
}

// TruncateRunes 넣어보세요 s 이하로 잘림 max 문자（），초과 시 줄임표 추가。
// max<=0 은 제한 없음을 의미합니다.。
//
// 그리고 TruncateBytes 차이점은 플랫폼 구경에 있습니다.：Qiwei에는 바이트 길이 제한이 있습니다.，Telegram 문자 수에 따라 길이가 제한됩니다.。
// 잘못된 구경을 사용해도 오류가 보고되지 않습니다.，은 메시지가 예상보다 훨씬 짧아지게 만듭니다.（중국어 1 단어 = 3 바이트，
// 바이트 단위로 잘라내기 4096 약 1365 단어），따라서 두 기능을 모두 유지해야 합니다.、채널별 선택。
func TruncateRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return string(runes[:max])
	}
	return string(runes[:max-1]) + ellipsis
}

// TruncateHTML 문자 수만큼 잘림 HTML 단편，및 절반 라벨이 생성되지 않음을 보장합니다.。
//
// 직접 HTML 문자 잘림이 잘립니다. `<a href="htt` 이런 불완전한 라벨，플랫폼 파서 또는
// 오류 신고 및 기사 전체 거부、또는 후속 텍스트를 속성 값으로 삼키십시오.。여기서의 접근 방식은 다음과 같습니다.：문자별로 먼저 자릅니다.，
// 닫히지 않은 꼬리가 있는지 다시 확인해보세요. `<`，가능하다면 후퇴하세요.。
//
// 라벨 밸런싱 없음（완료 </b> 등）：Telegram 님 HTML 파서는 닫히지 않은 태그를 자동으로 닫습니다.，
// 균형을 직접 구현하려면 속성의 따옴표를 처리해야 합니다.、댓글、자동 폐쇄 태그，복잡성은 이점에 비례하지 않습니다。
func TruncateHTML(s string, max int) string {
	if max <= 0 || len([]rune(s)) <= max {
		return s
	}
	cut := TruncateRunes(s, max)
	// 꼬리가 `<` 처음부터의 단편（마지막 등장 `<` 그다음엔 아무것도 아니야 `>`），복귀 `<` 전에。
	if lt := strings.LastIndex(cut, "<"); lt >= 0 && !strings.Contains(cut[lt:], ">") {
		cut = cut[:lt]
	}
	// 꼬리가 잘린 경우 HTML 엔터티（ `&amp;` 잘려졌습니다 `&amp`），역시 반납해야함。
	// 엔터티 조각으로 인해**메시지 전체**거부됨——2개 이상
	// 길이에 상한이 있는 요약 메시지는 이미 일반적입니다.，알림 전체를 버릴 가치가 없습니다.。
	if amp := strings.LastIndex(cut, "&"); amp >= 0 && !strings.Contains(cut[amp:], ";") {
		cut = cut[:amp]
	}
	return cut
}

// packItemCount 예산 내에서 에너지를 계산합니다.**완료**몇 줄이나 내려놓으셨나요?，요약 메시지를 전체 메시지로 패키지화하는 경우。
//
// 전체 기사를 렌더링한 다음 자르는 대신 전체 기사를 누르는 이유는 무엇입니까?：잘림으로 인해 항목의 후반부가 허공에서 사라집니다.，
// 및 배송 기록은 계속 배송됨으로 표시됩니다.——메시지에 안보이네요、배송내역에서 볼 수 없습니다.，
// 허점이 사라졌습니다。전체적으로 포장한 후，불러올 수 없는 항목은 라이브러리에 남겨두고 다음 배치가 됩니다.，
// 발신자가 수신합니다. kept 은 실제로 전달된 메시지 수입니다.。
//
// 매개변수：maxSize<=0 은 제한 없음을 의미합니다.；reserve 은 메시지 헤더용입니다./테일에 예약된 금액；
// size 측정담당（플랫폼마다 성능이 다릅니다：치웨이/바이트의 DingTalk，Telegram 문자수 기준——
// 잘못된 구경을 사용해도 오류가 보고되지 않습니다.，은 중국어 메시지를 상한보다 훨씬 낮은 수준까지만 억제합니다.）；
// render 첫 번째 idx 막대가 실제 텍스트로 렌더링됩니다.——내용에 따라 길이가 다름，추정에 의존할 수 없음。
//
// 최소한 반환 1（항목이 있는 한）。이 메시지는 단일 메시지가 매우 긴 경우에도 전송되어야 합니다.、발신자에 의해
// 드디어 결론을 잘랐다，그렇지 않으면 매우 긴 허점이 전체 배치를 영구적으로 차단합니다.。
func packItemCount(items []Item, maxSize, reserve int, footer string, size func(string) int, render func(Item, int) string) int {
	if maxSize <= 0 {
		return len(items)
	}
	budget := maxSize - reserve - size(footer)
	if budget < 0 {
		budget = 0
	}
	used := 0
	for i, it := range items {
		used += size(render(it, i))
		if used > budget && i > 0 {
			return i
		}
	}
	return len(items)
}

// byteSize / runeSize 네 packItemCount 의 두 측정 구경，전화를 걸지 않으려면 이름을 지정하세요.
// 벌거벗은 모습 func(s string) int 폐쇄，그렇지 않으면 어떤 구경을 사용하는지 한눈에 알기 어렵습니다.。
func byteSize(s string) int { return len(s) }
func runeSize(s string) int { return utf8.RuneCountInString(s) }

// assetLine 자산 목록을 표시 텍스트 줄로 렌더링합니다.，이상 limit 나머지는 생략하고 총 개수를 표시하세요。
// 취약점으로 인해 수십 개의 자산이 고정될 수 있습니다.，다 나열하면 뉴스거리가 많이 나올 것 같아요。
func assetLine(assets []string, limit int) string {
	if len(assets) == 0 {
		return ""
	}
	if limit <= 0 || len(assets) <= limit {
		return strings.Join(assets, "、")
	}
	return strings.Join(assets[:limit], "、") + " 등 " + itoa(len(assets)) + " "
}

// itoa 네 strconv.Itoa 의 약식 별칭，표시 텍스트 연결에만 사용됩니다.，어디서나 피하세요 import strconv。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
