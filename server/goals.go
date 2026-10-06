package server

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
)

type goalSpec struct {
	Text      string
	VulnClass string
}

// launchTask runs the shared post-creation sequence for a task created via ANY
// path (HTTP createTask 또는 orchestration spawn_task),두 군데에 복사해서 붙여넣기는 피하세요:
//  1. seed 루트 자산,피드 이벤트 중심 loop;
//  2. 선택적 시드 의도,worker 1라운드를 기다릴 필요가 없습니다. planner 바로 달리기 시작;
//  3. 배경 비동기 타겟 분해(보내기「아니요. 0 휠타겟 해체」round + LLM 분해 단계 + 하나씩 goal,페이지가 보입니다),
//     분해한 후 engine.Run —— 엔진은 goal 노드가 준비되면 시작하세요.,피하세요 planner 먼저 받아보세요 goal 이전 경주의 레이스 상태。
//
// 비동기(goroutine)그러면 발신자는 바로 돌아옵니다.,두 경로 모두 동일하게 동작합니다.:몇 초 만에 작업 구축、배경 철거 대상。
func (s *Server) launchTask(t *Task, seedText string, seedFirstIntent bool) {
	if !s.engine.beginTaskOperation(t.ID) {
		return
	}
	s.seed(t, seedText)
	if seedFirstIntent {
		s.seedFirstIntent(t)
	}
	s.engine.decInflight(t.ID)
	if _, err := s.admitTask(t, "bootstrap"); err != nil {
		log.Printf("[concurrency] task %s 시작 실패: %v", t.ID, err)
	}
}

func (s *Server) startTaskEngine(t *Task) {
	ctx := s.engine.execContextFor(s.ctx, t.ID)
	if ctx.Err() != nil || s.engine.IsDeleting(t.ID) {
		return
	}
	s.engine.emitActivity(t, db.Activity{Worker: "planner", Kind: "round",
		Summary: "아니요. 0 휠타겟 해체"})
	goals := s.createGoals(ctx, t, func(r db.Activity) {
		s.engine.emitActivity(t, r)
	})
	if ctx.Err() != nil || s.engine.IsDeleting(t.ID) {
		return
	}
	for _, g := range goals {
		summary := g.Text
		if g.VulnClass != "" {
			summary = fmt.Sprintf("[%s] %s", g.VulnClass, g.Text)
		}
		s.engine.emitActivity(t, db.Activity{Worker: "planner", Kind: "text", Summary: summary})
	}
	if ctx.Err() != nil || s.engine.IsDeleting(t.ID) {
		return
	}
	s.engine.Run(s.ctx, t)
}

func (s *Server) occupiesConcurrencySlot(t *Task) bool {
	if t == nil {
		return false
	}
	lifecycle := t.lifecycleSnapshot()
	if lifecycle.Queued || lifecycle.Paused || isTerminalStatus(lifecycle.Status) {
		return false
	}
	// Deletion temporarily pauses the Engine but has not committed yet. Preserve
	// the task's slot until PostgreSQL deletion succeeds; otherwise FIFO promotion
	// during the drain window could over-admit if deletion later aborts and the
	// persisted running task is restored.
	if s.engine.IsDeleting(t.ID) {
		return true
	}
	return !s.engine.IsPaused(t.ID) && (s.engine.ReadyFor(t) || s.engine.ActiveLLMCalls(t.ID) > 0)
}

func (s *Server) runningTaskCount(excludeID string) int {
	count := 0
	for _, task := range s.m.List() {
		if task.ID != excludeID && s.occupiesConcurrencySlot(task) {
			count++
		}
	}
	return count
}

// admitTask is the single admission path for new, resumed, rerun and follow-up
// work. It atomically either starts the task or appends it to the persistent FIFO
// queue. mode is bootstrap for a freshly-created task and resume otherwise.
func (s *Server) admitTask(t *Task, mode string) (queued bool, err error) {
	return s.admitTaskWhen(t, mode, false)
}

// admitPausedTask is the task-control resume path. The paused precondition is
// checked under the same scheduler lock as admission so a concurrent pause,
// dequeue or FIFO promotion cannot leave database and Engine state divergent.
func (s *Server) admitPausedTask(t *Task) (queued bool, err error) {
	return s.admitTaskWhen(t, "resume", true)
}

func (s *Server) admitTaskWhen(t *Task, mode string, requirePaused bool) (queued bool, err error) {
	if t == nil {
		return false, fmt.Errorf("task not found")
	}
	if mode != "bootstrap" {
		mode = "resume"
	}
	s.concMu.Lock()
	defer s.concMu.Unlock()
	// Delete installs its barrier under concMu as well. Re-resolve after acquiring
	// the lock so a request that captured a task pointer before successful deletion
	// cannot revive that stale handle after StopTask clears its Engine maps.
	current, exists := s.m.Task(t.ID)
	if !exists || current != t || s.engine.IsDeleting(t.ID) {
		return false, fmt.Errorf("task is being deleted")
	}
	if !s.engine.beginTaskOperation(t.ID) {
		return false, fmt.Errorf("task is being deleted")
	}
	defer s.engine.decInflight(t.ID)
	lifecycle := t.lifecycleSnapshot()
	if requirePaused {
		if isTerminalStatus(lifecycle.Status) {
			return false, fmt.Errorf("마지막 작업을 계속할 수 없습니다.")
		}
		if !lifecycle.Paused {
			return false, fmt.Errorf("일시 중지된 작업만 계속할 수 있습니다.")
		}
		mode = s.resumeAdmissionMode(t)
	}
	wasTerminal := isTerminalStatus(lifecycle.Status)
	wasPaused := lifecycle.Paused || s.engine.IsPaused(t.ID)
	wasQueued := lifecycle.Queued
	engineWasPaused := s.engine.IsPaused(t.ID)
	enabled, limit := s.m.ConcurrencyLimit()
	ready := s.engine.ReadyFor(t)

	// Work added to a task that is already admitted only needs a wake-up. This
	// matters when the configured limit was lowered below the current running
	// count: an existing task must not suddenly mark itself queued while its
	// planner/workers are still live. Still compare-and-commit the persisted
	// status: a concurrent terminal transition must win instead of being silently
	// reported as a successful follow-up admission.
	if !wasTerminal && !wasPaused && !wasQueued && s.engine.Started(t.ID) && (!enabled || ready) {
		if err := s.m.ApplyTaskAdmission(t.ID, lifecycle.Status, lifecycle.Status, false, "resume", false); err != nil {
			return false, err
		}
		s.startAdmittedTask(t, "resume")
		return false, nil
	}

	// Preserve the original first-run mode across repeated admissions. Legacy
	// queued rows have an empty queue_mode, so infer bootstrap from their graph.
	if wasQueued {
		switch lifecycle.QueueMode {
		case "bootstrap":
			mode = "bootstrap"
		case "":
			mode = s.resumeAdmissionMode(t)
		}
	}

	readyBacklog := enabled && s.hasReadyQueuedTask(t.ID)
	atCapacity := enabled && s.runningTaskCount(t.ID) >= limit
	shouldQueue := enabled && (!ready || readyBacklog || atCapacity)

	// Install the execution barrier before reviving a terminal/paused task. Without
	// this ordering, its already-running worker loops can claim the newly-opened
	// intent in the gap between status=running and queued=true.
	if shouldQueue || wasTerminal || wasPaused || wasQueued {
		s.engine.Pause(t.ID, agent.Causef("queued_for_admission", "실행 중인 액세스를 기다리는 작업",
			"작업이 동시 대기열 또는 승인 상태 제출을 기다리고 있습니다.，이 실행이 중지되었습니다.；런닝 슬롯을 획득한 후에야 의도가 다시 획득됩니다."))
	}

	status := lifecycle.Status
	if wasTerminal {
		status = "running"
	}
	if err := s.m.ApplyTaskAdmission(t.ID, lifecycle.Status, status, shouldQueue, mode, wasQueued); err != nil {
		if !engineWasPaused && !wasPaused && !wasQueued {
			s.engine.Resume(t)
		}
		return false, err
	}
	if lifecycle.Status == "timeout" && status == "running" {
		// ApplyTaskAdmission reset this timed-out run's persisted clock. Clear the
		// matching Engine gates before either parking it in FIFO or starting work;
		// otherwise the old settling flag would make every worker skip forever.
		s.engine.resetTimeoutRevival(t.ID)
	}
	if shouldQueue {
		if !wasQueued {
			summary := fmt.Sprintf("대기 중：동시성 제한에 도달했습니다. %d，공간을 기다린 후 자동으로 시작됩니다.", limit)
			switch {
			case !ready:
				summary = "대기 중：현재 실행 가능한 항목이 없습니다. LLM 구성，구성 복구 후 자동으로 시작"
			case readyBacklog:
				summary = "대기 중：실행 대기 중인 이전 작업이 있습니다.，누르면 됩니다 FIFO 시퀀스가 자동으로 시작됩니다."
			}
			s.engine.emitActivity(t, db.Activity{Worker: "system", Kind: "text", Summary: summary})
		}
		return true, nil
	}
	s.startAdmittedTask(t, mode)
	return false, nil
}

func (s *Server) hasReadyQueuedTask(excludeID string) bool {
	for _, task := range s.m.List() {
		lifecycle := task.lifecycleSnapshot()
		if task.ID != excludeID && lifecycle.Queued && !lifecycle.Paused && !isTerminalStatus(lifecycle.Status) && s.engine.ReadyFor(task) {
			return true
		}
	}
	return false
}

func (s *Server) startAdmittedTask(t *Task, mode string) {
	if mode == "bootstrap" {
		if !s.engine.beginTaskOperation(t.ID) {
			return
		}
		// A first-run task may have kept the Engine pause barrier while waiting
		// in the concurrency queue. Clear it only after operation admission, or
		// startTaskEngine's first execContextFor call would return a cancelled
		// context and silently strand the dequeued task.
		if s.engine.IsPaused(t.ID) {
			s.engine.Resume(t)
		}
		go func() {
			defer s.engine.decInflight(t.ID)
			s.startTaskEngine(t)
		}()
		return
	}
	s.engine.Run(s.ctx, t)
	s.engine.Resume(t)
	// Run returns early for an already-started task. Explicitly ensure a timeout
	// coordinator exists after resume; resetTimeoutRevival cleared the completed
	// coordinator's marker and the next real call will stamp a fresh deadline.
	s.engine.startDeadlineCoordinator(s.ctx, t)
}

func (s *Server) reconcileConcurrency() {
	s.concMu.Lock()
	defer s.concMu.Unlock()
	enabled, limit := s.m.ConcurrencyLimit()

	// A task whose provider chain becomes unavailable cannot do useful work and
	// must not reserve a limited running slot forever. Park it persistently so a
	// later profile edit/recovery can re-enter through the same FIFO path.
	if enabled {
		for _, task := range s.m.List() {
			lifecycle := task.lifecycleSnapshot()
			if lifecycle.Queued || lifecycle.Paused || s.engine.IsPaused(task.ID) || s.engine.IsDeleting(task.ID) ||
				isTerminalStatus(lifecycle.Status) || s.engine.ReadyFor(task) || s.engine.ActiveLLMCalls(task.ID) > 0 {
				continue
			}
			mode := s.resumeAdmissionMode(task)
			s.engine.Pause(task.ID, agent.Causef("llm_unavailable_queued", "LLM 사용할 수 없음，작업이 대기 대기열에 들어갑니다.",
				"작업이 현재 실행 가능 파일을 구문 분석할 수 없습니다. Planner/Worker LLM，동시성 슬롯 출시됨；구성 복구 후 대기열 순서로 계속 진행"))
			if err := s.m.EnqueueTask(task.ID, mode); err != nil {
				s.engine.Resume(task)
				log.Printf("[concurrency] task %s 왜냐하면 LLM 사용할 수 없음 대기열에 참가하지 못했습니다.: %v", task.ID, err)
				continue
			}
			s.engine.emitActivity(task, db.Activity{Worker: "system", Kind: "text",
				Summary: "대기 중：현재 실행 가능한 항목이 없습니다. LLM 구성，구성 복구 후 자동으로 시작"})
		}
	}

	type queuedTask struct {
		task      *Task
		lifecycle taskLifecycleState
	}
	queued := []queuedTask{}
	for _, task := range s.m.List() {
		lifecycle := task.lifecycleSnapshot()
		if lifecycle.Queued && !isTerminalStatus(lifecycle.Status) {
			queued = append(queued, queuedTask{task: task, lifecycle: lifecycle})
		}
	}
	sort.SliceStable(queued, func(i, j int) bool {
		left, right := queued[i].lifecycle.QueuedAt, queued[j].lifecycle.QueuedAt
		if left == 0 {
			left = queued[i].task.CreatedAt * int64(1e9)
		}
		if right == 0 {
			right = queued[j].task.CreatedAt * int64(1e9)
		}
		if left != right {
			return left < right
		}
		// Old rows may not have queued_at and task creation timestamps are only
		// kept to second precision in memory. Task ids are monotonic, providing a
		// deterministic oldest-first fallback for those ties.
		leftID, leftErr := strconv.ParseInt(queued[i].task.ID, 10, 64)
		rightID, rightErr := strconv.ParseInt(queued[j].task.ID, 10, 64)
		if leftErr == nil && rightErr == nil {
			return leftID < rightID
		}
		return queued[i].task.ID < queued[j].task.ID
	})
	slots := len(queued)
	if enabled {
		slots = limit - s.runningTaskCount("")
	}
	for _, entry := range queued {
		if slots <= 0 {
			break
		}
		task := entry.task
		lifecycle := task.lifecycleSnapshot()
		// Keep FIFO order, but do not consume a concurrency slot for a task
		// whose explicit chain/global provider is unavailable. It will be retried
		// after the user configures or resets its LLM chain.
		if lifecycle.Paused || (enabled && !s.engine.ReadyFor(task)) {
			continue
		}
		mode := lifecycle.QueueMode
		if mode != "bootstrap" && mode != "resume" {
			mode = s.resumeAdmissionMode(task)
		}
		if mode != "bootstrap" {
			mode = "resume"
		}
		if !s.engine.beginTaskOperation(task.ID) {
			continue
		}
		if err := s.m.ApplyTaskAdmission(task.ID, lifecycle.Status, lifecycle.Status, false, mode, false); err != nil {
			s.engine.decInflight(task.ID)
			continue
		}
		s.startAdmittedTask(task, mode)
		s.engine.decInflight(task.ID)
		slots--
	}
}

// reviveTask 중지된 작업 다시 시작:최종상태를 넣어주세요(done/failed/timeout)물러서라 running、
// 일시정지 해제,그리고(무거움)엔진 사이클 시작 + 일어나세요。은 이미 왔습니다 running 및 일시 중단되지 않은 작업:만 Run 일단 들어가세요
// Notify,부작용이 거의 없음。「스승님 agent set_goals 새 대상 추가」그리고「재방송 blocked 의도」두 곳。
//
// 왜 명시적으로 부활해야 합니까?:planner/worker 루프의 최종 상태 게이트(engine.go)평범한 것을 삼킬 것이다 notify——광학적 변형
// 사진 + Notify 완료된 작업을 깨울 수 없습니다;재시작 후 최종 상태 작업 goroutine 여기에 더 이상 없을 수도 있습니다,그래서 아직 필요해요 Run。
func (s *Server) reviveTask(t *Task) {
	if t == nil {
		return
	}
	if _, err := s.admitTask(t, "resume"); err != nil {
		log.Printf("[revive] task %s 복구 실패: %v", t.ID, err)
	}
}

// createGoals materializes the goal node(s) under the task root (rel objective).
// Decomposition is done ENTIRELY by the LLM (the project requires an LLM). There is
// no rule-based fallback splitter — it only ever produced garbage (shredded URLs,
// meaningless 2-way splits). If the LLM yields nothing (an error), the raw task goal
// is used verbatim as a single goal so the task still has something to judge against.
// Returns the seeded specs so callers can emit activity records for them.
// emit, when non-nil, is forwarded to DecomposeGoals so LLM steps are visible in the UI.
func (s *Server) createGoals(ctx context.Context, t *Task, emit func(db.Activity)) []goalSpec {
	if t == nil {
		return nil
	}
	// Use the SAME LLM the task runs on (its pinned profile, else the active profile),
	// NOT agent.FromEnv() — the LLM is configured via the UI (DB profile), not env vars,
	// so FromEnv returned empty and every task silently fell back to the crude rule
	// splitter (which shredded URLs / made meaningless 2-way splits).
	var specs []goalSpec
	var as *db.AssetStore
	if s.m != nil {
		as = s.m.Assets()
	}
	taskID, _ := strconv.ParseInt(t.ID, 10, 64)
	s.engine.BeginLLMCall(t.ID)
	goalRuntime := s.agentsForTask(t).runtime
	decomposed := agent.DecomposeGoalsWithProvider(ctx, goalRuntime, s.m.dir, t.Goal, t.Description, as, t.Store, taskID, goalRuntime.nonStreaming(), goalRuntime.maxTokens(), emit)
	s.engine.EndLLMCall(t.ID)
	for _, g := range decomposed {
		if strings.TrimSpace(g.Text) != "" {
			specs = append(specs, goalSpec{Text: g.Text, VulnClass: g.VulnClass})
		}
	}
	if len(specs) == 0 {
		// No decomposed goals (LLM error / no provider): use the raw task goal verbatim
		// as a single goal so the task still has something to judge against. This is the
		// only path that writes here — decomposed goals are already persisted by the tool.
		if g := strings.TrimSpace(t.Goal); g != "" {
			log.Printf("[goals] task %s: LLM 대상 분해에 출력이 없습니다.，다음으로 돌아갑니다.「원래 대상을 단일 대상으로」", t.ID)
			origin, _ := t.Store.OriginFactID()
			id, _ := t.Store.AddNode(db.KindGoal, map[string]any{"text": g}, 0, "open", "system", nil)
			if origin > 0 && id > 0 {
				_ = t.Store.Link(origin, db.RelSpawns, id) // goal descends from the task root (origin fact)
			}
			specs = []goalSpec{{Text: g}}
		}
	}
	return specs
}
