package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// 이 파일은 불변 테스트입니다.：**아무거나**채널 구현에서 나오는 오류 텍스트에는 자격 증명이 포함되어서는 안 됩니다.。
//
// 왜 별도의 파일을 가져오나요?：초기 채널 사용 사례에서는 성공적인 경로와 플랫폼 비즈니스 오류만 다룹니다.，
// 전송 계층 실패를 전혀 보지 못했습니다.。정확히는 전송 계층 오류입니다.（연결이 거부되었습니다./DNS 실패/시간 초과）가장 위험한——
// http.Client.Do 반환됨 *url.Error 그럴게요**완료 URL** 오류 텍스트 입력 중，그리고 이 기능은
// 이들 기업의 자격증은 여기 URL 내부。자격 증명은 이 스트림을 따라 4개의 출구로 나갔습니다.：
//
//	notification_deliveries.last_error  → 데이터베이스에 저장된 일반 텍스트
//	GET /api/notify/deliveries 응답     → 우회 채널 구성 마스크，브라우저에 에코
//	서버 로그                          → 아웃소싱을 통해 보유하는 경우가 많음
//	테스트 전송 인터페이스 502 응답             → 프런트엔드에서 직접 플레이
//
// 그래서 여기서 테스트한 기능은 단 하나가 아닙니다.，대신 실패할 수밖에 없는 요청을 하나씩 보내보세요.，어설션 오류 텍스트
// 에서 자격 증명을 찾을 수 없습니다.。

// credentialCases 전체 커버「자격 증명은 URL 내부」채널 양식：
// 딩톡/치웨이가 왔습니다 query，페이슈는 길의 끝에 있다，Telegram 길 중간에。
var credentialCases = []struct {
	name   string
	ch     Channel
	cfg    map[string]any
	secret string
}{
	{
		name:   "딩톡 access_token 에 query",
		ch:     dingTalkChannel{},
		cfg:    map[string]any{"webhook": "http://127.0.0.1:1/robot/send?access_token=" + leakProbeToken},
		secret: leakProbeToken,
	},
	{
		name:   "기업 위챗 key 에 query",
		ch:     weComChannel{},
		cfg:    map[string]any{"webhook": "http://127.0.0.1:1/cgi-bin/webhook/send?key=" + leakProbeToken},
		secret: leakProbeToken,
	},
	{
		name:   "페이슈 hook id 길 끝에",
		ch:     feishuChannel{},
		cfg:    map[string]any{"webhook": "http://127.0.0.1:1/open-apis/bot/v2/hook/" + leakProbeToken},
		secret: leakProbeToken,
	},
	{
		name:   "Telegram bot token 길 중간에",
		ch:     telegramChannel{},
		cfg:    map[string]any{"bot_token": leakProbeToken, "chat_id": "1", "base_url": "http://127.0.0.1:1"},
		secret: leakProbeToken,
	},
	{
		name:   "딩톡 서명키",
		ch:     dingTalkChannel{},
		cfg:    map[string]any{"webhook": "http://127.0.0.1:1/robot/send", "secret": leakProbeToken},
		secret: leakProbeToken,
	},
}

// leakProbeToken 은 절대 실제 크리덴셜이 될 수 없는 센티널 값입니다.，은 오류 텍스트에서 검색하는 데 사용됩니다.。
const leakProbeToken = "LEAKPROBE0123456789abcdef"

// TestChannelErrorsNeverLeakCredentials 은 핵심 불변입니다.。
func TestChannelErrorsNeverLeakCredentials(t *testing.T) {
	for _, tc := range credentialCases {
		t.Run(tc.name, func(t *testing.T) {
			// 실패해야 하는 피어：127.0.0.1:1 아무도 듣지 않는다，접속 거부의 길을 택했습니다.。
			_, err := tc.ch.Send(context.Background(), tc.cfg, Message{
				Items: []Item{{FindingID: 1, Severity: "high", Name: "누출 프로브"}},
			})
			if err == nil {
				t.Fatal("연결할 수 없는 주소에 대한 오류를 보고해야 합니다.")
			}
			assertNoSecret(t, err.Error(), tc.secret)
		})
	}
}

// TestChannelErrorsNeverLeakCredentialsInPermanentPath 영구 실패한 분기 덮어쓰기：
// URL 확인 실패、플랫폼 업무 오류 등도 외부로 오류 텍스트를 전송합니다.，또한 자격 증명을 가져올 수 없습니다.。
func TestChannelErrorsNeverLeakCredentialsInPermanentPath(t *testing.T) {
	cases := []struct {
		name string
		ch   Channel
		cfg  map[string]any
	}{
		// 주소에 자격 증명이 포함되어 있지만 형식이 잘못되었습니다. → 트리거 validateHTTPURL / url.Parse 지점。
		{"딩톡 주소가 불법입니다", dingTalkChannel{}, map[string]any{"webhook": "file:///" + leakProbeToken}},
		{"기업 마이크로 주소가 불법입니다", weComChannel{}, map[string]any{"webhook": "gopher://" + leakProbeToken}},
		{"페이슈 주소가 불법입니다", feishuChannel{}, map[string]any{"webhook": "ftp://" + leakProbeToken + "/hook"}},
		{"Telegram API 잘못된 주소입니다.", telegramChannel{}, map[string]any{"bot_token": "tok", "chat_id": "1", "base_url": "file://" + leakProbeToken}},
		{"일반 Webhook 잘못된 주소입니다.", webhookChannel{}, map[string]any{"url": "javascript:" + leakProbeToken}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.ch.Send(context.Background(), tc.cfg, Message{Items: []Item{{Severity: "high"}}})
			if err == nil {
				t.Fatal("잘못된 구성은 오류를 보고해야 합니다.")
			}
			assertNoSecret(t, err.Error(), leakProbeToken)
		})
	}
}

func assertNoSecret(t *testing.T, text, secret string) {
	t.Helper()
	if strings.Contains(text, secret) {
		t.Fatalf("오류 텍스트 유출된 자격 증명 %q:\n    %s", secret, text)
	}
}

func TestRedactRequestTargetKeepsOnlySchemeAndHost(t *testing.T) {
	cases := map[string]string{
		"https://oapi.dingtalk.com/robot/send?access_token=S1":    "https://oapi.dingtalk.com/…",
		"https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=S2": "https://qyapi.weixin.qq.com/…",
		"https://open.feishu.cn/open-apis/bot/v2/hook/S3":         "https://open.feishu.cn/…",
		"https://api.telegram.org/botS4/sendMessage":              "https://api.telegram.org/…",
		"http://10.0.0.5:8080/hook":                               "http://10.0.0.5:8080/…",
	}
	for in, want := range cases {
		got := redactRequestTarget(in)
		if got != want {
			t.Errorf("redactRequestTarget(%q) = %q，기대 %q", in, got, want)
		}
		// 감도 줄이기 결과 자체에는 더 이상 원래 주소에 대한 경로가 포함되어서는 안 됩니다./쿼리 조각。
		if parts := strings.SplitN(in, "://", 2); len(parts) == 2 {
			if hostAndRest := strings.SplitN(parts[1], "/", 2); len(hostAndRest) == 2 && hostAndRest[1] != "" {
				if strings.Contains(got, hostAndRest[1]) {
					t.Errorf("둔감화 후에도 여전히 경로가 포함되어 있습니다./쿼리 조각 %q: %q", hostAndRest[1], got)
				}
			}
		}
	}
	// 구문 분석할 수 없는 입력은 원래 문자열을 반영하지 않습니다.。
	for _, bad := range []string{"", "://", "not a url", "http://"} {
		if got := redactRequestTarget(bad); strings.Contains(got, bad) && bad != "" {
			t.Errorf("구문 분석할 수 없는 입력 %q 은 다음과 같이 에코됩니다. %q", bad, got)
		}
	}
}

// TestRedactTransportErrorStripsURL 직접보기 *url.Error 이 특정 유형：
// 그렇죠 http.Client.Do 의 반환 유형，도 첫 유출장면이군요。
func TestRedactTransportErrorStripsURL(t *testing.T) {
	inner := errors.New("dial tcp 127.0.0.1:1: connect: connection refused")
	uerr := &url.Error{
		Op:  "Post",
		URL: "https://api.telegram.org/bot" + leakProbeToken + "/sendMessage",
		Err: inner,
	}
	got := redactTransportError(uerr)
	assertNoSecret(t, got, leakProbeToken)
	if !strings.Contains(got, "api.telegram.org") {
		t.Errorf("을 유지해야 합니다. host 문제 해결용，받았어요 %q", got)
	}
	if !strings.Contains(got, "connection refused") {
		t.Errorf("문제 해결을 위해 근본적인 이유를 유지해야 합니다.，받았어요 %q", got)
	}
	// Op 도 유지하세요（POST 그래도 GET 문제 해결에 의미가 있음）。
	if !strings.Contains(got, "Post") {
		t.Errorf("작업 이름은 유지되어야 합니다.，받았어요 %q", got)
	}
}

// TestRedactURLsInTextHandlesFallback 비밀의 길：아니요 *url.Error 에 대한 사용자 정의 오류
// （리디렉션 정책에서 반환된 오류 등）의 주소도 제거됩니다.。
func TestRedactURLsInTextHandlesFallback(t *testing.T) {
	in := fmt.Sprintf("호스트 간 리디렉션 거부（a.example → http://b.example/bot%s/send）", leakProbeToken)
	got := redactURLsInText(in)
	assertNoSecret(t, got, leakProbeToken)
	if !strings.Contains(got, "http://b.example/…") {
		t.Errorf("주소는 둔감한 형식으로 바꿔야 합니다.，받았어요 %q", got)
	}
	// 주소 없는 문자는 그대로 놔두세요。
	if plain := "dial tcp: connection refused"; redactURLsInText(plain) != plain {
		t.Error("주소가 없는 텍스트는 수정하면 안 됩니다.")
	}
}

// TestCrossHostRedirectRefused 재정의「자격 증명은 URL 내부 + 호스트 간 점프를 따르세요. = 자격 증명을 넘겨주세요」。
// httptest 의 두 서비스는 다음에서 모니터링됩니다. 127.0.0.1 에 대한 다른 포트，포트가 다릅니다. Host 다르다，
// 호스트 간 점프를 구성할 뿐입니다.。
func TestCrossHostRedirectRefused(t *testing.T) {
	var hit bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/robot/send?access_token="+leakProbeToken, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	_, err := (dingTalkChannel{}).Send(context.Background(),
		map[string]any{"webhook": redirector.URL + "/robot/send?access_token=" + leakProbeToken},
		Message{Items: []Item{{Severity: "high"}}})
	if err == nil {
		t.Fatal("호스트 간 리디렉션은 거부되어야 합니다.")
	}
	if hit {
		t.Fatal("점프 대상에 액세스했습니다.——리디렉션으로 인해 자격 증명이 유출되었습니다.")
	}
	assertNoSecret(t, err.Error(), leakProbeToken)
}

// TestSameHostRedirectAllowed 역 사용 사례：동일한 호스트 점프（끝에 슬래시를 추가하면）은 계속 사용 가능해야 합니다.，
// 그렇지 않으면 정상적인 작업 흐름이 차단됩니다.。
func TestSameHostRedirectAllowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robot/send" {
			// 같은 호스트、동일한 포트로 점프。
			http.Redirect(w, r, "/robot/send/", http.StatusTemporaryRedirect)
			return
		}
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer srv.Close()

	if _, err := (dingTalkChannel{}).Send(context.Background(),
		map[string]any{"webhook": srv.URL + "/robot/send"},
		Message{Items: []Item{{Severity: "high"}}}); err != nil {
		t.Fatalf("동일한 호스트 리디렉션을 거부해서는 안 됩니다.: %v", err)
	}
}
