package notify

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateBytesKeepsValidUTF8(t *testing.T) {
	// 이것은 이 패키지의 가장 중요한 불변입니다.。치 위챗**바이트**길이 제한，중국어 3 바이트/단어，
	// 바이트 단위로 하드 커팅을 구현하면 한자가 반으로 잘립니다.，잘못된 출력 UTF-8 플랫폼에서 거부되었습니다.。
	// 가능한 모든 컷 포인트에 도달하려면 상호 소수 길이의 여러 중국어 및 영어 혼합 입력을 사용하십시오.。
	inputs := []string{
		"중국어 시험 내용",
		"믹스 mixed 내용 content",
		"a에b문자c테스트d시도해 보세요e",
		"🔴🟠🟡🔵", // 4 바이트 emoji，잘못된 컷이 더 뻔하다
		strings.Repeat("취약점", 100),
	}
	for _, in := range inputs {
		for max := 1; max <= len(in)+2; max++ {
			got := TruncateBytes(in, max)
			if !utf8.ValidString(got) {
				t.Fatalf("입력 %q max=%d: 잘못된 출력 UTF-8 %q", in, max, got)
			}
			if len(got) > max {
				t.Fatalf("입력 %q max=%d: 결과 %d 바이트가 상한을 초과했습니다.", in, max, len(got))
			}
			// 내용이 잘리지 않으면 변경하면 안 됩니다.。
			if len(in) <= max && got != in {
				t.Fatalf("입력 %q max=%d: 제한을 초과하지 않고 내용이 변경되었습니다. -> %q", in, max, got)
			}
		}
	}
}

func TestTruncateBytesZeroMeansUnlimited(t *testing.T) {
	long := strings.Repeat("x", 10000)
	if got := TruncateBytes(long, 0); got != long {
		t.Fatal("max=0 은 제한이 없음을 의미해야 합니다.")
	}
	if got := TruncateBytes(long, -5); got != long {
		t.Fatal("max<0 은 제한이 없음을 의미해야 합니다.")
	}
}

func TestTruncateBytesEllipsisBudget(t *testing.T) {
	// max 줄임표 자체보다 작은 경우，타원을 추가하여 제한을 초과할 수 없습니다.。
	got := TruncateBytes("abcdefgh", 1)
	if len(got) > 1 {
		t.Fatalf("max=1 시간 결과 %q 길이 %d 한도 초과", got, len(got))
	}
	// 일반적으로 줄임표가 있어야 합니다.。
	if got := TruncateBytes("abcdefgh", 5); !strings.HasSuffix(got, ellipsis) {
		t.Fatalf("줄임표가 필요합니다.，받았어요 %q", got)
	}
}

func TestTruncateRunesCountsCharactersNotBytes(t *testing.T) {
	// 그리고 TruncateBytes 의 구경 차이는 유지되어야 합니다.：Telegram 문자 길이 제한，
	// 바이트 구경을 사용하면 중국어 메시지가 1/3로 줄어듭니다.。
	s := "하나, 둘, 셋, 넷, 다섯, 여섯, 일곱, 여덟, 아흔"
	got := TruncateRunes(s, 5)
	if n := utf8.RuneCountInString(got); n != 5 {
		t.Fatalf("기대 5 문자，받았어요 %d  (%q)", n, got)
	}
	// 동일한 문자열의 바이트 크기는 상당히 짧아야 합니다.。
	if utf8.RuneCountInString(TruncateBytes(s, 5)) >= 5 {
		t.Fatal("바이트 구경은 문자 구경과 동일한 수의 문자를 생성해서는 안 됩니다.")
	}
}

func TestOneLineCollapsesWhitespace(t *testing.T) {
	got := OneLine("첫줄\n\n두 번째 줄\t표로 작성   여러 공백", 0)
	if strings.ContainsAny(got, "\n\t") {
		t.Fatalf("모든 공백은 축소되어야 합니다.，받았어요 %q", got)
	}
	if strings.Contains(got, "  ") {
		t.Fatalf("연속된 공백은 유지하면 안 됩니다.，받았어요 %q", got)
	}
	// 잘린 후에도 여전히 읽을 수 있고 합법적이어야 합니다.。
	got = OneLine("하나, 둘, 셋, 넷, 다섯, 여섯, 일곱, 여덟, 아흔", 4)
	if n := utf8.RuneCountInString(got); n != 4 {
		t.Fatalf("기대 4 문자，받았어요 %d (%q)", n, got)
	}
}

func TestTruncateHTMLNeverCutsTagInHalf(t *testing.T) {
	// 직접 잘림 HTML 잘리겠습니다 `<a href="htt` 그런 단편들，플랫폼에서 전체 메시지를 거부합니다.。
	s := `<b>제목</b>텍스트 텍스트 텍스트<a href="https://example.com/very/long/path">자세히 보기</a>`
	for max := 1; max <= utf8.RuneCountInString(s)+2; max++ {
		got := TruncateHTML(s, max)
		if n := utf8.RuneCountInString(got); max > 0 && n > max {
			t.Fatalf("max=%d: 결과 %d 글자수 초과", max, n)
		}
		// 꼬리는 열 수 없습니다 `<`（은 마지막 단락에 나타납니다. `<` 하지만 안돼 `>`）。
		if lt := strings.LastIndex(got, "<"); lt >= 0 && !strings.Contains(got[lt:], ">") {
			t.Fatalf("max=%d: 꼬리 라벨이 잘려있습니다 -> %q", max, got)
		}
	}
}

func TestAssetLineOmitsExcess(t *testing.T) {
	if got := assetLine(nil, 3); got != "" {
		t.Fatalf("자산이 없으면 빈 문자열이 반환되어야 합니다.，받았어요 %q", got)
	}
	if got := assetLine([]string{"a", "b"}, 3); got != "a、b" {
		t.Fatalf("한도 내 모든 항목을 기재해야 합니다.，받았어요 %q", got)
	}
	// 상한치를 초과하는 경우 총 개수를 표시해야 합니다.，그렇지 않으면 독자는 목록에 없는 자산이 몇 개인지 알 수 없습니다.。
	got := assetLine([]string{"a", "b", "c", "d", "e"}, 2)
	if !strings.Contains(got, "등 5 ") {
		t.Fatalf("총 개수를 표기해야 합니다. 5，받았어요 %q", got)
	}
}

func TestSeverityAndStatusLabels(t *testing.T) {
	if AtLeast("", "low") {
		t.Fatal("빈 수준 서수는 다음과 같습니다. 0，은 모든 임계값에 의해 차단되어야 합니다.")
	}
	if !AtLeast("critical", "") {
		t.Fatal("빈 임계값을 해제해야 합니다.")
	}
	if got := StatusLabel("fixed"); got != "고정됨" {
		t.Fatalf("알 수 없는 상태 매핑，받았어요 %q", got)
	}
	// 알 수 없는 상태가 그대로 에코됩니다.，라벨을 만들지 마세요.。
	if got := StatusLabel("weird_status"); got != "weird_status" {
		t.Fatalf("알 수 없는 상태가 그대로 표시되어야 합니다.，받았어요 %q", got)
	}
}

// TestTruncateHTMLNeverCutsEntity 커버리지 감사에서 지적된 누락：잘림은 피해야 할 뿐만 아니라
// 하프 라벨，또한 잘리는 것을 피하십시오 HTML 엔터티。
//
// `&amp;` 잘려졌습니다 `&amp` 이후，엔터티만 인식하는 파서는 거부할 수 있습니다.**기사 전체**메시지——
// 그리고 매우 긴 요약 메시지는 이미 일반화되어 있습니다.，가격이 너무 비싸요。
func TestTruncateHTMLNeverCutsEntity(t *testing.T) {
	s := "aaaa&amp;bbbb&lt;cccc&quot;dddd"
	for max := 1; max <= utf8.RuneCountInString(s)+2; max++ {
		got := TruncateHTML(s, max)
		// 꼬리가 나오지 않아야 합니다.「예 & 그런데 해당사항이 없어요 ;」의 물리적 조각。
		if amp := strings.LastIndex(got, "&"); amp >= 0 && !strings.Contains(got[amp:], ";") {
			t.Fatalf("max=%d: 꼬리에 물리적 조각을 남겨둔다 %q", max, got[amp:])
		}
		if strings.Contains(got, "&amp\x00") {
			t.Fatalf("max=%d: 잘못된 개체가 나타납니다.", max)
		}
	}
}
