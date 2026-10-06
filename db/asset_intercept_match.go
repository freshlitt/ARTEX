package db

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// 자산 차단 규칙 일치/실행 계층。asset_intercept.go 은 규칙 저장만 담당합니다.，이것은 다음을 담당합니다.
// 「대상자산」의 도메인 이름/IP/URL 활성화된 규칙을 일치시킵니다.。 agent 도구（add_intent、
// insert_assets）아래 사진을 보내주세요 / 자산을 삽입하기 전에 호출됩니다.，맞으면 거절。

// AssetInterceptKindLabel 복귀 kind 의 중국어 태그，는 다음과 같이 사용됩니다. agent 의 설명 메시지。
func AssetInterceptKindLabel(kind string) string {
	switch kind {
	case "exact_domain":
		return "도메인 이름(합동)"
	case "exact_ip":
		return "IP(합동)"
	case "exact_url":
		return "URL(합동)"
	case "fuzzy_domain":
		return "도메인 이름(흐릿하다)"
	case "fuzzy_ip":
		return "IP(흐릿하다)"
	case "fuzzy_url":
		return "URL(흐릿하다)"
	case "cidr":
		return "CIDR 네트워크 세그먼트"
	}
	return kind
}

// Reason 읽을 수 있는 적중 이유를 반환합니다.，모양은 다음과 같습니다：자산 차단 규칙 적중 [도메인 이름(흐릿하다): .gov.cn]（비고）。
func (r AssetInterceptRule) Reason() string {
	s := fmt.Sprintf("자산 차단 규칙 적중 [%s: %s]", AssetInterceptKindLabel(r.Kind), r.Pattern)
	if note := strings.TrimSpace(r.Note); note != "" {
		s += "（" + note + "）"
	}
	return s
}

// matchOne 활성화된 단일 규칙이 특정 도메인 이름에 적용되는지 확인/IP/URL 후보 문자열，히트의 특정 값을 반환합니다.。
func matchOne(r AssetInterceptRule, domains, ips, urls []string) (string, bool) {
	p := strings.TrimSpace(r.Pattern)
	if p == "" {
		return "", false
	}
	switch r.Kind {
	case "exact_domain":
		for _, d := range domains {
			if strings.EqualFold(strings.TrimSpace(d), p) {
				return d, true
			}
		}
	case "exact_ip":
		for _, ip := range ips {
			if strings.TrimSpace(ip) == p {
				return ip, true
			}
		}
	case "exact_url":
		for _, u := range urls {
			if strings.TrimSpace(u) == p {
				return u, true
			}
		}
	case "fuzzy_domain":
		lp := strings.ToLower(p)
		for _, d := range domains {
			if d != "" && strings.Contains(strings.ToLower(d), lp) {
				return d, true
			}
		}
	case "fuzzy_ip":
		for _, ip := range ips {
			if ip != "" && strings.Contains(ip, p) {
				return ip, true
			}
		}
	case "fuzzy_url":
		lp := strings.ToLower(p)
		for _, u := range urls {
			if u != "" && strings.Contains(strings.ToLower(u), lp) {
				return u, true
			}
		}
	case "cidr":
		_, ipnet, err := net.ParseCIDR(p)
		if err != nil {
			return "", false
		}
		for _, ip := range ips {
			if pip := net.ParseIP(strings.TrimSpace(ip)); pip != nil && ipnet.Contains(pip) {
				return ip, true
			}
		}
	}
	return "", false
}

// MatchAssetInterceptRules 주어진 첫 번째 히트를 반환합니다. 도메인 이름/IP/URL 후보 문자열 활성화 규칙，및 조회의 구체적인 값。
//
//	insert_assets 원시 입력 사용（아직 재고가 없습니다 assetInputItem）일치。
func MatchAssetInterceptRules(rules []AssetInterceptRule, domains, ips, urls []string) (AssetInterceptRule, string, bool) {
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		if v, ok := matchOne(r, domains, ips, urls); ok {
			return r, v, true
		}
	}
	return AssetInterceptRule{}, "", false
}

// interceptCandidates 차단 매칭을 위해 드롭된 자산을 추출합니다. 도메인 이름/IP/URL 후보 문자열。
// URL 님 host 을 분리하여 분류하겠습니다.，만드세요「지참만 하세요 URL」의 서비스 자산도 가능합니다. 도메인 이름/IP 규칙 적중。
func (a *Asset) interceptCandidates() (domains, ips, urls []string) {
	add := func(dst *[]string, s string) {
		if s = strings.TrimSpace(s); s != "" {
			*dst = append(*dst, s)
		}
	}
	add(&domains, a.Domain)
	add(&domains, a.RootDomain)
	for _, d := range a.BoundDomains {
		add(&domains, d)
	}
	add(&ips, a.IP)
	add(&urls, a.URL)
	if a.URL != "" {
		if u, err := url.Parse(a.URL); err == nil {
			if h := u.Hostname(); h != "" {
				if net.ParseIP(h) != nil {
					add(&ips, h)
				} else {
					add(&domains, h)
				}
			}
		}
	}
	return domains, ips, urls
}

// InterceptLabel 자산의 짧은 식별자를 반환합니다.，는 다음과 같이 사용됩니다. agent 의 설명 메시지。
func (a *Asset) InterceptLabel() string {
	var target string
	switch {
	case a.Domain != "":
		target = a.Domain
	case a.URL != "":
		target = a.URL
	case a.IP != "":
		target = a.IP
	default:
		target = fmt.Sprintf("#%d", a.ID)
	}
	return fmt.Sprintf("자산#%d[%s] %s", a.ID, a.Type, target)
}

// hasEnabledRule 규칙 세트에 활성화된 규칙이 있는지 확인합니다.。
func hasEnabledRule(rules []AssetInterceptRule) bool {
	for _, r := range rules {
		if r.Enabled {
			return true
		}
	}
	return false
}

// AssetGateDecision 네「먼저 차단하고 허용」일련의 후보 문자열에 대한 게이트의 판단 결과。
type AssetGateDecision struct {
	Allowed bool
	Reason  string // 거절 이유（자산 식별자가 포함되어 있지 않습니다.）；Allowed=true 이 비어 있습니다.
}

// EvaluateAssetGate 작업 수준 게이트 결정 실행：
//  1. 활성화된 항목을 누르세요. blockRules → 거부（차단 이유）。
//  2. 그렇지 않으면 allowRules 활성화된 항목이 있는데 적중된 항목이 없습니다. → 거부（허용되지 않음）。
//  3. 그렇지 않으면 공개됩니다。
//
// allowRules 이 비어 있습니다./활성화된 항목이 없는 경우，게이트가 적용되지 않도록 허용（즉, 화이트리스트가 활성화되어 있지 않습니다.，모두 공개），
// 피하세요「허용 규칙이 구성되지 않았습니다.」모든 자산 차단。
func EvaluateAssetGate(blockRules, allowRules []AssetInterceptRule, domains, ips, urls []string) AssetGateDecision {
	if rule, _, ok := MatchAssetInterceptRules(blockRules, domains, ips, urls); ok {
		return AssetGateDecision{Allowed: false, Reason: rule.Reason()}
	}
	if hasEnabledRule(allowRules) {
		if _, _, ok := MatchAssetInterceptRules(allowRules, domains, ips, urls); !ok {
			return AssetGateDecision{Allowed: false, Reason: "작업에 허용되지 않음(화이트리스트)범위 내，테스트가 허용되지 않습니다."}
		}
	}
	return AssetGateDecision{Allowed: true}
}

// AssetInterceptHit 게이트에서 거부된 자산을 설명합니다.（가로채기 적중 또는 허용되지 않음）。
type AssetInterceptHit struct {
	Asset  *Asset
	Reason string // 읽을 수 있는 이유
}

// Describe 읽을 수 있는 설명을 반환합니다.：자산정보 + 이유。
func (h AssetInterceptHit) Describe() string {
	return fmt.Sprintf("%s → %s", h.Asset.InterceptLabel(), h.Reason)
}

// ListAssetInterceptRules 네 *DB 동일한 이름의 메소드를 투명하게 전송，붙잡기만 하자 AssetStore 의 발신자
// （ agent 도구）도 규칙을 읽을 수 있습니다.。
func (s *AssetStore) ListAssetInterceptRules() ([]AssetInterceptRule, error) {
	return s.db.ListAssetInterceptRules()
}

// CheckAssetsIntercept 언론 id 자산 로드，하나씩 실행해 보세요「먼저 차단하고 허용」게이트 판단，모두 반환
// 거부된 자산。차단 규칙 = 글로벌 ∪ 작업 수준 block；허용 규칙 = 작업 수준 allow（이 작업만）。
// 없음 id 하면 빨리 돌아오세요.。전역 사용 GetByIDs（작업 범위로 필터링되지 않음）가로채지 않도록 하기 위해 scope 약화됨。
func (s *AssetStore) CheckAssetsIntercept(taskID int64, ids []int64) ([]AssetInterceptHit, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	blockRules, err := s.db.ListAssetInterceptRules()
	if err != nil {
		return nil, err
	}
	var allowRules []AssetInterceptRule
	if taskID > 0 {
		tb, ta, err := s.TaskInterceptRulesSplit(taskID)
		if err != nil {
			return nil, err
		}
		blockRules = append(blockRules, tb...)
		allowRules = ta
	}
	// 차단규칙은 없습니다、활성화된 허용 규칙도 없습니다. → 판단 불필요，모두 공개。
	if len(blockRules) == 0 && !hasEnabledRule(allowRules) {
		return nil, nil
	}
	assets, err := s.GetByIDs(ids)
	if err != nil {
		return nil, err
	}
	var hits []AssetInterceptHit
	for _, a := range assets {
		domains, ips, urls := a.interceptCandidates()
		if d := EvaluateAssetGate(blockRules, allowRules, domains, ips, urls); !d.Allowed {
			hits = append(hits, AssetInterceptHit{Asset: a, Reason: d.Reason})
		}
	}
	return hits, nil
}
