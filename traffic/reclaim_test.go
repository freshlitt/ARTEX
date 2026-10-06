package traffic

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bulkRecord fills the index with inline bodies — the ones that actually make
// index.sqlite grow. Binary content type keeps them out of the full-text index so
// the test stays fast; the FTS side is covered by TestReclaimMergesFTSTombstones.
func bulkRecord(tr *Traffic, host string, n, size int) {
	body := []byte(strings.Repeat("A", size))
	for i := 0; i < n; i++ {
		tr.record(newFlow(host, "GET", fmt.Sprintf("/blob/%d", i), nil, body,
			withRespType("application/octet-stream")))
	}
}

func TestNewIndexEnablesIncrementalVacuum(t *testing.T) {
	tr, _ := openTraffic(t)
	if !tr.incrementalVacuum {
		t.Fatal("새 인덱스 라이브러리는 증분 재활용을 활성화하지 않습니다.")
	}
	var mode int
	if err := tr.DB().QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != autoVacuumIncremental {
		t.Fatalf("auto_vacuum=%d，이어야 합니다. %d", mode, autoVacuumIncremental)
	}
}

// TestDeleteReclaimsIndexSpace is the regression: deleting traffic used to leave
// index.sqlite at its high-water mark forever, because SQLite only chains freed
// pages onto its freelist and nothing ever returned them to the filesystem.
func TestDeleteReclaimsIndexSpace(t *testing.T) {
	tr, _ := openTraffic(t)
	const host = "bulk.example.com"
	// 30 × 200KB stays under maxInlineBody, so every body lands in the database
	// itself rather than the blob store — that is where the growth was invisible.
	bulkRecord(tr, host, 30, 200*1024)
	grown := tr.indexBytes()
	if grown < 5<<20 {
		t.Fatalf("인덱스만 %d 바이트，복구를 확인하기에는 샘플이 부족합니다.", grown)
	}

	if n, err := tr.DeleteHostsExact([]string{host}); err != nil || n != 30 {
		t.Fatalf("DeleteHostsExact=(%d,%v)，이어야 합니다. (30,nil)", n, err)
	}
	tr.reaping.Wait() // 재활용은 백그라운드에서 청크로 수행됩니다.

	after := tr.indexBytes()
	if after > grown/4 {
		t.Fatalf("삭제 후에도 인덱스가 계속 점유됩니다. %d 바이트（삭제 전 %d），파일 시스템에 공간이 반환되지 않습니다.", after, grown)
	}
	// A handful of pages incremental_vacuum could not move to the end of the file
	// is a normal residual; the ~1500 that the deletion freed must be gone.
	var free int
	if err := tr.DB().QueryRow(`PRAGMA freelist_count`).Scan(&free); err != nil {
		t.Fatal(err)
	}
	if free > 64 {
		t.Fatalf("아직은 %d 무료 페이지는 재활용되지 않습니다.", free)
	}
}

// TestReclaimMergesFTSTombstones covers the second half of the leak: ex_fts is a
// contentless_delete index, so a DELETE only writes tombstones. Without a merge
// the index keeps growing on every deletion — deleting traffic made it bigger.
func TestReclaimMergesFTSTombstones(t *testing.T) {
	tr, _ := openTraffic(t)
	if !tr.fts {
		t.Skip("드라이버가 활성화되지 않았습니다. FTS5")
	}
	// Deleted in batches, which is what leaves tombstones spread over many
	// segments rather than emptying the index in one shot.
	for round := 0; round < 4; round++ {
		host := fmt.Sprintf("fts%d.example.com", round)
		for i := 0; i < 20; i++ {
			tr.record(newFlow(host, "GET", fmt.Sprintf("/p/%d", i), nil,
				[]byte(strings.Repeat("secret token 중국어 텍스트 padding ", 200))))
		}
		if _, err := tr.DeleteHostsExact([]string{host}); err != nil {
			t.Fatal(err)
		}
		tr.reaping.Wait()
	}

	var exchanges, segments int
	if err := tr.DB().QueryRow(`SELECT COUNT(*) FROM exchanges`).Scan(&exchanges); err != nil {
		t.Fatal(err)
	}
	if err := tr.DB().QueryRow(`SELECT COUNT(*) FROM ex_fts_data`).Scan(&segments); err != nil {
		t.Fatal(err)
	}
	if exchanges != 0 {
		t.Fatalf("아직 남았네요 %d 교통", exchanges)
	}
	// A fully merged, empty contentless index keeps only its structure rows.
	if segments > 8 {
		t.Fatalf("전체 텍스트 인덱스 남음 %d 선분 데이터，tombstone 병합하지 않고 재활용함", segments)
	}
}

// TestReclaimOnLegacyIndexIsHarmless covers installs created before
// auto_vacuum=incremental became the default: incremental_vacuum is a silent
// no-op there, so reclamation must report the situation and finish rather than
// spin or fail. Only a full compaction can convert such a file.
func TestReclaimOnLegacyIndexIsHarmless(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "_index"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Create the tables first, with auto_vacuum left at its default 0 — exactly the
	// shape Open used to leave behind.
	legacy, err := sql.Open("sqlite", filepath.Join(dir, "_index", "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(indexSchema); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tr.Close() })
	if tr.incrementalVacuum {
		t.Fatal("오래된 라이브러리는 증분 재활용이 활성화되었다고 보고하면 안 됩니다.")
	}

	const host = "legacy.example.com"
	bulkRecord(tr, host, 8, 200*1024)
	if n, err := tr.DeleteHostsExact([]string{host}); err != nil || n != 8 {
		t.Fatalf("DeleteHostsExact=(%d,%v)，이어야 합니다. (8,nil)", n, err)
	}
	tr.reaping.Wait() // 수렴해야 함，예산에 얽매이지 마세요

	// The freelist stays populated: that is the whole reason a compaction entry
	// point is needed for pre-existing databases.
	var free int
	if err := tr.DB().QueryRow(`PRAGMA freelist_count`).Scan(&free); err != nil {
		t.Fatal(err)
	}
	if free == 0 {
		t.Fatal("오래된 도서관은 실제로 무료 페이지를 재활용했습니다.，테스트가 실제로 이전 라이브러리를 구성하지 않았다는 의미입니다.")
	}
}

// TestDeleteAllPurgesAndCompacts covers the page's clear-everything action: it
// must leave nothing behind — including host directories the index no longer
// knows about — and it must hand the index space back, since an emptied index is
// the one moment a full rewrite is cheap.
func TestDeleteAllPurgesAndCompacts(t *testing.T) {
	tr, dir := openTraffic(t)
	bulkRecord(tr, "a.example.com", 10, 200*1024)
	bulkRecord(tr, "b.example.com", 10, 200*1024)
	// A text body so the full-text index has real content, and a spilled one so a
	// blob exists to collect.
	tr.record(newFlow("c.example.com", "GET", "/page", nil, []byte(strings.Repeat("secret-token ", 500))))
	tr.record(newFlow("c.example.com", "GET", "/big", nil,
		[]byte(strings.Repeat("B", maxInlineBody+1024)), withRespType("application/sql")))
	// An orphaned legacy directory: no index row points at it, so only a
	// clear-everything should take it.
	orphan := filepath.Join(dir, "orphan.example.com")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	grown := tr.indexBytes()
	if grown < 5<<20 {
		t.Fatalf("인덱스만 %d 바이트，샘플 부족", grown)
	}

	deleted, reclaimed, err := tr.DeleteAll()
	if err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}
	if deleted != 22 {
		t.Fatalf("deleted=%d，이어야 합니다. 22", deleted)
	}
	tr.reaping.Wait()

	if reclaimed < grown/2 {
		t.Fatalf("재활용만 가능 %d 바이트（이전 인덱스 삭제 %d）", reclaimed, grown)
	}
	if after := tr.indexBytes(); after > grown/8 {
		t.Fatalf("클리어 후에도 인덱스는 계속 점유됩니다. %d 바이트（삭제 전 %d）", after, grown)
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM exchanges`,
		`SELECT COUNT(*) FROM exchange_bodies`,
		`SELECT COUNT(*) FROM blob_refs`,
	} {
		var c int
		if err := tr.DB().QueryRow(q).Scan(&c); err != nil {
			t.Fatal(err)
		}
		if c != 0 {
			t.Fatalf("%s = %d，이어야 합니다. 0", q, c)
		}
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("고립된 역사 host 디렉토리가 정리되지 않았습니다.：%v", err)
	}
	// Recording must keep working against the freshly rewritten file.
	tr.record(newFlow("d.example.com", "GET", "/after", nil, []byte("클리어 후에도 녹음 가능")))
	if n, err := tr.Count(); err != nil || n != 1 {
		t.Fatalf("클리어 후 Count=(%d,%v)，이어야 합니다. (1,nil)", n, err)
	}
}

// TestDeleteAllConvertsLegacyIndex is why the purge compacts rather than just
// deleting: auto_vacuum cannot be switched on after the fact except through a
// VACUUM, and an emptied index is the cheapest place to pay for one. After this,
// ordinary deletions reclaim space on their own.
func TestDeleteAllConvertsLegacyIndex(t *testing.T) {
	dir := t.TempDir()
	old := openLegacyIndex(t, dir)
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	tr, err := Open(dir, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tr.Close() })
	if tr.incrementalVacuum {
		t.Fatal("오래된 라이브러리는 증분 재활용이 활성화되었다고 보고하면 안 됩니다.")
	}

	bulkRecord(tr, "legacy.example.com", 10, 200*1024)
	if _, _, err := tr.DeleteAll(); err != nil {
		t.Fatalf("DeleteAll: %v", err)
	}
	if !tr.incrementalVacuum {
		t.Fatal("기존 라이브러리를 삭제한 후 증분 재활용 모드로 변환되지 않았습니다.")
	}

	// The converted database now reclaims on an ordinary host deletion.
	bulkRecord(tr, "again.example.com", 10, 200*1024)
	grown := tr.indexBytes()
	if _, err := tr.DeleteHostsExact([]string{"again.example.com"}); err != nil {
		t.Fatal(err)
	}
	tr.reaping.Wait()
	if after := tr.indexBytes(); after > grown/4 {
		t.Fatalf("변환 후 일반 삭제는 아직 재활용되지 않았습니다.：%d 바이트（삭제 전 %d）", after, grown)
	}
}
