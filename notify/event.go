package notify

// Snapshot 네 notification_events.snapshot 이거요 JSONB 칼럼 계약서。작성방법은 db 레이어
// 취약점 로깅 사항，독자님은 server 레이어 전달 엔진 및 필터 매칭。정의는 다음과 같기 때문에 이 패키지에 배치됩니다.
// 「알림 필드」페이로드：db 은 직렬화만 담당합니다.，해당 필드의 의미를 이해하지 못합니다.。
//
// 렌더링 중에 중복된 취약 필드를 확인하지 않는 이유는 무엇입니까?：해당 취약점은 추후 이름이 변경될 예정입니다.、레벨 변경、상태 변경，
// 그리고 푸시 내용에 반영되어야 합니다.**사건 당시**의 결론——다시 확인하면 얻을 수 있습니다.「은 나중에 다음으로 변경되었습니다. low」님
// 오해를 불러일으킬 위험이 있음。역시 fan-out 렌더링이 있으므로 필요 없음 JOIN findings/tasks/assets 테이블 3개。
type Snapshot struct {
	// 이벤트 종류：finding_created / finding_status_changed
	Kind      string  `json:"kind"`
	FindingID int64   `json:"finding_id"`
	TaskID    int64   `json:"task_id"`
	VulnClass string  `json:"vulnclass"`
	Name      string  `json:"name"`
	Severity  string  `json:"severity"`
	Summary   string  `json:"summary"`
	AssetIDs  []int64 `json:"asset_ids"`
	// 만 kind=finding_status_changed 시간은 비어있지 않다。
	FromStatus string `json:"from_status,omitempty"`
	ToStatus   string `json:"to_status,omitempty"`
}

// Item 은 푸시해야 할 취약점입니다.，채널 렌더링용。
type Item struct {
	FindingID int64
	Name      string
	VulnClass string
	Severity  string
	Summary   string
	// Assets 은 구문 분석된 자산 표시 이름입니다.（도메인 이름 등/IP）。 server 레이어 채우기——
	// 이 패키지는 데이터베이스에 영향을 미치지 않습니다.，이름을 알 수 없습니다。
	Assets []string
	// DetailURL 은 취약점 세부정보 링크입니다.；비어 있으면 구성되지 않았음을 의미합니다. public_base_url，렌더링시 생략。
	DetailURL string
	// 상태 변경 이벤트 전용；두 항목이 모두 비어 있지 않으면 다음으로 렌더링됩니다.「보류 중 → 고정됨」。
	FromStatus string
	ToStatus   string
}

// IsStatusChange 항목이 상태 변경 이벤트인지 보고합니다.。
func (i Item) IsStatusChange() bool { return i.FromStatus != "" || i.ToStatus != "" }

// Title 항목의 표시 제목을 반환합니다.：인위적인 네이밍을 우선으로 하겠습니다. name，대체 취약점 유형 vulnclass，
// 둘 다 비어 있으면 자리 표시자를 사용하십시오.——빈 제목을 출력하지 마세요.。
func (i Item) Title() string {
	if i.Name != "" {
		return i.Name
	}
	if i.VulnClass != "" {
		return i.VulnClass
	}
	return "(이름이 없는 취약점)"
}

// Message 은 하나의 채널에서 보내는 완전한 콘텐츠입니다.。
type Message struct {
	// 한 번 누르는 길이는 1；요약 푸시（digest）은 전체 배치입니다.。
	// 빈 슬라이스는 불법입니다.，발신자는 최소한 하나의。
	Items []Item
	// Batch=true 타임프레스 요약 메시지 렌더링（제목 변경、기간 및 응모횟수 가져오기）。
	Batch bool
	// WindowMinutes 은 요약 기간입니다.（분），만 Batch=true 은 카피라이팅에 사용됩니다.「근처 N 분」。
	// 렌더링할 때 계산하는 대신 의도적으로 구성에서 전달합니다. time.Since：렌더링은 결정적으로 유지됩니다.，은 테스트하기 쉽습니다。
	WindowMinutes int
	// HomeURL 은 플랫폼 패널 주소입니다.（글로벌 public_base_url）；비어 있으면 패널 입구가 없습니다.。
	HomeURL string
}
