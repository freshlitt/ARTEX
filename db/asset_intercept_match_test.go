package db

import "testing"

func rule(kind, pattern string, enabled bool) AssetInterceptRule {
	return AssetInterceptRule{Kind: kind, Pattern: pattern, Enabled: enabled}
}

func TestMatchAssetInterceptRules(t *testing.T) {
	cases := []struct {
		name    string
		rules   []AssetInterceptRule
		domains []string
		ips     []string
		urls    []string
		want    bool
		wantVal string
	}{
		{"퍼지 정부 도메인 이름 내장", []AssetInterceptRule{rule("fuzzy_domain", ".gov.cn", true)},
			[]string{"www.beijing.gov.cn"}, nil, nil, true, "www.beijing.gov.cn"},
		{"퍼지 교육 도메인 이름 히트", []AssetInterceptRule{rule("fuzzy_domain", ".edu", true)},
			[]string{"mit.edu"}, nil, nil, true, "mit.edu"},
		{"일치하는 도메인 이름 조회수는 대소문자를 구분하지 않습니다.", []AssetInterceptRule{rule("exact_domain", "Example.com", true)},
			[]string{"example.com"}, nil, nil, true, "example.com"},
		{"정확한 도메인 이름이 하위 도메인과 일치하지 않습니다.", []AssetInterceptRule{rule("exact_domain", "example.com", true)},
			[]string{"a.example.com"}, nil, nil, false, ""},
		{"합동IP히트", []AssetInterceptRule{rule("exact_ip", "203.0.113.5", true)},
			nil, []string{"203.0.113.5"}, nil, true, "203.0.113.5"},
		{"흐릿하다IP접두사 히트", []AssetInterceptRule{rule("fuzzy_ip", "203.0.113.", true)},
			nil, []string{"203.0.113.99"}, nil, true, "203.0.113.99"},
		{"CIDR 히트", []AssetInterceptRule{rule("cidr", "192.168.0.0/16", true)},
			nil, []string{"192.168.5.20"}, nil, true, "192.168.5.20"},
		{"CIDR 놓쳤어요", []AssetInterceptRule{rule("cidr", "192.168.0.0/16", true)},
			nil, []string{"10.0.0.1"}, nil, false, ""},
		{"합동URL히트", []AssetInterceptRule{rule("exact_url", "https://a.gov.cn/login", true)},
			nil, nil, []string{"https://a.gov.cn/login"}, true, "https://a.gov.cn/login"},
		{"흐릿하다URL히트 경로", []AssetInterceptRule{rule("fuzzy_url", "/admin", true)},
			nil, nil, []string{"https://x.com/admin/panel"}, true, "https://x.com/admin/panel"},
		{"비활성화된 규칙이 적중되지 않습니다.", []AssetInterceptRule{rule("fuzzy_domain", ".gov.cn", false)},
			[]string{"www.gov.cn"}, nil, nil, false, ""},
		{"규칙 없이는 히트가 없습니다", nil, []string{"www.gov.cn"}, nil, nil, false, ""},
		{"비어 있음pattern놓쳤어요", []AssetInterceptRule{rule("fuzzy_domain", "  ", true)},
			[]string{"www.gov.cn"}, nil, nil, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, val, ok := MatchAssetInterceptRules(c.rules, c.domains, c.ips, c.urls)
			if ok != c.want {
				t.Fatalf("히트 = %v, 기대 %v (rule=%+v)", ok, c.want, r)
			}
			if ok && val != c.wantVal {
				t.Fatalf("적중값 = %q, 기대 %q", val, c.wantVal)
			}
		})
	}
}

func TestEvaluateAssetGate(t *testing.T) {
	block := []AssetInterceptRule{rule("fuzzy_domain", ".gov.cn", true)}
	allow := []AssetInterceptRule{rule("fuzzy_domain", "example.com", true)}

	// 1. 히트 차단 규칙 → 거부（차단 사유가 우선입니다.）。
	if d := EvaluateAssetGate(block, allow, []string{"www.gov.cn"}, nil, nil); d.Allowed {
		t.Fatal("적중 차단 규칙을 거부해야 합니다.")
	}

	// 2. 차단 실패、허용 규칙이 있지만 적중되지 않습니다. → 거부（허용되지 않음）。
	d := EvaluateAssetGate(block, allow, []string{"foo.other.com"}, nil, nil)
	if d.Allowed {
		t.Fatal("화이트리스트가 있으며 거부할 수 있는 적중이 없습니다.")
	}
	if d.Reason == "" {
		t.Fatal("거절에는 이유가 포함되어야 합니다.")
	}

	// 3. 차단 실패、허용 규칙 적중 → 출시。
	if d := EvaluateAssetGate(block, allow, []string{"api.example.com"}, nil, nil); !d.Allowed {
		t.Fatal("화이트리스트에 도달하면 허용되어야 합니다.")
	}

	// 4. 허용 규칙 없음（화이트리스트가 활성화되지 않았습니다.）→ 차단 및 해제 실패。
	if d := EvaluateAssetGate(block, nil, []string{"foo.other.com"}, nil, nil); !d.Allowed {
		t.Fatal("화이트리스트가 없으면 놓친 차단을 허용해야 합니다.")
	}

	// 5. 모든 규칙을 비활성화하도록 허용 → 화이트리스트는 활성화되지 않은 것으로 간주됩니다.，출시。
	disabledAllow := []AssetInterceptRule{rule("fuzzy_domain", "example.com", false)}
	if d := EvaluateAssetGate(nil, disabledAllow, []string{"foo.other.com"}, nil, nil); !d.Allowed {
		t.Fatal("모든 화이트리스트가 비활성화된 경우 허용되어야 합니다.")
	}

	// 6. 허가보다 차단이 우선입니다.：같은 대상이 요격과 허용 모두 적중 → 거부。
	if d := EvaluateAssetGate(
		[]AssetInterceptRule{rule("fuzzy_domain", ".gov.cn", true)},
		[]AssetInterceptRule{rule("fuzzy_domain", ".gov.cn", true)},
		[]string{"www.gov.cn"}, nil, nil,
	); d.Allowed {
		t.Fatal("허용보다 차단이 우선되어야 합니다.")
	}
}

func TestAssetInterceptCandidates(t *testing.T) {
	// 지참만 하세요 URL 의 서비스 자산：host 을 도메인 이름 후보로 구분하여 분류해야 합니다.，그래서 fuzzy_domain 히트。
	a := &Asset{Type: "service", URL: "https://portal.beijing.gov.cn:8443/app"}
	domains, _, urls := a.interceptCandidates()
	if len(urls) != 1 || urls[0] != a.URL {
		t.Fatalf("urls = %v", urls)
	}
	found := false
	for _, d := range domains {
		if d == "portal.beijing.gov.cn" {
			found = true
		}
	}
	if !found {
		t.Fatalf("URL host 도메인 이름 후보가 분할되지 않았습니다.: %v", domains)
	}
	r, _, ok := MatchAssetInterceptRules([]AssetInterceptRule{rule("fuzzy_domain", ".gov.cn", true)}, domains, nil, urls)
	if !ok {
		t.Fatalf(" URL 의 정부 서비스 자산은 다음과 같습니다. fuzzy_domain 히트, rule=%+v", r)
	}

	// URL host 네 IP 은 다음과 같이 분류되어야 합니다. IP 후보，할 수 있습니다 CIDR 히트。
	b := &Asset{Type: "service", URL: "http://10.1.2.3/x"}
	_, ips, _ := b.interceptCandidates()
	if r, _, ok := MatchAssetInterceptRules([]AssetInterceptRule{rule("cidr", "10.0.0.0/8", true)}, nil, ips, nil); !ok {
		t.Fatalf("URL 에 IP 이어야 합니다. CIDR 히트, ips=%v rule=%+v", ips, r)
	}
}
