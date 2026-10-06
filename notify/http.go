package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"
)

// allowLocalTargets 메시지가 루프백으로 전달되도록 허용할지 여부를 결정합니다. / 링크 로컬 주소。
//
// 기본적으로 거부됨。이 주소는 IM 로봇이나 공용 메일 서버가 나타나는 곳，그리고 그들은 할 수 있습니다
// 맞은 건 굉장히 예민해요.：동일한 머신에 있는 다른 서비스의 관리 포트、및 클라우드 환경의 메타데이터 엔드포인트
// （169.254.169.254，인스턴스 자격 증명 읽기）。배송주소는 관리자가 지정합니다.，하지만 하나는 XSS/CSRF
// 빌려온 관리 세션、또는 공유 JWT 님의 두 번째 사람，구성을 변경하면 응답 내용을 다시 읽을 수 있습니다.
// ——doJSON 그럴게요 4xx/5xx 의 응답 본문 200 쓴 바이트 수 last_error，그리고 배송이력 인터페이스
// 이(가) 에코로 출력됩니다.，세미블라인드 리딩 프리미티브입니다.。
//
// 하지만「본 기기 SMTP 릴레이」（127.0.0.1:25 에 postfix）은 자체 작성 이메일의 일반적인 구성입니다.，
// 한 가지 크기로 모든 것에 적합하면 사람들을 함정에 빠뜨릴 것입니다.。그러니 하드코딩된 탈출구 대신 명시적인 탈출구를 남겨두세요.：
// 설정 ARTEX_NOTIFY_ALLOW_LOCAL=1 그거면 된다。
//
// 다음으로 내보내기 AllowLocalTargetsEnv 은 테스트에서 명시적으로 열 수 있도록 하기 위한 것입니다.——이 패키지는 다음과 관련되어 있습니다. server 포함됨
// 사용 사례가 많이 사용됩니다. 127.0.0.1 에 httptest 가짜 수신 종료，열지 않으면 경비병들이 다 막는다.。
const AllowLocalTargetsEnv = "ARTEX_NOTIFY_ALLOW_LOCAL"

func allowLocalTargets() bool {
	v := strings.TrimSpace(os.Getenv(AllowLocalTargetsEnv))
	return v == "1" || strings.EqualFold(v, "true")
}

// isBlockedDialIP 신고 대상 IP 에 속합니까?「배송은 기본적으로 불가합니다」의 주소 세그먼트。
//
// 루프백만 거부、로컬 링크（클라우드 메타데이터가 포함되어 있습니다. 169.254.169.254）、지정되지 않음 및 멀티캐스트。
// **거절하지 마세요** RFC1918 사설망：자체 구축된 인트라넷 Mattermost / SMTP 릴레이는 매우 일반적이고 합법적인 사용법입니다.，
// 모두 차단하면 실제 환경에서는 해당 기능을 바로 사용할 수 없게 됩니다.。의도적인 선택입니다——
// 보호는 매우 민감한 대상을 차단해야 합니다.，동시에 일반 배포를 폐지할 수는 없습니다.。
func isBlockedDialIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	// IPv4-mapped IPv6（::ffff:127.0.0.1）복원하려면 IPv4 재선고，그렇지 않으면 검사가 우회됩니다.。
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

// blockInternalDial 네 http.Transport 다이얼러 Control 후크，에**연결이 완료되면**
// 대상 주소를 확인하세요.。
//
// 구성을 저장할 때만 확인하는 것이 아니라 전화 접속 단계에서 확인하는 이유는 무엇입니까?：이것이 마지막 유효점이다。
// 구성 확인을 동시에 우회하는 두 가지 상황을 다룹니다.——DNS 다시 바인딩（은 확인 중에 공용 네트워크로 확인됩니다. IP、
// 실제로 연결되면 인트라넷으로 해결）및 리디렉션（호스트 간 점프를 거부했지만，그런데 같은 호스트가 점프를 하네요
// 다른 경로를 가리키는 것은 여전히 가능합니다.）。
func blockInternalDial(_, address string, _ syscall.RawConn) error {
	if allowLocalTargets() {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("대상 주소를 확인할 수 없습니다. %q", host)
	}
	if isBlockedDialIP(ip) {
		return fmt.Errorf("이 기계로의 배송을 거부합니다/링크 로컬 주소 %s（꼭 현지 서비스로 전달해야 하는 경우，설정 %s=1）", ip, AllowLocalTargetsEnv)
	}
	return nil
}

// notifyTransport 기본값 Transport 기준으로 다이얼가드 1개만 추가됩니다.。
// 사용 Clone 모든 기본 튜닝을 유지합니다.（연결 풀、HTTP/2、시간 초과、proxy 등），
// 단지 확인을 추가하기 위해 다른 동작을 변경하지 마십시오.。
var notifyTransport = func() *http.Transport {
	t, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Transport{}
	}
	clone := t.Clone()
	clone.DialContext = (&net.Dialer{Timeout: 10 * time.Second, Control: blockInternalDial}).DialContext
	return clone
}()

// httpClient 은 모든 채널 전송에서 공유되는 클라이언트입니다.。
//
// 일부러**아니요**재사용 프로젝트 글로벌 수출 대행（server 쪽 GlobalProxy）：저 에이전트는 침투용이다
// 타겟 트래픽의 경우，종종 불안정한 터널，그리고 알림의 가용성이 대상 네트워크의 지터로 인해 방해받아서는 안 됩니다.。
// IM 바로접속을 밀어주시면 됩니다。시간 초과가 다음으로 설정되었습니다. 15 초——이보다 느린 피어는 실제로 결함이 있는 상태입니다.。
//
// 호스트 간 리디렉션 거부：이 기능의 배송 주소는「고정됨 endpoint」양식，보통은 아님
// 다른 호스트로 리디렉션；그리고 이들 기업의 자격증은（딩딩 access_token、치웨이 key、Telegram 님
// bot token）**바로 거기 URL 내부**，호스트 간 점프를 따르는 것은 자격 증명을 리디렉션 대상에 전달하는 것과 같습니다.。같은 호스트
// 점프（끝에 슬래시를 추가하면）그래도 허용됨。
var httpClient = &http.Client{
	Timeout:   15 * time.Second,
	Transport: notifyTransport,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("리디렉션이 너무 많습니다.")
		}
		if len(via) > 0 && req.URL.Host != via[0].URL.Host {
			return fmt.Errorf("호스트 간 리디렉션 거부（%s → %s）", via[0].URL.Host, req.URL.Host)
		}
		return nil
	},
}

// respBodyLimit 읽기 응답 본문의 크기를 제한합니다.。예외가 발생하면 피어가 매우 큰 콘텐츠를 다시 뱉어낼 수 있습니다.，그리고 우리에게 필요한 것은
// 오류 코드와 간단한 오류 설명은 배송 기록에 표시되는 데 사용됩니다.。
const respBodyLimit = 8 << 10

// doJSON 요청을 보내고 응답 본문을 반환합니다.（길이 제한）。
//
// payload 입니다 nil 은 비어 있는 상태로 전송됩니다. body（ GET 또는 플랫폼에서 필요하지 않음 body 장면）。
// headers 의 키 값은 그대로 추가됩니다.，일반용 Webhook 에 대한 사용자 정의 헤더。
//
// 오류 분류가 이 기능의 핵심 역할입니다.：네트워크 계층 오류 및 5xx/408/429 로 분류됨「다시 시도할 수 있습니다」，
// 나머지는 4xx 로 분류됨「영구 장애」——다시 시도해보세요 403 같은 오류만 브러싱하세요 3 로그를 살펴보세요。
func doJSON(ctx context.Context, method, url string, headers map[string]string, payload any) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			// 직렬화 실패는 로컬입니다. bug（구성 필드 유형이 잘못되었습니다.），다시 시도해도 나아지지 않을 것 같아요。
			return nil, Permanent(fmt.Errorf("요청 본문을 구성하지 못했습니다.: %w", err))
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		// URL 불법——사용자가 잘못된 주소를 입력했을 가능성이 높습니다.，은 영구적인 실패입니다.。
		// 여기서도 투명전송은 불가합니다 err：url.Parse 의 오류 텍스트에는 전체 주소가 포함되어 있습니다.。
		return nil, Permanent(fmt.Errorf("요청한 주소가 불법입니다: %s", redactRequestTarget(url)))
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		// 연결이 거부되었습니다.、DNS 실패、시간 초과——대부분 일시적인 오류，백오프로 넘겨주고 다시 시도해보세요。
		//
		// 오류 텍스트를 외부로 전송하기 전에 민감도를 낮추어야 합니다.。이유：http.Client.Do 반환 *url.Error，
		// 그래요 Error() 네 `Op "완료URL": 근본적인 오류`，그리고 이 기능에 대한 이들 회사의 자격 증명**바로 거기 URL 내부**
		// （딩톡 access_token、치웨이 key、페이슈 hook id、Telegram /bot<token>/）。
		// 둔감하지 않은 경우，이 오류에 따라 자격 증명이 4곳으로 스트리밍됩니다.：notification_deliveries
		// 님 last_error（데이터베이스에 저장된 일반 텍스트）、배송이력 인터페이스 응답（**우회 채널 구성 마스크**）、
		// 서버 로그、및 테스트 전송 인터페이스를 프런트 엔드로 다시 전송 502 문자。
		return nil, fmt.Errorf("요청 실패: %s", redactTransportError(err))
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, respBodyLimit))
	if readErr != nil {
		return nil, fmt.Errorf("응답을 읽지 못했습니다.: %w", readErr)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return raw, nil
	}
	// 429（전류 제한）그리고 408（시간 초과）다시 시도해 볼 가치가 있습니다；나머지는 4xx 구성이나 권한 문제입니다，재시도는 의미가 없습니다。
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusRequestTimeout {
		return nil, fmt.Errorf("상대방의 현재 제한 또는 시간 초과 (HTTP %d): %s", resp.StatusCode, snippet(raw))
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("상대방의 서비스가 비정상적입니다. (HTTP %d): %s", resp.StatusCode, snippet(raw))
	}
	return nil, Permanent(fmt.Errorf("상대방이 요청을 거부했습니다. (HTTP %d): %s", resp.StatusCode, snippet(raw)))
}

// snippet 응답 본문을 짧은 텍스트 줄로 압축합니다.，。응답에 줄 바꿈과 많은 공백이 포함될 수 있습니다.，
// 그냥 꽂으세요 last_error 을 사용하면 배송 내역 페이지 레이아웃이 접혀집니다.。
func snippet(raw []byte) string {
	return OneLine(string(raw), 200)
}

// redactRequestTarget 배송주소를 눌러주세요「scheme://host/…」，。
//
// 이것은 이 패키지의 유일한 주소 둔감화 구경입니다.，일부러 한거임**충분히 거칠다**：제외 scheme 그리고 host，
// 나머지는 폐기됩니다。이유는 사람이 없어서「보편적이고 안전함」판단하는 방법 URL 의 어느 부분이 자격 증명인가요?：
//
//	딩톡   자격 증명은 query      /robot/send?access_token=xxx
//	치웨이   자격 증명은 query      /cgi-bin/webhook/send?key=xxx
//	페이슈   자격 증명은**경로 끝** /open-apis/bot/v2/hook/<hook_id>
//	Telegram 자격 증명은**경로 중간 부분** /bot<token>/sendMessage
//
// 생각해보세요「유용한 부분만 남겨두세요」채널에 맞게 패치를 하셔야 합니다，그리고 하나라도 누락되면 자격 증명 위반입니다.。
// 예약됨 host 이면 문제 해결에 충분합니다.（DNS 구문 분석할 수 없습니다.、연결할 수 없습니다、인증서가 올바르지 않더라도 위치를 찾을 수 있습니다.），
// 특정 로봇은 채널 구성에서 마스크 꼬리 번호로 식별됩니다.。
//
// 구문 분석 실패 시 반환되는 고정 자리 표시자——원래 문자열을 에코하지 마십시오.。
func redactRequestTarget(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "(주소를 확인할 수 없습니다.)"
	}
	return u.Scheme + "://" + u.Host + "/…"
}

// redactTransportError 전송 계층 오류에서 주소 제거，근본적인 이유만 유지하세요。
//
// *url.Error 의 구조는 다음과 같습니다. {Op, URL, Err}，Error() 그럴게요 URL 같이 써보세요。
// 여기서는 명시적으로 받아들이십시오. Err 필드，우회하세요 Error() ——나중에 문자열 교체를 하는 것보다 더 안정적입니다.，
// 교체는 정확하게 처리해야 하기 때문에 URL 인코딩/탈출 후 다양한 변주，누출되기 쉬움。
func redactTransportError(err error) string {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		host := ""
		if u, parseErr := url.Parse(uerr.URL); parseErr == nil {
			host = u.Host
		}
		if uerr.Err != nil {
			return fmt.Sprintf("%s %s: %s", uerr.Op, host, uerr.Err)
		}
		return fmt.Sprintf("%s %s: 알 수 없는 오류", uerr.Op, host)
	}
	// 아니요 *url.Error（리디렉션 정책에서 반환된 오류 등）주소도 포함될 수 있음，균일한 둔감화。
	return redactURLsInText(err.Error())
}

// redactURLsInText 본문에 나오는 단어를 넣어보세요 http(s) 주소는 민감하지 않은 형식으로 대체됩니다.。
//
// 구조화된 필드를 가져오지 못하는 오류를 처리하는 데 사용됩니다.（리디렉션 전략 오류、타사 라이브러리의 사용자 정의 오류）。
// 인식만 가능 http/https 접두사，공백과 따옴표로 분할——주소에는 이 두 가지 유형의 문자가 포함되지 않습니다.。
func redactURLsInText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		rest := s[i:]
		if strings.HasPrefix(rest, "http://") || strings.HasPrefix(rest, "https://") {
			end := len(rest)
			if j := strings.IndexAny(rest, " \t\n\"'"); j >= 0 {
				end = j
			}
			b.WriteString(redactRequestTarget(rest[:end]))
			i += end
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
