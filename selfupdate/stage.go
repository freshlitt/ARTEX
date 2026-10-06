package selfupdate

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"runtime"
	"strings"
	"time"
)

// sumsAsset 네 release.yml 생성된 체크섬 목록，재정의 Release 올인 zip。
const sumsAsset = "SHA256SUMS"

// maxBinarySize 압축 해제된 바이너리 크기를 제한합니다.，변형 방지 zip 디스크를 채우세요。
const maxBinarySize = 512 << 20 // 512 MiB

// Phase 은 업그레이드 프로세스의 단계입니다.，은 다음과 같이 직접 사용됩니다. SSE 사건 당시 phase 필드。
type Phase string

const (
	PhaseIdle     Phase = "idle"
	PhaseDownload Phase = "downloading"
	PhaseVerify   Phase = "verifying"
	PhaseExtract  Phase = "extracting"
	PhaseStaged   Phase = "staged"
	PhaseFailed   Phase = "failed"
)

// Progress 발신자 제공，진행 상황을 프런트 엔드로 푸시하는 데 사용됩니다.。pct 다운로드 단계에서만 의미가 있습니다.（0-100），
// 남은 단계는 통과 -1。
type Progress func(ph Phase, pct int, msg string)

// Stage 지정 다운로드 Release 의 현재 플랫폼 릴리스 패키지，확인 후 새 바이너리를 임시로 저장합니다. artex.new。
//
// 걷기가 완료되었습니다 zip ，두 가지 이유가 있습니다.：기존 Release 님 SHA256SUMS 원래는
// 표지만 zip，가자 zip 변경할 필요가 없습니다. CI，이미 출시된 히스토리 버전과도 호환됩니다.；zip 아직도 들고 다닌다
// skills/，향후 동기화를 위해 내장됨 skill 구멍을 남겨주세요。가격은 다운로드수만 높을 뿐 skills 그 몇백 KB。
//
// 함수가 반환된다는 것은 임시저장이 완료된 것을 의미합니다.，그런 다음 발신자는 다음과 같이 정상적으로 종료됩니다. ExitRestart 종료。
func Stage(ctx context.Context, c *http.Client, rel *Release, currentVersion string, prog Progress) error {
	if prog == nil {
		prog = func(Phase, int, string) {}
	}
	p, err := ResolvePaths()
	if err != nil {
		return err
	}
	if err := checkWritable(p.Dir); err != nil {
		return err
	}

	name := AssetName(rel.TagName, runtime.GOOS, runtime.GOARCH)
	asset, ok := rel.FindAsset(name)
	if !ok {
		return fmt.Errorf("이 버전은 제공하지 않습니다 %s/%s 패키지 출시（없어짐 %s）", runtime.GOOS, runtime.GOARCH, name)
	}

	prog(PhaseDownload, 0, "체크섬 목록 가져오기…")
	sums, err := fetchSums(ctx, c, rel)
	if err != nil {
		return err
	}
	want, ok := sums[name]
	if !ok {
		return fmt.Errorf("%s 포함되지 않음 %s，검증되지 않은 바이너리 설치를 거부합니다.", sumsAsset, name)
	}

	// 모든 임시 파일은 대상 디렉터리에 있습니다.，마지막이 보장됨 rename 은 동일한 파일 시스템 내에서 원자적 작업입니다.
	// （크로스 디바이스 rename 실패합니다，그리고 /tmp 일반적으로 독립적인 마운트 지점）。
	zipPath := p.New + ".zip.part"
	binPath := p.New + ".part"
	defer func() {
		_ = os.Remove(zipPath)
		_ = os.Remove(binPath)
	}()

	prog(PhaseDownload, 0, fmt.Sprintf("다운로드 %s（%s）…", name, humanSize(asset.Size)))
	got, err := download(ctx, c, asset, zipPath, prog)
	if err != nil {
		return err
	}

	prog(PhaseVerify, -1, "확인 SHA256…")
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("SHA256 이 일치하지 않습니다.：기대 %s，실제 %s（다운로드가 손상되었거나 변조되었습니다.）", short(want), short(got))
	}

	prog(PhaseExtract, -1, "압축을 풀고 스모크 테스트…")
	if err := extractBinary(zipPath, binPath); err != nil {
		return err
	}
	if err := smokeTest(binPath); err != nil {
		return fmt.Errorf("현재 시스템에서는 새 버전을 실행할 수 없습니다.: %w", err)
	}

	// 임시파일은 본인의 파일입니다. sha256 별도의 사본을 저장하세요.：다음번 교체를 시작하기 전에 다시 한번 검증이 필요합니다.，
	// 임시저장 후 재시작까지의 기간 동안 파일이 변경되거나 잘못 기록되는 것을 방지。
	binSum, err := fileSHA256(binPath)
	if err != nil {
		return fmt.Errorf("새로운 바이너리 체크섬 계산: %w", err)
	}
	if err := os.WriteFile(p.Sum, []byte(binSum), 0o644); err != nil {
		return fmt.Errorf("체크섬 쓰기: %w", err)
	}
	if err := os.Rename(binPath, p.New); err != nil {
		_ = os.Remove(p.Sum)
		return fmt.Errorf("임시 새 버전: %w", err)
	}

	if err := writeMarker(p.Marker, marker{
		From:     currentVersion,
		To:       strings.TrimPrefix(rel.TagName, "v"),
		StagedAt: time.Now().Unix(),
	}); err != nil {
		// 이 표시는 자동 롤백 기능에만 영향을 미칩니다.，스크래치 파일 자체가 제자리에 있습니다，이로 인해 업그레이드가 중단되지는 않습니다.。
		prog(PhaseStaged, -1, "경고：업그레이드 표시 작성 실패，이 업그레이드에는 자동 롤백 보호 기능이 없습니다.")
	}

	prog(PhaseStaged, 100, "새 버전이 준비되었습니다，다시 시작하는 중…")
	return nil
}

// fetchSums 다운로드 및 구문 분석 SHA256SUMS，복귀 파일명 → 16진수 요약。
func fetchSums(ctx context.Context, c *http.Client, rel *Release) (map[string]string, error) {
	asset, ok := rel.FindAsset(sumsAsset)
	if !ok {
		return nil, fmt.Errorf("그게 Release 아니요 %s，무결성을 확인할 수 없습니다.，업그레이드 거부", sumsAsset)
	}
	body, err := get(ctx, c, asset.URL)
	if err != nil {
		return nil, fmt.Errorf("다운로드 %s: %w", sumsAsset, err)
	}
	defer body.Close()

	raw, err := io.ReadAll(io.LimitReader(body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("읽기 %s: %w", sumsAsset, err)
	}
	out := parseSums(string(raw))
	if len(out) == 0 {
		return nil, fmt.Errorf("%s 내용이 비어 있거나 형식을 인식할 수 없습니다.", sumsAsset)
	}
	return out, nil
}

// parseSums 분석 sha256sum 스타일 목록，복귀 파일명 → 16진수 요약。
//
// 첫 번째 필드는 다음과 같아야 합니다. 64 숫자만 포함됩니다.。누르기만 하세요"정확히 2개의 필드"판단력이 부족하다——
// 한 줄에 두 단어로 된 설명은 모두 합법적인 항목으로 간주됩니다.，요약 테이블을 쓰레기 값으로 채웁니다.，
// 실제 자산이 잘못된 다이제스트와 일치할 수 있습니다.。
func parseSums(raw string) map[string]string {
	out := map[string]string{}
	for line := range strings.Lines(raw) {
		// 형식은 다음과 같습니다. "<sha256>  <filename>"（sha256sum 이중 공백을 사용하세요.；shasum 의 바이너리
		// 모드가 추가됩니다 * 접두사）。
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 || !isHexSHA256(fields[0]) {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name == "" {
			continue
		}
		out[name] = strings.ToLower(fields[0])
	}
	return out
}

func isHexSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// download 자산 쓰기 dst，동시계산 SHA256 그리고 누르세요 Content-Length 진행상황 보고。
func download(ctx context.Context, c *http.Client, a Asset, dst string, prog Progress) (string, error) {
	body, err := get(ctx, c, a.URL)
	if err != nil {
		return "", fmt.Errorf("다운로드 %s: %w", a.Name, err)
	}
	defer body.Close()

	f, err := os.Create(dst)
	if err != nil {
		return "", fmt.Errorf("임시 파일 생성: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	pw := &progressWriter{total: a.Size, prog: prog, name: a.Name, last: time.Now()}
	if _, err := io.Copy(io.MultiWriter(f, h, pw), body); err != nil {
		return "", fmt.Errorf("다운로드가 중단되었습니다.: %w", err)
	}
	if err := f.Sync(); err != nil {
		return "", fmt.Errorf("배치 실패: %w", err)
	}
	if a.Size > 0 && pw.written != a.Size {
		return "", fmt.Errorf("다운로드가 완료되지 않았습니다.：기대 %d 바이트，실제 %d 바이트", a.Size, pw.written)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// get 화이트리스트 제한을 시작합니다. GET，응답 본문을 반환합니다.。
func get(ctx context.Context, c *http.Client, rawURL string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if err := checkURL(req.URL); err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "artex-selfupdate")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return resp.Body, nil
}

// extractBinary 릴리스 패키지에서 가져옴 artex 실행파일。
//
// 패키지의 구조는 다음과 같습니다. artex-<버전>-<os>-<arch>/artex，그런데 여기를 클릭하세요**기본 이름**전체 내용을 철자하기보다는 일치시키세요.
// 경로：버전 번호는 패키지 이름에 한 번 나타납니다.，한 문자의 철자가 틀리면 전체 업그레이드가 실패합니다.，기본 이름으로 변경에 대한 저항력을 더 찾아보세요。
func extractBinary(zipPath, dst string) error {
	want := "artex"
	if runtime.GOOS == "windows" {
		want = "artex.exe"
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("릴리스 패키지를 엽니다.: %w", err)
	}
	defer zr.Close()

	for _, entry := range zr.File {
		if entry.FileInfo().IsDir() || !strings.EqualFold(path.Base(entry.Name), want) {
			continue
		}
		rc, err := entry.Open()
		if err != nil {
			return fmt.Errorf("읽기 %s: %w", entry.Name, err)
		}
		defer rc.Close()

		f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return fmt.Errorf("새 바이너리 쓰기: %w", err)
		}
		defer f.Close()

		n, err := io.Copy(f, io.LimitReader(rc, maxBinarySize+1))
		if err != nil {
			return fmt.Errorf("압축을 푼다 %s: %w", entry.Name, err)
		}
		if n > maxBinarySize {
			return fmt.Errorf("릴리스 패키지의 실행 파일이 초과되었습니다. %s，압축해제 거부", humanSize(maxBinarySize))
		}
		if n == 0 {
			return fmt.Errorf("릴리스 패키지 %s 은 빈 파일입니다", want)
		}
		return f.Sync()
	}
	return fmt.Errorf("릴리스 패키지에서 찾을 수 없습니다. %s", want)
}

// checkWritable 디렉토리에 쓰기가 가능한지 미리 확인하세요。그런 단계는 없습니다，아니요 root 달려라、또는 바이너리가 시스템에 배치됩니다.
// 디렉토리 시간，은 수십 개의 다운로드를 다운로드합니다. MB 그러다 옷 갈아입는 순간 실패。
func checkWritable(dir string) error {
	probe, err := os.CreateTemp(dir, ".artex-update-probe-*")
	if err != nil {
		return fmt.Errorf("프로그램 디렉토리 %s 쓸 수 없음，자동으로 업데이트할 수 없습니다.（권한을 확인하거나 수동 업그레이드를 대신 사용해 주세요.）: %w", dir, err)
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return nil
}

// progressWriter 쓴 바이트 통계 및 빈도 제한 보고，다음을 피하십시오. 32KiB 각 블록마다 한 블록씩 푸시 SSE。
type progressWriter struct {
	total   int64
	written int64
	name    string
	prog    Progress
	last    time.Time
}

func (w *progressWriter) Write(b []byte) (int, error) {
	w.written += int64(len(b))
	if time.Since(w.last) < 300*time.Millisecond {
		return len(b), nil
	}
	w.last = time.Now()
	pct := -1
	if w.total > 0 {
		pct = int(w.written * 100 / w.total)
	}
	w.prog(PhaseDownload, pct, fmt.Sprintf("다운로드 중 %s / %s", humanSize(w.written), humanSize(w.total)))
	return len(b), nil
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

func short(sum string) string {
	if len(sum) > 12 {
		return sum[:12] + "…"
	}
	return sum
}
