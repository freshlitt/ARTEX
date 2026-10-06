package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const FindingRetestAgentKey = "retester"

var ErrRetestNotRunning = errors.New("이 재테스트는 종료되었거나 아직 시작되지 않았습니다.，취약점 세부정보에서 새로 재테스트를 시작하세요.")

// FindingRetest is an immutable historical test once its conversation turn ends.
// Snapshot is only loaded for the agent, never sent with the history list.
type FindingRetest struct {
	ID             int64           `json:"id"`
	FindingID      int64           `json:"finding_id"`
	ConversationID *int64          `json:"conversation_id"`
	Status         string          `json:"status"`
	Verdict        string          `json:"verdict"`
	Notes          string          `json:"notes"`
	Snapshot       json.RawMessage `json:"snapshot,omitempty"`
	Summary        string          `json:"summary"`
	Evidence       string          `json:"evidence"`
	Error          string          `json:"error"`
	CreatedAt      time.Time       `json:"created_at"`
	StartedAt      *time.Time      `json:"started_at"`
	FinishedAt     *time.Time      `json:"finished_at"`
}

const retestCols = `id, finding_id, conversation_id, status, verdict, notes, summary, evidence, error, created_at, started_at, finished_at`

// ActiveFindingRetest is the small status payload polled by the findings list.
// Finding IDs use the same string representation as the findings API.
type ActiveFindingRetest struct {
	ID             int64  `json:"id"`
	FindingID      int64  `json:"finding_id,string"`
	ConversationID int64  `json:"conversation_id"`
	Status         string `json:"status"`
}

func (d *DB) ListActiveFindingRetests(ctx context.Context) ([]ActiveFindingRetest, error) {
	rows, err := d.QueryContext(ctx, `SELECT id, finding_id, conversation_id, status FROM finding_retests
	WHERE status IN ('pending','running') AND conversation_id IS NOT NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ActiveFindingRetest{}
	for rows.Next() {
		var item ActiveFindingRetest
		if err := rows.Scan(&item.ID, &item.FindingID, &item.ConversationID, &item.Status); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanRetest(row interface{ Scan(...any) error }) (*FindingRetest, error) {
	r := &FindingRetest{}
	err := row.Scan(&r.ID, &r.FindingID, &r.ConversationID, &r.Status, &r.Verdict, &r.Notes,
		&r.Summary, &r.Evidence, &r.Error, &r.CreatedAt, &r.StartedAt, &r.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

// CreateFindingRetest atomically snapshots the source, creates its conversation
// and persists the first message. A finding row lock deduplicates simultaneous
// clicks across clients; an existing active run is returned without dispatching.
func (d *DB) CreateFindingRetest(ctx context.Context, findingID int64, notes string) (*FindingRetest, *Conversation, bool, error) {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, false, err
	}
	defer tx.Rollback()
	var title string
	var snapshot []byte
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(NULLIF(f.name,''), NULLIF(f.vulnclass,''), '분류되지 않음'),
	jsonb_build_object('finding', to_jsonb(f),
	 'assets', COALESCE((SELECT jsonb_agg(to_jsonb(a)) FROM assets a WHERE f.asset_ids @> to_jsonb(ARRAY[a.id])), '[]'::jsonb),
	 'constraints', COALESCE((SELECT jsonb_agg(to_jsonb(c)) FROM task_constraints c JOIN tasks t ON t.exploration_id=c.exploration_id WHERE t.id=f.task_id), '[]'::jsonb))
	FROM findings f WHERE f.id=$1 FOR UPDATE OF f`, findingID).Scan(&title, &snapshot)
	if err != nil {
		return nil, nil, false, err
	}
	r, err := scanRetest(tx.QueryRowContext(ctx, `SELECT `+retestCols+` FROM finding_retests WHERE finding_id=$1 AND status IN ('pending','running')`, findingID))
	if err != nil {
		return nil, nil, false, err
	}
	if r != nil {
		return r, nil, false, nil
	}
	// Keep the title within the same limit as ordinary conversations.
	if runes := []rune(title); len(runes) > 100 {
		title = string(runes[:100])
	}
	c, err := scanConv(tx.QueryRowContext(ctx, `INSERT INTO conversations(agent_key,title) VALUES ($1,$2) RETURNING `+convCols,
		FindingRetestAgentKey, fmt.Sprintf("재테스트 #%d · %s", findingID, title)))
	if err != nil {
		return nil, nil, false, err
	}
	r, err = scanRetest(tx.QueryRowContext(ctx, `INSERT INTO finding_retests(finding_id,conversation_id,notes,snapshot) VALUES ($1,$2,$3,$4) RETURNING `+retestCols,
		findingID, c.ID, strings.TrimSpace(notes), snapshot))
	if err != nil {
		return nil, nil, false, err
	}
	msg := r.InitialMessage()
	_, err = tx.ExecContext(ctx, `INSERT INTO conversation_activities(conversation_id,worker,kind,summary,detail) VALUES ($1,$2,'user',$3,$4)`,
		c.ID, FindingRetestAgentKey, fmt.Sprintf("취약점을 다시 테스트해 보세요. #%d", findingID), msg)
	if err != nil {
		return nil, nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, false, err
	}
	return r, &c, true, nil
}

func (r *FindingRetest) InitialMessage() string {
	msg := fmt.Sprintf("취약점을 다시 테스트해 보세요. #%d。먼저 전화해 보세요 get_finding_retest_context 이 세션과 관련된 원본 증거 및 제약 조건을 읽어보세요.，타겟 검증을 다시 수행합니다.，마지막 통화 record_finding_retest_result 결론 저장。", r.FindingID)
	if r.Notes != "" {
		msg += "\n\n이번 재시험에 대한 보충 지침：\n" + r.Notes
	}
	return msg
}

func (d *DB) ListFindingRetests(findingID int64) ([]*FindingRetest, error) {
	rows, err := d.Query(`SELECT `+retestCols+` FROM finding_retests WHERE finding_id=$1 ORDER BY id DESC`, findingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*FindingRetest{}
	for rows.Next() {
		r, err := scanRetest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (d *DB) FindingRetestForConversation(ctx context.Context, conversationID int64) (*FindingRetest, error) {
	r, err := scanRetest(d.QueryRowContext(ctx, `SELECT `+retestCols+` FROM finding_retests WHERE conversation_id=$1`, conversationID))
	if err != nil || r == nil {
		return r, err
	}
	err = d.QueryRowContext(ctx, `SELECT snapshot FROM finding_retests WHERE id=$1`, r.ID).Scan(&r.Snapshot)
	return r, err
}

// FailPendingRetestForConversation seals a conversation's unfinished retest when
// the runner could not even load it — the retest ID is unknown on that path, so
// the conversation ID is the only handle. Without it a transient read error
// leaves the row 'pending' forever: the findings list keeps showing 재시험 중 and
// every later 재테스트 시작 is deduped against a run that is not happening, with only
// a process restart (RecoverFindingRetests) able to clear it.
func (d *DB) FailPendingRetestForConversation(conversationID int64, reason string) error {
	_, err := d.Exec(`UPDATE finding_retests SET status='failed', error=$2, finished_at=now()
		WHERE conversation_id=$1 AND status IN ('pending','running')`, conversationID, reason)
	return err
}

func (d *DB) StartFindingRetest(ctx context.Context, id int64) (bool, error) {
	res, err := d.ExecContext(ctx, `UPDATE finding_retests SET status='running', started_at=now() WHERE id=$1 AND status='pending'`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// RecordFindingRetestResult never accepts a finding ID: ownership comes from the
// runtime conversation. Identical retries are safe; a second verdict is refused.
func (d *DB) RecordFindingRetestResult(ctx context.Context, conversationID int64, verdict, summary, evidence string) error {
	if verdict != "reproduced" && verdict != "fixed" && verdict != "inconclusive" {
		return errors.New("verdict 반드시 reproduced / fixed / inconclusive")
	}
	summary, evidence = strings.TrimSpace(summary), strings.TrimSpace(evidence)
	if summary == "" || evidence == "" {
		return errors.New("summary 그리고 evidence 은 비워둘 수 없습니다.；확인이 불가능할 경우 실제 점검 내용과 방해 원인을 설명해 주시기 바랍니다.")
	}
	if len(summary) > 16000 || len(evidence) > 128000 {
		return errors.New("재시험 결론이 너무 깁니다.（summary ≤ 16KB，evidence ≤ 128KB）")
	}
	res, err := d.ExecContext(ctx, `UPDATE finding_retests SET verdict=$2,summary=$3,evidence=$4
	WHERE conversation_id=$1 AND status='running' AND (verdict='' OR (verdict=$2 AND summary=$3 AND evidence=$4))`, conversationID, verdict, summary, evidence)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrRetestNotRunning
	}
	return nil
}

// FinishFindingRetest seals the result. Cancellation/failure takes precedence
// over a staged verdict so an interrupted test cannot appear successfully fixed.
// Only a newly completed fixed verdict updates triage, in the same transaction.
func (d *DB) FinishFindingRetest(id int64, status, reason string) error {
	if status != "completed" && status != "failed" && status != "stopped" {
		return errors.New("invalid terminal retest status")
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Lock the finding before the retest, matching creation and cascading deletion.
	var findingID int64
	err = tx.QueryRow(`SELECT f.id FROM findings f WHERE f.id=(SELECT finding_id FROM finding_retests WHERE id=$1) FOR UPDATE OF f`, id).Scan(&findingID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // Finding/retest already deleted.
	}
	if err != nil {
		return err
	}
	var finalStatus, verdict string
	err = tx.QueryRow(`UPDATE finding_retests SET
	status=CASE WHEN $2='completed' AND verdict='' THEN 'failed' ELSE $2 END,
	error=CASE WHEN $2='completed' AND verdict='' THEN 'Agent 재시험 결론이 저장되지 않았습니다.，세션을 확인하고 다시 테스트해 보세요.' ELSE $3 END,
	finished_at=now() WHERE id=$1 AND status IN ('pending','running') RETURNING status,verdict`, id, status, reason).Scan(&finalStatus, &verdict)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // A replay must not overwrite a later manual triage decision.
	}
	if err != nil {
		return err
	}
	if finalStatus == "completed" && verdict == "fixed" {
		// 운송 통지 버전，은 세부정보 페이지에서 상태를 수동으로 변경하는 것과 동일한 의미 집합을 공유합니다.。
		//
		// 예전에는 여기 알몸이었는데 UPDATE：재시험 및 판단「고정됨」상태가 정말 바뀌었어요，그런데 일치하네요
		// on_status_change 님의 채널은 푸시를 전혀 수신할 수 없습니다.——인터페이스에서 상태가 조용히 바뀌었습니다.，
		// 플랫폼을 열어야 운영과 유지보수를 알 수 있습니다。상태 업데이트와 푸시 이벤트는 함께 기록되어야 합니다.，
		// SetFindingStatusTx 내부적으로 처리됨「상태가 변하지 않으면 등록하지 않습니다.」및 기타 세부사항。
		// 사용 context.Background()：전체 기능은 None 입니다. ctx 의 옛날 스타일（d.Begin()/
		// tx.QueryRow/tx.Exec），취소 신호가 전달되지 않습니다.，하나만 추가하세요 ctx 매개변수 회의
		// 영향력 server 사이드 콜 포인트 및 여러 테스트，이 변경 범위를 벗어납니다.。
		if _, _, _, _, err := SetFindingStatusTx(context.Background(), tx, findingID, FindingFixed); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) RecoverFindingRetests() error {
	_, err := d.Exec(`UPDATE finding_retests SET status='stopped', error='서비스 재시작，재시험이 중단되었습니다，다시 시작해주세요', finished_at=now() WHERE status IN ('pending','running')`)
	return err
}
