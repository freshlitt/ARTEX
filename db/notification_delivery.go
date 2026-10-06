package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// 본 문서는 배송 업무의 수집 및 현황 이관에 관한 문서입니다.。
//
// 수집용「임대」긴 거래 대신：행을 다음으로 설정합니다. sending 그리고 넣어 next_attempt_at 미래를 향해 나아가다
// 임대 만료 시간，네트워크 전송을 하기 전에 트랜잭션을 제출하세요.。이런 식으로 배송 중에는 데이터베이스 잠금이 유지되지 않습니다.——
// 네트워크 요청에는 몇 초 정도 걸릴 수 있습니다.（클라이언트 시간 초과 15 초），행 잠금을 유지하면 동일한 데이터베이스의 다른 쓰기 작업이 중단됩니다.。
//
// 배송 중에 프로세스가 충돌하면 비용이 발생합니다.，길드가 멈춘 곳은 sending。이건**스스로 치유할 수 있다**님：임대 만료 후
// next_attempt_at 과거에 빠져들다，다음번 컬렉션에서도 같은 라인이 다시 픽업될 예정입니다.（받기조건을 확인해보세요
// state IN ('pending','sending')）。수신시 재시도 횟수가 설정됩니다. +1，따라서 충돌이 발생하지 않습니다.
// 무한 재시도——MaxNotifyAttempts 기회가 부족해서 넘어진다 failed 및 기타 수동 처리。

// MaxNotifyAttempts 은 최대 전송 시도 횟수입니다.（처음 포함해서）。
// 은 여기에 정의되어 있으며 전달 엔진에는 정의되어 있지 않습니다.：상태머신 자체의 전략이다.，엔진은 단지 실행자일 뿐이다.。
const MaxNotifyAttempts = 3

// MaxDigestBatchSize 은 한 번에 단일 요약 배치로 결합할 수 있는 최대 배달 수입니다.。
//
// 존재이유는 자원이다：요약주기에 수만개의 취약점이 발견된다면（완전 가능——전체 스캔
// 할 수 있어요），상한이 설정되지 않으면 모든 행을 메모리로 읽어옵니다.、매우 긴 메시지로 렌더링됨，
// 그랬더니 채널의 최대 길이만큼 대부분이 잘려 나갔습니다.——메모리 낭비다，다시**침묵이 사라졌다**잡아낸 그 허점들。
// 상한선 설정 후，초과분은 라이브러리에 남아 다음 배치가 됩니다.，다음주기에는 자연스럽게 발송될 예정입니다，잃지 않을 거예요。
//
// 받아 500 기준：Qiwei의 메시지로 렌더링됩니다. 4096 아직 바이트 상한 내입니다."읽을 내용이 있습니다"의 크기；
// 아무리 크더라도 뒤에서 잘림 현상이 발생하게 됩니다.。
const MaxDigestBatchSize = 500

// NotificationDelivery 은 배달 업무입니다，렌더링에 필요한 채널 구성 및 이벤트 스냅샷이 포함되어 있습니다.。
type NotificationDelivery struct {
	ID            int64           `json:"id"`
	EventID       int64           `json:"event_id"`
	ChannelID     int64           `json:"channel_id"`
	State         string          `json:"state"`
	Attempts      int             `json:"attempts"`
	NextAttemptAt time.Time       `json:"next_attempt_at"`
	LastError     string          `json:"last_error"`
	BatchID       *int64          `json:"batch_id,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	SentAt        *time.Time      `json:"sent_at,omitempty"`
	Snapshot      json.RawMessage `json:"snapshot,omitempty"`
	// 공동으로 로드된 렌더링 컨텍스트，들어가지 않음 JSON（ server 레이어 조립 DTO）。
	Channel *NotificationChannel `json:"-"`
	// FindingID/EventKind 행사에서 가져오기，취약점 세부정보로 바로 이동할 수 있는 기록 목록 제공。
	FindingID int64  `json:"finding_id,string"`
	EventKind string `json:"event_kind"`
	// ChannelName/ChannelKind 은 목록 표시에 사용되는 중복 필드입니다.，프런트 엔드 보조 쿼리 저장。
	ChannelName string `json:"channel_name"`
	ChannelKind string `json:"channel_kind"`
}

const notificationDeliveryCols = `d.id, d.event_id, d.channel_id, d.state, d.attempts, d.next_attempt_at,
       d.last_error, d.batch_id, d.created_at, d.sent_at`

// joinedDeliveryQuery 은 배달 라인의 통합 읽기 모양입니다.：배송 + 이벤트 스냅샷 + 채널 구성。
// 메시지를 렌더링하려면 세 가지가 모두 필수입니다.，왕복 3개는 따로 확인해서 작성해주세요。
const joinedDeliveryQuery = `SELECT ` + notificationDeliveryCols + `,
       e.snapshot, e.kind, e.finding_id,
       c.id, c.name, c.kind, c.enabled, c.config, c.mode, c.filter, c.rate_per_min
FROM notification_deliveries d
JOIN notification_events e ON e.id = d.event_id
JOIN notification_channels c ON c.id = d.channel_id`

func scanNotificationDelivery(sc interface{ Scan(...any) error }) (*NotificationDelivery, error) {
	var (
		dl        NotificationDelivery
		lastErr   sql.NullString
		batchID   sql.NullInt64
		sentAt    sql.NullTime
		snapshot  []byte
		eventKind string
		channel   NotificationChannel
		chEnabled bool
	)
	if err := sc.Scan(&dl.ID, &dl.EventID, &dl.ChannelID, &dl.State, &dl.Attempts, &dl.NextAttemptAt,
		&lastErr, &batchID, &dl.CreatedAt, &sentAt,
		&snapshot, &eventKind, &dl.FindingID,
		&channel.ID, &channel.Name, &channel.Kind, &chEnabled, &channel.Config, &channel.Mode, &channel.Filter, &channel.RatePerMin); err != nil {
		return nil, err
	}
	dl.LastError = lastErr.String
	if batchID.Valid {
		dl.BatchID = &batchID.Int64
	}
	if sentAt.Valid {
		dl.SentAt = &sentAt.Time
	}
	dl.Snapshot = json.RawMessage(snapshot)
	dl.EventKind = eventKind
	dl.ChannelName = channel.Name
	dl.ChannelKind = channel.Kind
	channel.Enabled = &chEnabled
	dl.Channel = &channel
	return &dl, nil
}

// claimQuery 일회성 수집에 대한 설명：먼저 눌러주세요 sel 후보를 선택하고 잠급니다.，그런 다음 sending 그리고
// 임대 연장。sel 에 lease 발신자가 사용하는 위치입니다. $n 위치를 차지하고 직접 매개 변수를 전달。
type claimQuery struct {
	sql  string
	args []any
}

// ClaimRealtimeDeliveries 특정 채널에서 만료된 실시간 배송을 일괄 수신합니다.，대부분 limit 글。
//
// 일부러 눌러보세요**단일 채널**대신 받기「전체 배치를 가져간 다음 처리하세요.」：전류 제한 게이트는 채널에 따라 전달 엔진에 있습니다.
// 유지보수，유일한 방법은 이 채널이 이번 라운드에 얼마나 많은 게시물을 보낼 수 있는지 아는 것입니다.、같은 금액 또 받아，전류가 소모되지 않도록 제한하세요.
// 재시도 횟수。반대라면 먼저 리드하고 포기하세요.，현재 제한에 의해 차단된 행이 1회 계산되었습니다. attempts，
// 3 순전한 대기로 인해 예산이 소모됩니다.，드디어 빠졌어요 failed。
//
// 조건은 다음과 같습니다「임대가 만료되었습니다 sending」——그게 붕괴와 자가치유의 포인트다。lease 은 단일보다 훨씬 커야 합니다.
// 최악의 배송 시간（채널 HTTP 클라이언트 시간 초과 15 초），그렇지 않으면 동일한 줄이 두 줄로 대체됩니다. dispatcher
// 동시배송。비활성화된 채널도 차단합니다.：비활성화 작업에서 인벤토리 배송을 다음으로 표시했습니다. skipped，
// 여기 또 다른 블록，비활성화와 동시 수신 시 누출 방지。
func (d *DB) ClaimRealtimeDeliveries(ctx context.Context, channelID int64, limit int, lease time.Duration) ([]*NotificationDelivery, error) {
	if limit <= 0 {
		return nil, nil
	}
	return d.claimDeliveries(ctx, lease, claimQuery{
		sql: `SELECT dd.id FROM notification_deliveries dd
JOIN notification_channels c ON c.id = dd.channel_id
WHERE dd.channel_id = $1 AND dd.state IN ($2,$3) AND dd.next_attempt_at <= now()
  AND c.enabled AND c.mode = $4
ORDER BY dd.next_attempt_at, dd.id
FOR UPDATE OF dd SKIP LOCKED
LIMIT $5`,
		args: []any{channelID, NotifyStatePending, NotifyStateSending, NotifyModeRealtime, limit},
	}, nil)
}

// DigestBatchDue 채널이 만료된 배치 하나를 충분히 축적했는지 보고합니다.：배송 대기 중입니다.，그리고**가장 나이 많은 사람**
// 연령이 집계기간에 도달했습니다.。
//
// 판정은 벽시계가 아닌 가장 오래된 제출 연령을 기준으로 합니다.：이렇게 하면 새로 구축된 채널은 시간에 맞게 정렬되기 때문에 차단되지 않습니다.
// 즉시 하나만 뱉어내세요「요약」，오랫동안 밀린 배치들은 또 한 라운드를 헛되이 기다리지 않을 것입니다.。
//
// 그리고 ClaimDigestBatch 의미가 다르기 때문에 분리됨：이 기능은 응답에만 사용됩니다.「올려야 할까요?」，
// 그리고 채널을 빼야 받을 수 있어요**모두**출시 예정（아직 미성년자도 포함）——그렇지 않으면 한 사이클
// 은 여러 개의 메시지로 분할됩니다.，요약하면 의미가 없습니다.。
func (d *DB) DigestBatchDue(ctx context.Context, channelID int64, minAge time.Duration) (bool, error) {
	var due bool
	err := d.QueryRowContext(ctx, `SELECT EXISTS (
  SELECT 1 FROM notification_deliveries d
  JOIN notification_channels c ON c.id = d.channel_id
  WHERE d.channel_id = $1 AND d.state IN ($2,$3) AND c.enabled
  GROUP BY d.channel_id
  HAVING min(d.created_at) <= now() - make_interval(secs => $4)
)`, channelID, NotifyStatePending, NotifyStateSending, int64(minAge.Seconds())).Scan(&due)
	return due, err
}

// ClaimDigestBatch 특정 채널로부터 현재 만료된 대기 중인 전송을 수신합니다.，요약 배치로，
// 단일 배치의 최대 개수 MaxDigestBatchSize 글。
//
// 동일한 배치의 모든 배송이 공유됩니다. batch_id，집합에서 가장 작은 값을 사용합니다. id 배치번호를 만든다（안정적、읽기 가능、
// 추가 시퀀스가 필요하지 않습니다.）。재시도 시 사용 COALESCE 원래 배치 번호를 유지하십시오.，만드세요「이번 배치는 N 기사가 함께 발행됩니다.」
// 이 계속 보류됩니다.。
//
// 언론 id 오름차순으로 앞장서서 N 무작위 선택 대신：가장 빠른 배송이 먼저 발송됩니다.，백로그되면 표시되지 않습니다.
// 「새로운 취약점이 먼저 공개되었습니다.、오래된 허점은 항상 뒤에 있다」의 배고픔。
func (d *DB) ClaimDigestBatch(ctx context.Context, channelID int64, limit int, lease time.Duration) ([]*NotificationDelivery, error) {
	if limit <= 0 {
		return nil, nil
	}
	// limit 네**메모리 상한**，발신자 통과 MaxDigestBatchSize；또 하나 있어요，
	// 호출자가 더 큰 값을 전달하는 것을 방지합니다.。
	//
	// 일부러 받아들이지 않음「전류 제한」배치 크기 역할을 합니다.：현재 제한의 단위는 메시지 수입니다.——한 묶음만 발송됩니다
	// 메시지、토큰 소비， server 레이어 takeTokens 공제——그리고「한 묶음에 몇 개입니까?
	// 취약점」은 두 가지 다른 차원입니다.。일단 놔두기 위해서 rate_per_min 예 digest 이 적용되어 각 라운드에 배치됩니다.
	// 요청 예산이 배치 크기로 전달됩니다.，결과 rate=20/min 의 채널은 각 배치에만 설치됩니다. 1 허점，
	// digest 요약 카피라이팅으로 실시간 푸시로 변질。현재 한도를 변경하고 싶다면 변경해주세요. takeTokens 님 want，
	// 여기로 움직이지 마세요。
	if limit > MaxDigestBatchSize {
		limit = MaxDigestBatchSize
	}
	out, err := d.claimDeliveries(ctx, lease, claimQuery{
		sql: `SELECT dd.id FROM notification_deliveries dd
JOIN notification_channels c ON c.id = dd.channel_id
WHERE dd.channel_id = $1 AND dd.state IN ($2,$3) AND dd.next_attempt_at <= now() AND c.enabled
ORDER BY dd.id
FOR UPDATE OF dd SKIP LOCKED
LIMIT $4`,
		args: []any{channelID, NotifyStatePending, NotifyStateSending, limit},
	}, func(tx *sql.Tx, ids []int64) error {
		batchID := ids[0]
		for _, id := range ids {
			if id < batchID {
				batchID = id
			}
		}
		ph, idArgs := placeholders(2, ids)
		_, err := tx.ExecContext(ctx, `UPDATE notification_deliveries SET batch_id = COALESCE(batch_id, $1)
WHERE id IN (`+ph+`)`, append([]any{batchID}, idArgs...)...)
		return err
	})
	return out, err
}

// claimDeliveries 실행「선택 + 세트 sending 임대 연장 + 전체 줄 읽기」，올인원 트랜잭션。
// postClaim 은 선택적 추가 단계입니다.（요약 배치 작성에 사용합니다. batch_id）。
func (d *DB) claimDeliveries(ctx context.Context, lease time.Duration, cq claimQuery, postClaim func(*sql.Tx, []int64) error) ([]*NotificationDelivery, error) {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck // 성공적으로 제출한 후, no-op

	ids, err := selectForClaim(ctx, tx, cq.sql, cq.args...)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, tx.Commit()
	}
	// 세트 sending 그리고 넣어 next_attempt_at 미래를 향해 나아가다：미래의 이 순간이 임대 만료 시간이다，
	// 「임대가 만료되지 않았습니다.」그리고「재시도 시간이 아직 도착하지 않았습니다.」그래서 그들은 동일한 조건식을 공유합니다.，새 열을 추가할 필요가 없습니다.。
	ph, idArgs := placeholders(3, ids)
	if _, err := tx.ExecContext(ctx, `UPDATE notification_deliveries
SET state=$1, attempts=attempts+1, next_attempt_at=now()+make_interval(secs => $2)
WHERE id IN (`+ph+`)`,
		append([]any{NotifyStateSending, lease.Seconds()}, idArgs...)...); err != nil {
		return nil, err
	}
	if postClaim != nil {
		if err := postClaim(tx, ids); err != nil {
			return nil, err
		}
	}
	out, err := loadDeliveriesTx(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

func selectForClaim(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func loadDeliveriesTx(ctx context.Context, tx *sql.Tx, ids []int64) ([]*NotificationDelivery, error) {
	ph, args := placeholders(1, ids)
	rows, err := tx.QueryContext(ctx, joinedDeliveryQuery+` WHERE d.id IN (`+ph+`) ORDER BY d.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*NotificationDelivery{}
	for rows.Next() {
		dl, err := scanNotificationDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, dl)
	}
	return out, rows.Err()
}

// MarkDeliveriesSent 일괄 배송을 배송됨으로 표시。
func (d *DB) MarkDeliveriesSent(ctx context.Context, ids []int64) error {
	ph, args := placeholders(2, ids)
	if len(args) == 0 {
		return nil
	}
	_, err := d.ExecContext(ctx, `UPDATE notification_deliveries
SET state=$1, sent_at=now(), last_error='' WHERE id IN (`+ph+`)`, append([]any{NotifyStateSent}, args...)...)
	return err
}

// RescheduleDeliveries 배송물 일괄 반품 pending 및 재시도 시간 연기。
//
// 복귀 pending 새로운 중간 상태를 도입하는 대신，는「기회는 얼마나 남았나요?」딱 한자리만
// 표현（MaxNotifyAttempts），재시도 전략을 사용하여 상태 머신 분기가 확장되지 않도록 방지。
func (d *DB) RescheduleDeliveries(ctx context.Context, ids []int64, delay time.Duration, errMsg string) error {
	ph, args := placeholders(4, ids)
	if len(args) == 0 {
		return nil
	}
	_, err := d.ExecContext(ctx, `UPDATE notification_deliveries
SET state=$1, next_attempt_at=now()+make_interval(secs => $2), last_error=$3
WHERE id IN (`+ph+`)`,
		append([]any{NotifyStatePending, delay.Seconds(), truncateNotifyError(errMsg)}, args...)...)
	return err
}

// DeferDeliveries 배송물 일괄 반품 pending、즉시 상환 가능，그리고**시계 수령 시도 취소**。
//
// 목적은 하나뿐이다：채널 길이의 상한에 따라 요약 메시지를 세그먼트로 전송하는 경우，이 글에 포함되지 않은 항목은 다음 배치를 위해 저장됩니다.。
// 그건 실패가 아니다，그래서 재시도 예산이 소모되어서는 안 된다.——수신 시 attempts 이미 낙관적이다 +1 ，
// 이건 줄여야해。그렇지 않으면 하나 500 물품잔고는 다음과 같이 나누어집니다. 20 스트립으로 자릅니다. 25 섹션，
// 꼬리 항목은 다음과 같습니다. 3 두안은 MaxNotifyAttempts 종료됨 failed，그리고 그들은 어떤 실수도 하지 않았습니다.。
//
// GREATEST(...,0) 잠깐만요「누군가가 수동으로 다시 보냈습니다. attempts 클리어하고 다시 여기로 왔어요」의 상황，
// 카운트가 마이너스가 되지 않도록 하세요.。
func (d *DB) DeferDeliveries(ctx context.Context, ids []int64, reason string) error {
	ph, args := placeholders(3, ids)
	if len(args) == 0 {
		return nil
	}
	_, err := d.ExecContext(ctx, `UPDATE notification_deliveries
SET state=$1, attempts=GREATEST(attempts-1, 0), next_attempt_at=now(), last_error=$2
WHERE id IN (`+ph+`)`,
		append([]any{NotifyStatePending, truncateNotifyError(reason)}, args...)...)
	return err
}

// FailDeliveries 일괄 배송을 최종 실패로 표시합니다.，배송내역에서 수동 재전송을 기다리는 중입니다.。
func (d *DB) FailDeliveries(ctx context.Context, ids []int64, errMsg string) error {
	// 자리 표시자 $3 시작：$1 네 state、$2 네 last_error。
	ph, args := placeholders(3, ids)
	if len(args) == 0 {
		return nil
	}
	_, err := d.ExecContext(ctx, `UPDATE notification_deliveries SET state=$1, last_error=$2 WHERE id IN (`+ph+`)`,
		append([]any{NotifyStateFailed, truncateNotifyError(errMsg)}, args...)...)
	return err
}

// RetryNotificationDelivery 수동으로 배송 재전송：재설정 pending、재시도 횟수 지우기、
// 즉시 만료됩니다.。카운트 클리어는 고의적——매뉴얼 포인트「재전송」은 이전 실패 원인이 처리되었음을 의미합니다.，
// 예전 카운트로 제한하는 건 말도 안 돼요。
func (d *DB) RetryNotificationDelivery(ctx context.Context, id int64) error {
	res, err := d.ExecContext(ctx, `UPDATE notification_deliveries
SET state=$2, attempts=0, next_attempt_at=now(), last_error=''
WHERE id=$1 AND state IN ($3,$4)`, id, NotifyStatePending, NotifyStateFailed, NotifyStateSkipped)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("배송 %d 존재하지 않거나 현재 재전송을 허용하지 않는 상태입니다.", id)
	}
	return nil
}

// NotificationDeliveryFilter 은 배송이력 조회 조건입니다.。
type NotificationDeliveryFilter struct {
	ChannelID int64
	State     string
	EventKind string
}

func (f NotificationDeliveryFilter) where() (string, []any) {
	var conds []string
	var args []any
	if f.ChannelID > 0 {
		args = append(args, f.ChannelID)
		conds = append(conds, fmt.Sprintf("d.channel_id=$%d", len(args)))
	}
	if f.State != "" {
		args = append(args, f.State)
		conds = append(conds, fmt.Sprintf("d.state=$%d", len(args)))
	}
	if f.EventKind != "" {
		args = append(args, f.EventKind)
		conds = append(conds, fmt.Sprintf("e.kind=$%d", len(args)))
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// ListNotificationDeliveries 페이지별로 배송내역으로 돌아가기，새로운 것 먼저。
func (d *DB) ListNotificationDeliveries(ctx context.Context, f NotificationDeliveryFilter, page, pageSize int) ([]*NotificationDelivery, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 50
	}
	where, args := f.where()

	var total int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM notification_deliveries d
JOIN notification_events e ON e.id = d.event_id`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	q := fmt.Sprintf("%s%s ORDER BY d.id DESC LIMIT $%d OFFSET $%d",
		joinedDeliveryQuery, where, len(args)+1, len(args)+2)
	rows, err := d.QueryContext(ctx, q, append(args, pageSize, (page-1)*pageSize)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []*NotificationDelivery{}
	for rows.Next() {
		dl, err := scanNotificationDelivery(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, dl)
	}
	return out, total, rows.Err()
}

// truncateNotifyError 오류 메시지를 허용 가능한 열 길이로 자릅니다.。채널에서 반환된 응답 본문이 매우 길 수 있습니다.
// （일반 Webhook 특히 자체 구축 서비스를 호출할 때），잘림에 실패하면 기록 목록의 로드가 확장됩니다.。
func truncateNotifyError(msg string) string {
	const max = 500
	if len(msg) <= max {
		return msg
	}
	// 문자 경계로 뒤로 물러남，반만 남기지 마세요 UTF-8 문자로 인해 프런트 엔드에 잘못된 문자가 표시됩니다.。
	cut := max
	for cut > 0 && !isUTF8Start(msg[cut]) {
		cut--
	}
	return msg[:cut] + "…"
}

func isUTF8Start(b byte) bool { return b&0xC0 != 0x80 }

// placeholders 생성 위치 start 시작됨 $n 자리표시자 문자열 및 해당 매개변수， IN (...) 사용。
// 예를 들면 start=3, ids=[7,8] → "$3,$4", [7,8]。
func placeholders(start int, ids []int64) (string, []any) {
	ph := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for i, id := range ids {
		ph = append(ph, fmt.Sprintf("$%d", start+i))
		args = append(args, id)
	}
	return strings.Join(ph, ","), args
}
