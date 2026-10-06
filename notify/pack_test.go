package notify

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// 이 문서에는 다음이 포함됩니다.「패키지 전체」이 수정 사항은：요약 메시지가 채널 길이 상한을 초과하는 경우，필수**전체글을 클릭하세요**
// 제거된 항목 수를 잘라내어 사실대로 보고합니다.，발신자는 실제로 전달된 것만 표시하도록 하세요.。
//
// 이전 방법은 기사 전체를 랜더링한 후 잘라내는 방식이었습니다.、그러면 마커 전체 배치가 배송되었습니다.：메시지의 후반부가 허공으로 사라졌습니다.，
// 그리고 배송 이력을 보니 모두 성공적이더군요.——허점이 사라졌습니다，어디서도 찾을 수 없어요。

func TestMarkdownBodyPacksWholeItemsWithinByteLimit(t *testing.T) {
	// 200 중국 기사 요약，Qiwei를 훨씬 능가해야합니다 4096 바이트。
	m := batchMsg(200)
	body, kept := markdownBody(m, weComMarkdownLimit)

	if len(body) > weComMarkdownLimit {
		t.Fatalf("문자 %d 바이트가 상한을 초과했습니다. %d", len(body), weComMarkdownLimit)
	}
	if !utf8.ValidString(body) {
		t.Fatal("본문이 불법입니다 UTF-8")
	}
	if kept <= 0 || kept >= len(m.Items) {
		t.Fatalf("일부만 설치해야 합니다.（0 < kept < %d），받았어요 %d", len(m.Items), kept)
	}
	// 헤더에는 이 기사에 포함된 항목 수를 정확하게 명시해야 합니다.、또 몇 명이나 있나요?——그렇지 않으면 독자가 고개를 돌릴 것이다.
	// 그 번호를 그대로 받아들이세요。
	if !strings.Contains(body, "나머지는") || !strings.Contains(body, "다음 메시지로 계속") {
		t.Fatalf("헤더에는 이 기사에 포함되지 않은 추가 항목 수를 나타내야 합니다.:\n%s", body[:minInt(400, len(body))])
	}
	// 은 첫 번째 항목만 포함해야 합니다. kept 글。
	for i := 0; i < kept; i++ {
		if !strings.Contains(body, "취약점"+itoa(i+1)) {
			t.Fatalf("아니요. %d 이 메시지에 있어야 합니다.:\n%s", i+1, body)
		}
	}
	if strings.Contains(body, "취약점"+itoa(kept+1)) {
		t.Fatalf("아니요. %d 이 표시되어서는 안 됩니다.（다음 배치에 속합니다）", kept+1)
	}
}

func TestMarkdownBodyKeepsEverythingWhenUnderLimit(t *testing.T) {
	m := batchMsg(3)
	body, kept := markdownBody(m, 0) // 0 = 제한 없음
	if kept != len(m.Items) {
		t.Fatalf("길이 제한이 없으면 모두 유지해야 합니다.，받았어요 kept=%d", kept)
	}
	if strings.Contains(body, "나머지는") {
		t.Fatalf("잘림이 없으면 잘림 프롬프트가 나타나지 않아야 합니다.:\n%s", body)
	}
}

func TestMarkdownBodyAlwaysKeepsAtLeastOneItem(t *testing.T) {
	// 예산이 너무 작아서 한 작품도 담을 수 없을 때，아직도 보내고 싶어요（궁극적인 잘림）。
	// 그렇지 않으면 매우 긴 허점이 전체 배치를 영구적으로 차단합니다.：받을때마다 못맞춰요.、매번 보내지 않음。
	m := batchMsg(5)
	_, kept := markdownBody(m, 50)
	if kept != 1 {
		t.Fatalf("최소한은 보관해야지 1 글，받았어요 %d", kept)
	}
}

func TestMarkdownBodySingleReturnsOne(t *testing.T) {
	_, kept := markdownBody(singleMsg(), 4096)
	if kept != 1 {
		t.Fatalf("단일 메시지는 전달된 것으로 보고되어야 합니다. 1 글，받았어요 %d", kept)
	}
	// 빈 메시지에 전달할 항목이 없습니다.。
	if _, k := markdownBody(Message{}, 4096); k != 0 {
		t.Fatalf("빈 메시지를 보고해야 합니다. 0 글，받았어요 %d", k)
	}
}

func TestTelegramPackingUsesRuneBudget(t *testing.T) {
	m := batchMsg(200)
	text, kept := telegramHTML(m)
	// Telegram 언론**문자수**길이 제한；바이트 크기를 사용하면 중국어 메시지가 1/3로 줄어듭니다.。
	if n := utf8.RuneCountInString(text); n > telegramTextLimit {
		t.Fatalf("문자 %d 글자수 제한을 초과했습니다. %d", n, telegramTextLimit)
	}
	if kept <= 0 || kept >= len(m.Items) {
		t.Fatalf("일부만 설치해야 합니다.，받았어요 %d", kept)
	}
	if !strings.Contains(text, "다음으로 계속") {
		t.Fatalf("포함되지 않은 잉여금이 있다고 명시해야합니다:\n%.300s", text)
	}
}

func TestFeishuPackingReportsKept(t *testing.T) {
	m := batchMsg(2000)
	_, kept := feishuCard(m)
	if kept <= 0 || kept >= len(m.Items) {
		t.Fatalf("카드는 일부분만 담아야 합니다.，받았어요 %d", kept)
	}
}

func TestWebhookAndEmailReportAllItems(t *testing.T) {
	// 이 두 채널은 텍스트를 자르지 않습니다.，전체 배치가 배송된 것으로 간주됩니다.。
	m := batchMsg(7)
	if n := len(m.Items); n != 7 {
		t.Fatal("전제조건이 성립되지 않았습니다.")
	}
	// 렌더러의 반환값을 통해 간접적으로 확인함：markdownBody(0) 제한이 없으면 모두 유지。
	if _, k := markdownBody(m, 0); k != len(m.Items) {
		t.Fatalf("길이 제한이 없을 경우 모두 적용，받았어요 %d", k)
	}
}

// TestMarkdownEscapesUntrustedContent 네「신뢰할 수 없는 콘텐츠로 인해 메시지 구조가 변경되어서는 안 됩니다.」에 대한 회귀 테스트。
// 제목과 요약은 모델 출력에서 따옴（모델은 측정된 대상의 응답을 읽습니다.），자산 이름은 테스트 중인 대상에서 나옵니다. URL。
func TestMarkdownEscapesUntrustedContent(t *testing.T) {
	cases := []struct {
		name  string
		item  Item
		must  []string // 이 결과에 나타나야 합니다.（이스케이프 양식）
		wrong []string // 은 결과에 표시되어서는 안 됩니다.（이스케이프되지 않은 형식）
	}{
		{
			name: "제목 줄바꿈 + 외부링크",
			item: Item{
				Severity: "high",
				Name:     "로그인 포트 SQL 주사\n[긴급：계정을 확인하려면 여기를 클릭하세요.](http://attacker.tld)",
			},
			// 줄 바꿈은 접혀야 합니다.（그렇지 않으면 새로운 목록 항목이 위조될 수 있습니다./인용 블록）；
			// 대괄호와 둥근 괄호는 이스케이프되어야 합니다.（그렇지 않으면 클릭 가능한 외부 링크입니다.）。
			must:  []string{`\[긴급：계정을 확인하려면 여기를 클릭하세요.\]`, `\(http://attacker.tld\)`},
			wrong: []string{"\n[긴급", "\n\n[긴급"},
		},
		{
			name: "헤더의 이미지 비콘",
			item: Item{
				Severity: "high",
				Name:     "취약점 ![](http://attacker.tld/beacon)",
			},
			must:  []string{`\!`, `\(http://attacker.tld/beacon\)`},
			wrong: []string{"![]("},
		},
		{
			name: "자산 이름의 강조 및 참조",
			item: Item{
				Severity: "high",
				Name:     "일반제목",
				Assets:   []string{"a.com/*주사*>견적"},
			},
			must:  []string{`\*주사\*`, `\>`},
			wrong: []string{"*주사*"},
		},
		{
			name: "초록의 백틱 및 세로 막대",
			item: Item{
				Severity: "high",
				Name:     "제목",
				Summary:  "`code` | 양식",
			},
			must:  []string{"\\`code\\`", `\|`},
			wrong: []string{"`code`"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Message{Items: []Item{tc.item}}
			// 단일 모드 쓰기Item 세 개가 있어요 markdown 채널별로 공유되는 렌더링 경로。
			var b strings.Builder
			writeItem(&b, tc.item, "", true)
			got := b.String()
			for _, want := range tc.must {
				if !strings.Contains(got, want) {
					t.Errorf("탈출 양식이 누락되었습니다. %q:\n%s", want, got)
				}
			}
			for _, bad := range tc.wrong {
				if strings.Contains(got, bad) {
					t.Errorf("이스케이프되지 않은 양식이 나타납니다. %q（을 사용하여 구조나 외부 링크를 삽입할 수 있습니다.）:\n%s", bad, got)
				}
			}
			_ = m
		})
	}
}

// TestMarkdownEscapeBackslashFirst 잠금 탈출 명령：백슬래시를 먼저 처리해야 합니다.，
// 그렇지 않으면 끝에 추가된 백슬래시가 다른 레이어로 덮이게 됩니다.，출력에 이중 백슬래시가 나타납니다.。
func TestMarkdownEscapeBackslashFirst(t *testing.T) {
	if got := markdownEscape(`a\b*c`); got != `a\\b\*c` {
		t.Fatalf("탈출 명령이 잘못되었습니다.，받았어요 %q", got)
	}
}

// TestTelegramTitleHasNoMarkdownEscapes 특정 반품을 잠급니다.：
// markdown 이스케이프는 다음으로 유출될 수 없습니다. Telegram 님 HTML 출력에서（는 한때 공유 제목 기능에 있었습니다.
// 탈출됨，결과 Telegram 이 메시지에 나타납니다. `\(1\)` 이렇게 보이는 백슬래시）。
func TestTelegramTitleHasNoMarkdownEscapes(t *testing.T) {
	m := Message{Items: []Item{{Severity: "high", Name: "alert(1) *핵심사항*"}}}
	text, _ := telegramHTML(m)
	if strings.Contains(text, `\(`) || strings.Contains(text, `\*`) {
		t.Fatalf("Telegram 본문에 나옴 markdown 의 백슬래시 이스케이프:\n%s", text)
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
