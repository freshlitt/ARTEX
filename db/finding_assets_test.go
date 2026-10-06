package db

import (
	"strconv"
	"strings"
	"testing"
)

// cleanupTreeFixtures 사용 사례에서 생성된 자산 및 결과 삭제。필수 defer 등록(대신
// t.Cleanup):t.Cleanup 테스트 함수 반환 후 실행,그때는 defer d.Close() 연결이 끊어졌습니다,
// 정리가 자동으로 실패하고 공유 개발 라이브러리에 더러운 데이터가 남습니다.。
func cleanupTreeFixtures(d *DB, taskID int64, rootDomains ...string) {
	d.Exec(`DELETE FROM assets WHERE root_domain = ANY($1::text[])`, rootDomains) //nolint:errcheck
	d.DeleteFindingsByTask(taskID)                                                //nolint:errcheck
}

// seedTreeAsset inserts one asset row.
func seedTreeAsset(t *testing.T, d *DB, kind string, cols map[string]any) int64 {
	t.Helper()
	names := []string{"type"}
	values := []any{kind}
	placeholders := []string{"$1"}
	for k, v := range cols {
		values = append(values, v)
		names = append(names, k)
		placeholders = append(placeholders, "$"+strconv.Itoa(len(values)))
	}
	q := "INSERT INTO assets(" + strings.Join(names, ",") + ") VALUES (" +
		strings.Join(placeholders, ",") + ") RETURNING id"
	var id int64
	if err := d.QueryRow(q, values...).Scan(&id); err != nil {
		t.Fatalf("seed %s asset: %v", kind, err)
	}
	return id
}

func nodeByKey(tree *FindingAssetTree, key string) *FindingAssetNode {
	for i := range tree.Nodes {
		if tree.Nodes[i].Key == key {
			return &tree.Nodes[i]
		}
	}
	return nil
}

// TestBuildFindingAssetTree covers the whole shape of the「자산별」tree: the
// root→subdomain→service→endpoint chain gets rebuilt from a finding that only
// points at the leaf, ancestors aggregate their subtree, assets without any
// finding stay out, and a finding whose asset row is gone lands in the
// unassigned bucket.
func TestBuildFindingAssetTree(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	tk, err := d.CreateTask("자산 트리 테스트", "대상", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(tk.ID)

	const root = "tree-test.example"
	const sub = "api.tree-test.example"
	defer cleanupTreeFixtures(d, tk.ID, root)
	rootID := seedTreeAsset(t, d, "root_domain", map[string]any{"domain": root, "root_domain": root})
	subID := seedTreeAsset(t, d, "subdomain", map[string]any{"domain": sub, "root_domain": root})
	svcID := seedTreeAsset(t, d, "service", map[string]any{
		"domain": sub, "root_domain": root, "url": "https://" + sub, "port": 443, "service_type": "http",
	})
	epID := seedTreeAsset(t, d, "endpoint", map[string]any{
		"domain": sub, "root_domain": root, "url": "https://" + sub + "/admin", "port": 443, "method": "GET",
	})
	// 동일한 도메인 이름의 다른 서비스,발견에 매달리지 마세요 —— 트리에 나타나지 않아야 합니다.。
	seedTreeAsset(t, d, "service", map[string]any{
		"domain": sub, "root_domain": root, "url": "http://" + sub + ":8080", "port": 8080, "service_type": "http",
	})

	// 가장 깊은 발견에만 매달리세요 endpoint 에,닥나무 자체로 조상 사슬을 수리해야합니다。
	if _, err := d.AddFinding(tk.ID, 0, "XSS", "반사형 XSS", "high", "s", "e", "w", []int64{epID}); err != nil {
		t.Fatal(err)
	}
	// 서비스로 바로 연결되는 링크,을 사용하여 확인합니다. Self 그리고 Total 의 차이점。
	if _, err := d.AddFinding(tk.ID, 0, "Info", "정보 유출", "low", "s", "e", "w", []int64{svcID}); err != nil {
		t.Fatal(err)
	}
	// 자산 행이 존재하지 않습니다.(자산이 삭제되었습니다.)→ 연결되지 않은 버킷。
	if _, err := d.AddFinding(tk.ID, 0, "Misc", "고아", "medium", "s", "e", "w", []int64{999000111}); err != nil {
		t.Fatal(err)
	}

	tree, err := d.BuildFindingAssetTree(FindingFilter{TaskID: strconv.FormatInt(tk.ID, 10)})
	if err != nil {
		t.Fatal(err)
	}
	if tree.FindingTotal != 3 {
		t.Fatalf("finding_total: want 3, got %d", tree.FindingTotal)
	}

	rootNode := nodeByKey(tree, assetKey(rootID))
	subNode := nodeByKey(tree, assetKey(subID))
	svcNode := nodeByKey(tree, assetKey(svcID))
	epNode := nodeByKey(tree, assetKey(epID))
	for name, n := range map[string]*FindingAssetNode{
		"root": rootNode, "subdomain": subNode, "service": svcNode, "endpoint": epNode,
	} {
		if n == nil {
			t.Fatalf("%s node missing from tree", name)
		}
	}

	// 아버지-아들 체인:endpoint → service → subdomain → root_domain。
	if epNode.Parent != svcNode.Key {
		t.Errorf("endpoint parent: want %s, got %s", svcNode.Key, epNode.Parent)
	}
	if svcNode.Parent != subNode.Key {
		t.Errorf("service parent: want %s, got %s", subNode.Key, svcNode.Parent)
	}
	if subNode.Parent != rootNode.Key {
		t.Errorf("subdomain parent: want %s, got %s", rootNode.Key, subNode.Parent)
	}
	if rootNode.Parent != "" {
		t.Errorf("root parent: want top level, got %s", rootNode.Parent)
	}

	// 집계:두 개의 루트 도메인 이름(endpoint 님 high + service 님 low),service 자신、두 개의 하위 트리。
	if rootNode.Total != 2 || rootNode.High != 1 || rootNode.Low != 1 {
		t.Errorf("root totals: want 2/high1/low1, got %d/high%d/low%d", rootNode.Total, rootNode.High, rootNode.Low)
	}
	if rootNode.Self != 0 {
		t.Errorf("root self: want 0 (그냥 조상님), got %d", rootNode.Self)
	}
	if svcNode.Total != 2 || svcNode.Self != 1 {
		t.Errorf("service total/self: want 2/1, got %d/%d", svcNode.Total, svcNode.Self)
	}
	if epNode.Total != 1 || epNode.Self != 1 {
		t.Errorf("endpoint total/self: want 1/1, got %d/%d", epNode.Total, epNode.Self)
	}

	// 찾지 못한 형제는 나무에 들어갈 수 없게 됩니다.。
	for _, n := range tree.Nodes {
		if n.Label == "http://"+sub+":8080" {
			t.Errorf("asset without findings should be hidden: %+v", n)
		}
	}

	// 연결되지 않은 버킷은 삭제된 자산을 가리키는 검색을 수락합니다.。
	none := nodeByKey(tree, FindingUnassignedAsset)
	if none == nil || none.Total != 1 || none.Medium != 1 {
		t.Fatalf("unassigned bucket: want 1 medium, got %+v", none)
	}
}

// TestFindingAssetScopeFilter verifies노드를 선택하세요 narrows the findings list to
// that node's whole subtree, and that the unassigned sentinel works too.
func TestFindingAssetScopeFilter(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	tk, err := d.CreateTask("자산 선별 테스트", "대상", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(tk.ID)

	const root = "scope-test.example"
	const sub = "api.scope-test.example"
	const other = "other-scope-test.example"
	defer cleanupTreeFixtures(d, tk.ID, root, other)
	rootID := seedTreeAsset(t, d, "root_domain", map[string]any{"domain": root, "root_domain": root})
	subID := seedTreeAsset(t, d, "subdomain", map[string]any{"domain": sub, "root_domain": root})
	otherID := seedTreeAsset(t, d, "root_domain", map[string]any{"domain": other, "root_domain": other})

	if _, err := d.AddFinding(tk.ID, 0, "A", "하위 도메인에", "high", "s", "e", "w", []int64{subID}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddFinding(tk.ID, 0, "B", "", "high", "s", "e", "w", []int64{otherID}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddFinding(tk.ID, 0, "C", "자산 없음", "high", "s", "e", "w", nil); err != nil {
		t.Fatal(err)
	}
	// 삭제된 자산을 가리키는 검색,그리고 asset_ids 비어 있으면 다음에 속합니다.「관련되지 않음」——나무통이 그것을 받아들인다,
	// 목록 필터링도 알아낼 수 있어야 합니다.,두 구경이 일치하지 않으면 배럴에 표시된 숫자가 클릭한 후의 숫자보다 커집니다.。
	if _, err := d.AddFinding(tk.ID, 0, "D", "자산이 삭제되었습니다.", "high", "s", "e", "w", []int64{999000333}); err != nil {
		t.Fatal(err)
	}

	base := FindingFilter{TaskID: strconv.FormatInt(tk.ID, 10)}
	cases := []struct {
		name  string
		scope string
		want  int
	}{
		{"전체 하위 트리", assetKey(rootID), 1},      // 루트 도메인 이름 아래에 하위 도메인 이름만 있습니다.
		{"리프 노드", assetKey(subID), 1},          // 하위 도메인 이름 자체
		{"또 다른 나무", assetKey(otherID), 1},      // 서로 길을 건너지 마세요
		{"관련되지 않음", FindingUnassignedAsset, 2}, // asset_ids 이 비어 있습니다. + 은 삭제된 자산을 가리킵니다.
		{"존재하지 않는 노드", "a:999000222", 0},       // 현재 필터링 중인 노드가 없습니다. → 빈 결과,필터링을 안하는건 아닙니다
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := base
			f.AssetScope = tc.scope
			items, total, err := d.ListFindingsPage(f, 1, 50)
			if err != nil {
				t.Fatal(err)
			}
			if total != tc.want || len(items) != tc.want {
				t.Fatalf("scope %s: want %d findings, got total=%d items=%d", tc.scope, tc.want, total, len(items))
			}
		})
	}

	// 포함되지 않음 scope 시조시죠가 왔어요。
	if _, total, err := d.ListFindingsPage(base, 1, 50); err != nil || total != 4 {
		t.Fatalf("unscoped: want 4, got %d (%v)", total, err)
	}

	// 트리의 연결되지 않은 버킷 수는 클릭한 후 발견된 수와 일치해야 합니다. —— 두 구경이 분리되면 붕괴된다는 주장이 바로 그것이다.。
	tree, err := d.BuildFindingAssetTree(base)
	if err != nil {
		t.Fatal(err)
	}
	none := nodeByKey(tree, FindingUnassignedAsset)
	if none == nil || none.Total != 2 {
		t.Fatalf("unassigned bucket count: want 2, got %+v", none)
	}
}
