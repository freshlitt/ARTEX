package notify

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Filter 네 notification_channels.filter 이거요 JSONB 칼럼 계약서：채널 인스턴스의 필터 조건。
// 모든 항목은 선택사항입니다.，기본값은「필터링 없음」——이것이 바로 비정상 구성의 숨겨진 의미입니다.，또 만나요 ParseFilter。
type Filter struct {
	// MinSeverity 은 최저 수준 임계값입니다.（low/medium/high/critical），비어 있음=임계값 없음。
	MinSeverity string `json:"min_severity"`
	// TaskIDs / AssetIDs 빈 배열은 제한이 없음을 의미합니다.；비어 있지 않으면 이벤트가 교차해야 합니다.。
	TaskIDs  []int64 `json:"task_ids"`
	AssetIDs []int64 `json:"asset_ids"`
	// VulnClassInclude 비어 있으면 모두 수신함을 의미합니다.；비어 있지 않은 경우 필수 vulnclass 키워드 중 하나를 누르십시오.。
	// VulnClassExclude 키워드가 일치하면 제외（제외가 포함보다 우선합니다.）。
	// 일치 방법은 대소문자를 구분하지 않는 하위 문자열입니다.——정규식보다 안전함：사용자 구성이 잘못되어도 채널이 자동으로 실패하지 않습니다.。
	VulnClassInclude []string `json:"vulnclass_include"`
	VulnClassExclude []string `json:"vulnclass_exclude"`
	// OnStatusChange 이 채널이 취약점 상태 변경 이벤트를 수신하는지 여부를 결정합니다.（만 realtime 패턴이 이해가 되네요）。
	OnStatusChange bool `json:"on_status_change"`
}

// ParseFilter 구문 분석 채널 필터링 구성。
//
// **돌아오지 않는다 error。** 이는 의도적인 디자인 선택입니다.：필터 조건 구성이 잘못되면 항상 0 값으로 변질됩니다.
// Filter（= 필터링 없음 = 전체 히트），취약점 알림 시스템 때문입니다.，**트윗 한 번만 더 하는 게 훨씬 낫다
// 고위험 품목을 소리 없이 놓치다**。파싱 실패를 하자「밀지 마세요」，사용자에게 준비된 사진을 제공하는 것과 같습니다.、
// 실제로 아무것도 푸시하지 않는 채널——최악의 실패모드다。
func ParseFilter(raw []byte) Filter {
	var f Filter
	if len(raw) == 0 {
		return f
	}
	// 파싱 실패 시 f 값을 0으로 유지하세요.，즉, 필터링이 없습니다.。
	_ = json.Unmarshal(raw, &f)
	return f
}

// ValidMinSeverity 신고 s 법적 수준의 문턱인가?（빈 문자열은 임계값이 없음을 의미합니다.）。
func ValidMinSeverity(s string) bool {
	if s == "" {
		return true
	}
	_, ok := severityRank[s]
	return ok
}

// Validate 검증 필터 구성에서**값이 제한되어 있습니다.**필드，채널 저장 시 호출됩니다.。
//
// 글을 쓸 때 왜 차단을 해야 하나요?：Match 알 수 없는 임계값의 결정은 다음과 같습니다. `rank >= 0`，항상 그렇습니다——
// 즉, min_severity 틀린말（"hgih"），필터회의**조용한 실패**이 됩니다.
// 「모두 추천」。이 패키지와 관련된 내용입니다.「나는 밀어붙이는 것보다 더 밀어붙이는 편이 낫다」의 선택은 같은 방향이다（누출되지 않습니다），
// 그러나 결과적으로 사용자는 계층적 푸시를 수행하고 있다고 생각하게 됩니다.、실제로 모든 취약점을 그룹에 쏟아 붓는다.，
// 그리고 그 사람이 부적합한 징후도 없습니다。이런「자동 다운그레이드」입구에서 막아야지。
//
// 주의 Validate 전용**쓰기**경로。독서경로는 아직 진행중입니다 ParseFilter 의 허용적 의미，
// 이렇게 하면 과거 데이터에 이미 존재하는 잘못된 값으로 인해 채널을 읽을 수 없게 되는 일이 발생하지 않습니다.。
func (f Filter) Validate() error {
	if !ValidMinSeverity(f.MinSeverity) {
		return fmt.Errorf("최하위 레벨 %q 유효하지 않음，선택사항：low / medium / high / critical，또는 제한이 없음을 의미하려면 공백으로 두십시오.", f.MinSeverity)
	}
	return nil
}

// Match 이 필터 조건을 사용하여 이벤트를 채널에 전달해야 하는지 여부를 결정합니다.。
//
// **돌아오지 않는다 error**，같은 이유 ParseFilter：내부 예외는 다음과 같습니다.「히트」처리 중。
// 판결명령：이벤트 종류 → 레벨 임계값 → 임무/자산 범위 → 취약점 유형 키워드。
func Match(f Filter, s Snapshot) bool {
	// 상태 변경 이벤트는 명시적으로 열린 채널에서만 수신할 수 있습니다.。기본값은 꺼짐，대다수의 사용자가
	// 기대「푸시」은 다음을 가리킨다.「새로운 취약점 발견」，기존 계좌처럼 상태 이전마다 후속 조치를 취하는 대신。
	if s.Kind == EventFindingStatusChanged && !f.OnStatusChange {
		return false
	}
	if !AtLeast(s.Severity, f.MinSeverity) {
		return false
	}
	if len(f.TaskIDs) > 0 && !slices.Contains(f.TaskIDs, s.TaskID) {
		return false
	}
	if len(f.AssetIDs) > 0 && !intersectsInt(f.AssetIDs, s.AssetIDs) {
		return false
	}
	// 제외가 우선입니다.：제외 키워드 중 하나라도 맞으면 탈락됩니다.，인클루드 리스트가 동시에 히트되어도。
	if len(f.VulnClassExclude) > 0 && containsAnyFold(s.VulnClass, f.VulnClassExclude) {
		return false
	}
	if len(f.VulnClassInclude) > 0 && !containsAnyFold(s.VulnClass, f.VulnClassInclude) {
		return false
	}
	return true
}

func intersectsInt(a, b []int64) bool {
	// 작은 세트를 선형으로 스캔할 수 있습니다.；양쪽의 크기는 다음과 같습니다.「수십개를 손으로 확인」，
	// 빌드 map 혜택보다 비용이 더 많이 듭니다.。
	for _, v := range b {
		if slices.Contains(a, v) {
			return true
		}
	}
	return false
}

// containsAnyFold 신고 s 포함되나요? keywords 임의의 키워드（대소문자를 구분하지 않습니다.）。
func containsAnyFold(s string, keywords []string) bool {
	lower := strings.ToLower(s)
	for _, kw := range keywords {
		kw = strings.ToLower(strings.TrimSpace(kw))
		if kw != "" && strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
