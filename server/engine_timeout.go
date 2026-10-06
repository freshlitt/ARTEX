package server

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
)

// 작업 수준 제한 시간 조정자(또 만나요 docs/작업 수준 제한 시간 및 종료 설계.md §4/§8.5)。
// 절대벽시계:각 밴드 timeout 님의 작업이 예약되었습니다 goroutine,포인트 중심의 순차적 종료 타이밍:
//   ① settling → ② worker 새로운 의도는 그만 받아라 / ③ planner 일반 삭제 notify
//   ④ 기다리며 달리고 있다 worker drain(수락됨 grace) → ⑤ 결승전 planner 판결 → ⑥ 최종 상태(경비병들과 함께)

const (
	settleDrainGrace     = 90 * time.Second // 기다리며 달리고 있다 worker 우아한엔딩의 상한;넘으면 힘들어요 cancel
	deadlinePollInterval = 2 * time.Second  // deadline 스탬프가 찍혀있지 않음/LLM 준비되지 않은 경우 폴링 간격
	deadlineMaxSleep     = 30 * time.Second // 한 번에 가장 긴 수면을 취한 사람(최종 상태에 대한 정기적인 검토를 촉진합니다.)
)

// ---------- settling 상태 ----------

func (e *Engine) isSettling(taskID string) bool {
	v, _ := e.settling.Load(taskID)
	b, _ := v.(bool)
	return b
}

// markSettling flips settling on; returns true only for the first caller.
func (e *Engine) markSettling(taskID string) bool {
	_, loaded := e.settling.LoadOrStore(taskID, true)
	return !loaded
}

// ---------- 달리고 세어가는 중(worker.Execute + planner.Plan), drain ----------

func (e *Engine) inflightCounter(taskID string) *int64 {
	v, _ := e.inflight.LoadOrStore(taskID, new(int64))
	return v.(*int64)
}

// beginTaskOperation atomically registers a task-owned operation unless deletion
// has already installed its barrier. The delete handler can therefore wait for
// inflight==0 without a check-then-start race recreating files after cleanup.
func (e *Engine) beginTaskOperation(taskID string) bool {
	e.deleteMu.RLock()
	defer e.deleteMu.RUnlock()
	if e.IsDeleting(taskID) {
		return false
	}
	atomic.AddInt64(e.inflightCounter(taskID), 1)
	return true
}

func (e *Engine) decInflight(taskID string) { atomic.AddInt64(e.inflightCounter(taskID), -1) }
func (e *Engine) inflightCount(taskID string) int64 {
	return atomic.LoadInt64(e.inflightCounter(taskID))
}

// ---------- deadline ----------

// taskDeadline returns the task's absolute deadline (unix). Prefers the in-process
// map (stamped this session); falls back to the DB-loaded value (restart), seeding
// the map. 0 = no timeout / not yet stamped.
func (e *Engine) taskDeadline(t *Task) int64 {
	if v, ok := e.deadline.Load(t.ID); ok {
		return v.(int64)
	}
	deadlineAt := t.lifecycleSnapshot().DeadlineAt
	if deadlineAt > 0 {
		e.deadline.Store(t.ID, deadlineAt)
		return deadlineAt
	}
	return 0
}

// resetTimeoutRevival clears only the per-run timeout state after PostgreSQL has
// atomically committed timeout -> running and reset first_run_at/deadline_at. The
// configured TimeoutSeconds remains on Task, so the next real Planner/Worker run
// stamps a fresh full budget. coordStarted is reset because the coordinator that
// produced the timeout has already completed (or is in its final return path).
func (e *Engine) resetTimeoutRevival(taskID string) {
	e.settling.Delete(taskID)
	e.deadline.Delete(taskID)
	e.stamped.Delete(taskID)
	e.coordStarted.Delete(taskID)
}

// stampFirstRun records first_run_at + deadline_at on the FIRST real run (LLM ready)
// of a timeout task, once per process. No-op when the task has no timeout.
func (e *Engine) stampFirstRun(t *Task) {
	if t.TimeoutSeconds <= 0 {
		return
	}
	if _, loaded := e.stamped.LoadOrStore(t.ID, true); loaded {
		return
	}
	dl, err := e.m.StampTaskFirstRun(t.ID)
	if err != nil {
		log.Printf("[deadline] task %s 각인됨 first_run 실패: %v", t.ID, err)
		e.stamped.Delete(t.ID) // 다음에 재시도 허용
		return
	}
	if dl > 0 {
		e.deadline.Store(t.ID, dl)
		log.Printf("[deadline] task %s 첫 실행,현재 %s", t.ID, time.Unix(dl, 0).Format("2006-01-02 15:04:05"))
	}
}

// clockCtx layers the task's TaskClock (absolute deadline) onto a run's context so
// worker/planner can clamp their wall-clock budget and pick per-run vs task-timeout
// wrap-up words. final marks the coordinator-driven terminal planner round.
func (e *Engine) clockCtx(base context.Context, t *Task, final bool) context.Context {
	dl := e.taskDeadline(t)
	if dl <= 0 && !final {
		return base // no timeout → unchanged behavior
	}
	return agent.WithTaskClock(base, agent.TaskClock{DeadlineUnix: dl, Final: final})
}

// ---------- 코디네이터 ----------

// startDeadlineCoordinator launches the per-task deadline timer once (idempotent).
// Called from Run() and from the restart reload path, so non-active timeout tasks
// still get settled after their deadline even without live planner/worker loops.
func (e *Engine) startDeadlineCoordinator(ctx context.Context, t *Task) {
	if t == nil || t.TimeoutSeconds <= 0 {
		return
	}
	e.deleteMu.RLock()
	if e.IsDeleting(t.ID) {
		e.deleteMu.RUnlock()
		return
	}
	if _, loaded := e.coordStarted.LoadOrStore(t.ID, true); loaded {
		e.deleteMu.RUnlock()
		return
	}
	rt := e.registerTaskRoutines(ctx, t.ID, 1)
	e.deleteMu.RUnlock()
	runTaskRoutine(rt, func(loopCtx context.Context) { e.deadlineCoordinator(loopCtx, t) })
}

// deadlineCoordinator waits until the task's absolute deadline, then runs the settle
// sequence. Absolute wall-clock: it keeps counting through pauses.
func (e *Engine) deadlineCoordinator(ctx context.Context, t *Task) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if isTerminalStatus(e.m.TaskStatus(t.ID)) {
			return // already finished (goals met / failed) — nothing to time out
		}
		dl := e.taskDeadline(t)
		if dl <= 0 {
			if sleepCtx(ctx, deadlinePollInterval) { // not yet stamped (task hasn't really run)
				return
			}
			continue
		}
		if remaining := time.Until(time.Unix(dl, 0)); remaining > 0 {
			nap := remaining
			if nap > deadlineMaxSleep {
				nap = deadlineMaxSleep
			}
			if sleepCtx(ctx, nap) {
				return
			}
			continue
		}
		e.settleTask(ctx, t)
		return
	}
}

// settleTask runs the ordered settle sequence once (§4 steps ①–⑥).
func (e *Engine) settleTask(ctx context.Context, t *Task) {
	if !e.markSettling(t.ID) {
		return
	}
	log.Printf("[deadline] task %s 시간 초과 제한에 도달했습니다.,최종 시퀀스 진입", t.ID)

	// ④ 기다리며 달리고 있다 worker/planner drain(실행 중 run 꼬집어서 MaxDuration 혼자서 끝내세요);
	// 이상 grace 아직 클리어 안됨 → 딱딱하다 cancel 임무 exec ctx(settling-aware 가지가 올바르게 분류되었습니다.)。
	hardStop := time.Now().Add(settleDrainGrace)
	for e.inflightCount(t.ID) > 0 {
		if time.Now().After(hardStop) {
			log.Printf("[deadline] task %s drain 시간 초과(%s),강제 취소가 실행 중입니다. run", t.ID, settleDrainGrace)
			e.cancelExec(t.ID, agent.AbortSettleDrainTimeout)
			_ = sleepCtx(ctx, 3*time.Second) // 주다 worker 브랜치를 라이브러리에 추가하는 데 시간이 좀 걸립니다./분류
			break
		}
		if sleepCtx(ctx, 500*time.Millisecond) {
			return // 엔진 전체가 정지됨
		}
	}

	// ⑤ 결승전 planner(작업 시간 초과 단어,최종 목표 결정,새로운 의도는 없음)。
	met := e.runFinalPlannerRound(ctx, t)
	if !e.beginTaskOperation(t.ID) {
		return
	}
	defer e.decInflight(t.ID)

	// ⑥ 최종 상태(경비병들과 함께):met → done(completed);그렇지 않으면 timeout。일반 경로가 먼저 삭제된 경우 done,
	// 경비병(SetTaskStatusGuarded)덮어쓰기를 거부합니다.,예약됨 completed 의미。
	status := "timeout"
	if met {
		status = "done"
	}
	won, err := e.m.SetTaskStatusGuarded(t.ID, status)
	switch {
	case err != nil:
		log.Printf("[deadline] task %s 최종 상태가 실패했습니다.: %v", t.ID, err)
	case won:
		log.Printf("[deadline] task %s 마무리 완료,최종 상태=%s", t.ID, status)
	default:
		log.Printf("[deadline] task %s 엔딩이 최종 상태,원래 상태를 유지하세요", t.ID)
	}
}

// runFinalPlannerRound drives exactly ONE terminal planner round with the
// task-timeout planner words (final goal judgment; no new intents). Waits for the
// LLM to be ready (bounded by ctx) so a completable task isn't mis-judged timeout.
func (e *Engine) runFinalPlannerRound(ctx context.Context, t *Task) (met bool) {
	if e.IsDeleting(t.ID) {
		return false
	}
	planner, _ := e.snapshotFor(t)
	for planner == nil {
		if sleepCtx(ctx, deadlinePollInterval) {
			return false
		}
		if isTerminalStatus(e.m.TaskStatus(t.ID)) {
			return false
		}
		if e.IsDeleting(t.ID) {
			return false
		}
		planner, _ = e.snapshotFor(t)
	}
	// 독립 ctx(끊지 마세요 execCancel,피하세요 pause/딱딱하다 cancel 이번 마지막 라운드를 중단하세요),와 함께 Final 작업 시간 초과 단어 삽입。
	fctx := e.clockCtx(ctx, t, true)
	if !e.beginTaskOperation(t.ID) {
		return false
	}
	defer e.decInflight(t.ID)
	emit := func(r db.Activity) { e.emitActivity(t, r) }
	e.emitActivity(t, db.Activity{Worker: "planner", Kind: "round",
		Summary: fmt.Sprintf("작업 시간 초과가 종료되었습니다.·최종판결(아니요. %d 휠)", e.nextPlannerRound(t.ID))})
	tTaskID, _ := strconv.ParseInt(t.ID, 10, 64)
	e.BeginLLMCall(t.ID)
	met, reason, err := planner.Plan(fctx, tTaskID, e.m.assets, t.Store, t.Goal, t.drainTriggers(), emit)
	e.EndLLMCall(t.ID)
	if err != nil {
		log.Printf("[deadline] task %s 최종 계획이 틀렸어요: %v", t.ID, err)
	} else if met {
		log.Printf("[deadline] task %s 최종 판정 목표 달성: %s", t.ID, reason)
	}
	return met
}
