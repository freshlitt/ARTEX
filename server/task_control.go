package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
)

const maxBatchControlIDs = 100

type taskControlResult struct {
	ID     string `json:"id"`
	Paused bool   `json:"paused"`
	Queued bool   `json:"queued"`
	Status string `json:"status"`
}

type intentControlResult struct {
	ID      int64             `json:"id"`
	State   string            `json:"state"`
	Deleted *db.IntentCleanup `json:"deleted,omitempty"`
}

// parsedTaskID carries one requested batch id together with whether it parsed.
// Invalid ids are kept rather than dropped so the response can name them.
type parsedTaskID struct {
	id    string
	valid bool
}

// normalizeBatchTaskIDs trims, canonicalizes and de-duplicates the ids of one
// batch request while preserving the caller's order. Shared by every batch
// endpoint so they agree on what counts as a duplicate.
func normalizeBatchTaskIDs(raw []string) []parsedTaskID {
	seen := map[string]bool{}
	seenInvalid := map[string]bool{}
	taskIDs := make([]parsedTaskID, 0, len(raw))
	for _, item := range raw {
		trimmed := strings.TrimSpace(item)
		id, valid := canonicalTaskID(trimmed)
		if !valid {
			if seenInvalid[trimmed] {
				continue
			}
			seenInvalid[trimmed] = true
			taskIDs = append(taskIDs, parsedTaskID{id: trimmed})
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		taskIDs = append(taskIDs, parsedTaskID{id: id, valid: true})
	}
	return taskIDs
}

type batchControlItem struct {
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Status string `json:"status,omitempty"`
	Queued bool   `json:"queued,omitempty"`
	Error  string `json:"error,omitempty"`
}

func (s *Server) resumeAdmissionMode(t *Task) string {
	if t == nil {
		return "resume"
	}
	lifecycle := t.lifecycleSnapshot()
	if lifecycle.QueueMode == "bootstrap" {
		return "bootstrap"
	}
	if lifecycle.FirstRunAt == 0 {
		goals, err := t.Store.ListByKind(db.KindGoal, 1)
		if err == nil && len(goals) == 0 {
			return "bootstrap"
		}
	}
	return "resume"
}

// applyTaskControl is shared by the single and batch endpoints. Task resume is
// deliberately limited to paused tasks; reruns and finding follow-ups use
// admitTask directly when they need to revive a terminal task.
func (s *Server) applyTaskControl(t *Task, action string) (taskControlResult, error) {
	return s.applyTaskControlWithCause(t, action, agent.AbortPausedByUser)
}

func (s *Server) applyTaskControlWithCause(t *Task, action string, pauseCause error) (taskControlResult, error) {
	if t == nil {
		return taskControlResult{}, fmt.Errorf("task not found")
	}
	out := taskControlResult{ID: t.ID}
	switch action {
	case "pause":
		s.concMu.Lock()
		defer s.concMu.Unlock()
		current, exists := s.m.Task(t.ID)
		if !exists || current != t || s.engine.IsDeleting(t.ID) {
			return out, fmt.Errorf("작업을 삭제하는 중입니다.，제어할 수 없습니다")
		}
		if !s.engine.beginTaskOperation(t.ID) {
			return out, fmt.Errorf("작업을 삭제하는 중입니다.，제어할 수 없습니다")
		}
		defer s.engine.decInflight(t.ID)
		lifecycle := t.lifecycleSnapshot()
		if isTerminalStatus(lifecycle.Status) {
			return out, fmt.Errorf("최종 작업을 실행하고 일시 중지할 수 없습니다.")
		}
		if lifecycle.Paused {
			return out, fmt.Errorf("작업이 일시 중지되었습니다.")
		}
		wasQueued := lifecycle.Queued
		wasEnginePaused := s.engine.IsPaused(t.ID)
		if pauseCause == nil {
			pauseCause = agent.AbortPausedByUser
		}
		s.engine.Pause(t.ID, pauseCause)
		if err := s.m.ApplyTaskPause(t.ID); err != nil {
			if !wasEnginePaused && !wasQueued {
				s.engine.Resume(t)
			}
			return out, err
		}
		// Main Agent is independently cancellable. Only cancel its current turn
		// after the persistent pause commits, so a failed control request is fully
		// compensated and does not lose an otherwise valid conversation turn.
		s.cancelTaskChat(t.ID, agent.AbortChatPausedWithTask)
		out.Paused, out.Status = true, "paused"
		go s.reconcileConcurrency()
	case "resume":
		queued, err := s.admitPausedTask(t)
		if err != nil {
			return out, err
		}
		out.Queued = queued
		out.Status = map[bool]string{true: "queued", false: "running"}[queued]
	default:
		return out, fmt.Errorf("action must be pause|resume")
	}
	log.Printf("[task] #%s %s", t.ID, map[string]string{"pause": "정지됨", "resume": "계속"}[action])
	return out, nil
}

// intentSummaryOf 의도를 얻으세요 payload 에 summary,인텐트 노드에서 삭제 알림이 사라집니다.(정말 삭제됐어요)먼저 파일을 남겨주세요。
func intentSummaryOf(n *db.Node) string {
	if n == nil {
		return ""
	}
	var p map[string]any
	if json.Unmarshal(n.Payload, &p) == nil {
		if s, ok := p["summary"].(string); ok {
			return s
		}
	}
	return ""
}

func (s *Server) applyIntentControl(ctx context.Context, t *Task, iid int64, action, reason, mode string) (intentControlResult, error) {
	out := intentControlResult{ID: iid}
	node, err := t.Store.GetNode(iid)
	if err != nil {
		return out, err
	}
	if node == nil {
		if inherited, sourceErr := t.Store.GetNodeWithSources(iid); sourceErr == nil && inherited != nil && inherited.Inherited {
			return out, fmt.Errorf("상속 의도는 읽기 전용입니다.，통제가 안 돼요")
		}
		return out, fmt.Errorf("intent not found")
	}
	if node.Kind != db.KindIntent {
		return out, fmt.Errorf("node is not an intent")
	}
	switch action {
	case "pause":
		if node.State != "running" {
			return out, fmt.Errorf("실행 중인 인텐트만 일시 중지할 수 있습니다.")
		}
		if err := s.engine.ControlWork(ctx, iid, "pause"); err != nil {
			return out, err
		}
		out.State = "paused"
	case "resume":
		if node.State != "paused" {
			return out, fmt.Errorf("일시 중단된 인텐트만 재개할 수 있습니다.")
		}
		changed, err := t.Store.CompareAndSetIntentState(iid, "paused", "open")
		if err != nil {
			return out, err
		}
		if !changed {
			return out, fmt.Errorf("%w: 그 의도는 더 이상 paused 상태", db.ErrIntentStateConflict)
		}
		t.Notify()
		out.State = "open"
	case "cancel":
		// 삭제는 두 가지 모드를 지원합니다.:
		//   soft(기본값,가짜삭제):중단할 의사 있음 state='deleted'、삭제 이유를 기록해 주세요 delete_reason 필드,
		//     인텐트 노드와 모든 출력을 유지합니다./블러드라인,은 더 이상 사진에 추가되지 않습니다. fact。
		//   hard(정말 삭제됐어요):의도를 물리적으로 삭제하고"그것에서만 지원됩니다."의 단독 하위 노드(계단식으로 나뭇잎으로),숙박을 피하세요
		//     고아 데이터;공유 노드、goal、작업 루트 사실 보유。
		// 두 모드 모두 사용 cancelled 트리거 알림 planner(인텐트 내용 + 삭제 이유),그에 맞게 다시 계획하자。
		if node.State != "running" && node.State != "paused" && node.State != "open" {
			return out, fmt.Errorf("수집만 가능/달리고 있다/일시 중지된 인텐트를 삭제할 수 있습니다.")
		}
		reason = strings.TrimSpace(reason)
		if reason == "" {
			return out, fmt.Errorf("삭제사유를 적어주세요")
		}
		if node.State == "running" {
			if err := s.engine.ControlWork(ctx, iid, "cancel"); err != nil {
				return out, err
			}
		}
		summary := intentSummaryOf(node)
		if mode == "hard" {
			cleanup, err := t.Store.CancelIntent(iid)
			if err != nil {
				return out, err
			}
			s.cancelWorkerSide(t.ID, t.ExpID, iid)
			t.NotifyCancelled(iid, summary, reason)
			out.Deleted = &cleanup
			out.State = "" // 노드가 삭제되었습니다.,프런트엔드 데이터 Deleted 목록에서 제거
		} else {
			if _, err := t.Store.SoftDeleteIntent(iid, reason); err != nil {
				return out, err
			}
			s.cancelWorkerSide(t.ID, t.ExpID, iid)
			t.NotifyCancelled(iid, summary, reason)
			out.State = db.StateIntentDeleted
		}
	default:
		return out, fmt.Errorf("action must be pause|resume|cancel")
	}
	return out, nil
}

func (s *Server) controlTasksBatch(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	var req struct {
		TaskIDs []string `json:"task_ids"`
		Action  string   `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	if req.Action != "pause" && req.Action != "resume" {
		writeErr(w, 400, "action must be pause|resume")
		return
	}
	taskIDs := normalizeBatchTaskIDs(req.TaskIDs)
	if len(taskIDs) == 0 || len(taskIDs) > maxBatchControlIDs {
		writeErr(w, 400, fmt.Sprintf("task_ids 수량은 다음과 같아야 합니다. 1-%d", maxBatchControlIDs))
		return
	}
	items := make([]batchControlItem, 0, len(taskIDs))
	for _, parsed := range taskIDs {
		item := batchControlItem{ID: parsed.id}
		if !parsed.valid {
			item.Error = "bad task id"
			items = append(items, item)
			continue
		}
		t, ok := s.m.Task(parsed.id)
		if !ok {
			item.Error = "task not found"
			items = append(items, item)
			continue
		}
		result, err := s.applyTaskControl(t, req.Action)
		if err != nil {
			item.Error = err.Error()
		} else {
			item.OK, item.Status, item.Queued = true, result.Status, result.Queued
		}
		items = append(items, item)
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
