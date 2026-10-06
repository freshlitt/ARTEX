package notify

import (
	"net/url"
	"testing"
	"time"
)

// 서명 참조 값은 다음과 같습니다. OpenSSL 독립적으로 계산됨，은 이 패키지의 자체 구현에 의해 생성되지 않습니다.——
// 그렇지 않으면 증명만 가능「코드가 변경되지 않았습니다」，증명할 수 없음「알고리즘 쌍」。
//
//	TS=1700000000000, SECRET=SECtest123
//	딩톡: printf '%s\n%s' "$TS" "$SECRET" | openssl dgst -sha256 -hmac "$SECRET" -binary | openssl base64 -A
//	      -> w3RMHXzixTMdzr8OHJUmVLS4IoPJVdu+Ut1LE48MePE=
//	페이슈: printf '' | openssl dgst -sha256 -hmac "$(printf '%s\n%s' "$TS" "$SECRET")" -binary | openssl base64 -A
//	      -> Hd4xFWQU6R6ad4nzy4ETIznzlqebqH7xcTFVmONTudo=
const (
	signTestTSMillis = int64(1700000000000)
	signTestSecret   = "SECtest123"
	dingTalkExpected = "w3RMHXzixTMdzr8OHJUmVLS4IoPJVdu+Ut1LE48MePE="
	feishuExpected   = "Hd4xFWQU6R6ad4nzy4ETIznzlqebqH7xcTFVmONTudo="
)

func TestDingTalkSignMatchesReference(t *testing.T) {
	got, err := dingTalkSignedURL("https://oapi.dingtalk.com/robot/send?access_token=tok", signTestSecret, time.UnixMilli(signTestTSMillis))
	if err != nil {
		t.Fatalf("서명 실패: %v", err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("출력 주소를 확인할 수 없습니다.: %v", err)
	}
	q := u.Query()
	if q.Get("sign") != dingTalkExpected {
		t.Errorf("서명이 일치하지 않습니다.\n기대 %s\n받았어요 %s", dingTalkExpected, q.Get("sign"))
	}
	if q.Get("timestamp") != "1700000000000" {
		t.Errorf("타임스탬프는 밀리초 단위여야 하며 있는 그대로 가져와야 합니다.，받았어요 %q", q.Get("timestamp"))
	}
	// 원본 query 매개변수（access_token）은 서명으로 덮어쓸 수 없습니다.。
	if q.Get("access_token") != "tok" {
		t.Errorf("원본 query 매개변수가 누락되었습니다.，받았어요 %q", q.Get("access_token"))
	}
}

func TestFeishuSignMatchesReference(t *testing.T) {
	got := feishuSign("1700000000000", signTestSecret)
	if got != feishuExpected {
		t.Errorf("서명이 일치하지 않습니다.\n기대 %s\n받았어요 %s", feishuExpected, got)
	}
}

// TestSignAlgorithmsDiffer 두 회사의 알고리즘 차이를 잠그세요。우연히 서로의 파라메타 순서가 되었네요
// （딩톡 key=secret，페이슈 key=서명할 문자열），타사에서 복사한 경우 인증에 실패합니다.，
// 이 사용 사례는 향후 리팩토링에서 두 가지가 동일한 함수로 병합되지 않도록 보장합니다.。
func TestSignAlgorithmsDiffer(t *testing.T) {
	ts := "1700000000000"
	dingURL, err := dingTalkSignedURL("https://example.com/hook", signTestSecret, time.UnixMilli(signTestTSMillis))
	if err != nil {
		t.Fatal(err)
	}
	dq, _ := url.Parse(dingURL)
	if dq.Query().Get("sign") == feishuSign(ts, signTestSecret) {
		t.Fatal("DingTalk와 Feishu의 서명이 동일합니다.，알고리즘 구현 중 하나가 잘못되었음을 의미합니다.")
	}
}

func TestDingTalkNoSecretLeavesURLUntouched(t *testing.T) {
	// 서명을 추가하는 로봇이 활성화되지 않았습니다.：허공에서 추가할 수 없습니다. timestamp/sign 매개변수。
	const hook = "https://oapi.dingtalk.com/robot/send?access_token=tok"
	got, err := dingTalkSignedURL(hook, "", time.UnixMilli(signTestTSMillis))
	if err != nil {
		t.Fatal(err)
	}
	if got != hook {
		t.Fatalf("구성되지 않음 secret 일 때 주소를 변경하면 안 됩니다.，받았어요 %q", got)
	}
}

func TestValidateHTTPURL(t *testing.T) {
	ok := []string{"https://example.com/hook", "http://10.0.0.1:8080/x?y=1"}
	for _, s := range ok {
		if err := validateHTTPURL(s); err != nil {
			t.Errorf("%q 이 허용되어야 합니다.: %v", s, err)
		}
	}
	// file:// 등은 허용되어서는 안 된다.——http.Client 기대 이상의 처리。
	bad := []string{"", "file:///etc/passwd", "ftp://example.com", "https://", "gopher://x"}
	for _, s := range bad {
		if err := validateHTTPURL(s); err == nil {
			t.Errorf("%q 은 거부되어야 합니다", s)
		}
	}
}
