// Package selfupdate implements ARTEX 의 페이지는 한 번의 클릭으로 업데이트 가능：님으로부터 GitHub Release 새 버전을 당겨보세요
// 바이너리、확인、임시저장，다음 시작 시 모양을 원자적으로 변경합니다.。
//
// 업무분장 전반（또 만나요 start.sh / start.bat）：
//
//	시작 스크립트  = 풀 가드 루프，책임만 진다"프로세스 종료 후 종료 코드를 눌러 다시 시작할지 여부를 결정하십시오."
//	이 패키지      = 모든 오류가 발생하기 쉬운 논리（다운로드 / SHA256 확인 / 연기 / 옷을 입으세요 / 롤백 실패）
//
// 드레싱을 올려놓은 이유 Go 스크립트 대신，그렇기 때문이죠 sha256 체크섬 스모크 테스트 sh 그리고 bat 에
// 두 세트를 써야 해요（sha256sum / shasum / certutil），그리고 이 부분은 절대 틀릴 수 없는 부분이에요——다른 것으로 바꿔주세요
// 바이너리를 실행할 수 없습니다.，데몬은 충실히 반복해서 끌어올 것입니다.，사용자는 머신에 수동으로만 저장할 수 있습니다.。
//
// 완전한 업그레이드를 위해서는 세 번의 프로세스 시작이 필요합니다.：
//
//	① 이전 버전 server 받음 /api/update/apply → 다운로드 확인 → 임시저장 artex.new → exit 75
//	② 스크립트가 이전 버전을 다시 시작합니다. → Bootstrap 찾음 artex.new → 확인+연기 → 옷을 입으세요 → exit 75
//	③ 스크립트가 다시 시작되었습니다，이것은 새로운 버전입니다. → Bootstrap 한 번 시도로 표시 → 성공적으로 시작되면 표시를 지웁니다.
//
// 어느 단계라도 실패하면 이전 버전으로 돌아가세요.：② 검증에 실패할 경우 임시 파일을 삭제하고 이전 버전을 계속 실행하시기 바랍니다.；③ 연속 3 번은 살아남지 못했습니다
// 표시 지우기（일어나지 못하면 쓰러진다.）그러면 자동으로 artex.old 다시 변경。
package selfupdate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ExitRestart 네"데몬이 나를 다시 끌어 올려주세요."의 종료 코드（EX_TEMPFAIL）。시작 스크립트가 이를 확인합니다.
// 즉시 다시 실행，충돌 백오프에 포함되지 않음。0 는 사용자가 정상적으로 중지되었음을 의미합니다.（스크립트가 루프를 종료합니다.），나머지는 충돌로 간주됩니다.。
const ExitRestart = 75

// maxAttempts 은 교체 후 허용되는 시작 시도 횟수입니다.。새 버전은 시작할 때마다 계산됩니다. +1，살았다
// settleDelay 그런 다음 표시를 지웁니다.；계속되는 붕괴 maxAttempts 번은 새 버전을 전혀 시작할 수 없음을 나타냅니다.，자동 롤백。
const maxAttempts = 3

// Paths 은 업그레이드와 관련된 모든 파일입니다.，일제히 버텨라**실행파일이 위치한 디렉터리**다음。
// 일부러 사용하지 않음 CWD：서비스 런타임 작업 디렉터리는 다음과 같을 수 있습니다. / 또는 임의의 경로，사용 CWD 으로 인해 임시 파일이 다음 위치로 이동됩니다.
// 다른 곳，드레싱 논리가 직접적으로 실패합니다.。
type Paths struct {
	Dir     string // 실행파일이 위치한 디렉터리
	Current string // 현재 바이너리 실행 중        artex      / artex.exe
	New     string // 임시 새 버전            artex.new  / artex.new.exe
	Sum     string // 새 버전 sha256（hex）  artex.new.sha256 / artex.new.exe.sha256
	Old     string // 변경 전 이전 버전을 백업해 두었습니다.      artex.old  / artex.old.exe
	Marker  string // 업그레이드 상태 태그            artex.upgrade.json
}

// ResolvePaths 현재 실행 파일을 기준으로 모든 업그레이드 경로를 추론합니다.。
//
// Windows 에 .new/.old 또한 지참해야 함 .exe 접미사，그렇지 않으면 스모크 테스트 및 교체 후 실행이 실패하게 됩니다.，
// 그럼 접미사를 먼저 떼고 철자를 쓰세요，두 플랫폼의 네이밍은 대칭적이다.。
func ResolvePaths() (Paths, error) {
	exe, err := os.Executable()
	if err != nil {
		return Paths{}, fmt.Errorf("실행 파일 찾기: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)
	name := filepath.Base(exe)
	ext := filepath.Ext(name) // Windows 예 ".exe"，Unix 은 일반적으로 비어 있습니다.
	stem := strings.TrimSuffix(name, ext)

	join := func(suffix string) string { return filepath.Join(dir, stem+suffix+ext) }
	return Paths{
		Dir:     dir,
		Current: exe,
		New:     join(".new"),
		Sum:     join(".new") + ".sha256",
		Old:     join(".old"),
		Marker:  filepath.Join(dir, stem+".upgrade.json"),
	}, nil
}

// marker 드레스 갈아입는 과정을 기록한다，새 버전을 시작할 수 없을 때 자동 롤백을 트리거하는 데 사용됩니다.。
type marker struct {
	From     string `json:"from"`     // 업그레이드 전 버전
	To       string `json:"to"`       // 대상 버전
	Attempts int    `json:"attempts"` // 교체 후 시동 시도 횟수
	StagedAt int64  `json:"staged_at"`
}

func readMarker(path string) (marker, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return marker{}, false
	}
	var m marker
	if json.Unmarshal(b, &m) != nil {
		return marker{}, false
	}
	return m, true
}

func writeMarker(path string, m marker) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// cleanStaged 임시 파일 지우기。잘 차려입었어요、확인 실패、사용자가 취소하면 없어집니다.，잔류 방지
// artex.new 다음 부팅 시 다시 시도됩니다.。
func cleanStaged(p Paths) {
	_ = os.Remove(p.New)
	_ = os.Remove(p.Sum)
}

// CompareVersions 두 버전 번호 비교，복귀 -1/0/1（a<b / a==b / a>b）。
// ok=false 적어도 한 쪽이 비교할 수 있는 버전 번호가 아님을 나타냅니다.（예를 들어 로컬에서 개발 및 구축됩니다. "dev" 또는
// git describe 제작 "0.3.7-2-gabc1234-dirty"），이때 발신자는 원클릭 업데이트를 비활성화해야 합니다.，
// 그렇지 않으면 개발 중인 빌드는"업그레이드"정식 버전이 됩니다.、커밋되지 않은 변경 사항 덮어쓰기。
func CompareVersions(a, b string) (int, bool) {
	av, aok := parseVersion(a)
	bv, bok := parseVersion(b)
	if !aok || !bok {
		return 0, false
	}
	for i := range 3 {
		if av[i] != bv[i] {
			if av[i] < bv[i] {
				return -1, true
			}
			return 1, true
		}
	}
	return 0, true
}

// parseVersion 분석 "v0.3.7" / "0.3.7" 형식의 버전 번호는 다음과 같습니다. [3]int。
//
// 순수한 세 부분으로 구성된 형식만 허용됩니다.：build.sh  tag 빌드할 때 사용 git describe 출력
// "0.3.7-2-gabc1234" 그런 접미사 버전，비교불가라고 판단해야죠，으로 간주되는 대신
// 0.3.7 —— 그렇지 않으면 개발 빌드가 다음과 같이 잘못 판단될 것입니다."이 최신입니다"아니면 정식버전에서 다룰 수도 있겠네요。
func parseVersion(s string) ([3]int, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return [3]int{}, false
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// InDocker 컨테이너에서 프로세스가 실행 중인지 보고합니다.。Docker 다음 변경사항은 컨테이너 쓰기 가능 레이어를 작성하는 것입니다.，
// `docker compose up -d` 컨테이너를 다시 빌드하면 이미지와 함께 제공되는 버전이 반환됩니다.——이는 예상된 동작입니다.
// （당시 유저들은 새로운 이미지를 뽑아내고 있었는데），하지만 프론트엔드는 이를 바탕으로 명확하게 말할 수 있어야 합니다.。
func InDocker() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	b, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		return false
	}
	s := string(b)
	return strings.Contains(s, "docker") || strings.Contains(s, "containerd")
}
