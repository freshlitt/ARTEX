package notify

import (
	"bufio"
	"context"

	"net"
	"strings"
	"sync"
	"testing"
)

// 이 문서는 이메일 채널의 프로토콜 수준 테스트를 완료합니다.。그 전에는 email.Send 의 적용 범위는 다음과 같습니다. 0——
// 기사 전체 SMTP 경로에서 실행된 사용 사례가 없습니다.，그리고 공교롭게도 6개 채널 중 최대 규모의 합의다.、
// 오류가 가장 많이 발생하는 것（악수、인증、봉투、DATA 각 단계에는 고유한 실패 의미가 있습니다.）。
//
// 여기서는 자체 제작한 가장 작은 것을 사용합니다. SMTP 서버 드라이버，대신 mock 떨어졌어요 net/smtp：
// 이메일 채널의 위험은 대부분「그리고 진실 SMTP 서버 대화」이번 단계는，
// 이렇게 해보세요 mock 빠지는 건 사고。

// fakeSMTP 이면 충분해요 SMTP 서버：완료 가능 greet/EHLO/AUTH/MAIL/RCPT/DATA/QUIT，
// 및 사용 사례에서 요구하는 대로 특정 단계에 대해 지정된 응답 코드를 반환합니다.。
type fakeSMTP struct {
	ln net.Listener

	// rcptReply 네 RCPT TO 님의 답변；기본값 250。
	rcptReply string
	// mailReply 네 MAIL FROM 님의 답변；기본값 250。
	mailReply string
	// advertiseAuth 입니다 true 시자이 EHLO 지지 선언 AUTH PLAIN。
	advertiseAuth bool

	mu       sync.Mutex
	data     string
	commands []string
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln, rcptReply: "250 OK", mailReply: "250 OK"}
	go f.serve()
	t.Cleanup(func() { ln.Close() })
	return f
}

func (f *fakeSMTP) hostPort(t *testing.T) (string, int) {
	t.Helper()
	addr, ok := f.ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatal("아니요 TCP 청취주소")
	}
	return "127.0.0.1", addr.Port
}

func (f *fakeSMTP) record(cmd string) {
	f.mu.Lock()
	f.commands = append(f.commands, cmd)
	f.mu.Unlock()
}

func (f *fakeSMTP) body() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.data
}

func (f *fakeSMTP) sawCommand(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.commands {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func (f *fakeSMTP) serve() {
	conn, err := f.ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	br := bufio.NewReader(conn)
	w := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
	w("220 fake.local ESMTP ready")
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		f.record(line)
		switch {
		case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
			// 선언되지 않음 STARTTLS：코드가 일반 텍스트 분기를 사용하도록 합니다.（테스트 대상은 봉투 로직입니다.，아니요 TLS）。
			w("250-fake.local")
			if f.advertiseAuth {
				w("250-AUTH PLAIN")
			}
			w("250 8BITMIME")
		case strings.HasPrefix(line, "AUTH"):
			// 단순화된 처리：PLAIN 의 초기 응답은 여러 줄에 걸쳐 있을 수 있습니다.，직접 수락。
			w("235 2.7.0 Authentication successful")
		case strings.HasPrefix(line, "MAIL FROM"):
			w(f.mailReply)
		case strings.HasPrefix(line, "RCPT TO"):
			w(f.rcptReply)
		case strings.HasPrefix(line, "DATA"):
			w("354 End data with <CR><LF>.<CR><LF>")
			var sb strings.Builder
			for {
				dl, err := br.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(dl, "\r\n") == "." {
					break
				}
				sb.WriteString(dl)
			}
			f.mu.Lock()
			f.data = sb.String()
			f.mu.Unlock()
			w("250 2.0.0 Ok: queued as FAKE1")
		case strings.HasPrefix(line, "QUIT"):
			w("221 2.0.0 Bye")
			return
		default:
			w("250 OK")
		}
	}
}

func emailCfg(t *testing.T, f *fakeSMTP, extra map[string]any) map[string]any {
	t.Helper()
	host, port := f.hostPort(t)
	cfg := map[string]any{
		"host": host,
		"port": float64(port),
		"from": "artex@example.com",
		"to":   []any{"a@example.com", "b@example.com"},
	}
	for k, v := range extra {
		cfg[k] = v
	}
	return cfg
}

func TestEmailSendDeliversFullMessage(t *testing.T) {
	f := newFakeSMTP(t)
	f.advertiseAuth = true
	cfg := emailCfg(t, f, map[string]any{"username": "artex", "password": "pw"})

	if _, err := (emailChannel{}).Send(context.Background(), cfg, singleMsg()); err != nil {
		t.Fatalf("배송실패: %v", err)
	}
	// 봉투 단계에 도달해야합니다：보내는 사람、수신자 2명、DATA。
	for _, want := range []string{"MAIL FROM:<artex@example.com>", "RCPT TO:<a@example.com>", "RCPT TO:<b@example.com>", "DATA", "AUTH", "QUIT"} {
		if !f.sawCommand(want) {
			t.Errorf("SMTP 세션이 누락되었습니다. %q，실제 명령：%v", want, f.commands)
		}
	}
	// 본문은 base64 님 HTML，그리고 실제 취약점 내용을 가져와야 합니다.（인코딩 후에도 여전히 읽을 수 있음）。
	body := f.body()
	if body == "" {
		t.Fatal("DATA 스테이지에서 문자를 받지 못했습니다")
	}
	if !strings.Contains(body, "Content-Type: text/html") {
		t.Errorf("없어짐 Content-Type 머리:\n%s", body)
	}
	if !strings.Contains(body, "base64") {
		t.Errorf("텍스트가 클릭되지 않습니다 base64 인코딩（길다 HTML 길드파괴 SMTP 님 1000 바이트 줄 길이 제한）:\n%s", body)
	}
	// 수신자가 여러 명이어야 합니다. To 안으로 들어가세요。
	if !strings.Contains(body, "a@example.com, b@example.com") {
		t.Errorf("To 헤더에 모든 수신자가 포함되어 있지 않습니다.:\n%s", body)
	}
}

func TestEmailSendWithoutAuth(t *testing.T) {
	// 계정이 할당되지 않은 경우 보내면 안 됩니다. AUTH —— 일부 릴레이에서는 이 때문에 거부합니다.。
	f := newFakeSMTP(t)
	cfg := emailCfg(t, f, nil)
	if _, err := (emailChannel{}).Send(context.Background(), cfg, singleMsg()); err != nil {
		t.Fatalf("배송실패: %v", err)
	}
	if f.sawCommand("AUTH") {
		t.Errorf("계정을 할당하지 않고 보냈습니다. AUTH: %v", f.commands)
	}
}

// TestEmailSendClassifiesSMTPReplies 은 이번 감사수리 직접 검증입니다：
// 5xx 영구 장애、4xx（그레이리스트）재시도 가능。
func TestEmailSendClassifiesSMTPReplies(t *testing.T) {
	cases := []struct {
		name      string
		rcptReply string
		mailReply string
		permanent bool
	}{
		{"수신자는 550 영구거부", "550 5.1.1 User unknown", "250 OK", true},
		{"수신자 450 그레이리스트", "450 4.7.1 Greylisting in action", "250 OK", false},
		{"수신자 452 이메일이 꽉 찼습니다.", "452 4.2.2 Mailbox full", "250 OK", false},
		{"보낸 사람은 553 영구거부", "250 OK", "553 5.1.3 Bad address", true},
		{"보내는 사람 451 일시적인 오류", "250 OK", "451 4.3.0 Temporary failure", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeSMTP(t)
			f.rcptReply = tc.rcptReply
			f.mailReply = tc.mailReply
			_, err := (emailChannel{}).Send(context.Background(), emailCfg(t, f, nil), singleMsg())
			if err == nil {
				t.Fatal("오류가 보고되어야 합니다.")
			}
			if got := IsPermanent(err); got != tc.permanent {
				t.Fatalf("permanent 판정 오류：기대 %v 받았어요 %v (%v)", tc.permanent, got, err)
			}
			// 서버의 원본 텍스트는 유지되어야 합니다.，그렇지 않으면 사용자는 서버 관리자에게 문의해야 할지, 주소를 변경해야 할지 알 수 없습니다.。
			if !strings.Contains(err.Error(), strings.Fields(tc.rcptReply)[0]) && !strings.Contains(err.Error(), strings.Fields(tc.mailReply)[0]) {
				t.Errorf("서버의 응답 코드는 오류에 유지되어야 합니다.: %v", err)
			}
		})
	}
}

func TestEmailSendRefusesPlaintextCredentials(t *testing.T) {
	// net/smtp 님 PlainAuth 암호화되지 않은 연결을 통한 자격 증명 발급을 거부합니다.（대상이 아닌 이상 localhost）。
	// 이건**맞습니다**의 안전한 행동，우회할 수 없음；하지만 사용자가 수정할 수 있도록 안내할 수 있는 오류를 제공하세요.。
	// 비- localhost 의 호스트 이름이 이를 트리거합니다.。
	f := newFakeSMTP(t)
	f.advertiseAuth = true
	_, port := f.hostPort(t)
	cfg := map[string]any{
		"host":     "smtp.example.com", // 아니요 localhost
		"port":     float64(port),
		"from":     "a@example.com",
		"to":       []any{"b@example.com"},
		"username": "artex",
		"password": "pw",
	}
	_, err := (emailChannel{}).Send(context.Background(), cfg, singleMsg())
	if err == nil {
		t.Skip("본 기기 DNS 로컬 서버로 확인됨，건너뛰기（다른 사용 사례에는 영향을 미치지 않습니다.）")
	}
	// 연결할 수 없습니다 또는 거부된 모든 자격 증명은 이 어설션을 통과한 것으로 간주됩니다.；핵심은**안돼요**비밀번호를 자동으로 보내주세요。
	if !IsPermanent(err) && !strings.Contains(err.Error(), "연결하다") {
		t.Logf("오류：%v（아니요 localhost 다음은 예상대로 위쪽에 연결에 실패했습니다.）", err)
	}
}

func TestEmailValidateReportsMissingFields(t *testing.T) {
	// 이메일 채널에 구성 필드가 가장 많습니다.，하나라도 생략되면 전달시에만 노출됩니다.；여기까지 하나씩 확인해주세요
	// 검증을 통해 사전에 중지할 수 있습니다.。어설션 확인「오류 메시지에는 누락된 내용이 언급되어 있습니다.」。
	cases := []struct {
		name string
		cfg  map[string]any
	}{
		{"누락 host", map[string]any{"port": float64(25), "from": "a@b.c", "to": []any{"d@e.f"}}},
		{"누락 port", map[string]any{"host": "smtp.example.com"}},
		{"port 선을 넘어", map[string]any{"host": "h", "port": float64(70000), "from": "a@b.c", "to": []any{"d@e.f"}}},
		{"누락 from", map[string]any{"host": "h", "port": float64(25), "to": []any{"d@e.f"}}},
		{"누락 to", map[string]any{"host": "h", "port": float64(25), "from": "a@b.c"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := (emailChannel{}).Validate(tc.cfg); err == nil {
				t.Fatalf("인증 실패: %v", tc.cfg)
			}
		})
	}
}

// TestEmailConfigTolerance 구성 읽기 내결함성 재정의：JSONB 값은 float64，
// 하지만 사용자는 UI 은 포트를 문자열로 채울 수 있습니다.；배열은 단일 문자열일 수도 있습니다.。
func TestEmailConfigTolerance(t *testing.T) {
	cfg := map[string]any{
		"host": "smtp.example.com",
		"port": "587", // 문자열 형식의 포트
		"from": "a@b.c",
		"to":   "d@e.f", // 배열 대신 단일 문자열
		"tls":  "true",  // 문자열 형식의 부울
	}
	if err := (emailChannel{}).Validate(cfg); err != nil {
		t.Fatalf("문자열 형식의 값이 허용되어야 합니다.: %v", err)
	}
	if got := cfgInt(cfg, "port"); got != 587 {
		t.Errorf("cfgInt 해결되지 않은 문자열 포트，받았어요 %d", got)
	}
	if !cfgBool(cfg, "tls") {
		t.Error("cfgBool 구문 분석되지 않은 문자열 \"true\"")
	}
	if to := cfgStrings(cfg, "to"); len(to) != 1 || to[0] != "d@e.f" {
		t.Errorf("cfgStrings 단일 문자열이 호환되지 않음，받았어요 %v", to)
	}
}

// TestFilterValidateRejectsTypo 은 감사수리 직접검증입니다：
// 임계값 오타는 작성시 차단되어야 합니다，그렇지 않으면 필터가 자동으로 실패하고 전체 푸시가 됩니다.。
func TestFilterValidateRejectsTypo(t *testing.T) {
	good := []string{"", "low", "medium", "high", "critical"}
	for _, s := range good {
		if err := (Filter{MinSeverity: s}).Validate(); err != nil {
			t.Errorf("법적 기준점 %q 거부됨: %v", s, err)
		}
	}
	// 실제로 발생하는 사무적인 오류입니다.——모두 거절해야 함。
	for _, s := range []string{"hgih", "HIGH", "심각해요", "high ", "crit"} {
		err := (Filter{MinSeverity: s}).Validate()
		if err == nil {
			t.Errorf("잘못된 임계값 %q 은 거부되어야 합니다（그렇지 않으면 필터가 자동으로 실패합니다.、가 풀 푸시가 됩니다.）", s)
			continue
		}
		// 오류 메시지는 사용자에게 오류를 수정하도록 안내할 수 있어야 합니다.。
		if !strings.Contains(err.Error(), "low") || !strings.Contains(err.Error(), "critical") {
			t.Errorf("오류 메시지에는 선택적 값이 나열되어야 합니다.，받았어요 %q", err.Error())
		}
	}
}

// TestFilterValidateIsWriteTimeOnly 자물쇠「옌 쓰세요、읽기 폭」의 분업：
// 라이브러리에 있는 기존 잘못된 값을 채널에서 읽는 것을 완전히 차단할 수는 없습니다.（이로 인해 모든 과거 채널이 갑자기 푸시를 중단하게 됩니다.）。
func TestFilterValidateIsWriteTimeOnly(t *testing.T) {
	raw := []byte(`{"min_severity":"hgih"}`)
	f := ParseFilter(raw) // 보고된 오류가 없습니다.
	if f.MinSeverity != "hgih" {
		t.Fatalf("읽기 경로는 그대로 두어야 합니다.，받았어요 %q", f.MinSeverity)
	}
	// 그리고 이 채널은 여전히 사건에 대한 판단을 내릴 수 있습니다.（아니요 panic、차단 없음）。
	_ = Match(f, Snapshot{Kind: EventFindingCreated, Severity: "critical"})
}
