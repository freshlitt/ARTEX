package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/Autumn-27/artex/selfupdate"
)

// 원클릭으로 페이지 업데이트가 가능합니다 HTTP 얼굴。실제 다운로드/확인/차려입는 논리는 다 거기서 거기 selfupdate 바올리，
// 인증 경계만 담당합니다.、동시 상호 배제、진행방송，그리고 넣어"이제 그만둘 시간이다"말해봐 main。
//
// 이 과정으로는 재시작이 완료되지 않습니다.：새 버전을 임시 저장한 후 프로세스가 진행됩니다. selfupdate.ExitRestart 종료，
// 가드 스크립트로（start.sh / start.bat，Docker 다음은 ENTRYPOINT）다시 차를 세워。

// restartCh 업그레이드 준비 또는 롤백 완료 후 종료，main 수신 후 ExitRestart 종료。
var (
	restartOnce sync.Once
	restartCh   = make(chan struct{})
)

// RestartRequested "종료하고 데몬이 다시 끌어오도록 하세요."폐쇄되었습니다 channel。
func RestartRequested() <-chan struct{} { return restartCh }

func requestRestart() { restartOnce.Do(func() { close(restartCh) }) }

// bootState 이 스타트업 때 selfupdate.Bootstrap 의 결론（업그레이드 성공 / 방금 롤백했습니다. /
// 임시파일이 폐기되었습니다）， main 주사， /api/update/check 지난 업그레이드에서 발생한 일을 프런트엔드에 솔직하게 알려주세요.。
var (
	bootStateMu sync.Mutex
	bootState   selfupdate.State
)

// SetBootUpdateState  main 시작 시 한 번 호출됩니다.。
func SetBootUpdateState(st selfupdate.State) {
	bootStateMu.Lock()
	defer bootStateMu.Unlock()
	bootState = st
}

func bootUpdateState() selfupdate.State {
	bootStateMu.Lock()
	defer bootStateMu.Unlock()
	return bootState
}

// releaseCache 캐시 GitHub 의 최신 버전 쿼리 결과입니다.。
//
// 상단 컬럼"새 버전이 있습니다"전체 페이지가 로드될 때마다 프롬프트가 확인됩니다.，인증되지 않음 GitHub API 네
// 매 IP 시간별 60 회——캐시되지 않은 경우，탭을 몇 개 더 열거나 페이지를 몇 번 새로 고치면 할당량이 소진됩니다.，
// 나중에 꼭 업데이트 하려고 했는데 못찾았네요.。사용자 명시적 포인트"업데이트 확인"예 force 캐시 우회。
type releaseCache struct {
	mu  sync.Mutex
	rel *selfupdate.Release
	err error
	at  time.Time
	// fetch 은 숫자 함수입니다.，테스트 전용으로 예약된 주입 지점；입니다 nil 현실로 나아갈 시간 GitHub 질의。
	fetch func(context.Context, *http.Client) (*selfupdate.Release, error)
}

const (
	releaseTTL = 30 * time.Minute
	// 실패 결과도 잠시 동안 캐시됩니다.，그렇지 않으면 GitHub 연결할 수 없는 경우 페이지가 로드될 때마다 시간 초과를 기다려야 합니다.；
	// 하지만 TTL 짧게 해주세요，네트워크가 복구되면 곧 괜찮을 겁니다.。
	releaseErrTTL = 2 * time.Minute
	// 쿼리 시간 초과。NewClient 님 30 분 시간 초과는 전체 패키지 다운로드에 대한 것입니다.，버전 확인이 너무 기다려지네요.。
	releaseTimeout = 20 * time.Second
)

var relCache = &releaseCache{}

// get 최신으로 돌아가기 Release，캐시에 적중되면 네트워크에 접속할 수 없습니다.。
//
// 가져오는 동안 잠금이 유지됩니다.：동시 요청은 하나의 쿼리 결과와 동일하도록 대기열에 추가됩니다.，서로 싸우기보다는 GitHub
// （페이지가 방금 로드되면 여러 탭이 동시에 확인됩니다.，전류 제한이 발생할 가능성이 가장 높은 순간입니다.）。
func (c *releaseCache) get(ctx context.Context, client *http.Client, force bool) (*selfupdate.Release, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !force {
		ttl := releaseTTL
		if c.err != nil {
			ttl = releaseErrTTL
		}
		if !c.at.IsZero() && time.Since(c.at) < ttl {
			return c.rel, c.err
		}
	}

	fetch := c.fetch
	if fetch == nil {
		fetch = selfupdate.FetchLatest
	}
	ctx, cancel := context.WithTimeout(ctx, releaseTimeout)
	defer cancel()
	rel, err := fetch(ctx, client)
	// 요청이 취소되었습니다.（사용자가 탭을 닫았습니다.）그런 뜻은 아니다 GitHub 문제가 생겼어요，캐시에 쓰지 마세요.，
	// 그렇지 않으면 다음 방문자는 설명할 수 없는 메시지를 받게 됩니다."취소됨"오류。
	if err != nil && ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
		return c.rel, err
	}
	c.rel, c.err, c.at = rel, err, time.Now()
	return rel, err
}

// updateProgress 프런트 엔드로 진행되는 진행 상황입니다.。
type updateProgress struct {
	Phase   selfupdate.Phase `json:"phase"`
	Percent int              `json:"percent"` // 다운로드 단계만 의미가 있음；나머지는 -1
	Message string           `json:"message"`
	Version string           `json:"version,omitempty"`
	Error   string           `json:"error,omitempty"`
}

// updateHub 업그레이드 진행 상황을 보관하고 이를 방송합니다. SSE 구독자。
//
// running 상호배제 역할도 함：업그레이드 중에 다시 한번 POST /api/update/apply 직접 409，
// 둘 다 피하세요 goroutine 같은 곳으로 동시에 이동 artex.new 쓰기。
type updateHub struct {
	mu      sync.Mutex
	running bool
	cur     updateProgress
	subs    map[chan updateProgress]struct{}
}

var updHub = &updateHub{
	cur:  updateProgress{Phase: selfupdate.PhaseIdle, Percent: -1},
	subs: map[chan updateProgress]struct{}{},
}

// begin 업그레이드 권한 확보，이미 진행 중이면 돌아가기 false。
func (h *updateHub) begin(version string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.running {
		return false
	}
	h.running = true
	h.cur = updateProgress{Phase: selfupdate.PhaseDownload, Percent: 0, Message: "준비중…", Version: version}
	h.fanout(h.cur)
	return true
}

// finish 업그레이드 종료。err 입니다 nil 은 임시저장이 성공했다는 의미입니다.，재시작 대기 중。
func (h *updateHub) finish(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.running = false
	if err != nil {
		h.cur = updateProgress{Phase: selfupdate.PhaseFailed, Percent: -1, Message: "업데이트 실패", Error: err.Error(), Version: h.cur.Version}
	} else {
		h.cur = updateProgress{Phase: selfupdate.PhaseStaged, Percent: 100, Message: "새 버전이 준비되었습니다，다시 시작하는 중…", Version: h.cur.Version}
	}
	h.fanout(h.cur)
}

func (h *updateHub) publish(ph selfupdate.Phase, pct int, msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cur = updateProgress{Phase: ph, Percent: pct, Message: msg, Version: h.cur.Version}
	h.fanout(h.cur)
}

// fanout 꼭 개최해야 함 h.mu 일 때 호출됨。구독자 channel 이 버퍼링되었습니다.，가득 차면 버리세요——
// 진행은 폐기될 수 있는 일시적인 정보입니다.，절대로 막히지 않게 하세요. SSE 연결차단 업그레이드 자체。
func (h *updateHub) fanout(p updateProgress) {
	for ch := range h.subs {
		select {
		case ch <- p:
		default:
		}
	}
}

func (h *updateHub) snapshot() (updateProgress, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cur, h.running
}

func (h *updateHub) subscribe() (<-chan updateProgress, func()) {
	ch := make(chan updateProgress, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, ch)
			h.mu.Unlock()
			close(ch)
		})
	}
}

// updateCheck 질의 GitHub 의 최신 공식 버전을 확인하고 현재 버전과 비교하세요.。
//
// 프런트 엔드도 직접 연결됩니다 api.github.com（GitHub 님 CORS 네 *），하지만**이 인터페이스의 적용을 받습니다.**：
// 다운로드는 백엔드에서 수행됩니다.，백엔드만 접근 가능 GitHub 그래야만 업데이트에 대해 이야기 할 수 있습니다。브라우저에서 연결할 수 있습니다.、서버에 연결할 수 없습니다
// 은 매우 일반적입니다.（서버 인트라넷、또는 프록시는 브라우저에서만 사용할 수 있습니다.），그때는 포인트 업데이트가 꼭 실패할 겁니다，
// 확인 단계에서는 오류를 사실대로 보고하는 것이 좋습니다.。
func (s *Server) updateCheck(w http.ResponseWriter, r *http.Request) {
	current := BuildVersion
	mode := "binary"
	if selfupdate.InDocker() {
		mode = "docker"
	}
	boot := bootUpdateState()
	out := map[string]any{
		"current":     current,
		"mode":        mode,
		"os":          runtime.GOOS,
		"arch":        runtime.GOARCH,
		"has_backup":  selfupdate.HasBackup(),
		"repo":        selfupdate.Repo,
		"boot_notice": boot.Detail,
		"rolled_back": boot.RolledBack,
	}

	// 상단 표시줄에 캐시하라는 메시지가 표시됨（기본값）；사용자 포인트"업데이트 확인"시간대 force=1 강제 원점복귀。
	force := r.URL.Query().Get("force") != ""
	client := selfupdate.NewClient(s.m.GlobalProxy())
	rel, err := relCache.get(r.Context(), client, force)
	if err != nil {
		out["error"] = err.Error()
		writeJSON(w, 200, out)
		return
	}

	latest := rel.TagName
	out["latest"] = latest
	out["notes"] = rel.Body
	out["html_url"] = rel.HTMLURL
	if !rel.PublishedAt.IsZero() {
		out["published_at"] = rel.PublishedAt.Format(time.RFC3339)
	}

	asset := selfupdate.AssetName(latest, runtime.GOOS, runtime.GOARCH)
	out["asset"] = asset
	if a, ok := rel.FindAsset(asset); ok {
		out["asset_available"] = true
		out["size"] = a.Size
	} else {
		out["asset_available"] = false
	}

	cmp, comparable := selfupdate.CompareVersions(current, latest)
	out["comparable"] = comparable
	out["has_update"] = comparable && cmp < 0
	if !comparable {
		// 개발 및 구축（dev / git describe 접미사 포함）비교할 수 있는 버전 번호가 없습니다.。릴리스는
		// 디버깅 중인 로컬 바이너리를 공식 버전으로 덮어씁니다.，그래서 직접 업데이트는 하지 않겠습니다。
		out["reason"] = fmt.Sprintf("현재 버전 %q 정식 출시 버전이 아닙니다.，원클릭 업데이트 비활성화됨", current)
	}
	writeJSON(w, 200, out)
}

// updateApply 새 버전을 다운로드하고 임시 저장하세요.，완료 후 프로세스를 종료하고 데몬 스크립트를 다시 시작합니다.。
//
// 즉시 복귀하세요 202，실제 작업은 배경에 있습니다 goroutine 달려라：전체 패키지를 다운로드하는 데 몇 분 정도 걸릴 수 있습니다.，
// 요청을 보류하면 역생성 시간 초과로 인해 중단됩니다.。진행되고 있어요 /api/update/stream。
func (s *Server) updateApply(w http.ResponseWriter, r *http.Request) {
	current := BuildVersion

	// 캐시로 이동：설치된 버전이 사용자가 인터페이스에서 보고 확인하는 버전인지 확인하세요.。
	client := selfupdate.NewClient(s.m.GlobalProxy())
	rel, err := relCache.get(r.Context(), client, false)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	cmp, comparable := selfupdate.CompareVersions(current, rel.TagName)
	if !comparable {
		writeErr(w, 400, fmt.Sprintf("현재 버전 %q 정식 출시 버전이 아닙니다.，원클릭 업데이트 비활성화됨", current))
		return
	}
	if cmp >= 0 {
		writeErr(w, 400, fmt.Sprintf("현재 최신버전입니다 %s", current))
		return
	}
	if !updHub.begin(rel.TagName) {
		writeErr(w, 409, "업데이트가 진행 중입니다")
		return
	}

	go func() {
		// 의도적으로 이용함 s.ctx 요청 대신 ctx：HTTP 응답이 반환되는 즉시 요청이 종료됩니다.，
		// 기다리시면 다운로드가 즉시 취소됩니다.。
		err := selfupdate.Stage(s.ctx, client, rel, current, func(ph selfupdate.Phase, pct int, msg string) {
			updHub.publish(ph, pct, msg)
		})
		updHub.finish(err)
		if err != nil {
			log.Printf("[update] 업데이트 실패：%v", err)
			return
		}
		log.Printf("[update] %s → %s 임시 저장됨，변경을 완료하기 위해 종료하려고 합니다.", current, rel.TagName)
		// 마지막 진행 상황을 프런트 엔드로 푸시할 시간을 남겨주세요.，다시 종료 트리거。
		time.Sleep(1500 * time.Millisecond)
		requestRestart()
	}()

	writeJSON(w, 202, map[string]any{"ok": true, "target": rel.TagName})
}

// updateRollback 주도적으로 이전 버전으로 복귀（옷 갈아입기 전 백업 artex.old）。
func (s *Server) updateRollback(w http.ResponseWriter, r *http.Request) {
	if _, running := updHub.snapshot(); running {
		writeErr(w, 409, "업데이트 진행 중，롤백할 수 없습니다.")
		return
	}
	if err := selfupdate.Rollback(); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	log.Printf("[update] 이전 버전으로 수동 롤백，전환을 완료하기 위해 종료하려고 합니다.")
	writeJSON(w, 202, map[string]any{"ok": true})
	go func() {
		time.Sleep(500 * time.Millisecond)
		requestRestart()
	}()
}

// updateStream 에게 SSE 푸시 업데이트 진행。
func (s *Server) updateStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, unsub := updHub.subscribe()
	defer unsub()

	send := func(p updateProgress) {
		b, _ := json.Marshal(p)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}
	// 먼저 현재 상태를 추가하세요.，페이지를 새로 고치면 진행 중인 업그레이드를 즉시 확인할 수 있습니다.。
	cur, _ := updHub.snapshot()
	send(cur)

	ctx := r.Context()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case p, ok := <-ch:
			if !ok {
				return
			}
			send(p)
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
