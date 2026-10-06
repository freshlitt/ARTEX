package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

// smokeEnv 스모크 테스트로 끌어올린 하위 프로세스를 직접 건너뛰도록 합니다. Bootstrap。
//
// 엄밀히 말하면 추가하지 않으면 아무 일도 일어나지 않습니다：하위 프로세스 os.Executable() 네 artex.new，파생됨
// 모든 경로에는 다음이 포함됩니다. .new 접두사，실제 업그레이드 파일을 찾을 수 없습니다。그런데 이 우연에 기대는 건 너무 취약한 일이다.，
// 명시적인 단락이 한눈에 명확합니다.，또한 불필요한 디스크 감지를 방지하기 위해 하위 프로세스를 저장합니다.。
const smokeEnv = "ARTEX_SELFUPDATE_SMOKE"

// Action 네 Bootstrap 주다 main 지침。
type Action int

const (
	// Continue：평소대로 시작하세요 server。
	Continue Action = iota
	// Restart：즉시 ExitRestart 종료，데몬 스크립트를 다시 시작하세요.。
	Restart
)

// State 이번 시작 시 업그레이드 상태를 설명해주세요.， /api/update/check 프론트엔드에게 사실대로 말해주세요
// "마지막 업그레이드가 성공했습니까, 아니면 롤백되었습니까?"。
type State struct {
	Pending     bool   // 변경 후 안정성이 확인되지 않았습니다.
	RolledBack  bool   // 이 시작은 방금 자동 롤백을 실행했습니다.
	FailedStage bool   // 임시 파일 확인/연기 실패，폐기됨
	Detail      string // 사용자를 위한 한 문장 설명
}

// Bootstrap 에 main 의 맨 처음이 실행됩니다.，은 모든 수신 포트에 있어야 합니다.、데이터베이스 열기 전에 호출됨。
//
// 세 가지 상황：
//
//	① 임시파일이 존재합니다. artex.new  → 확인 + 연기，합격하면 옷을 갈아입고 다시 시작하라고 하세요；실패하면 폐기하고 이전 버전을 계속 실행하세요.
//	② 표시된 파일만 남습니다.          → 방금 옷 갈아입었다는 뜻이에요.，총 1번 시도；연속실패 횟수가 충분할 경우 롤백
//	③ 아무것도 아니다            → 정상 시작
func Bootstrap() (Action, State) {
	if os.Getenv(smokeEnv) != "" {
		return Continue, State{}
	}
	p, err := ResolvePaths()
	if err != nil {
		log.Printf("[update] 부트스트랩 건너뛰기：%v", err)
		return Continue, State{}
	}

	if _, err := os.Stat(p.New); err == nil {
		return applyStaged(p)
	}

	m, ok := readMarker(p.Marker)
	if !ok {
		return Continue, State{}
	}
	return confirmOrRollback(p, m)
}

// applyStaged 처리 중"임시파일이 존재합니다."상황：인증 통과 후 의상 변경，실패하면 폐기하세요.。
//
// 전체 업그레이드 링크에서 실행 파일을 덮어쓰는 유일한 위치입니다.，마지막 관문이기도 해요——연기 테스트가 차단되었습니다.
// 다운로드가 손상되었습니다、잘못된 아키텍처 선택、동적링크 누락 등의 문제。실행할 수 없는 바이너리가 출시되면，
// 데몬 스크립트는 지칠 줄 모르고 반복해서 끌어올 것입니다.，그리고 Go 코드가 전혀 실행될 기회가 없습니다.，자동 롤백은 불가능합니다.。
func applyStaged(p Paths) (Action, State) {
	m, _ := readMarker(p.Marker)

	if err := verifyStaged(p); err != nil {
		log.Printf("[update] 임시 저장된 새 버전이 확인에 실패했습니다.，폐기됨，현재 버전을 계속 실행하세요：%v", err)
		cleanStaged(p)
		_ = os.Remove(p.Marker)
		return Continue, State{FailedStage: true, Detail: "새 버전 확인 실패，폐기됨：" + err.Error()}
	}

	if err := swap(p); err != nil {
		log.Printf("[update] 옷을 갈아입지 못했습니다.，현재 버전을 계속 실행하세요：%v", err)
		cleanStaged(p)
		_ = os.Remove(p.Marker)
		return Continue, State{FailedStage: true, Detail: "옷을 갈아입지 못했습니다.：" + err.Error()}
	}

	// 잘 차려입었어요。예약된 태그，다음 창업에 맡겨주세요（새 버전을 실행 중입니다.）안정적인지 확인해주세요。
	m.Attempts = 0
	if m.StagedAt == 0 {
		m.StagedAt = time.Now().Unix()
	}
	if err := writeMarker(p.Marker, m); err != nil {
		log.Printf("[update] 업그레이드 표시 작성 실패（자동 롤백 기능 상실）：%v", err)
	}
	log.Printf("[update] 이(가) 변경되었습니다. %s，종료하고 다시 시작하세요.（exit %d）", orUnknown(m.To), ExitRestart)
	return Restart, State{Pending: true}
}

// confirmOrRollback 처리 중"차려입고 스타트업"：누적 시도 횟수，한도를 초과하면 이전 버전으로 교체하세요.。
//
// 에서만 계산됩니다. Go 코드가 실행된 후 증가됩니다.，그래서 다룹니다."실행할 수 있지만 초기화 중에 충돌이 발생함"
// （구성이 호환되지 않습니다.、포트가 사용 중입니다.、DB 마이그레이션이 폭발적으로 증가했습니다.）이런 종류의 오류；"불가능해요 exec" 옷 갈아입기전부터
// 연기 테스트가 차단되었습니다.，둘이 합쳐지면 완성。
func confirmOrRollback(p Paths, m marker) (Action, State) {
	m.Attempts++
	if m.Attempts > maxAttempts {
		if err := rollback(p); err != nil {
			// 롤백에 실패하면 다시 시작하지 마세요.，그렇지 않으면 무한 재시작 상태에 빠지게 됩니다.。표시 지우기，
			// 프로세스가 현재 상태에서 시작되도록 하세요.——일어날 수 없다면 사용자는 최소한 로그에서 이유를 볼 수 있습니다.。
			log.Printf("[update] 새로운 버전이 계속됩니다 %d 시작 실패，및 롤백 실패：%v", maxAttempts, err)
			_ = os.Remove(p.Marker)
			return Continue, State{Detail: "새 버전을 시작하지 못하고 롤백에 실패했습니다.：" + err.Error()}
		}
		log.Printf("[update] 새로운 버전이 계속됩니다 %d 시작 실패，이(가) 롤백되었습니다. %s，종료하고 다시 시작하세요.（exit %d）",
			maxAttempts, orUnknown(m.From), ExitRestart)
		_ = os.Remove(p.Marker)
		return Restart, State{RolledBack: true, Detail: fmt.Sprintf("새 버전을 시작하지 못했습니다.，이(가) 롤백되었습니다. %s", orUnknown(m.From))}
	}
	if err := writeMarker(p.Marker, m); err != nil {
		log.Printf("[update] 업그레이드 표시를 업데이트하지 못했습니다.：%v", err)
	}
	log.Printf("[update] 새로운 버전이 시작됩니다（아니요. %d/%d 시도），안정적인 운영 후 업그레이드가 확정될 예정입니다.",
		m.Attempts, maxAttempts)
	return Continue, State{Pending: true}
}

// Settle 새 버전이 안정적으로 실행되는지 확인하세요.，업그레이드 표시 지우기。
//
//	main 에 HTTP 모니터링 후 통화가 늦어졌습니다.：이 시간 동안 살아남아야 카운트 된다，그렇지 않으면 표시가 그대로 유지됩니다.，
//
// 다음 스타트업은 계속해서 시도 횟수가 누적됩니다.，롤백이 시작될 때까지。
func Settle() {
	p, err := ResolvePaths()
	if err != nil {
		return
	}
	settle(p)
}

func settle(p Paths) {
	if _, ok := readMarker(p.Marker); !ok {
		return // 업그레이드 후 시작이 되지 않습니다.，할 일이 없다
	}
	if err := os.Remove(p.Marker); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("[update] 업그레이드 표시를 지우지 못했습니다.：%v", err)
		return
	}
	log.Printf("[update] 새 버전이 안정적으로 실행됩니다.，업그레이드 완료（이전 버전은 그대로 유지됩니다. %s）", p.Old)
}

// SettleDelay 네 판단입니다"새 버전이 남아 있습니다."소요 러닝타임。
const SettleDelay = 30 * time.Second

// verifyStaged 검증임시파일：먼저 비교해보세요 SHA256，당겨서 다시 달려봐。
func verifyStaged(p Paths) error {
	want, err := os.ReadFile(p.Sum)
	if err != nil {
		return fmt.Errorf("체크섬 읽기: %w", err)
	}
	got, err := fileSHA256(p.New)
	if err != nil {
		return fmt.Errorf("체크섬 계산: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(string(want)), got) {
		return errors.New("SHA256 이 일치하지 않습니다.（다운로드가 손상되었거나 변조되었습니다.）")
	}
	return smokeTest(p.New)
}

// smokeTest 사용 -h 새 바이너리를 가져옵니다.，현재 시스템에서 실제로 실행이 가능한지 확인。
// 이렇게 하면 다운로드 잘림을 방지할 수 있습니다.、잘못된 아키텍처 선택（exec format error）、종속성 누락 및 기타 주요 문제。
func smokeTest(bin string) error {
	if err := os.Chmod(bin, 0o755); err != nil {
		return fmt.Errorf("실행 권한 부여: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "-h")
	cmd.Env = append(os.Environ(), smokeEnv+"=1")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return errors.New("연기 테스트 시간이 초과되었습니다.（새 바이너리가 응답하지 않습니다.）")
	}
	if err != nil {
		snippet := strings.TrimSpace(string(out))
		if len(snippet) > 300 {
			snippet = snippet[:300] + "…"
		}
		return fmt.Errorf("스모크 테스트 실패: %v: %s", err, snippet)
	}
	return nil
}

// swap 현재 바이너리를 새 임시 버전으로 교체합니다.。
//
// Unix 그리고 Windows 모두 허용됨 rename 실행 중인 실행 파일（Windows 금지사항은 삭제이며,
// 재정의，rename 은 그 중에 없습니다），그러면 여기서 플랫폼을 나눌 필요가 없습니다.，먼저 멈출 필요는 없어。
func swap(p Paths) error {
	// Windows 님 rename 기존 대상을 덮어쓰지 않습니다.，이전 업그레이드에서 남은 부분 .old 먼저 클리어해야 합니다。
	if err := os.Remove(p.Old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("오래된 백업 정리 %s: %w", p.Old, err)
	}
	if err := os.Rename(p.Current, p.Old); err != nil {
		return fmt.Errorf("현재 버전을 백업하세요: %w", err)
	}
	if err := os.Rename(p.New, p.Current); err != nil {
		// 드레스변경 실패했는데 현재버전이 옮겨졌네요，그대로 다시 넣어야 함，그렇지 않으면 다음 시작 시 실행 파일이 없습니다.。
		if rerr := os.Rename(p.Old, p.Current); rerr != nil {
			return fmt.Errorf("새 버전을 로드하지 못했습니다.(%v)，및 현재 버전 복원에 실패했습니다.: %w", err, rerr)
		}
		return fmt.Errorf("새 버전 로드: %w", err)
	}
	_ = os.Remove(p.Sum)
	return nil
}

// rollback 넣어보세요 swap 이전 백업 버전을 교체하세요.。
func rollback(p Paths) error {
	if _, err := os.Stat(p.Old); err != nil {
		return fmt.Errorf("롤백할 백업이 없습니다. %s: %w", p.Old, err)
	}
	// 시작할 수 없는 새 버전을 다음 버전으로 이동하세요. .failed 문제 해결을 위해 예약되었습니다.，직접 삭제하는 대신。
	failed := p.Current + ".failed"
	_ = os.Remove(failed)
	if err := os.Rename(p.Current, failed); err != nil {
		return fmt.Errorf("실패한 버전 제거: %w", err)
	}
	if err := os.Rename(p.Old, p.Current); err != nil {
		return fmt.Errorf("이전 버전 복원: %w", err)
	}
	return nil
}

// Rollback 네 /api/update/rollback 구현：주도적으로 이전 버전으로 복귀。
// 차려입기만 하면 된다，재시작도 데몬 스크립트에 전달됩니다.（그러면 발신자는 ExitRestart 종료）。
func Rollback() error {
	p, err := ResolvePaths()
	if err != nil {
		return err
	}
	if _, err := os.Stat(p.Old); err != nil {
		return errors.New("롤백할 이전 버전이 없습니다.（" + p.Old + " 이 존재하지 않습니다）")
	}
	cleanStaged(p)
	if err := smokeTest(p.Old); err != nil {
		return fmt.Errorf("이전 버전을 실행할 수 없습니다.，롤백 거부: %w", err)
	}
	// 현재와 백업을 교환합니다.：롤백 후 다시 롤백 가능。
	tmp := p.Current + ".swap"
	_ = os.Remove(tmp)
	if err := os.Rename(p.Current, tmp); err != nil {
		return fmt.Errorf("현재 버전 제거: %w", err)
	}
	if err := os.Rename(p.Old, p.Current); err != nil {
		_ = os.Rename(tmp, p.Current)
		return fmt.Errorf("이전 버전을 불러옵니다.: %w", err)
	}
	if err := os.Rename(tmp, p.Old); err != nil {
		log.Printf("[update] 롤백 후 백업 정리 실패（작동에 영향을 미치지 않습니다.）：%v", err)
	}
	_ = os.Remove(p.Marker)
	return nil
}

// HasBackup 롤백 가능한 이전 버전이 있는지 보고합니다.，프런트 엔드에서 롤백 버튼 표시 여부를 결정하기 위해。
func HasBackup() bool {
	p, err := ResolvePaths()
	if err != nil {
		return false
	}
	_, err = os.Stat(p.Old)
	return err == nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "알 수 없는 버전"
	}
	return s
}
