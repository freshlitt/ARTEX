package notify

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// emailDialTimeout / emailSessionTimeout 연결과 전체 세그먼트를 각각 제한 SMTP 대화。
// net/smtp 자체에는 시간 초과 메커니즘이 없습니다.，이 두 줄이 제공되지 않는 경우，정체된 피어로 인해
// 배송 goroutine 거기 영원히 매달려있어——그리고 dispatcher 예 goroutine 직렬 처리，
// 전체 알림 시스템이 종료된다는 의미입니다.。
const (
	emailDialTimeout    = 10 * time.Second
	emailSessionTimeout = 45 * time.Second
)

// emailChannel 구현 SMTP 이메일 전달。
type emailChannel struct{}

func (emailChannel) Kind() string { return KindEmail }

// 이메일에는 플랫폼 제한이 없습니다.，하지만 화면을 새로 고치는 용도로 사용하면 안 됩니다.；느슨한 기본값 제공。
func (emailChannel) DefaultRatePerMin() int { return 60 }

// 비밀번호만 마스크。SMTP 호스트、계정、받는 사람 중 비밀이 없습니다.，마스크하면 편집이 더 번거로워질 뿐입니다.。
func (emailChannel) SecretKeys() []string { return []string{"password"} }

// host/port 어느 서버에 비밀번호를 부여할지 결정하세요.；tls 전송 암호화 여부를 결정합니다.。세 가지에 변화가 있으면
// 비밀번호 재설정 요청——그런데「꺼지세요 TLS」이 단계에서는 자격 증명을 명시적으로 가져와야 합니다.，그냥 바꾸는 것보다。
func (emailChannel) DestinationKeys() []string { return []string{"host", "port", "tls"} }

func (emailChannel) Validate(cfg map[string]any) error {
	if cfgString(cfg, "host") == "" {
		return errors.New("없어짐 SMTP 서버 주소")
	}
	port := cfgInt(cfg, "port")
	if port <= 0 || port > 65535 {
		return errors.New("SMTP 잘못된 포트（이어야 합니다. 1-65535）")
	}
	if cfgString(cfg, "from") == "" {
		return errors.New("보낸 사람 주소가 누락되었습니다.")
	}
	if len(cfgStrings(cfg, "to")) == 0 {
		return errors.New("수신자 주소가 하나 이상 필요합니다.")
	}
	return nil
}

func (c emailChannel) Send(ctx context.Context, cfg map[string]any, m Message) (int, error) {
	if err := c.Validate(cfg); err != nil {
		return 0, Permanent(err)
	}
	host := cfgString(cfg, "host")
	port := cfgInt(cfg, "port")
	from := cfgString(cfg, "from")
	to := cfgStrings(cfg, "to")
	username := cfgString(cfg, "username")
	password := cfgString(cfg, "password")
	implicitTLS := cfgBool(cfg, "tls")

	msg, err := buildEmailMessage(from, to, m)
	if err != nil {
		return 0, Permanent(err)
	}

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	client, err := emailDial(ctx, addr, host, implicitTLS)
	if err != nil {
		return 0, err
	}
	defer client.Close()

	// STARTTLS：상대방이 지원하면 업그레이드하세요.。일반 텍스트 세션에서는 자격 증명을 보낼 수 없습니다.（아래 참조 auth 설명）。
	if !implicitTLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
				return 0, fmt.Errorf("STARTTLS 실패: %w", err)
			}
		}
	}
	if username != "" {
		if err := client.Auth(smtp.PlainAuth("", username, password, host)); err != nil {
			// smtp.PlainAuth 은 암호화되지 않은 연결을 통한 자격 증명 전송을 거부합니다.（대상이 아닌 이상 localhost）。
			// 이건**맞습니다**의 안전한 행동，우회 불가，하지만 그 이유를 명확히 번역해야 합니다.——
			// 그렇지 않으면 사용자는「unencrypted connection」어떻게 해야할지 모르겠어요。
			if strings.Contains(err.Error(), "unencrypted connection") {
				return 0, Permanent(fmt.Errorf("자격 증명이 거부되었습니다.：연결이 암호화되지 않았습니다.。활성화해 주세요 TLS，또는 대신 사용 465 포트(암시적 TLS)，또는 넣어「활성화 TLS」연결 (%w)", err))
			}
			return 0, Permanent(fmt.Errorf("SMTP 인증 실패: %w", err))
		}
	}
	if err := client.Mail(from); err != nil {
		return 0, smtpStageError(fmt.Sprintf("보내는 사람 %s 거부됨", from), err)
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return 0, smtpStageError(fmt.Sprintf("수신자 %s 거부됨", rcpt), err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return 0, fmt.Errorf("SMTP DATA 실패: %w", err)
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return 0, fmt.Errorf("이메일 본문 작성 실패: %w", err)
	}
	if err := w.Close(); err != nil {
		return 0, fmt.Errorf("이메일 제출 실패: %w", err)
	}
	// Quit 실패는 영향을 미치지 않습니다「서버에서 이메일을 받았습니다.」이 사실은，그러니 오류를 무시하세요.。
	_ = client.Quit()
	// 이메일이 잘리지 않습니다（HTML 전문을 보내주세요），전체 배치가 배송된 것으로 간주됩니다.。
	return len(m.Items), nil
}

// emailDial 생성 SMTP 연결하다。
//
// implicitTLS=true 가자 465 이런「바로 연결됨 TLS」방법；false 가자 25/587 연결을 명확하게 한 후
// STARTTLS。이 둘은 섞일 수 없습니다.：예 465 항만발명기사 greeting 바로 연결이 끊어집니다.。
//
// 세션이 만료됩니다.**연락사무소**그냥 설정하세요（고민보다는），왜냐하면 net/smtp 님 Client 맨 아래층을 넣어주세요
// 내보내지 않은 필드에 연결이 숨겨져 있습니다.，외부에서는 못받음；일단 연결이 넘겨지면 프리셋에만 의존할 수 있습니다. deadline
// 사실대로 말해주세요。이는 핸드셰이크 단계 중 차단에도 적용됩니다.。
// Control 끊으세요 blockInternalDial 그리고 HTTP 부서 채널은 동일한 가드를 공유합니다.。전화를 끊지 않으면 SMTP
// 이게 전부에요 SSRF 보호 공백：host 작성하세요 169.254.169.254 또는 127.0.0.1 직접 연결 가능，
// 그리고 smtp.NewClient Handshake가 실패하면 Peer가 반환한 라인이 오류에 포함됩니다.、 last_error
// 배송 내역 인터페이스에 반영됨，은 반맹검 읽기 프리미티브를 구성합니다.；「연결이 거부되었습니다. vs 시간 초과」의 시간 소모적인 차이는 여전히
// 은 포트를 감지하는 데 사용됩니다.。다이얼링 단계가 최종 유효 지점입니다.，도 포함됨 DNS 다시 바인딩。
func emailDial(ctx context.Context, addr, host string, implicitTLS bool) (*smtp.Client, error) {
	d := &net.Dialer{Timeout: emailDialTimeout, Control: blockInternalDial}
	var conn net.Conn
	var err error
	if implicitTLS {
		conn, err = tls.DialWithDialer(d, "tcp", addr, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("연결하다 SMTP 서버 실패: %w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(emailSessionTimeout))
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("SMTP 핸드셰이크 실패: %w", err)
	}
	return client, nil
}

// smtpStageError 언론 SMTP 응답 코드는 특정 단계의 실패를 다음과 같이 구분합니다.「다시 시도할 수 있습니다」그리고「영구 장애」。
//
// 왜 구별해야 할까요?：SMTP 님 4xx 그리고 5xx 의미가 완전히 다릅니다——
//   - 4xx（450 그레이리스트、451 로컬 오류、452 저장공간 부족）네**임시**거부，
//     일반적인 접근 방식은 나중에 다시 시도하는 것입니다.；특히 그레이리스트는요，거의 모든 첫 배송이 발생합니다.。
//   - 5xx（550 사용자가 존재하지 않습니다.、553 잘못된 주소입니다.）이 영구적으로 거부되었습니다.，다시 해봐도 소용없어。
//
// 항상 영구적인 실패라면，그레이리스팅이 활성화된 메일 서버는 다음을 허용합니다.**여러분 모두**푸시는 처음이에요
// 시도하다가 빠졌어요 failed——그리고 이런 종류의 실패는 바로 자동 재시도가 가장 효과적인 시나리오입니다.。
// 응답 코드는 오류 텍스트의 처음 세 자리를 사용합니다.；코드를 얻을 수 없을 때 다시 시도하려면 누르세요.（한 번 더 해보고 싶어요，
// 분석할 수 없다는 이유만으로 일시적인 오류가 발생할 수 있다고 판단하지 마세요.）。
func smtpStageError(what string, err error) error {
	code := smtpReplyCode(err.Error())
	if code >= 500 && code < 600 {
		return Permanent(fmt.Errorf("%s: %w", what, err))
	}
	return fmt.Errorf("%s: %w", what, err)
}

// smtpReplyCode 님으로부터 SMTP 오류 텍스트에서 앞의 3자리 응답 코드를 가져옵니다.，반품을 검색할 수 없습니다. 0。
// net/smtp 오류 코드 필드를 내보내지 마십시오.，은 텍스트에서만 가져올 수 있습니다.；형식은 다음과 같습니다.「450 4.7.1 ...」。
func smtpReplyCode(text string) int {
	if len(text) < 3 {
		return 0
	}
	n, err := strconv.Atoi(text[:3])
	if err != nil {
		return 0
	}
	return n
}

// buildEmailMessage 조립완료 RFC 5322 이메일。
//
// 텍스트용 base64 인코딩을 하는 이유는 두 가지가 있습니다.：먼저 SMTP 한 줄을 초과하지 않도록 규정되어 있습니다. 1000 바이트，그리고 HTML
// 문자（특별 요약 이메일）매우 긴 줄이 쉽게 나타날 수 있습니다.；두 번째는 base64 당연히 없겠죠 "." 로 시작
// 행，생략 SMTP 점 이스케이프 문제。
func buildEmailMessage(from string, to []string, m Message) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(to, ", "))
	// 중국어 테마를 꼭 하셔야 합니다 RFC 2047 인코딩，그렇지 않으면 클라이언트에서 잘못된 문자로 표시됩니다.。
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", htmlTitle(m)))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n")
	// 이메일 길이에는 엄격한 제한이 없습니다.，따라서 텍스트가 잘리지 않습니다.。
	b.WriteString("\r\n")
	encoded := base64.StdEncoding.EncodeToString([]byte(htmlBody(m, 0)))
	// base64 언론 76 문자 감싸기，준수 RFC 2045。
	for len(encoded) > 76 {
		b.WriteString(encoded[:76] + "\r\n")
		encoded = encoded[76:]
	}
	b.WriteString(encoded + "\r\n")
	return b.String(), nil
}
