package db

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Finding asset tree — the「자산별」view of the global findings list.
//
// 레벨과 BuildCoverageGraph 같은 출신(company → root_domain/ip/app → subdomain →
// service → endpoint),그런데 그 몫은「업무 범위 내의 자산」의 힘 방향 다이어그램,이건
// 「전체 라이브러리에서 자산이 발견되었습니다.」의 나무:발견된 자산과 해당 상위 체인만 허용됩니다.,하위 트리 집계 수가 있는 노드。
// 둘의 부모-자식 우선순위 규칙은 일관되어야 합니다.,변경사항이 있는지 확인해주세요 task_scope.go 다른 곳으로 바꿔주세요。
// ---------------------------------------------------------------------------

// FindingUnassignedAsset 네「연결되지 않은 자산」노드 key,은 목록 인터페이스의 필터 센티널이기도 합니다.:
// 히트 asset_ids 이 비어 있습니다.、또는 참조된 자산이 삭제되었다는 발견。
const FindingUnassignedAsset = "__none__"

// findingAssetTreeMaxNodes 은 프런트 엔드로 반환되는 노드의 상한입니다.。초과시 레이어 전체를 아래에서 위로 폐기
// (endpoint 우선순위,둘째 service):해당 개수가 상위 노드에 누적되었습니다.,노드를 잃어도 숫자는 잃지 않습니다.。
const findingAssetTreeMaxNodes = 3000

// FindingAssetNode 은 자산 트리의 노드입니다.。Key 커버리지 그래프와 동형:자산 행은 다음과 같습니다. "a:<id>"、
// 회사는 "c:<id>"、자산 행이 없는 루트 도메인 이름은 합성입니다. "r:<domain>"、연결되지 않은 버킷은 다음과 같습니다. "__none__"。
type FindingAssetNode struct {
	Key       string `json:"key"`
	Parent    string `json:"parent,omitempty"`
	Kind      string `json:"kind"` // company|root_domain|subdomain|ip|service|app|endpoint|none
	Label     string `json:"label"`
	AssetID   int64  `json:"asset_id,omitempty"`
	CompanyID int64  `json:"company_id,omitempty"`
	// Self 은 자산에 직접 연결된 검색 번호입니다.;Total 모든 하위 항목을 포함하고 누릅니다. finding 중복 제거
	// (다수의 자산이 첨부된 것이 발견된 경우,공통 조상은 한 번만 계산합니다.)。
	Self        int       `json:"self"`
	Total       int       `json:"total"`
	Critical    int       `json:"critical"`
	High        int       `json:"high"`
	Medium      int       `json:"medium"`
	Low         int       `json:"low"`
	LastFoundAt time.Time `json:"last_found_at"`
}

// FindingAssetTree 은 전체 트리의 일회성 스냅샷입니다.。Nodes 정렬됨:동일한 상위 노드 아래의 검색 수
// 내림차순、라벨 오름차순,「연결되지 않은 자산」언제나 마지막엔。
type FindingAssetTree struct {
	Nodes        []FindingAssetNode `json:"nodes"`
	FindingTotal int                `json:"finding_total"`
	// Truncated=true 은 볼륨 조절을 위해 폐기되었음을 의미합니다. DroppedKinds 의 레벨。
	Truncated    bool     `json:"truncated"`
	DroppedKinds []string `json:"dropped_kinds,omitempty"`
}

// assetRow 은 Mulberry에서 요구하는 자산 필드의 하위 집합입니다.。
type assetRow struct {
	id          int64
	kind        string
	companyID   int64
	domain      string
	rootDomain  string
	ip          string
	url         string
	port        int
	serviceType string
	appName     string
}

func (a *assetRow) coverageNode() CoverageGraphNode {
	return CoverageGraphNode{
		Kind: a.kind, Domain: a.domain, RootDomain: a.rootDomain, IP: a.ip,
		URL: a.url, Port: a.port, ServiceType: a.serviceType, AppName: a.appName,
	}
}

// label 오버레이에 대한 태그 규칙 재사용(URL > domain > ip > app_name > root_domain),하지만 안돼
// URL 님의 서비스(SMB、아니요 HTTP 포트 등)포트 추가 필요:그렇지 않으면 해당 레이블은 호스트와 동일합니다. IP/도메인 이름 줄
// 똑같습니다,나무 위의 두 줄의 아버지와 아들은 완전히 복제된 것처럼 보입니다.。
func (a *assetRow) label() string {
	if a.kind == "service" && a.url == "" {
		if host, port := a.hostPort(); host != "" && port > 0 {
			return host + ":" + strconv.Itoa(port)
		}
	}
	n := a.coverageNode()
	n.Key = assetKey(a.id)
	return coverageNodeLabel(&n)
}

// hostPort 적용 범위 지도와 일치:우선순위 domain,둘째 URL 에 host,드디어 ip。
func (a *assetRow) hostPort() (string, int) {
	n := a.coverageNode()
	return hostPortOf(&n)
}

const findingAssetSelectCols = `a.id, a.type, COALESCE(a.company_id,0),
       COALESCE(a.domain,''), COALESCE(a.root_domain,''), COALESCE(a.ip,''),
       COALESCE(a.url,''), COALESCE(a.port,0), COALESCE(a.service_type,''),
       COALESCE(a.app_name,'')`

func scanAssetRows(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}) ([]*assetRow, error) {
	defer rows.Close()
	var out []*assetRow
	for rows.Next() {
		a := &assetRow{}
		if err := rows.Scan(&a.id, &a.kind, &a.companyID, &a.domain, &a.rootDomain,
			&a.ip, &a.url, &a.port, &a.serviceType, &a.appName); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// findingAssetHit 은 트리 구축 단계를 찾는 데 필요한 최소한의 메시지입니다.。
type findingAssetHit struct {
	severity string
	ts       time.Time
	assetIDs []int64
}

// BuildFindingAssetTree 현재 필터에 따라 자산 트리 구축。AssetScope 참여하지 않겠습니다(그렇지 않으면 트리는
// 선택한 노드가 체인으로 축소됩니다.)。
func (d *DB) BuildFindingAssetTree(f FindingFilter) (*FindingAssetTree, error) {
	return d.buildFindingAssetTree(f, findingAssetTreeMaxNodes)
}

// buildFindingAssetTree 은 노드 상한이 있는 내부 구현입니다.。maxNodes<=0 은 잘림이 없음을 의미합니다.——분석
// AssetScope 일 때 사용해야 합니다.,그렇지 않으면 버려질 것이다 endpoint 하위 트리를 만듭니다. id 불완전한 수집。
func (d *DB) buildFindingAssetTree(f FindingFilter, maxNodes int) (*FindingAssetTree, error) {
	f.AssetScope = ""
	f.assetIDs, f.assetNone, f.assetMiss = nil, false, false
	where, args := f.where()

	rows, err := d.Query(`SELECT COALESCE(f.severity,''), f.created_at,
       COALESCE(f.asset_ids::text,'[]')
FROM findings f LEFT JOIN tasks t ON f.task_id = t.id`+where, args...)
	if err != nil {
		return nil, err
	}
	hits := []findingAssetHit{}
	assetIDs := map[int64]bool{}
	for rows.Next() {
		var h findingAssetHit
		var aidsJSON string
		if err := rows.Scan(&h.severity, &h.ts, &aidsJSON); err != nil {
			rows.Close()
			return nil, err
		}
		_ = json.Unmarshal([]byte(aidsJSON), &h.assetIDs)
		for _, id := range h.assetIDs {
			if id > 0 {
				assetIDs[id] = true
			}
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	tree := &FindingAssetTree{Nodes: []FindingAssetNode{}, FindingTotal: len(hits)}
	byID, err := d.loadFindingAssetRows(assetIDs)
	if err != nil {
		return nil, err
	}

	nodes, parentOf := d.assembleFindingAssetNodes(byID)
	if err := d.attachCompanyNodes(nodes, parentOf); err != nil {
		return nil, err
	}

	// 수:각 자산의 조상을 따라 연결되는 검색 체인,중복된 것을 모아라 key 하나씩 모아보세요 +1,
	// 따라서 하나의 검색에 여러 하위 자산이 연결되어 있으므로 상위 노드는 반복적으로 계산되지 않습니다.。
	unassigned := &FindingAssetNode{Key: FindingUnassignedAsset, Kind: "none", Label: "연결되지 않은 자산"}
	touched := map[string]bool{}
	for _, h := range hits {
		clear(touched)
		var direct []*FindingAssetNode
		for _, id := range h.assetIDs {
			node := nodes[assetKey(id)]
			if node == nil {
				continue
			}
			direct = append(direct, node)
			for key := node.Key; key != ""; key = parentOf[key] {
				touched[key] = true
			}
		}
		if len(direct) == 0 {
			countFinding(unassigned, h)
			unassigned.Self++
			continue
		}
		for _, node := range direct {
			node.Self++
		}
		for key := range touched {
			countFinding(nodes[key], h)
		}
	}

	for _, node := range nodes {
		if node.Total > 0 {
			tree.Nodes = append(tree.Nodes, *node)
		}
	}
	if unassigned.Total > 0 {
		tree.Nodes = append(tree.Nodes, *unassigned)
	}
	sortFindingAssetNodes(tree.Nodes)
	truncateFindingAssetTree(tree, maxNodes)
	return tree, nil
}

// countFinding 노드에 발견을 축적(합계 / 심각도 버킷팅 / 마지막 검색 시간)。
func countFinding(n *FindingAssetNode, h findingAssetHit) {
	if n == nil {
		return
	}
	n.Total++
	switch h.severity {
	case "critical":
		n.Critical++
	case "high":
		n.High++
	case "medium":
		n.Medium++
	case "low":
		n.Low++
	}
	if h.ts.After(n.LastFoundAt) {
		n.LastFoundAt = h.ts
	}
}

// loadFindingAssetRows 히트 자산 행 읽기,그리고 차례대로 조상을 완성하세요(service 의 호스트 도메인 이름/IP、
// 하위 도메인 이름의 루트 도메인 이름)。조상님 자신은 아무것도 발견하지 못했을 수도 있습니다.,하지만 나무가 모양을 갖추려면 나무가 필요합니다.。
func (d *DB) loadFindingAssetRows(ids map[int64]bool) (map[int64]*assetRow, error) {
	byID := map[int64]*assetRow{}
	if len(ids) == 0 {
		return byID, nil
	}
	idList := make([]int64, 0, len(ids))
	for id := range ids {
		idList = append(idList, id)
	}
	rows, err := d.Query(`SELECT `+findingAssetSelectCols+` FROM assets a WHERE a.id = ANY($1::bigint[])`, idList)
	if err != nil {
		return nil, err
	}
	found, err := scanAssetRows(rows)
	if err != nil {
		return nil, err
	}
	for _, a := range found {
		byID[a.id] = a
	}

	// 각 라운드에서 누락된 상위 노드의 호스트 ID를 알아냅니다.,한 레이어씩 일괄 추가;레이어 수는 고정되어 있습니다.(endpoint→service→
	// subdomain/ip→root_domain),4 라운드가 충분히 수렴되었습니다.。
	for range 4 {
		want := missingParents(byID)
		if want.empty() {
			break
		}
		added, err := d.loadAssetsByHost(want, byID)
		if err != nil {
			return nil, err
		}
		if added == 0 {
			break
		}
	}
	return byID, nil
}

// missingHosts 보충학습 때 도서관에서 찾아야 할 호스트 ID입니다.,대상 자산 유형별로 구분。
type missingHosts struct {
	services []string // endpoint 의 호스트(찾는다 service 알았어)
	domains  []string // service/endpoint 의 호스트 도메인 이름(찾는다 subdomain 알았어)
	ips      []string // service/endpoint 의 호스트 IP(찾는다 ip 알았어)
	roots    []string // 하위 도메인 이름의 루트 도메인 이름(찾는다 root_domain 알았어)
}

func (m missingHosts) empty() bool {
	return len(m.services) == 0 && len(m.domains) == 0 && len(m.ips) == 0 && len(m.roots) == 0
}

// missingParents 아직 로드되지 않은 호스트를 요약합니다.:service( endpoint 소속)、하위 도메인 이름/IP(
// service 그리고 endpoint 소속)및 루트 도메인 이름(하위 도메인 이름 제휴 제공)。
func missingParents(byID map[int64]*assetRow) missingHosts {
	haveService := map[string]bool{}
	haveDomain := map[string]bool{}
	haveIP := map[string]bool{}
	haveRoot := map[string]bool{}
	for _, a := range byID {
		switch a.kind {
		case "service":
			if host, _ := a.hostPort(); host != "" {
				haveService[host] = true
			}
		case "subdomain":
			haveDomain[a.domain] = true
		case "ip":
			haveIP[a.ip] = true
		case "root_domain":
			haveRoot[a.domain] = true
		}
	}
	wantService := map[string]bool{}
	wantDomain := map[string]bool{}
	wantIP := map[string]bool{}
	wantRoot := map[string]bool{}
	for _, a := range byID {
		switch a.kind {
		case "service", "endpoint":
			host, _ := a.hostPort()
			// endpoint 호스트가 같은 사람을 먼저 찾아보세요 service;포트가 일치하지 않습니다. service 그럴게요 Total=0
			// 이 드디어 걸러졌습니다,나무를 오염시키지 않을 것입니다。
			if a.kind == "endpoint" && host != "" && !haveService[host] {
				wantService[host] = true
			}
			if host != "" && !haveDomain[host] && !haveIP[host] && !haveRoot[host] {
				if isIPLiteral(host) {
					wantIP[host] = true
				} else {
					wantDomain[host] = true
				}
			}
			if a.ip != "" && !haveIP[a.ip] {
				wantIP[a.ip] = true
			}
		case "subdomain":
			if a.rootDomain != "" && !haveRoot[a.rootDomain] {
				wantRoot[a.rootDomain] = true
			}
		}
	}
	return missingHosts{
		services: keysOf(wantService),
		domains:  keysOf(wantDomain),
		ips:      keysOf(wantIP),
		roots:    keysOf(wantRoot),
	}
}

func keysOf(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// isIPLiteral 대략적인 판단 host 그렇죠? IP 리터럴(가기로 결심하곤 했어요 ip 그래도 subdomain 호스트를 구합니다)。
func isIPLiteral(host string) bool {
	if strings.Contains(host, ":") {
		return true // IPv6
	}
	if host == "" {
		return false
	}
	for _, part := range strings.Split(host, ".") {
		if part == "" {
			return false
		}
		if _, err := strconv.Atoi(part); err != nil {
			return false
		}
	}
	return strings.Count(host, ".") == 3
}

// loadAssetsByHost 호스트 ID에 따라 일괄적으로 자산 행을 완료합니다.,이번 라운드의 새 행 수를 반환합니다.。
func (d *DB) loadAssetsByHost(want missingHosts, byID map[int64]*assetRow) (int, error) {
	added := 0
	load := func(q string, arg []string) error {
		if len(arg) == 0 {
			return nil
		}
		rows, err := d.Query(q, arg)
		if err != nil {
			return err
		}
		found, err := scanAssetRows(rows)
		if err != nil {
			return err
		}
		for _, a := range found {
			if _, ok := byID[a.id]; ok {
				continue
			}
			byID[a.id] = a
			added++
		}
		return nil
	}
	if err := load(`SELECT `+findingAssetSelectCols+` FROM assets a
WHERE a.type='service' AND (a.domain = ANY($1::text[]) OR a.ip = ANY($1::text[]))`, want.services); err != nil {
		return 0, err
	}
	if err := load(`SELECT `+findingAssetSelectCols+` FROM assets a
WHERE a.type='subdomain' AND a.domain = ANY($1::text[])`, want.domains); err != nil {
		return 0, err
	}
	if err := load(`SELECT `+findingAssetSelectCols+` FROM assets a
WHERE a.type='ip' AND a.ip = ANY($1::text[])`, want.ips); err != nil {
		return 0, err
	}
	if err := load(`SELECT `+findingAssetSelectCols+` FROM assets a
WHERE a.type='root_domain' AND a.domain = ANY($1::text[])`, want.roots); err != nil {
		return 0, err
	}
	return added, nil
}

// assembleFindingAssetNodes 자산 행을 노드로 전환하고 상위-하위 관계를 연결합니다.。상위 노드가 없는 경우(카레
// 그런 루트 도메인 이름 자산이 전혀 없습니다)합성 "r:<domain>" 자리표시자 노드,오버레이 처리와 일치。
func (d *DB) assembleFindingAssetNodes(byID map[int64]*assetRow) (map[string]*FindingAssetNode, map[string]string) {
	nodes := map[string]*FindingAssetNode{}
	parentOf := map[string]string{}
	rootByDomain := map[string]string{}
	subByDomain := map[string]string{}
	ipByAddr := map[string]string{}
	svcByHost := map[string]string{}
	svcByHostPort := map[string]string{}

	for _, a := range byID {
		key := assetKey(a.id)
		nodes[key] = &FindingAssetNode{
			Key: key, Kind: a.kind, Label: a.label(),
			AssetID: a.id, CompanyID: a.companyID,
		}
		switch a.kind {
		case "root_domain":
			if a.domain != "" {
				rootByDomain[a.domain] = key
			}
		case "subdomain":
			if a.domain != "" {
				subByDomain[a.domain] = key
			}
		case "ip":
			if a.ip != "" {
				ipByAddr[a.ip] = key
			}
		case "service":
			if host, port := a.hostPort(); host != "" {
				svcByHost[host] = key
				svcByHostPort[host+"|"+strconv.Itoa(port)] = key
			}
		}
	}

	// 하위 도메인 이름의 루트 도메인 이름이 라이브러리에 자산 행이 없는 경우,자리표시자 루트 합성,하위 도메인 이름이 최상위 수준으로 조각화되는 것을 방지。
	for _, a := range byID {
		if a.kind != "subdomain" || a.rootDomain == "" {
			continue
		}
		if _, ok := rootByDomain[a.rootDomain]; ok {
			continue
		}
		key := "r:" + a.rootDomain
		nodes[key] = &FindingAssetNode{Key: key, Kind: "root_domain", Label: a.rootDomain}
		rootByDomain[a.rootDomain] = key
	}

	firstOf := func(keys ...string) string {
		for _, k := range keys {
			if k != "" {
				if _, ok := nodes[k]; ok {
					return k
				}
			}
		}
		return ""
	}
	for _, a := range byID {
		key := assetKey(a.id)
		var parent string
		switch a.kind {
		case "subdomain":
			parent = firstOf(rootByDomain[a.rootDomain])
		case "service":
			host, _ := a.hostPort()
			parent = firstOf(subByDomain[a.domain], subByDomain[host],
				ipByAddr[a.ip], ipByAddr[host], rootByDomain[a.rootDomain], rootByDomain[host])
		case "endpoint":
			host, port := a.hostPort()
			parent = firstOf(svcByHostPort[host+"|"+strconv.Itoa(port)], svcByHost[host],
				subByDomain[host], subByDomain[a.domain], ipByAddr[host], ipByAddr[a.ip],
				rootByDomain[a.rootDomain], rootByDomain[host])
		}
		if parent != "" && parent != key {
			parentOf[key] = parent
			nodes[key].Parent = parent
		}
	}
	return nodes, parentOf
}

// attachCompanyNodes 최상위 자산으로(루트 도메인 이름 / IP / 신청)기업의 상위 노드를 보완합니다.——자산만이 참이다
// 기업에 속해 있는 경우에만 기업 레이어가 나타납니다.,소유하지 않은 자산은 여전히 최상위 계층입니다.。
func (d *DB) attachCompanyNodes(nodes map[string]*FindingAssetNode, parentOf map[string]string) error {
	want := map[int64]bool{}
	for _, n := range nodes {
		if n.Parent != "" || n.CompanyID <= 0 {
			continue
		}
		switch n.Kind {
		case "root_domain", "ip", "app":
			want[n.CompanyID] = true
		}
	}
	if len(want) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	rows, err := d.Query(`SELECT id, COALESCE(name,'') FROM companies WHERE id = ANY($1::bigint[])`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	names := map[int64]string{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return err
		}
		names[id] = name
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for id, name := range names {
		key := companyKey(id)
		if _, ok := nodes[key]; ok {
			continue
		}
		if name == "" {
			name = "기업 #" + strconv.FormatInt(id, 10)
		}
		nodes[key] = &FindingAssetNode{Key: key, Kind: "company", Label: name, CompanyID: id}
	}
	for _, n := range nodes {
		if n.Parent != "" || n.CompanyID <= 0 || n.Kind == "company" {
			continue
		}
		switch n.Kind {
		case "root_domain", "ip", "app":
			key := companyKey(n.CompanyID)
			if _, ok := nodes[key]; !ok {
				continue
			}
			n.Parent = key
			parentOf[n.Key] = key
		}
	}
	return nil
}

// sortFindingAssetNodes 정렬:더 많은 것을 먼저 알아보세요,같은 번호의 라벨을 눌러주세요;「연결되지 않은 자산」언제나 마지막엔。
// 프런트 엔드는 배열 순서대로 하위 노드를 정지합니다.,따라서 동일한 상위 노드 아래의 상대 순서가 정확하다면。
func sortFindingAssetNodes(nodes []FindingAssetNode) {
	sort.SliceStable(nodes, func(i, j int) bool {
		a, b := nodes[i], nodes[j]
		if (a.Kind == "none") != (b.Kind == "none") {
			return b.Kind == "none"
		}
		if a.Total != b.Total {
			return a.Total > b.Total
		}
		return a.Label < b.Label
	})
}

// truncateFindingAssetTree 노드가 너무 많으면 전체 레이어가 삭제됩니다.(먼저 endpoint 다시 service)。계산됨
// 상위 노드에 누적됨,잃어버린 것은 확장 가능한 세부 수준뿐입니다.。
func truncateFindingAssetTree(tree *FindingAssetTree, maxNodes int) {
	if maxNodes <= 0 || len(tree.Nodes) <= maxNodes {
		return
	}
	for _, kind := range []string{"endpoint", "service"} {
		kept := tree.Nodes[:0]
		for _, n := range tree.Nodes {
			if n.Kind == kind {
				continue
			}
			kept = append(kept, n)
		}
		tree.Nodes = kept
		tree.Truncated = true
		tree.DroppedKinds = append(tree.DroppedKinds, kind)
		if len(tree.Nodes) <= maxNodes {
			return
		}
	}
}

// applyAssetScope 넣어보세요 AssetScope(노드 key)은 다음으로 구문 분석됩니다. SQL 의 자산 id 컬렉션。선택됨
// 노드는 이를 선택하는 전체 하위 트리와 동일합니다.,그래서 먼저 트리를 구축한 후 자손을 수집해야 합니다.。
func (d *DB) applyAssetScope(f FindingFilter) (FindingFilter, error) {
	scope := strings.TrimSpace(f.AssetScope)
	f.assetIDs, f.assetNone, f.assetMiss = nil, false, false
	if scope == "" {
		return f, nil
	}
	if scope == FindingUnassignedAsset {
		f.assetNone = true
		return f, nil
	}
	// 잘림 없음:버려졌습니다 endpoint 도 참여합니다 id 컬렉션,그렇지 않으면 목록의 데이터가 적어집니다.。
	tree, err := d.buildFindingAssetTree(f, 0)
	if err != nil {
		return f, err
	}
	children := map[string][]FindingAssetNode{}
	byKey := map[string]FindingAssetNode{}
	for _, n := range tree.Nodes {
		byKey[n.Key] = n
		children[n.Parent] = append(children[n.Parent], n)
	}
	if _, ok := byKey[scope]; !ok {
		// 선택한 노드가 현재 필터 아래에 더 이상 존재하지 않습니다.,결과는 필터링 없음으로 변질되는 대신 비어 있어야 합니다.。
		f.assetMiss = true
		return f, nil
	}
	seen := map[string]bool{scope: true}
	queue := []string{scope}
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		if id := byKey[key].AssetID; id > 0 {
			f.assetIDs = append(f.assetIDs, id)
		}
		for _, child := range children[key] {
			if seen[child.Key] {
				continue
			}
			seen[child.Key] = true
			queue = append(queue, child.Key)
		}
	}
	if len(f.assetIDs) == 0 {
		f.assetMiss = true
	}
	return f, nil
}

// assetIDContainments 자산을 넣어 id 이 됩니다. jsonb 판단의 올바른 피연산자 집합이 포함되어 있습니다.,협력
// idx_findings_asset_ids(GIN jsonb_path_ops)사용。
func assetIDContainments(ids []int64) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, "["+strconv.FormatInt(id, 10)+"]")
	}
	return out
}
