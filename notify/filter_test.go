package notify

import "testing"

func TestParseFilterMalformedFallsBackToMatchAll(t *testing.T) {
	// 기형 JSON、빈 입력、잘못된 유형의 필드——모두 0 값으로 변질되어야 합니다. Filter，
	// 그렇죠「필터링 없음」。이 불변성은「나는 밀어붙이는 것보다 더 밀어붙이는 편이 낫다」의 착륙지점：
	// 오류 보고 또는 세미 파싱으로 변경되면，한 문자가 일치하지 않는 사용자는 모든 고위험 알림을 자동으로 삭제합니다.。
	cases := []struct {
		name string
		raw  string
	}{
		{"빈 입력", ""},
		{"불법 JSON", `{not json`},
		{"잘림 JSON", `{"min_severity":`},
		{"유형 불일치", `{"min_severity": 123, "task_ids": "abc"}`},
		{"최상위 수준은 배열입니다.", `[1,2,3]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := ParseFilter([]byte(tc.raw))
			if f.MinSeverity != "" || len(f.TaskIDs) != 0 || len(f.AssetIDs) != 0 {
				t.Fatalf("잘못된 구성은 0 값으로 변질되어야 합니다. Filter，받았어요 %+v", f)
			}
			// 값이 0입니다. Filter 이벤트가 발생해야 합니다.。
			ev := Snapshot{Kind: EventFindingCreated, Severity: "low", VulnClass: "XSS"}
			if !Match(f, ev) {
				t.Fatal("값이 0입니다. Filter 모든 이벤트에 응답합니다.")
			}
		})
	}
}

func TestMatchSeverityThreshold(t *testing.T) {
	ev := func(sev string) Snapshot {
		return Snapshot{Kind: EventFindingCreated, Severity: sev}
	}
	cases := []struct {
		min    string
		sev    string
		expect bool
	}{
		{"", "low", true},
		{"", "critical", true},
		{"high", "critical", true},
		{"high", "high", true},
		{"high", "medium", false},
		{"high", "low", false},
		{"critical", "high", false},
		{"critical", "critical", true},
		// 알 수 없는 레벨 번호는 0，은 비어 있지 않은 임계값에 의해 차단되어야 합니다.（의심스러우면 추천하지 마세요）。
		{"low", "", false},
		{"low", "unknown", false},
		{"", "", true},
	}
	for _, tc := range cases {
		got := Match(Filter{MinSeverity: tc.min}, ev(tc.sev))
		if got != tc.expect {
			t.Errorf("min=%q sev=%q: 기대 %v 받았어요 %v", tc.min, tc.sev, tc.expect, got)
		}
	}
}

func TestMatchScopeRestrictions(t *testing.T) {
	ev := Snapshot{
		Kind:      EventFindingCreated,
		Severity:  "high",
		TaskID:    7,
		AssetIDs:  []int64{10, 20},
		VulnClass: "SQL주사",
	}
	cases := []struct {
		name   string
		filter Filter
		expect bool
	}{
		{"빈 범위=제한 없음", Filter{}, true},
		{"미션 히트", Filter{TaskIDs: []int64{7}}, true},
		{"미션실패", Filter{TaskIDs: []int64{8}}, false},
		{"히트를 포함한 다양한 작업 선택", Filter{TaskIDs: []int64{8, 7}}, true},
		{"자산 중복", Filter{AssetIDs: []int64{20, 99}}, true},
		{"자산 교차 없음", Filter{AssetIDs: []int64{99}}, false},
		{"업무와 자산이 동시에 히트", Filter{TaskIDs: []int64{7}, AssetIDs: []int64{10}}, true},
		{"작업이 히트했지만 자산이 누락되었습니다.", Filter{TaskIDs: []int64{7}, AssetIDs: []int64{99}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Match(tc.filter, ev); got != tc.expect {
				t.Errorf("기대 %v 받았어요 %v", tc.expect, got)
			}
		})
	}
}

func TestMatchVulnClassKeywords(t *testing.T) {
	ev := func(class string) Snapshot {
		return Snapshot{Kind: EventFindingCreated, Severity: "high", VulnClass: class}
	}
	cases := []struct {
		name   string
		filter Filter
		class  string
		expect bool
	}{
		{"include 이 비어 있습니다.=다 받아보세요", Filter{}, "모든 유형", true},
		{"include 히트", Filter{VulnClassInclude: []string{"SQL"}}, "SQL주사", true},
		{"include 놓쳤어요", Filter{VulnClassInclude: []string{"명령 실행"}}, "SQL주사", false},
		{"include 여러 단어 중 하나가 히트됨", Filter{VulnClassInclude: []string{"명령 실행", "SQL"}}, "SQL주사", true},
		{"대소문자를 구분하지 않습니다.", Filter{VulnClassInclude: []string{"sql"}}, "SQL주사", true},
		{"exclude 적중시 제외", Filter{VulnClassExclude: []string{"정보 유출"}}, "정보 유출", false},
		{"exclude 안 맞으면 패스", Filter{VulnClassExclude: []string{"정보 유출"}}, "SQL주사", true},
		// 제외가 포함보다 우선합니다.：동시에 맞으면 아웃되야함。
		{"제외가 포함보다 우선합니다.", Filter{
			VulnClassInclude: []string{"SQL"},
			VulnClassExclude: []string{"주사"},
		}, "SQL주사", false},
		// 순수 공백 키워드는 무시해야 합니다.，그렇지 않으면 퇴화됩니다.「공백이 포함된 모든 문자열과 일치합니다.」。
		{"빈 키워드는 무시됩니다.", Filter{VulnClassInclude: []string{"", "  "}}, "SQL주사", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Match(tc.filter, ev(tc.class)); got != tc.expect {
				t.Errorf("기대 %v 받았어요 %v", tc.expect, got)
			}
		})
	}
}

func TestMatchStatusChangeRequiresOptIn(t *testing.T) {
	ev := Snapshot{Kind: EventFindingStatusChanged, Severity: "critical", FromStatus: "pending", ToStatus: "fixed"}
	// 기본값은 꺼짐：대부분의 사람들이 하는 말「푸시 취약점」은 새로운 취약점이 발견되었음을 나타냅니다.，스테이터스 계정이 아닙니다。
	if Match(Filter{MinSeverity: "low"}, ev) {
		t.Fatal("활성화되지 않은 경우 상태 변경 이벤트를 건너뛰어야 합니다.")
	}
	if !Match(Filter{OnStatusChange: true}, ev) {
		t.Fatal("켜세요 on_status_change 게시물 상태 변경 이벤트가 발생해야 합니다.")
	}
	// 생성 이벤트는 다음의 영향을 받지 않습니다. on_status_change 임팩트。
	created := Snapshot{Kind: EventFindingCreated, Severity: "critical"}
	if !Match(Filter{MinSeverity: "low"}, created) {
		t.Fatal("생성 이벤트는 다음에 의존해서는 안 됩니다. on_status_change")
	}
}
