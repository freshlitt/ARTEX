package notify

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

// 이 문서에서는 두 가지 관련 보강재를 다룹니다.：
//   ① 배송주소는 서버를 인트라넷 진입의 발판으로 삼아서는 안 됩니다. / 클라우드 메타데이터（SSRF）
//   ② 주소 확인 오류 메시지가 해당 주소의 자격 증명을 가져오면 안 됩니다.
//
// 테스트 환경에 대하여：이 패키지는 다양한 사용 사례에서 사용됩니다. 127.0.0.1 에 httptest 가짜 수신 종료，경비원이 기본적으로 차단하겠습니다.
// 그들。그래서 TestMain 하나되어 열린다 AllowLocalTargetsEnv，그리고 다음 각각 SSRF 사용 사례는 다음과 같습니다.
// 명시적으로 삭제하세요.，주장하다**기본적으로 거부됨**의 행동。

func TestMain(m *testing.M) {
	// 일반적인 사용 사례에서 로컬 가짜 수신기에 연결하도록 허용；SSRF 유스케이스가 일시적으로 자체적으로 삭제됩니다.。
	_ = os.Setenv(AllowLocalTargetsEnv, "1")
	os.Exit(m.Run())
}

// TestDialGuardRejectsLoopbackByDefault 네 SSRF 핵심 보호 주장：
// 기본 구성에서，루프백 주소로 배송되어야 합니다.**연결 레이어**거부。
func TestDialGuardRejectsLoopbackByDefault(t *testing.T) {
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		_, _ = io.WriteString(w, `{"errcode":0}`)
	}))
	defer srv.Close()

	t.Setenv(AllowLocalTargetsEnv, "") // 비상구를 닫으세요 = 기본 동작
	_, err := (dingTalkChannel{}).Send(context.Background(),
		map[string]any{"webhook": srv.URL + "/robot/send"}, Message{Items: []Item{{Severity: "high"}}})
	if err == nil {
		t.Fatal("루프백 주소로의 배달은 기본적으로 허용되지 않아야 합니다.")
	}
	if hit {
		t.Fatal("로컬 서비스로 요청이 전송되었습니다.——가드가 작동하지 않습니다.")
	}
	// 오류 메시지는 사용자에게 취소 방법을 안내할 수 있어야 합니다.（본 기기 SMTP 릴레이는 합법적인 구성입니다.）。
	if !strings.Contains(err.Error(), AllowLocalTargetsEnv) {
		t.Errorf("거부 메시지에는 명시적으로 해제하는 방법이 설명되어야 합니다.: %v", err)
	}
}

// TestDialGuardAllowsLoopbackWhenOptedIn 역 사용 사례：명시적으로 열어야 사용 가능합니다.，
// 그렇지 않으면 이 기계 postfix / 인트라넷 릴레이 등 법적 배포를 전면적으로 폐지합니다.。
func TestDialGuardAllowsLoopbackWhenOptedIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer srv.Close()

	t.Setenv(AllowLocalTargetsEnv, "1")
	if _, err := (dingTalkChannel{}).Send(context.Background(),
		map[string]any{"webhook": srv.URL + "/robot/send"}, Message{Items: []Item{{Severity: "high"}}}); err != nil {
		t.Fatalf("명시적 해제 후 전달 가능해야 함: %v", err)
	}
}

func TestIsBlockedDialIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.1.2.3", "::1",
		"169.254.169.254", // 클라우드 메타데이터 엔드포인트——이 기능이 존재하는 주된 이유
		"169.254.1.1", "fe80::1",
		"0.0.0.0", "::",
		"224.0.0.1", "ff02::1",
		"::ffff:127.0.0.1", // IPv4-mapped 심사 전 양식을 복원해야 합니다.，그렇지 않으면 우회입니다
		"",
	}
	for _, s := range blocked {
		if !isBlockedDialIP(net.ParseIP(s)) {
			t.Errorf("%s 은 거부되어야 합니다", s)
		}
	}
	// RFC1918 사설망**일부러 놔두세요**：자체 구축된 인트라넷 Mattermost / SMTP 릴레이는 일반적이고 합법적인 사용법입니다.。
	// 이 주장은 이러한 장단점을 해결합니다.——앞으로 누가 편리하게 사설망을 추가해 주면 판단하겠습니다.，이건 실패할 거예요，
	// 의식적인 결정을 강요（배포 배치를 자동으로 삭제하는 대신）。
	allowed := []string{"10.0.0.5", "172.16.3.4", "192.168.1.10", "8.8.8.8", "2606:4700::1111"}
	for _, s := range allowed {
		if isBlockedDialIP(net.ParseIP(s)) {
			t.Errorf("%s 풀어줘야지（사설 네트워크는 일반적인 법적 전달 대상입니다.）", s)
		}
	}
}

// TestValidateHTTPURLRejectsLiteralPrivateTargets 구성 단계에서 사전 프롬프트를 재정의합니다.：
// 리터럴 IP 저장시 거부했어야 했는데，첫 번째 배송이 실패하기를 기다리는 대신。
func TestValidateHTTPURLRejectsLiteralPrivateTargets(t *testing.T) {
	t.Setenv(AllowLocalTargetsEnv, "")
	for _, raw := range []string{
		"http://127.0.0.1:8080/hook",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]:8080/hook",
	} {
		if err := validateHTTPURL(raw); err == nil {
			t.Errorf("%s 은 구성 단계에서 거부되어야 합니다.", raw)
		}
	}
	// 공용 네트워크 주소와 사설 네트워크 주소는 평소대로 통과됩니다.（개인 네트워크는 전화 접속 단계용으로 예약되어 있습니다.，거기서 멈추지 마세요）。
	for _, raw := range []string{"https://oapi.dingtalk.com/robot/send", "http://10.0.0.9/hook"} {
		if err := validateHTTPURL(raw); err != nil {
			t.Errorf("%s 인증을 통과해야 합니다.: %v", raw, err)
		}
	}
}

// TestValidateHTTPURLErrorNeverLeaksCredentials 지난 라운드에서 놓친 것이 감사에서 지적한 지점입니다.。
//
// url.Parse **실패**이(가) 반환됩니다. *url.Error，그래요 Error() 전체 원본 주소가 포함되어 있습니다.。마지막 라운드 나만
// 둔감함 http.Client.Do 반환 오류，그리워지는 곳；그리고 그때 만들어낸거였어「영구 실패 경로」사용 사례
// （file://、gopher://、ftp://）사실 다 그럴 수도 있어요. url.Parse 성공적으로 구문 분석되었습니다.、나갑니다 scheme 지점，
// 그러므로 완전히 녹색이라고 해서 이 길이 안전하다는 것을 증명하는 것은 아닙니다.——허위보증입니다。
func TestValidateHTTPURLErrorNeverLeaksCredentials(t *testing.T) {
	cases := []string{
		"http://127.0.0.1/%zz?access_token=" + leakProbeToken,         // 잘못된 백분율 기호 이스케이프
		"https://a.example.com:port/x?access_token=" + leakProbeToken, // 포트가 숫자가 아닙니다.
		"http://[::1?access_token=" + leakProbeToken,                  // 대괄호가 일치하지 않습니다.
	}
	for _, raw := range cases {
		// 입력한 내용을 먼저 확인하세요.**과연**하자 url.Parse 실패。이 단계를 수행하지 않으면，사용 사례는 다음과 같을 수 있습니다.
		// 눈치채지 못한 채 다른 지점으로 걸어갔다（마지막 허위보증은 이렇게 됐다.）。
		if _, err := url.Parse(raw); err == nil {
			t.Errorf("%q 가정된 구문 분석에 실패했습니다.，그렇지 않으면 이 사용 사례는 대상 분기를 다루지 않습니다.", raw)
			continue
		}
		err := validateHTTPURL(raw)
		if err == nil {
			t.Errorf("%q 인증 실패", raw)
			continue
		}
		assertNoSecret(t, err.Error(), leakProbeToken)
	}
	// 채널레벨 패키징에서 주소가 빼지지 않은 것으로 확인되었습니다.。
	t.Setenv(AllowLocalTargetsEnv, "")
	err := (dingTalkChannel{}).Validate(map[string]any{"webhook": cases[0]})
	if err == nil {
		t.Fatal("불법 주소 확인 실패")
	}
	assertNoSecret(t, err.Error(), leakProbeToken)
}

// TestEmailDialGuardRejectsLoopbackByDefault 재정의 SMTP 채널 다이얼 가드。
//
// 예전에는 알몸이었던 이메일 채널 net.Dialer，은 완전한 세트입니다 SSRF 보호의 유일한 공백：host 작성하세요
// 169.254.169.254 또는 127.0.0.1 직접 연결 가능，그리고 smtp.NewClient 핸드셰이크가 실패하면
// 피어가 반환한 줄에 오류가 있습니다.、 last_error 배송 내역 인터페이스에 반영됨——그냥 다른 채널
// 세미블라인드 읽기 프리미티브가 꺼졌습니다.；「연결이 거부되었습니다. vs 시간 초과」의 시간이 많이 걸리는 차이를 사용하여 포트를 감지할 수도 있습니다.。
//
// 이 패키지 TestMain 전역적으로 사용 설정됨 AllowLocalTargetsEnv（다양한 활용 사례에 사용 127.0.0.1 에
// 가짜 수신 종료），따라서 이 사용 사례에서는 자체적으로 삭제해야 합니다.——그렇지 않으면 경비원 유무에 관계없이 통과됩니다.，
// 애초에 어떤 테스트에서도 그 격차가 발견되지 않은 이유가 바로 이것이다.。
func TestEmailDialGuardRejectsLoopbackByDefault(t *testing.T) {
	f := newFakeSMTP(t)
	cfg := emailCfg(t, f, nil)

	t.Setenv(AllowLocalTargetsEnv, "") // 비상구를 닫으세요 = 기본 동작
	_, err := (emailChannel{}).Send(context.Background(), cfg, singleMsg())
	if err == nil {
		t.Fatal("루프백 주소로의 메일 배달은 기본적으로 허용되지 않습니다.")
	}
	// 연결이 전혀 설정되어서는 안됩니다.：경비원이 왔습니다 Control 후크에 막혔습니다.，EHLO 절대 보낼 수 없습니다.。
	if f.sawCommand("EHLO") || f.sawCommand("HELO") {
		t.Fatal("SMTP 세션이 개설되었습니다——가드가 작동하지 않습니다.")
	}
	// 오류 메시지는 사용자에게 취소 방법을 안내할 수 있어야 합니다.（본 기기 postfix 릴레이는 합법적인 구성입니다.）。
	if !strings.Contains(err.Error(), AllowLocalTargetsEnv) {
		t.Errorf("거부 메시지에는 명시적으로 해제하는 방법이 설명되어야 합니다.: %v", err)
	}
}

// TestEmailDialGuardAllowsLoopbackWhenOptedIn 은 페어링의 반대 사용 사례입니다.：명시적으로 연 후
// 정상적으로 배송이 가능해야 합니다.。자체 구축된 인트라넷 SMTP / 네이티브 릴레이는 매우 일반적인 배포입니다.，한 가지 크기로 모든 것을 보호할 수는 없습니다.。
func TestEmailDialGuardAllowsLoopbackWhenOptedIn(t *testing.T) {
	f := newFakeSMTP(t)
	cfg := emailCfg(t, f, nil)

	t.Setenv(AllowLocalTargetsEnv, "1")
	if _, err := (emailChannel{}).Send(context.Background(), cfg, singleMsg()); err != nil {
		t.Fatalf("머신을 명시적으로 해제한 후 SMTP 배달 가능해야 함: %v", err)
	}
	if !f.sawCommand("EHLO") {
		t.Fatal("보이지 않음 EHLO——세션이 실제로 설정되지 않았습니다.")
	}
}
