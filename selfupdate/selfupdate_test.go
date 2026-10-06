package selfupdate

import (
	"archive/zip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// testPaths 격리된 업그레이드 디렉터리 생성。직접 사용할 수 없습니다. ResolvePaths()——그것은 테스트를 의미합니다.
// 바이너리 자체，그냥 도망가세요 go test 의 실행 파일 이름이 변경되었습니다.。
func testPaths(t *testing.T) Paths {
	t.Helper()
	dir := t.TempDir()
	return Paths{
		Dir:     dir,
		Current: filepath.Join(dir, "artex"),
		New:     filepath.Join(dir, "artex.new"),
		Sum:     filepath.Join(dir, "artex.new.sha256"),
		Old:     filepath.Join(dir, "artex.old"),
		Marker:  filepath.Join(dir, "artex.upgrade.json"),
	}
}

// fakeBin 가장하기 위한 실행 가능한 쉘 스크립트를 작성하십시오. artex。smokeTest 그냥 사용하세요 -h 위로 당겨서 종료 코드를 확인하세요.，
// 스크립트만으로도 충분합니다.，실제 바이너리를 컴파일하는 것보다 훨씬 빠릅니다.。
func fakeBin(t *testing.T, path, marker string, exitCode int) {
	t.Helper()
	script := "#!/bin/sh\necho " + marker + "\nexit " + itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("가짜 바이너리 작성 %s: %v", path, err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	return string(rune('0' + n))
}

// stage 넣어보세요 bin 은 다음과 같이 배열됩니다."은 교체를 위해 임시로 저장되었습니다."같네요：작성 artex.new 및 해당 체크섬。
func stage(t *testing.T, p Paths, marker string, exitCode int) {
	t.Helper()
	fakeBin(t, p.New, marker, exitCode)
	sum, err := fileSHA256(p.New)
	if err != nil {
		t.Fatalf("체크섬 계산: %v", err)
	}
	if err := os.WriteFile(p.Sum, []byte(sum), 0o644); err != nil {
		t.Fatalf("체크섬 쓰기: %v", err)
	}
}

func readAll(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("읽기 %s: %v", path, err)
	}
	return string(b)
}

func requireUnix(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("가짜 바이너리는 다음을 사용합니다. sh 스크립트，Windows 실행할 수 없습니다")
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b       string
		want       int
		comparable bool
	}{
		{"0.3.7", "0.3.8", -1, true},
		{"0.3.8", "0.3.7", 1, true},
		{"0.3.7", "0.3.7", 0, true},
		{"v0.3.7", "0.3.8", -1, true}, // build.sh 제거 v，tag 와 함께 v，양측 모두 인지해야 한다
		{"0.3.7", "v0.3.7", 0, true},
		{"0.9.0", "0.10.0", -1, true}, // 사전순이 아닌 숫자순으로
		{"1.0.0", "0.99.99", 1, true},
		// 개발 빌드는 비교할 수 없는 것으로 판단되어야 합니다，그렇지 않으면 커밋되지 않은 변경 사항을 공식 버전이 덮어쓰게 됩니다.。
		{"dev", "0.3.8", 0, false},
		{"0.3.7-2-gabc1234", "0.3.8", 0, false},
		{"0.3.7-dirty", "0.3.8", 0, false},
		{"0.3", "0.3.8", 0, false},
		{"", "0.3.8", 0, false},
	}
	for _, c := range cases {
		got, ok := CompareVersions(c.a, c.b)
		if ok != c.comparable {
			t.Errorf("CompareVersions(%q,%q) comparable=%v, 기대 %v", c.a, c.b, ok, c.comparable)
			continue
		}
		if ok && got != c.want {
			t.Errorf("CompareVersions(%q,%q)=%d, 기대 %d", c.a, c.b, got, c.want)
		}
	}
}

func TestResolvePathsNaming(t *testing.T) {
	p, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	// 키 불변：모든 업그레이드 파일은 실행 파일과 동일한 디렉터리에 있습니다.。떨어졌다 CWD 서비스로 실행하도록 하겠습니다
	// （작업 디렉터리는 다음과 같을 수 있습니다. /）의상변경 완전실패。
	for name, path := range map[string]string{"New": p.New, "Sum": p.Sum, "Old": p.Old, "Marker": p.Marker} {
		if filepath.Dir(path) != p.Dir {
			t.Errorf("%s 실행파일 디렉터리에 없습니다.: %s (기대 %s)", name, path, p.Dir)
		}
	}
	// Windows 에 .new/.old 유지해야 함 .exe，그렇지 않으면 스모크 테스트 및 교체 후 실행이 실패하게 됩니다.。
	if runtime.GOOS == "windows" {
		if !strings.HasSuffix(p.New, ".exe") || !strings.HasSuffix(p.Old, ".exe") {
			t.Errorf("Windows 에 .new/.old 다음으로 시작해야 합니다. .exe 끝: new=%s old=%s", p.New, p.Old)
		}
	}
}

func TestVerifyStagedRejectsTamperedBinary(t *testing.T) {
	requireUnix(t)
	p := testPaths(t)
	stage(t, p, "new", 0)

	// 체크섬을 작성한 후 파일을 수정하세요.，시뮬레이션 다운로드가 손상되었습니다. / 이 삭제되었습니다.。
	fakeBin(t, p.New, "tampered", 0)
	if err := verifyStaged(p); err == nil {
		t.Fatal("기대 SHA256 일치하지 않아 거부됨，그런데 합격했어요")
	}
}

func TestVerifyStagedRejectsUnrunnableBinary(t *testing.T) {
	requireUnix(t)
	p := testPaths(t)
	stage(t, p, "broken", 1) // 실행이 가능하지만 종료코드가 실행되지 않습니다. 0

	if err := verifyStaged(p); err == nil {
		t.Fatal("연기 테스트가 실패하고 거부될 것으로 예상합니다.，그런데 합격했어요")
	}
}

func TestApplyStagedHappyPath(t *testing.T) {
	requireUnix(t)
	p := testPaths(t)
	fakeBin(t, p.Current, "old", 0)
	stage(t, p, "new", 0)
	if err := writeMarker(p.Marker, marker{From: "0.3.7", To: "0.3.8"}); err != nil {
		t.Fatalf("태그 쓰기: %v", err)
	}

	action, st := applyStaged(p)
	if action != Restart {
		t.Fatalf("기대 Restart，받았어요 %v", action)
	}
	if !st.Pending {
		t.Error("변경 후 상태는 다음과 같아야 합니다. Pending")
	}
	if !strings.Contains(readAll(t, p.Current), "new") {
		t.Error("artex 은 새 버전으로 교체되어야 합니다.")
	}
	if !strings.Contains(readAll(t, p.Old), "old") {
		t.Error("이전 버전을 백업해야 합니다. artex.old")
	}
	if _, err := os.Stat(p.New); !os.IsNotExist(err) {
		t.Error("옷을 갈아입은 후 artex.new 사라졌어야 했는데")
	}
	if _, err := os.Stat(p.Sum); !os.IsNotExist(err) {
		t.Error("교체 후 체크섬 파일을 정리해야 합니다.")
	}
	// 표시는 반드시 유지되어야 합니다.，다음 스타트업（새 버전을 실행 중입니다.）중요하다、필요한 경우 롤백。
	if _, ok := readMarker(p.Marker); !ok {
		t.Error("업그레이드 마크는 의상 변경 후에도 유지되어야 합니다.")
	}
}

func TestApplyStagedKeepsCurrentWhenVerifyFails(t *testing.T) {
	requireUnix(t)
	p := testPaths(t)
	fakeBin(t, p.Current, "old", 0)
	stage(t, p, "new", 0)
	fakeBin(t, p.New, "tampered", 0) // 손상된 체크섬

	action, st := applyStaged(p)
	if action != Continue {
		t.Fatalf("인증 실패 시 예상 Continue，받았어요 %v", action)
	}
	if !st.FailedStage {
		t.Error("상태는 다음과 같이 표시되어야 합니다. FailedStage")
	}
	if !strings.Contains(readAll(t, p.Current), "old") {
		t.Fatal("검증 실패 시 현재 버전을 건드리면 안 됩니다.")
	}
	if _, err := os.Stat(p.New); !os.IsNotExist(err) {
		t.Error("검증에 실패한 임시 파일을 정리해야 합니다.，그렇지 않으면 다음에 시작할 때 다시 시도됩니다.")
	}
}

func TestSwapOverwritesPreviousBackup(t *testing.T) {
	requireUnix(t)
	p := testPaths(t)
	fakeBin(t, p.Current, "v2", 0)
	fakeBin(t, p.Old, "v1", 0) // 이전 업그레이드 라운드에서 남은 백업
	stage(t, p, "v3", 0)

	if err := swap(p); err != nil {
		t.Fatalf("swap: %v", err)
	}
	if !strings.Contains(readAll(t, p.Current), "v3") {
		t.Error("으로 변경해야 합니다. v3")
	}
	if !strings.Contains(readAll(t, p.Old), "v2") {
		t.Error("백업은 방금 교체된 백업으로 업데이트되어야 합니다. v2")
	}
}

func TestConfirmCountsAttemptsThenRollsBack(t *testing.T) {
	requireUnix(t)
	p := testPaths(t)
	fakeBin(t, p.Current, "broken-new", 0)
	fakeBin(t, p.Old, "good-old", 0)
	m := marker{From: "0.3.7", To: "0.3.8"}

	// 전 maxAttempts 시작 횟수만 누적 계산됩니다.，새로운 버전이 스스로 굳건히 설 수 있는 기회를 주세요。
	for i := 1; i <= maxAttempts; i++ {
		action, st := confirmOrRollback(p, m)
		if action != Continue {
			t.Fatalf("아니요. %d 시도가 예상됩니다. Continue，받았어요 %v", i, action)
		}
		if !st.Pending {
			t.Errorf("아니요. %d 시도 상태는 다음과 같아야 합니다. Pending", i)
		}
		got, ok := readMarker(p.Marker)
		if !ok || got.Attempts != i {
			t.Fatalf("아니요. %d 시도 후 attempts=%d（ok=%v），기대 %d", i, got.Attempts, ok, i)
		}
		m = got
	}

	// 또 충돌이 나면 한도를 초과하게 됩니다.，자동으로 이전 버전으로 전환。
	action, st := confirmOrRollback(p, m)
	if action != Restart {
		t.Fatalf("시도 제한 시간 예상을 초과했습니다. Restart，받았어요 %v", action)
	}
	if !st.RolledBack {
		t.Error("상태는 다음과 같이 표시되어야 합니다. RolledBack")
	}
	if !strings.Contains(readAll(t, p.Current), "good-old") {
		t.Fatal("이전 버전으로 롤백했어야 했는데")
	}
	if _, err := os.Stat(p.Marker); !os.IsNotExist(err) {
		t.Error("롤백 후 표시를 지워야 합니다.，그렇지 않으면 무한 롤백됩니다.")
	}
	// 시작할 수 없는 버전은 문제 해결을 위해 예약되어 있습니다.，직접 삭제하지 마세요。
	if _, err := os.Stat(p.Current + ".failed"); err != nil {
		t.Error("실패한 버전은 다음과 같이 유지되어야 합니다. .failed 문제 해결을 위해")
	}
}

func TestManualRollbackIsReversible(t *testing.T) {
	requireUnix(t)
	p := testPaths(t)
	fakeBin(t, p.Current, "v2", 0)
	fakeBin(t, p.Old, "v1", 0)

	// Rollback() 가자 ResolvePaths()，여기서는 기본 교환 의미론을 직접 테스트합니다.。
	tmp := p.Current + ".swap"
	if err := os.Rename(p.Current, tmp); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(p.Old, p.Current); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, p.Old); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readAll(t, p.Current), "v1") {
		t.Error("롤백 후 현재 버전은 v1")
	}
	if !strings.Contains(readAll(t, p.Old), "v2") {
		t.Error("롤백 후 백업은 다음과 같아야 합니다. v2，이렇게 하면 다시 돌아갈 수 있어요")
	}
}

func TestParseSums(t *testing.T) {
	const (
		linuxSum = "1111111111111111111111111111111111111111111111111111111111111111"
		winSum   = "ABCDEF0000000000000000000000000000000000000000000000000000000000"
	)
	// sha256sum 출력은 이중 공백으로 구분됩니다.；shasum -a 256 바이너리 모드에서는 파일명이 추가됩니다. *。
	raw := linuxSum + "  artex-0.3.8-linux-amd64.zip\n" +
		winSum + " *artex-0.3.8-windows-amd64.zip\n" +
		"\n" +
		"garbage line\n" + // 정확히 2개의 필드，하지만 첫 번째는 요약이 아닙니다.
		"deadbeef  artex-0.3.8-darwin-arm64.zip\n" // 요약 길이가 잘못되었습니다.

	out := parseSums(raw)
	if out["artex-0.3.8-linux-amd64.zip"] != linuxSum {
		t.Errorf("linux 항목 구문 분석 오류: %v", out)
	}
	// 초록은 소문자로 작성해야 합니다.，비교시 대소문자로 인한 불일치로 오인되지는 않을 것입니다.。
	if got := out["artex-0.3.8-windows-amd64.zip"]; got != strings.ToLower(winSum) {
		t.Errorf("windows 입력 오류（* 접두사를 제거해야 합니다.、초록은 소문자로 변환해야 합니다.）: %q", got)
	}
	if len(out) != 2 {
		t.Errorf("빈 줄은 무시해야 합니다.、요약되지 않은 줄과 잘못된 길이의 줄，받았어요 %v", out)
	}
}

func TestExtractBinaryFindsNestedEntry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("패키지의 기본 이름은 Windows 예 artex.exe，이 사용 사례는 Unix 명명된 구문")
	}
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "release.zip")

	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	// 실제 출시 패키지의 구조：artex-<버전>-<os>-<arch>/artex，일부 간섭 파일 추가。
	for name, body := range map[string]string{
		"artex-0.3.8-linux-amd64/README.md":           "readme",
		"artex-0.3.8-linux-amd64/skills/a.md":         "skill",
		"artex-0.3.8-linux-amd64/artex":               "#!/bin/sh\nexit 0\n",
		"artex-0.3.8-linux-amd64/config.example.json": "{}",
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	dst := filepath.Join(dir, "out")
	if err := extractBinary(zipPath, dst); err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if got := readAll(t, dst); !strings.Contains(got, "exit 0") {
		t.Errorf("압축해제 버전이 아닙니다. artex 실행파일: %q", got)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Error("압축 해제된 바이너리에는 실행 비트가 있어야 합니다.")
	}
}

func TestExtractBinaryMissingEntry(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "release.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("artex-0.3.8-linux-amd64/README.md")
	_, _ = w.Write([]byte("readme"))
	_ = zw.Close()
	f.Close()

	if err := extractBinary(zipPath, filepath.Join(dir, "out")); err == nil {
		t.Fatal("패키지에 실행 파일이 없으면 오류가 보고되어야 합니다.")
	}
}

func TestCheckURLRejectsNonGitHub(t *testing.T) {
	bad := []string{
		"http://github.com/x",           // 아니요 HTTPS
		"https://evil.com/artex.zip",    // 도메인 이름이 허용 목록에 없습니다.
		"https://github.com.evil.com/x", // 접미사 변장
		"https://raw.githubusercontent.com.evil.com/x",
	}
	for _, raw := range bad {
		u := mustParse(t, raw)
		if err := checkURL(u); err == nil {
			t.Errorf("checkURL(%q) 은 거부되어야 합니다", raw)
		}
	}
	good := []string{
		"https://api.github.com/repos/x/releases/latest",
		"https://objects.githubusercontent.com/blah",
		"https://GitHub.com/x", // 도메인 이름은 대소문자를 구분하지 않습니다.
	}
	for _, raw := range good {
		u := mustParse(t, raw)
		if err := checkURL(u); err != nil {
			t.Errorf("checkURL(%q) 석방되어야 한다，오류가 보고되었습니다.: %v", raw, err)
		}
	}
}

func TestAssetNameMatchesBuildScript(t *testing.T) {
	// build.sh 님 package_binary 사용 artex-<버전>-<os>-<arch>.zip，및 버전 번호
	// 삭제됨 v 접두사。여기서는 한 글자가 맞거나 틀립니다.，모든 플랫폼의 원클릭 업데이트에서 자산을 찾을 수 없습니다.。
	if got := AssetName("v0.3.8", "linux", "amd64"); got != "artex-0.3.8-linux-amd64.zip" {
		t.Errorf("AssetName = %q", got)
	}
	if got := AssetName("0.3.8", "windows", "amd64"); got != "artex-0.3.8-windows-amd64.zip" {
		t.Errorf("AssetName = %q", got)
	}
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("분석 %q: %v", raw, err)
	}
	return u
}

func TestSettleClearsMarkerAndStopsRollback(t *testing.T) {
	requireUnix(t)
	p := testPaths(t)
	fakeBin(t, p.Current, "new", 0)
	fakeBin(t, p.Old, "old", 0)
	if err := writeMarker(p.Marker, marker{From: "0.3.7", To: "0.3.8", Attempts: 2}); err != nil {
		t.Fatal(err)
	}

	settle(p)

	if _, err := os.Stat(p.Marker); !os.IsNotExist(err) {
		t.Fatal("안정성 확인 후 업그레이드 표시를 지워야 합니다.")
	}
	// 표시가 사라졌어요，이후의 정상적인 재시작은 더 이상 횟수를 누적하지 않습니다.、실수로 롤백을 실행하지 않습니다.。
	if _, ok := readMarker(p.Marker); ok {
		t.Error("태그 읽기가 실패해야 합니다.")
	}
	// 백업을 보관하세요，사용자가 수동으로 롤백할 수도 있습니다.。
	if _, err := os.Stat(p.Old); err != nil {
		t.Error("안정적인지 확인한 후 이전 버전의 백업을 그대로 유지해야 합니다.")
	}
}

func TestSettleIsNoopWithoutMarker(t *testing.T) {
	requireUnix(t)
	p := testPaths(t)
	fakeBin(t, p.Current, "cur", 0)
	settle(p) // 일반 시작 경로，안된다 panic 파일도 만지지 마세요
	if _, err := os.Stat(p.Current); err != nil {
		t.Error("표시가 없을 때 settle 어떤 파일에도 영향을 주지 않아야 합니다.")
	}
}
