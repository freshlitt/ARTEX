package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// 이 문서에는 다음이 포함됩니다. doJSON 님 HTTP 레이어 오류 분류。
//
// 왜 따로 테스트를 해야 하나요?：각 채널 어댑터는 플랫폼 자체의 비즈니스 오류 코드에만 관심이 있습니다.（딩톡 errcode、
// 페이슈 code、Telegram ok 필드），그리고**HTTP 레이어**평가됨 doJSON 일제히 제작，
// 둘은 독립적인 방어선이다.。이게 빠졌네요，원 리턴 503 의 전송 게이트웨이는 영구 오류로 처리됩니다.、
// 포기하고 다시 해보세요；그리고 하나 403 은 재시도 가능한 것으로 처리됩니다.、세 차례의 후퇴도 헛되다。

func replyServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDoJSONClassifiesHTTPStatus(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		permanent bool
	}{
		{"200 성공은 오류가 아니다", 200, false},
		{"429 현재 제한을 다시 시도할 수 있습니다.", 429, false},
		{"408 요청 시간이 초과되어 다시 시도할 수 있습니다.", 408, false},
		{"500 서버 오류가 발생하면 다시 시도할 수 있습니다.", 500, false},
		{"502 게이트웨이 오류를 다시 시도할 수 있습니다.", 502, false},
		{"503 서비스를 사용할 수 없으며 다시 시도할 수 있습니다.", 503, false},
		{"400 매개변수 오류 영구 실패", 400, true},
		{"401 인증에 영구적으로 실패했습니다.", 401, true},
		{"403 접속거부 영구실패", 403, true},
		{"404 주소가 존재하지 않습니다. 영구적인 실패.", 404, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := replyServer(t, tc.status, `{"detail":"upstream says no"}`)
			_, err := doJSON(context.Background(), "GET", srv.URL, nil, nil)
			if tc.status < 300 {
				if err != nil {
					t.Fatalf("2xx 오류가 보고되지 않아야 합니다.: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("아니요 2xx 오류가 보고되어야 합니다.")
			}
			if got := IsPermanent(err); got != tc.permanent {
				t.Fatalf("HTTP %d 님 permanent 판정 오류：기대 %v 받았어요 %v (%v)",
					tc.status, tc.permanent, got, err)
			}
			// 오류에 상태 코드가 나타나야 합니다.，그렇지 않으면 사용자는 자신이 구성을 잘못했는지 아니면 상대방이 전화를 끊었는지 확인할 수 없습니다.。
			// 대신 숫자를 주장 Go 영어로 StatusText：이 패키지의 카피라이팅은 중국어로 되어 있습니다.
			// （프로젝트의 다른 부분과 일치함），숫자는 언어에 독립적입니다.、Assertion을 안정시킬 수 있는 부분。
			if !strings.Contains(err.Error(), strconv.Itoa(tc.status)) {
				t.Errorf("오류 메시지가 나타나야 합니다. HTTP 상태 코드 %d，받았어요 %v", tc.status, err)
			}
		})
	}
}

// TestDoJSONIncludesResponseSnippet 재정의 snippet：피어가 반환한 오류 설명을 다시 가져와야 합니다.，
// 그렇지 않으면 사용자만 알 수 있습니다.「실패」，상대방이 왜 거절했는지 모르겠습니다.。
func TestDoJSONIncludesResponseSnippet(t *testing.T) {
	srv := replyServer(t, 400, `{"error":"invalid webhook token"}`)
	_, err := doJSON(context.Background(), "GET", srv.URL, nil, nil)
	if err == nil {
		t.Fatal("오류가 보고되어야 합니다.")
	}
	if !strings.Contains(err.Error(), "invalid webhook token") {
		t.Errorf("오류 메시지는 피어로부터 지침을 가져와야 합니다.，받았어요 %v", err)
	}
}

// TestDoJSONSnippetIsSingleLineAndBounded 제약 snippet 양식：
// 끝까지 응답이 그대로 입력됩니다. last_error 열 및 프런트엔드 테이블，여러 줄/너무 길면 조판과 로딩이 망가집니다。
func TestDoJSONSnippetIsSingleLineAndBounded(t *testing.T) {
	// 줄 바꿈 포함、탭 및 5000 지나치게 긴 문자 내용에 대한 대응。
	long := strings.Repeat("x", 5000)
	srv := replyServer(t, 500, "line1\nline2\r\n\tline3 "+long)
	_, err := doJSON(context.Background(), "GET", srv.URL, nil, nil)
	if err == nil {
		t.Fatal("오류가 보고되어야 합니다.")
	}
	msg := err.Error()
	if strings.ContainsAny(msg, "\r\n\t") {
		t.Errorf("오류 메시지는 한 줄로 압축되어야 합니다.，받았어요 %q", msg)
	}
	// snippet 상한 200 문자 + 고정 접두사，총액은 원래 응답보다 훨씬 적어야 합니다.。
	if len(msg) > 400 {
		t.Errorf("오류 메시지가 너무 깁니다.（%d 바이트），이어야 합니다. snippet 잘림: %q", len(msg), msg)
	}
}

// TestDoJSONRejectsOversizedResponse 읽기 상한선이 있는지 확인：피어가 비정상적으로 큰 콘텐츠를 반환하는 경우
// 전체 응답을 메모리로 읽을 수 없습니다.（배송 내역의 각 항목에 대한 사본이 저장됩니다. last_error）。
func TestDoJSONRejectsOversizedResponse(t *testing.T) {
	huge := strings.Repeat("A", 1<<20) // 1 MiB
	srv := replyServer(t, 400, huge)
	_, err := doJSON(context.Background(), "GET", srv.URL, nil, nil)
	if err == nil {
		t.Fatal("오류가 보고되어야 합니다.")
	}
	if len(err.Error()) > 400 {
		t.Errorf("너무 큰 응답은 길이를 제한하고 잘라서 읽어야 합니다.，오류 메시지 길이 %d", len(err.Error()))
	}
}
