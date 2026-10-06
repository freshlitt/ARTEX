package notify

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
)

// Channel 은 알림 채널용 어댑터입니다.。구현 필요**상태 없음**：여러 채널에서 동일한 인스턴스를 사용합니다.
// 동시 다중화 구성，자격 증명은 다음에서 제공되어야 합니다. cfg 전달된 매개변수。
type Channel interface {
	// Kind 반환 채널 유형 식별자，은 레지스트리의 키와 일치해야 합니다.。
	Kind() string
	// Validate 구성 저장 시 호출됨，필수 필드 및 형식을 확인하세요.。반환된 오류는 다음에 직접 표시됩니다.
	// 구성자，그래서 카피는 설명해야합니다「어떤 필드가 누락되었나요?」「잘못된 구성」。
	Validate(cfg map[string]any) error
	// Send 메시지를 한 번만 전달하세요.，복귀**실제 배송된 상품수**오류 발생。
	//
	// 왜 항목 개수를 반환해야 합니까?：각 플랫폼에는 최대 메시지 길이가 있습니다.，요약 메시지가 전체 배치에 맞지 않으면 잘립니다.。
	// 호출자가 무조건 전체 배치를 전달됨으로 표시하는 경우，잘린 항목은 사라집니다.——메시지에 안보이네요、
	// 배송 이력도 성공했음을 보여줍니다.，어디에서도 취약점이 발견되지 않았습니다. 발행되지 않음。복귀 kept 이후，
	// 발신자는 앞만 표시함 kept 글，나머지는 다음 배치를 위해 예약됩니다。
	//
	// 오류를 반환하면 배달 실패를 나타냅니다.，어디에서 *PermanentError 은 다시 시도하지 말라는 뜻입니다.。
	// 실패했을 때 kept 의미없어，발신자는 무시해야 합니다.。
	Send(ctx context.Context, cfg map[string]any, m Message) (int, error)
	// DefaultRatePerMin 이 채널에 대한 공식 권장 분당 전송 제한을 반환합니다.，새 채널 인스턴스로
	// 일 때 기본 전류 제한 값。복귀 0 은 알려진 제한 사항이 없음을 의미합니다.。
	DefaultRatePerMin() int
	// SecretKeys 채널 구성의 자격 증명에 속하는 키 이름을 반환합니다.。API 이 키의 값은 에코될 때 마스크됩니다.，
	// 업데이트 중에 마스크 값을 받으면 라이브러리의 원래 값이 유지됩니다.。구현에서만 자격 증명으로 간주되는 필드를 알 수 있습니다.
	// （기업 전체 WeChat Webhook 주소가 자격증명입니다，그리고 딩딩도 그 중 하나일 뿐이에요 secret），
	// 그래서 이 지식은 채널에서 제공되어야 합니다.，윗사람이 짐작할 수 없는 일이다。
	SecretKeys() []string
	// DestinationKeys 채널 구성으로 돌아가서 결정하세요.「메시지를 보낼 곳」의 키 이름。
	//
	// 그리고 SecretKeys 보안과 관련된 내용이기도 합니다.：대상 주소와 자격 증명은 두 개의 독립적인 필드 집합입니다.，
	// 허용된다면「주소만 바꾸세요、자격 증명을 그대로 둡니다.」，채널 구성을 변경할 수 있는 사람은 누구나 라이브러리에서 실제 자격 증명을 얻을 수 있습니다.
	// 제어하는 서버로 보내기，채널에 구성된 마스크는 전혀 의미가 없습니다.。
	// 자세히 보기 PrepareConfigUpdate。
	DestinationKeys() []string
}

// registry 은 채널 등록 양식입니다.。의도적으로 대신 명시적 리터럴을 사용합니다. init() 자가등록：이렇게요「어떤 채널이 있나요?」
// 한곳에서 모두 시청하세요，그리고 새 채널에서는 편집 중에 누락된 부분이 노출됩니다.，런타임 부작용에 의존하는 대신。
var registry = map[string]Channel{
	KindDingTalk: dingTalkChannel{},
	KindFeishu:   feishuChannel{},
	KindWeCom:    weComChannel{},
	KindWebhook:  webhookChannel{},
	KindTelegram: telegramChannel{},
	KindEmail:    emailChannel{},
}

// Get 유형에 따라 채널을 취하여 구현。
func Get(kind string) (Channel, bool) {
	c, ok := registry[kind]
	return c, ok
}

// ValidKind 신고 kind 지원되는 채널타입인가요?。
func ValidKind(kind string) bool {
	_, ok := registry[kind]
	return ok
}

// Kinds 지원되는 모든 채널 유형을 반환합니다.，사전순으로 정렬（ UI 드롭다운 안정적인 디스플레이）。
func Kinds() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PermanentError 재시도해서는 안 되는 전송 실패를 표시합니다.：자격 증명 오류、대상이 거부됨、요청 본문이 불법입니다.。
// 일시적인 오류에 대해서만 재시도（네트워크 지터、전류 제한、동료 5xx）의미있는；영구적인 실패 시 반복적으로 물러났다가 다시 시도합니다.
// 둘 다 성공하지 못할 것이다，실제 오류는 재시도 로그에 다시 플러시됩니다.。
type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// Permanent 넣어보세요 err 영구 실패로 표시됨。err 입니다 nil 일 때 반환됨 nil，
// 편리하게 작성되었습니다. `return Permanent(someCheck())`。
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{Err: err}
}

// IsPermanent 신고 err 체인에 영구적인 실패 표시가 있는지 여부。
func IsPermanent(err error) bool {
	var pe *PermanentError
	return errors.As(err, &pe)
}

// ---- 구성 읽기helper ----
//
// 채널 구성은 데이터베이스에서 옵니다. JSONB 칼럼， encoding/json 역직렬화 후에는 map[string]any，
// 값은 항상 float64、배열은 다음과 같습니다. []any。다음은 helper 변환 레이어 통합，및 사용자 허용
// 에 UI 을 공백으로 남겨두면 유형 편차가 발생합니다.（예를 들어 포트를 문자열로 입력합니다.）。

// cfgString 문자열 구성 항목 가져오기，앞뒤 공백은 잘립니다.——웹 양식에서 쉽게 복사하여 붙여넣을 수 있습니다.。
func cfgString(cfg map[string]any, key string) string {
	v, ok := cfg[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

// cfgInt 정수 구성 항목 가져오기，호환 가능 float64（JSON 기본값）및 문자열 두 소스。
func cfgInt(cfg map[string]any, key string) int {
	switch v := cfg[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0
		}
		return n
	default:
		return 0
	}
}

// cfgBool 부울 구성 항목 가져오기，호환 문자열 "true"/"1"。
func cfgBool(cfg map[string]any, key string) bool {
	switch v := cfg[key].(type) {
	case bool:
		return v
	case string:
		s := strings.ToLower(strings.TrimSpace(v))
		return s == "true" || s == "1" || s == "yes"
	default:
		return false
	}
}

// cfgStrings 문자열 배열 구성 항목 가져오기，공백을 자동으로 자르고 빈 문자열을 삭제합니다.。
func cfgStrings(cfg map[string]any, key string) []string {
	raw, ok := cfg[key].([]any)
	if !ok {
		// 단일 문자열도 허용됩니다.，값이 하나만 있는 경우 양식 제출에 편리합니다.。
		if s := cfgString(cfg, key); s != "" {
			return []string{s}
		}
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// cfgMap 문자열 매핑 구성 항목 가져오기（맞춤형인 경우 HTTP 머리），키 값이 모두 비어 있습니다.，빈 키 삭제。
func cfgMap(cfg map[string]any, key string) map[string]string {
	raw, ok := cfg[key].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		out[k] = s
	}
	return out
}
