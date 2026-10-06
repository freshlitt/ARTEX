package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/db"
)

// 개요「제약사항 관리」님의 노고 CRUD 인터페이스 + 분사범위 스위치 분석。운영상의 제약(allow/deny)그리고 agent 쪽
// set_constraints 도구는 동일하게 작성 task_constraints 테이블;여기가 인간이 있는 곳이다 UI 직접 추가, 삭제, 수정。제약조건은
// 프롬프트 컨텍스트——추가, 삭제, 수정 후【아니요】공지사항 planner,예정된 자연읽기 도서관 차기작 시행(제품에 따른 결정)。변화할 때마다 handler
// 다같이 가자 beginTaskOperation/decInflight,작업 삭제로 경쟁 조건 방지(및 대상/의도 CRUD 일관됨)。

// 주입 범위 스위치 settings key,둘 다 기본적으로 활성화되어 있습니다.(GetBool 두 번째 매개변수 = true)。
const (
	settingConstraintsInjectPlanner = "constraints_inject_planner"
	settingConstraintsInjectWorker  = "constraints_inject_worker"
)

// constraintInjectPlanner / constraintInjectWorker 해당 항목에 연산 제약사항이 주입되었는지 보고합니다. agent 님
// 시스템 프롬프트(기본적으로 켜져 있음)。 resolver 패스 planner/worker,라운드마다 읽어보세요 → 즉시 적용되도록 스위치를 변경하세요.。
func (s *Server) constraintInjectPlanner() bool {
	return s.m.pg.GetBool(settingConstraintsInjectPlanner, true)
}

func (s *Server) constraintInjectWorker() bool {
	return s.m.pg.GetBool(settingConstraintsInjectWorker, true)
}

// listConstraints 이 작업의 모든 운영 제약 조건을 반환합니다.(allow 앞、deny 뒤에)。
func (s *Server) listConstraints(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	rows, err := t.Store.ListConstraints()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"constraints": constraintDTOs(rows)})
}

// addConstraint 작업 제약 조건을 수동으로 추가(kind=allow|deny)。알림 없음 planner。
func (s *Server) addConstraint(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	if !s.engine.beginTaskOperation(t.ID) {
		writeErr(w, 409, "작업을 삭제하는 중입니다.,제약조건을 추가할 수 없습니다.")
		return
	}
	defer s.engine.decInflight(t.ID)

	var body struct {
		Text string `json:"text"`
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" {
		writeErr(w, 400, "제약조건 내용은 비워둘 수 없습니다.")
		return
	}
	kind := normalizeConstraintKind(body.Kind)
	if kind == "" {
		writeErr(w, 400, "kind 반드시 allow 또는 deny")
		return
	}
	id, err := t.Store.AddConstraint(kind, text, "human")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, ConstraintDTO{ID: strconv.FormatInt(id, 10), Kind: kind, Text: text, Origin: "human"})
}

// editConstraint 제약 조건을 수동으로 수정(kind + text)。알림 없음 planner。
func (s *Server) editConstraint(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	if !s.engine.beginTaskOperation(t.ID) {
		writeErr(w, 409, "작업을 삭제하는 중입니다.,제약 조건을 수정할 수 없습니다.")
		return
	}
	defer s.engine.decInflight(t.ID)

	cid, err := strconv.ParseInt(r.PathValue("cid"), 10, 64)
	if err != nil || cid <= 0 {
		writeErr(w, 400, "bad constraint id")
		return
	}
	var body struct {
		Text string `json:"text"`
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" {
		writeErr(w, 400, "제약조건 내용은 비워둘 수 없습니다.")
		return
	}
	kind := normalizeConstraintKind(body.Kind)
	if kind == "" {
		writeErr(w, 400, "kind 반드시 allow 또는 deny")
		return
	}
	if err := t.Store.UpdateConstraint(cid, kind, text); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, ConstraintDTO{ID: strconv.FormatInt(cid, 10), Kind: kind, Text: text})
}

// deleteConstraint 제약조건 수동 삭제。알림 없음 planner。
func (s *Server) deleteConstraint(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	if !s.engine.beginTaskOperation(t.ID) {
		writeErr(w, 409, "작업을 삭제하는 중입니다.,제약조건을 삭제할 수 없습니다.")
		return
	}
	defer s.engine.decInflight(t.ID)

	cid, err := strconv.ParseInt(r.PathValue("cid"), 10, 64)
	if err != nil || cid <= 0 {
		writeErr(w, 400, "bad constraint id")
		return
	}
	if err := t.Store.DeleteConstraint(cid); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// normalizeConstraintKind lowercases + validates the kind; "" on invalid.
func normalizeConstraintKind(k string) string {
	k = strings.TrimSpace(strings.ToLower(k))
	if k == "allow" || k == "deny" {
		return k
	}
	return ""
}

// constraintDTOs converts db rows to the frontend shape.
func constraintDTOs(in []db.Constraint) []ConstraintDTO {
	out := make([]ConstraintDTO, 0, len(in))
	for _, c := range in {
		out = append(out, ConstraintDTO{
			ID:     strconv.FormatInt(c.ID, 10),
			Kind:   c.Kind,
			Text:   c.Text,
			Origin: c.Origin,
			TS:     rfc3339(c.CreatedAt),
		})
	}
	return out
}
