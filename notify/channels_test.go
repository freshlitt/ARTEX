package notify

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

// singleMsg 따옴표와 줄 바꿈을 사용하여 단일 메시지를 구성합니다.。의도적으로 포함하여 사용 `"` 그리고 `\n` 직위/요약：
// 템플릿 보간법으로 가장 쉽게 생성되는 불법 패턴입니다. JSON 에서 입력。
func singleMsg() Message {
	return Message{
		Items: []Item{{
			FindingID: 42,
			Name:      `로그인 "SQL주사" 위험`,
			VulnClass: "SQL주사",
			Severity:  "high",
			Summary:   "매개변수 id\n필터링되지 않음 주사를 유발함",
			Assets:    []string{"a.example.com", "b.example.com"},
			DetailURL: "https://artex.local/function/findings/detail?id=42",
		}},
	}
}

// batchMsg 요약 메시지 배치 구성。
func batchMsg(n int) Message {
	m := Message{Batch: true, WindowMinutes: 30, HomeURL: "https://artex.local/function/findings"}
	for i := 0; i < n; i++ {
		m.Items = append(m.Items, Item{
			FindingID: int64(i + 1),
			Name:      "취약점" + itoa(i+1),
			VulnClass: "XSS",
			Severity:  "medium",
			Summary:   "반사적 크로스 사이트 스크립팅",
			Assets:    []string{"target.example.com"},
		})
	}
	return m
}

// capturePost 가짜 수신단 설정，수신된 요청 본문과 헤더를 다시 어설션 함수에 전달합니다.。
func capturePost(t *testing.T, respBody string, assert func(t *testing.T, body map[string]any, r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("요청 본문이 잘못되었습니다. JSON: %v\n원문: %s", err, raw)
			}
		}
		if assert != nil {
			assert(t, body, r)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDingTalkSendsActionCardWhenLinkPresent(t *testing.T) {
	srv := capturePost(t, `{"errcode":0,"errmsg":"ok"}`, func(t *testing.T, body map[string]any, _ *http.Request) {
		if body["msgtype"] != "actionCard" {
			t.Fatalf("답글 링크 있을때 보내야함 actionCard，받았어요 %v", body["msgtype"])
		}
		card, _ := body["actionCard"].(map[string]any)
		if card["singleURL"] != "https://artex.local/function/findings/detail?id=42" {
			t.Errorf("반품링크가 유실되었습니다: %v", card["singleURL"])
		}
	})
	if _, err := (dingTalkChannel{}).Send(context.Background(), map[string]any{"webhook": srv.URL}, singleMsg()); err != nil {
		t.Fatalf("배송실패: %v", err)
	}
}

func TestDingTalkFallsBackToMarkdownForBatch(t *testing.T) {
	srv := capturePost(t, `{"errcode":0,"errmsg":"ok"}`, func(t *testing.T, body map[string]any, _ *http.Request) {
		if body["msgtype"] != "markdown" {
			t.Fatalf("요약 메시지를 보내야 합니다. markdown，받았어요 %v", body["msgtype"])
		}
		md, _ := body["markdown"].(map[string]any)
		if !strings.Contains(md["text"].(string), "근처 30 분") {
			t.Errorf("요약 텍스트에 기간이 누락되었습니다.: %v", md["text"])
		}
	})
	if _, err := (dingTalkChannel{}).Send(context.Background(), map[string]any{"webhook": srv.URL}, batchMsg(3)); err != nil {
		t.Fatalf("배송실패: %v", err)
	}
}

// TestDingTalkBusinessErrorIsPermanent 자물쇠「HTTP 200 하지만 errcode 아니요 0」의 판단。
// 확인하지 않음 errcode 은 배달 실패를 성공으로 기록합니다.——국내산이에요 IM 플랫폼의 일반적인 함정。
func TestDingTalkBusinessErrorIsPermanent(t *testing.T) {
	srv := capturePost(t, `{"errcode":310000,"errmsg":"keywords not in content"}`, nil)
	_, err := (dingTalkChannel{}).Send(context.Background(), map[string]any{"webhook": srv.URL}, singleMsg())
	if err == nil {
		t.Fatal("errcode 아니요 0 오류가 보고되어야 합니다.")
	}
	if !IsPermanent(err) {
		t.Fatalf("키워드 불일치는 구성 오류입니다.，영구 실패로 표시되어야 합니다.，받았어요 %v", err)
	}
	if !strings.Contains(err.Error(), "310000") {
		t.Errorf("오류 메시지에는 플랫폼 오류 코드가 포함되어야 합니다.，받았어요 %v", err)
	}
}

func TestWeComTruncatesCJKWithinByteLimit(t *testing.T) {
	var contentLen int
	srv := capturePost(t, `{"errcode":0,"errmsg":"ok"}`, func(t *testing.T, body map[string]any, _ *http.Request) {
		md, _ := body["markdown"].(map[string]any)
		content, _ := md["content"].(string)
		contentLen = len(content)
		if !utf8.ValidString(content) {
			t.Fatal("잘린 후에는 올바르지 않습니다. UTF-8——Qiwei는 기사 전체를 거부합니다.")
		}
	})
	// 충분히 긴 중국어 요약 배치를 만듭니다.，초과해야 함 4096 바이트。
	m := batchMsg(200)
	if _, err := (weComChannel{}).Send(context.Background(), map[string]any{"webhook": srv.URL}, m); err != nil {
		t.Fatalf("배송실패: %v", err)
	}
	if contentLen > weComMarkdownLimit {
		t.Fatalf("문자 %d 바이트가 Qiwei의 상한을 초과했습니다. %d", contentLen, weComMarkdownLimit)
	}
	if contentLen == 0 {
		t.Fatal("텍스트가 비어 있습니다.")
	}
}

func TestWeComRateLimitIsRetryableButKeyErrorIsPermanent(t *testing.T) {
	limited := capturePost(t, `{"errcode":45009,"errmsg":"api freq out of limit"}`, nil)
	_, err := (weComChannel{}).Send(context.Background(), map[string]any{"webhook": limited.URL}, singleMsg())
	if err == nil || IsPermanent(err) {
		t.Fatalf("45009 은 롤링 창 현재 제한입니다.，다시 시도해볼 수 있을 것 같아요，받았어요 %v", err)
	}

	badKey := capturePost(t, `{"errcode":93000,"errmsg":"invalid webhook url"}`, nil)
	_, err = (weComChannel{}).Send(context.Background(), map[string]any{"webhook": badKey.URL}, singleMsg())
	if err == nil || !IsPermanent(err) {
		t.Fatalf("93000 네 key 유효하지 않음，재시도 자체가 복구되지 않음，영구적인 실패여야 합니다.，받았어요 %v", err)
	}
}

func TestFeishuCardStructureAndSign(t *testing.T) {
	const secret = "SECtest123"
	srv := capturePost(t, `{"code":0,"msg":"success"}`, func(t *testing.T, body map[string]any, _ *http.Request) {
		if body["msg_type"] != "interactive" {
			t.Fatalf("인터랙티브 카드를 발급받아야 합니다.，받았어요 %v", body["msg_type"])
		}
		card, _ := body["card"].(map[string]any)
		header, _ := card["header"].(map[string]any)
		if header["template"] != "orange" {
			t.Errorf("high 레벨이 되어야 합니다. orange 컬러 매칭，받았어요 %v", header["template"])
		}
		// 일치함 secret 서명 매개변수를 가져와야 합니다.，그렇지 않으면 Feishu는 19021 거부。
		if body["sign"] == nil || body["timestamp"] == nil {
			t.Fatalf("서명 매개변수가 누락되었습니다.: %v", body)
		}
		// 카드 요소에는 버튼이 포함되어야 합니다.，그래요 url 은 취약점 세부정보를 나타냅니다.。
		elements, _ := card["elements"].([]any)
		foundButton := false
		for _, e := range elements {
			em, _ := e.(map[string]any)
			if em["tag"] != "action" {
				continue
			}
			actions, _ := em["actions"].([]any)
			for _, a := range actions {
				am, _ := a.(map[string]any)
				if am["url"] == "https://artex.local/function/findings/detail?id=42" {
					foundButton = true
				}
			}
		}
		if !foundButton {
			t.Fatal("카드에 상세페이지로 연결되는 버튼이 없습니다.")
		}
	})
	cfg := map[string]any{"webhook": srv.URL, "secret": secret}
	if _, err := (feishuChannel{}).Send(context.Background(), cfg, singleMsg()); err != nil {
		t.Fatalf("배송실패: %v", err)
	}
}

func TestFeishuWithoutSecretOmitsSign(t *testing.T) {
	srv := capturePost(t, `{"code":0,"msg":"success"}`, func(t *testing.T, body map[string]any, _ *http.Request) {
		if body["sign"] != nil || body["timestamp"] != nil {
			t.Fatalf("구성되지 않음 secret 은 서명 매개변수를 포함해서는 안 됩니다.: %v", body)
		}
	})
	if _, err := (feishuChannel{}).Send(context.Background(), map[string]any{"webhook": srv.URL}, singleMsg()); err != nil {
		t.Fatalf("배송실패: %v", err)
	}
}

func TestTelegramEscapesHTMLInUntrustedContent(t *testing.T) {
	var text string
	srv := capturePost(t, `{"ok":true}`, func(t *testing.T, body map[string]any, _ *http.Request) {
		text, _ = body["text"].(string)
		if body["parse_mode"] != "HTML" {
			t.Fatalf("을 사용해야 합니다. HTML 구문 분석 모드，받았어요 %v", body["parse_mode"])
		}
	})
	m := Message{Items: []Item{{
		Severity: "high",
		// 제목과 요약은 테스트 중인 대상에서 따왔습니다./모델 출력，은 신뢰할 수 없는 콘텐츠입니다.。
		Name:    `<script>alert(1)</script>`,
		Summary: "a & b < c",
	}}}
	if _, err := (telegramChannel{}).Send(context.Background(),
		map[string]any{"bot_token": "tok", "chat_id": "1", "base_url": srv.URL}, m); err != nil {
		t.Fatalf("배송실패: %v", err)
	}
	if strings.Contains(text, "<script>") {
		t.Fatalf("이스케이프되지 않음 HTML，존재주입: %q", text)
	}
	if !strings.Contains(text, "&lt;script&gt;") {
		t.Fatalf("이스케이프된 엔터티가 필요합니다.，받았어요 %q", text)
	}
	if !strings.Contains(text, "a &amp; b") {
		t.Fatalf("& 이스케이프되지 않음，받았어요 %q", text)
	}
}

func TestTelegramErrorClassification(t *testing.T) {
	rateLimited := capturePost(t, `{"ok":false,"error_code":429,"description":"Too Many Requests"}`, nil)
	_, err := (telegramChannel{}).Send(context.Background(),
		map[string]any{"bot_token": "tok", "chat_id": "1", "base_url": rateLimited.URL}, singleMsg())
	if err == nil || IsPermanent(err) {
		t.Fatalf("429 다시 시도해볼 수 있을 것 같아요，받았어요 %v", err)
	}

	forbidden := capturePost(t, `{"ok":false,"error_code":403,"description":"bot was blocked by the user"}`, nil)
	_, err = (telegramChannel{}).Send(context.Background(),
		map[string]any{"bot_token": "tok", "chat_id": "1", "base_url": forbidden.URL}, singleMsg())
	if err == nil || !IsPermanent(err) {
		t.Fatalf("403 구성 문제입니다，영구적인 실패여야 합니다.，받았어요 %v", err)
	}
}

func TestWebhookDefaultTemplateProducesValidJSON(t *testing.T) {
	// 기본 템플릿의 의미입니다.：제목에 따옴표와 줄 바꿈이 있는 경우，아무 일반
	// `"title": "{{.Title}}"` 어떠한 방식으로 작성해도 불법행위가 됩니다. JSON。{{json .}} 안돼요。
	srv := capturePost(t, `{"ok":true}`, func(t *testing.T, body map[string]any, _ *http.Request) {
		if body["title"] != `[🟠 위험도 높음] 로그인 "SQL주사" 위험` {
			t.Errorf("제목이 올바르게 복원되지 않았습니다.: %v", body["title"])
		}
		items, _ := body["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("items 수량은 1，받았어요 %d", len(items))
		}
		it, _ := items[0].(map[string]any)
		if it["summary"] != "매개변수 id\n필터링되지 않음 주사를 유발함" {
			t.Errorf("요약이 올바르게 복원되지 않았습니다.: %v", it["summary"])
		}
		// 값은 다음과 같아야 합니다. JSON 문자열 대신 숫자（json:"...,string" 과 같이 쓰면 이 함정에 빠지게 됩니다.）。
		if _, ok := it["finding_id"].(float64); !ok {
			t.Errorf("finding_id 은 숫자여야 합니다.，받았어요 %T", it["finding_id"])
		}
	})
	if _, err := (webhookChannel{}).Send(context.Background(), map[string]any{"url": srv.URL}, singleMsg()); err != nil {
		t.Fatalf("배송실패: %v", err)
	}
}

func TestWebhookCustomTemplateAndHeaders(t *testing.T) {
	srv := capturePost(t, `{"ok":true}`, func(t *testing.T, body map[string]any, r *http.Request) {
		if r.Header.Get("X-Token") != "s3cret" {
			t.Errorf("사용자 정의 헤더가 누락되었습니다.: %v", r.Header)
		}
		if body["msg"] != "3 글" {
			t.Errorf("사용자 정의 템플릿 렌더링이 올바르지 않습니다.: %v", body["msg"])
		}
		if body["first"] != "취약점1" {
			t.Errorf("range 추출이 잘못되었습니다.: %v", body["first"])
		}
	})
	cfg := map[string]any{
		"url":           srv.URL,
		"headers":       map[string]any{"X-Token": "s3cret"},
		"body_template": `{"msg": {{json (printf "%d 글" .Count)}}, "first": {{json (index .Items 0).Name}}}`,
	}
	if _, err := (webhookChannel{}).Send(context.Background(), cfg, batchMsg(3)); err != nil {
		t.Fatalf("배송실패: %v", err)
	}
}

func TestWebhookRejectsNonJSONRenderResult(t *testing.T) {
	cfg := map[string]any{"url": "https://example.com/hook", "body_template": `not json at all`}
	_, err := (webhookChannel{}).Send(context.Background(), cfg, singleMsg())
	if err == nil || !IsPermanent(err) {
		t.Fatalf("렌더링 결과가 그렇지 않습니다. JSON 영구적인 실패여야 합니다.（템플릿이 잘못되었습니다.，재시도 소용없어），받았어요 %v", err)
	}
}

func TestWebhookValidateCatchesBadConfigEarly(t *testing.T) {
	bad := []map[string]any{
		{},
		{"url": "file:///etc/passwd"},
		{"url": "https://example.com", "method": "DELETE"},
		{"url": "https://example.com", "body_template": `{{.Items.`},
	}
	for i, cfg := range bad {
		if err := (webhookChannel{}).Validate(cfg); err == nil {
			t.Errorf("아니요. %d 그룹 구성을 거부해야 합니다.: %v", i, cfg)
		}
	}
}

func TestEmailMessageIsWellFormed(t *testing.T) {
	msg, err := buildEmailMessage("artex@example.com", []string{"a@example.com", "b@example.com"}, singleMsg())
	if err != nil {
		t.Fatalf("이메일 정리 실패: %v", err)
	}
	if !strings.HasPrefix(msg, "From: artex@example.com\r\n") {
		t.Fatalf("From 헤더가 잘못되었습니다.:\n%s", msg)
	}
	if !strings.Contains(msg, "To: a@example.com, b@example.com\r\n") {
		t.Fatalf("To 헤더가 잘못되었습니다.:\n%s", msg)
	}
	// 중국어 테마가 필요합니다 RFC 2047 인코딩，그렇지 않으면 클라이언트에 잘못된 문자가 표시됩니다.。
	if !strings.Contains(msg, "Subject: =?utf-8?") {
		t.Fatalf("주제가 완료되지 않았습니다. RFC 2047 인코딩:\n%s", msg)
	}
	if dec, err := new(mime.WordDecoder).DecodeHeader(mustExtractHeader(t, msg, "Subject")); err != nil {
		t.Fatalf("주제를 디코딩할 수 없습니다.: %v", err)
	} else if !strings.Contains(dec, "SQL주사") {
		t.Fatalf("토픽의 디코딩된 내용이 올바르지 않습니다.: %q", dec)
	}

	// 본문은 base64，해결책은 합법적이어야 합니다 HTML。
	parts := strings.SplitN(msg, "\r\n\r\n", 2)
	if len(parts) != 2 {
		t.Fatal("이메일에 헤더가 없습니다./신체분리")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(strings.TrimSpace(parts[1]), "\r\n", ""))
	if err != nil {
		t.Fatalf("문자 base64 디코딩 실패: %v", err)
	}
	html := string(decoded)
	if !strings.HasPrefix(html, "<div") {
		t.Fatalf("본문이 아닙니다. HTML: %.80s", html)
	}
	// 제목이 텍스트 위치 그대로 나타납니다.：HTML 텍스트 내용의 큰따옴표는 유효한 문자입니다.，탈출할 필요는 없어。
	// 여기에서 주장하세요.「그대로 놔두세요」은 나중에 누군가 실수로 이스케이프된 따옴표 레이어를 추가하는 것을 방지하기 위한 것입니다.，중국어 인용문을 보자
	// 은 다음과 같이 표시됩니다. &quot;。
	if !strings.Contains(html, `"SQL주사"`) {
		t.Fatalf("제목의 따옴표는 텍스트 위치에 그대로 두어야 합니다.: %.200s", html)
	}
}

// TestEmailEscapesStructuralInjection 주입을 방지하려면 이메일 본문을 덮어써야 합니다.：
// 취약점 제목 및 요약은 테스트된 대상 및 모델 출력에서 옵니다.，은 신뢰할 수 없는 콘텐츠입니다.。텍스트 위치를 이스케이프해야 합니다.
// & < >（그렇지 않으면 태그를 삽입할 수 있습니다.），속성 위치도 따옴표를 이스케이프해야 합니다.（그렇지 않으면 닫힐 수 있습니다 href）。
func TestEmailEscapesStructuralInjection(t *testing.T) {
	m := Message{
		Items: []Item{{
			Severity:  "high",
			Name:      `<script>alert(1)</script>`,
			Summary:   "a & b > c",
			DetailURL: `https://artex.local/x?a="onmouseover=alert(1)`,
		}},
	}
	html := htmlBody(m, 0)
	if strings.Contains(html, "<script>") {
		t.Fatalf("제목이 이스케이프되지 않았습니다.，주사형 태그: %s", html)
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Fatalf("이스케이프된 엔터티가 필요합니다.: %s", html)
	}
	if !strings.Contains(html, "a &amp; b &gt; c") {
		t.Fatalf("& 그리고 > 이스케이프되지 않음: %s", html)
	}
	// 뒤로 링크는 관리자가 구성할 수 있습니다. public_base_url，자체가 신뢰도가 높다，하지만 속성 위치는 여전히
	// 따옴표를 탈출하세요——그렇지 않으면 인용된 주소가 닫힙니다. href 및 이벤트 핸들러 삽입。
	if strings.Contains(html, `onmouseover=alert(1)">`) {
		t.Fatalf("href 속성이 올바르게 이스케이프되지 않았습니다.: %s", html)
	}
	if !strings.Contains(html, "&quot;") {
		t.Fatalf("속성 위치의 따옴표는 이스케이프되어야 합니다.: %s", html)
	}
}

func mustExtractHeader(t *testing.T, msg, name string) string {
	t.Helper()
	for _, line := range strings.Split(msg, "\r\n") {
		if strings.HasPrefix(line, name+": ") {
			return strings.TrimPrefix(line, name+": ")
		}
	}
	t.Fatalf("찾을 수 없음 %s 머리", name)
	return ""
}

func TestChannelValidateReportsMissingFields(t *testing.T) {
	// 확인 오류는 구성기에 직접 표시됩니다.，무엇이 부족한지 명확히 밝혀야 함，「잘못된 구성」。
	cases := []struct {
		kind   string
		cfg    map[string]any
		substr string
	}{
		{KindDingTalk, map[string]any{}, "Webhook"},
		{KindFeishu, map[string]any{}, "Webhook"},
		{KindWeCom, map[string]any{}, "Webhook"},
		{KindTelegram, map[string]any{}, "Bot Token"},
		{KindTelegram, map[string]any{"bot_token": "t"}, "Chat ID"},
		{KindEmail, map[string]any{}, "SMTP"},
		{KindEmail, map[string]any{"host": "h"}, "포트"},
		{KindEmail, map[string]any{"host": "h", "port": 587, "from": "f"}, "수신자"},
	}
	for _, tc := range cases {
		ch, ok := Get(tc.kind)
		if !ok {
			t.Fatalf("채널 %s 등록되지 않음", tc.kind)
		}
		err := ch.Validate(tc.cfg)
		if err == nil {
			t.Errorf("%s 구성 %v 인증 실패", tc.kind, tc.cfg)
			continue
		}
		if !strings.Contains(err.Error(), tc.substr) {
			t.Errorf("%s 에 대한 오류 메시지가 언급되어야 합니다. %q，받았어요 %q", tc.kind, tc.substr, err.Error())
		}
	}
}

// TestEmailSMTPErrorClassification 자물쇠 SMTP 4xx/5xx 의 의미적 구별。
// 만약에 4xx 무기징역도 선고，그레이리스팅이 활성화된 메일 서버는 모든 푸시를 처음으로 수행합니다.
// 시도하다가 빠졌어요 failed —— 그리고 그레이리스팅은 자동 재시도가 가장 좋은 역할을 해야 하는 시나리오입니다.。
func TestEmailSMTPErrorClassification(t *testing.T) {
	cases := []struct {
		reply     string
		permanent bool
	}{
		{"450 4.7.1 Greylisting in action, please come back later", false},
		{"451 4.3.0 Temporary system failure", false},
		{"452 4.2.2 Mailbox full", false},
		{"550 5.1.1 User unknown", true},
		{"553 5.1.3 Bad address syntax", true},
		{"554 5.7.1 Relay access denied", true},
		// 응답 코드를 얻을 수 없을 때 누르세요.「다시 시도할 수 있습니다」처리 중：한 번 더 해보고 싶어요，가능한 한 순간을 놓치지 마세요
		// 실패는 치명적이다。
		{"unexpected EOF", false},
		{"", false},
	}
	for _, tc := range cases {
		err := smtpStageError("수신자가 거부되었습니다.", errors.New(tc.reply))
		if got := IsPermanent(err); got != tc.permanent {
			t.Errorf("답변 %q: 기대 permanent=%v 받았어요 %v", tc.reply, tc.permanent, got)
		}
		// 어떻게 분류하든 상관없습니다.，사용자가 확인할 수 있도록 원문을 보관해야 합니다.。
		if tc.reply != "" && !strings.Contains(err.Error(), tc.reply) {
			t.Errorf("답변 %q 의 원본 텍스트는 삭제되었습니다.: %v", tc.reply, err)
		}
	}
}

func TestRegistryCoversAllKinds(t *testing.T) {
	// 6개의 채널이 필수입니다.——한명 줄겠습니다 UI 다운로드가 소리없이 사라졌습니다。
	want := []string{KindDingTalk, KindEmail, KindFeishu, KindTelegram, KindWebhook, KindWeCom}
	got := Kinds()
	if len(got) != len(want) {
		t.Fatalf("채널 수는 다음과 같아야 합니다. %d，받았어요 %d: %v", len(want), len(got), got)
	}
	for _, k := range want {
		if !ValidKind(k) {
			t.Errorf("채널 %s 등록되지 않음", k)
		}
		if ch, ok := Get(k); !ok || ch.Kind() != k {
			t.Errorf("채널 %s 님 Kind() 이 등록 키와 일치하지 않습니다.", k)
		}
	}
	if ValidKind("nope") {
		t.Error("등록되지 않은 유형은 확인을 통과해서는 안 됩니다.")
	}
}

func TestPermanentErrorUnwrap(t *testing.T) {
	base := &permanentSentinel{}
	err := Permanent(base)
	if !IsPermanent(err) {
		t.Fatal("영구실패로 인정해야함")
	}
	if !strings.Contains(err.Error(), "sentinel") {
		t.Fatalf("오류 정보는 하위 레이어에 투명하게 전달되어야 합니다.: %v", err)
	}
	if Permanent(nil) != nil {
		t.Fatal("Permanent(nil) 반납해야함 nil")
	}
	if IsPermanent(nil) {
		t.Fatal("nil 영구적인 실패는 아닙니다.")
	}
}

type permanentSentinel struct{}

func (*permanentSentinel) Error() string { return "sentinel" }
