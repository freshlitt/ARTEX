package db

import (
	"regexp"
	"testing"
)

// 내장「클래스 인터페이스 경로 삭제」규칙이 전체와 일치합니다. tool_input JSON 문자열，따라서 사용 사례는 다음과 같이 직접 시작됩니다.
// JSON 양식이 주어졌습니다，그리고 Interceptor 실제로 얻은 것 subject 일관됨。
func TestDeleteEndpointPathPattern(t *testing.T) {
	re := regexp.MustCompile(deleteEndpointPathPattern)

	hit := []string{
		`{"command":"curl -s 'http://t.com/api/user/delete?id=1'"}`,    // GET 삭제 인터페이스 열기
		`{"command":"curl -X POST http://t.com/admin/delete -d id=1"}`, // POST 삭제 인터페이스 열기
		`{"command":"curl 'http://t.com/api/deleteAll'"}`,
		`{"command":"curl 'http://t.com/api/delete_user?id=1'"}`,
		`{"command":"curl 'http://t.com/api/delete-user?id=1'"}`,
		`{"url":"http://t.com/api/remove?id=1"}`,
		`{"command":"curl http://t.com/files/unlink/3"}`,
		`{"command":"curl http://t.com/api/del?id=2"}`,
		`{"command":"curl -X POST http://t/v1/erase"}`,
		`{"command":"curl http://t/admin/destroyAll"}`, // v1 에 대한 경로 규칙은 접미사를 허용하지 않습니다.，여기에 추가하세요
	}
	for _, s := range hit {
		if !re.MatchString(s) {
			t.Errorf("답변은 했으나 놓음: %s", s)
		}
	}

	// 동사 뒤에는 구분 기호가 와야 합니다.，피하세요 /delivery、/details 이 유형의 읽기 전용 경로는 실수로 차단되었습니다.。
	miss := []string{
		`{"command":"curl 'http://t.com/api/delivery?id=1'"}`,
		`{"command":"curl 'http://t.com/order/details'"}`,
		`{"command":"curl 'http://t.com/api/delta/sync'"}`,
		`{"command":"curl 'http://t.com/user/delegate'"}`,
		`{"command":"curl 'http://delete.example.com/'"}`, // 삭제 동사가 경로 대신 도메인 이름에 나타납니다.
		`{"command":"curl 'http://t.com/remote/status'"}`,
		`{"command":"nmap -p80 10.0.0.1"}`,
	}
	for _, s := range miss {
		if re.MatchString(s) {
			t.Errorf("실수로 차단됨: %s", s)
		}
	}
}
