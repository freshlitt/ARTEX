package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/db"
)

// 개요「목표관리」님의 노고 CRUD 인터페이스。그리고 agent 쪽 set_goals 도구는 동일한 배치를 작성합니다. goal 노드,
// 근데 입구는 인간이네 UI 직접 추가, 삭제, 수정;새로운/수정 후 재사용「부활임무」논리(admitTask resume:
// 최종 상태→running、일시정지 해제、필요한 경우 대기열),부활하지 않고 삭제(제품에 따른 결정)。변화할 때마다 handler 다같이 가자
// beginTaskOperation/decInflight,작업 삭제로 경쟁 조건 방지(와 의도 CRUD 일관됨)。

// listGoals 이 작업의 모든 목표로 돌아가기(text/vulnclass/state 분해됨),대상 관리 카드 렌더링용。
func (s *Server) listGoals(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	goals, err := t.Store.ListByKind(db.KindGoal, 10000)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"goals": goalDTOs(goals)})
}

// addGoal 수동으로 대상 추가:라이브러리 삭제(작업 루트에 정지 spawns 다음)→ 하나만 기억하세요「새로운 대상이 추가되었습니다.」트리거 웨이크업
// planner → 부활임무,새로운 목표를 바탕으로 달성 여부를 기획자가 다시 판단하게 해주세요.。
func (s *Server) addGoal(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	if !s.engine.beginTaskOperation(t.ID) {
		writeErr(w, 409, "작업을 삭제하는 중입니다.,대상을 추가할 수 없습니다.")
		return
	}
	defer s.engine.decInflight(t.ID)

	var body struct {
		Text      string `json:"text"`
		VulnClass string `json:"vulnclass"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" {
		writeErr(w, 400, "대상 콘텐츠는 비워둘 수 없습니다.")
		return
	}
	payload := map[string]any{"text": text}
	if vc := strings.TrimSpace(body.VulnClass); vc != "" {
		payload["vulnclass"] = vc
	}
	id, err := t.Store.AddGoal(payload, "human")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if of, _ := t.Store.OriginFactID(); of > 0 && id > 0 {
		_ = t.Store.Link(of, db.RelSpawns, id) // goal descends from the task root (origin fact)
	}
	t.NotifyGoal([]string{text}) // 참고「사람들이 목표를 추가했습니다:…」트리거 및 깨우기 planner
	s.reviveTask(t)              // 완료/일시 중지된 작업을 다시 실행 상태로 되돌리고 계속 실행합니다.
	node, _ := t.Store.GetNode(id)
	if node == nil {
		writeErr(w, 500, "대상 쓰기 후 읽기에 실패했습니다.")
		return
	}
	writeJSON(w, 200, goalDTO(node))
}

// editGoal 대상 텍스트 수동 수정(그리고 vulnclass):데이터베이스 변경 → 참고「사용자가 대상을 수정했습니다. old 이 됩니다. new」
// 트리거 웨이크업 planner → 부활임무,기획자가 새로운 목표에 따라 방향을 조정하게 하세요.。
func (s *Server) editGoal(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	if !s.engine.beginTaskOperation(t.ID) {
		writeErr(w, 409, "작업을 삭제하는 중입니다.,대상을 수정할 수 없습니다.")
		return
	}
	defer s.engine.decInflight(t.ID)

	gid, err := strconv.ParseInt(r.PathValue("gid"), 10, 64)
	if err != nil || gid <= 0 {
		writeErr(w, 400, "bad goal id")
		return
	}
	var body struct {
		Text      string `json:"text"`
		VulnClass string `json:"vulnclass"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" {
		writeErr(w, 400, "대상 콘텐츠는 비워둘 수 없습니다.")
		return
	}
	node, err := t.Store.GetNode(gid)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if node == nil || node.Kind != db.KindGoal {
		writeErr(w, 404, "대상이 존재하지 않습니다.")
		return
	}
	oldText := goalDTO(node).Text
	if err := t.Store.UpdateGoalPayload(gid, text, strings.TrimSpace(body.VulnClass)); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	t.NotifyGoalEdited(oldText, text) // 참고「사람들이 타겟을 다음과 같이 수정했습니다. old 이 됩니다. new」트리거 및 깨우기 planner
	s.reviveTask(t)                   // 신규 추가에 맞춰:부활미션은 새로운 타겟을 기준으로 재심사 예정입니다.
	updated, _ := t.Store.GetNode(gid)
	if updated == nil {
		writeErr(w, 500, "대상 업데이트 후 읽기에 실패했습니다.")
		return
	}
	writeJSON(w, 200, goalDTO(updated))
}

// deleteGoal 대상 수동 삭제(완전 삭제,캐스케이드 에지 삭제/앵커 포인트):데이터베이스 삭제 → 참고「사용자가 대상을 삭제했습니다. X」
// 트리거 웨이크업 planner 이를 토대로 나머지 대상자를 재심사하게 됩니다.。제품에 따른 결정,삭제【아니요】부활임무。
func (s *Server) deleteGoal(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	if !s.engine.beginTaskOperation(t.ID) {
		writeErr(w, 409, "작업을 삭제하는 중입니다.,대상을 삭제할 수 없습니다.")
		return
	}
	defer s.engine.decInflight(t.ID)

	gid, err := strconv.ParseInt(r.PathValue("gid"), 10, 64)
	if err != nil || gid <= 0 {
		writeErr(w, 400, "bad goal id")
		return
	}
	node, err := t.Store.GetNode(gid)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if node == nil || node.Kind != db.KindGoal {
		writeErr(w, 404, "대상이 존재하지 않습니다.")
		return
	}
	text := goalDTO(node).Text
	if err := t.Store.DeleteGoal(gid); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	t.NotifyGoalDeleted(text) // 참고「사람이 이 대상을 삭제했습니다.:…」트리거 및 깨우기 planner(부활미션 없음)
	writeJSON(w, 200, map[string]bool{"ok": true})
}
