package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Autumn-27/artex/notify"
)

// 이 문서는 IM 푸시 채널 구성 및 이벤트 레이어。배송업무 수집 및 현황 흐름
// db/notification_delivery.go。
//
// 두 개의 불변，이 파일 변경시 꼭 보관해주세요：
//
//  1. 취약점 트랜잭션 쓰기(RecordFindingTx)통화만 가능 InsertNotificationEventTx 블라인드 삽입을 하세요，
//     알림 관련 표는 읽지 마세요.、필터 일치 없음。여기에 소개된 모든 읽기 작업은 다음으로 인해 발생할 수 있습니다.
//     사용자가 필터 조건을 불일치하여 취약한 쓰기 트랜잭션을 오염시키거나 심지어 중단합니다.。
//  2. 필터 일치는 오류를 보고하지 않습니다.：구성이 비정상이라면 다음을 누르세요.「히트」처리 중(또 만나요 notify.Match)。차라리 더 밀어붙이고 싶어，
//     놓칠 수 없다。

// ErrNotificationChannelNotFound 채널이 존재하지 않습니다。
var ErrNotificationChannelNotFound = errors.New("알림 채널이 존재하지 않습니다.")

// 배송상태。
const (
	NotifyStatePending = "pending" // 출발 준비 완료
	NotifyStateSending = "sending" // 이 되었습니다. dispatcher 받기，임대가 만료되지 않았습니다.
	NotifyStateSent    = "sent"    // 배달됨
	NotifyStateFailed  = "failed"  // 소진되거나 영구적인 실패를 재시도합니다.，수동으로 재전송 가능
	NotifyStateSkipped = "skipped" // 채널이 비활성화되었습니다，더 이상 보내지 않습니다.
)

// 푸시 모드。
const (
	NotifyModeRealtime = "realtime"
	NotifyModeDigest   = "digest"
)

// ValidNotifyMode 화이트리스트 확인 푸시 모드（그리고 findings.status 같은 이유：필요없어요 DB CHECK，
// 후속 확장을 용이하게 하기 위해）。
func ValidNotifyMode(m string) bool {
	return m == NotifyModeRealtime || m == NotifyModeDigest
}

// NotificationChannel 은 채널 인스턴스 구성입니다.。Config 그리고 Filter 원본 그대로 유지하세요 JSON，
// 분석은 에게 맡기세요 notify 패키지——db 레이어가 해당 필드의 의미를 이해하지 못합니다.。
type NotificationChannel struct {
	ID     int64           `json:"id"`
	Name   string          `json:"name"`
	Kind   string          `json:"kind"`
	Mode   string          `json:"mode"`
	Config json.RawMessage `json:"config"`
	Filter json.RawMessage `json:"filter"`
	// Enabled 구분을 위해 포인터가 사용됩니다.「이 필드는 전달되지 않습니다.」그리고「명시적 전송 false」——
	// 프런트 엔드 스위치 제어는 변경된 필드만 제출합니다.。
	Enabled    *bool     `json:"enabled,omitempty"`
	RatePerMin int       `json:"rate_per_min"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// IsEnabled 리턴 채널이 활성화되어 있나요?；Enabled 입니다 nil（로드되지 않음）처리 활성화를 누르세요.。
func (c *NotificationChannel) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }

// NotificationEvent 은 사건사실입니다。
type NotificationEvent struct {
	ID        int64           `json:"id"`
	Kind      string          `json:"kind"`
	FindingID int64           `json:"finding_id"`
	Snapshot  json.RawMessage `json:"snapshot"`
	CreatedAt time.Time       `json:"created_at"`
}

const notificationChannelCols = `id, name, kind, enabled, config, mode, filter, rate_per_min, created_at, updated_at`

func scanNotificationChannel(sc interface{ Scan(...any) error }) (*NotificationChannel, error) {
	var c NotificationChannel
	var enabled bool
	if err := sc.Scan(&c.ID, &c.Name, &c.Kind, &enabled, &c.Config, &c.Mode, &c.Filter, &c.RatePerMin, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	c.Enabled = &enabled
	return &c, nil
}

// ListNotificationChannels 모든 채널 인스턴스로 돌아가기，활성화된 항목이 1순위입니다.、동급프레스 id。
// 정렬 SQL 너를 위해서야 UI 그리고 dispatcher 동일한 안정적인 시퀀스를 참조하세요.。
func (d *DB) ListNotificationChannels(ctx context.Context) ([]*NotificationChannel, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+notificationChannelCols+` FROM notification_channels
ORDER BY enabled DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*NotificationChannel{}
	for rows.Next() {
		c, err := scanNotificationChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// NotificationChannelByID 단일 채널 구입。
func (d *DB) NotificationChannelByID(ctx context.Context, id int64) (*NotificationChannel, error) {
	row := d.QueryRowContext(ctx, `SELECT `+notificationChannelCols+` FROM notification_channels WHERE id=$1`, id)
	c, err := scanNotificationChannel(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotificationChannelNotFound
	}
	return c, err
}

// SaveNotificationChannel 채널 생성 또는 업데이트。
//
// 업데이트할 때 호출자가 명시적으로 제공한 필드만 덮어쓰게 됩니다.（아니요 nil / 비어있지 않음），이렇게 하면 프런트엔드에서 부분적으로 제출할 수 있습니다.
// 서랍 형태를 수정함，돌아오지 않고 config 의 필드——포스트백으로 인해
// 「마스크 값이 실제 키를 덮습니다.」님의 사고。
func (d *DB) SaveNotificationChannel(ctx context.Context, c *NotificationChannel) (int64, error) {
	if c.Mode == "" {
		c.Mode = NotifyModeRealtime
	}
	// 이건 고의적인거다**아니요**예 0 처리를 해주세요：0 은 합법적인 구성입니다.，뜻「전류 제한 없음」。
	//
	// 한번 쓴 글 `if c.RatePerMin <= 0 { c.RatePerMin = 기본값 }`，원래 의도는「지정하지 않은 경우
	// 안전한 기본값을 제공하세요.」，그런데 그거「명시적으로 다음으로 설정됨 0」도 같이 삼켰어요——문서、UI 팁 &amp;
	// takeTokens 모두 0 전류 제한이 없는 것으로 해석됩니다.，여기만 조용히 변경됩니다 20（딩톡/치웨이/Telegram）
	// 또는 100（페이슈），운영자는 전류 제한이 완화되었다고 생각했습니다.、실제로는 20/아무런 메시지도 없이 몇 분 동안 멈췄습니다.。
	//
	// 「지정되지 않음」그리고「명시적 0」의 차이점은 발신자만 알 수 있습니다.（요청 본문의 기본 필드 vs 전송 취소 0），
	// 따라서 기본값은 다음과 같습니다. server 필드가 기본값인 경우 레이어가 채워집니다.，또 만나요 notifyCreateChannel。
	if c.RatePerMin < 0 {
		return 0, errors.New("전류 제한 값은 음수일 수 없습니다.")
	}
	if c.Config == nil {
		c.Config = json.RawMessage(`{}`)
	}
	if c.Filter == nil {
		c.Filter = json.RawMessage(`{}`)
	}
	enabled := c.IsEnabled()

	if c.ID == 0 {
		var id int64
		err := d.QueryRowContext(ctx, `INSERT INTO notification_channels(name,kind,enabled,config,mode,filter,rate_per_min)
VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
			c.Name, c.Kind, enabled, string(c.Config), c.Mode, string(c.Filter), c.RatePerMin).Scan(&id)
		return id, err
	}
	res, err := d.ExecContext(ctx, `UPDATE notification_channels
SET name=$2, kind=$3, enabled=$4, config=$5, mode=$6, filter=$7, rate_per_min=$8
WHERE id=$1`,
		c.ID, c.Name, c.Kind, enabled, string(c.Config), c.Mode, string(c.Filter), c.RatePerMin)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, ErrNotificationChannelNotFound
	}
	return c.ID, nil
}

// SetNotificationChannelEnabled 시작 및 중지 전환。
//
// 채널 비활성화 시，보내지 않은 모든 배달을 다음으로 표시합니다. skipped：그렇지 않으면 다시 활성화한 후
// 갑자기 일괄배송이 오네요「비활성화 중 백로그」에 대한 오래된 취약점，기간이 만료되어 신규 추가로 오인되기 쉽습니다.。
func (d *DB) SetNotificationChannelEnabled(ctx context.Context, id int64, enabled bool) error {
	return d.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE notification_channels SET enabled=$2 WHERE id=$1`, id, enabled)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotificationChannelNotFound
		}
		if !enabled {
			if _, err := tx.ExecContext(ctx, `UPDATE notification_deliveries SET state=$2, last_error=$3
WHERE channel_id=$1 AND state IN ($4,$5)`,
				id, NotifyStateSkipped, "채널이 비활성화되었습니다", NotifyStatePending, NotifyStateSending); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteNotificationChannel 채널 삭제。외래키 캐스케이드와 함께 전송 내역이 삭제됩니다.
// （채널 구성이 사라졌습니다，역사를 해석할 수 없습니다.）。
func (d *DB) DeleteNotificationChannel(ctx context.Context, id int64) error {
	res, err := d.ExecContext(ctx, `DELETE FROM notification_channels WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotificationChannelNotFound
	}
	return nil
}

// RecordNotificationEventTx 발신자의 거래에서**최선을 다해**푸시 이벤트 작성。
//
// 취약점 쓰기 경로에 대한 유일한 알림 관련 변경 사항입니다.：한번 INSERT，표를 읽지 마세요.、채널을 모르겠어요、
// 필터링을 실행하지 마세요。거래 제출이 보장됩니다.「취약점 로깅」그리고「푸시 작업이 존재합니다.」원자적 일관성，
// 제출에 성공했지만 팀에 합류하지 못한 것은 없습니다.、메시지가 영구적으로 손실되는 창。
//
// 두 가지 핵심 디자인，아무렇게나 쓴 것이 아닙니다.：
//
//  1. **왜 사용하는가? SAVEPOINT**：PostgreSQL 거래 내역 중 어느 하나라도 오류가 발생하면 거래 전체가 입력됩니다.
//     aborted 상태，이 이후의 모든 진술（포함 COMMIT）항상 실패함。그래서「무시하세요 INSERT
//     오류、발신자가 계속해서 제출하도록 하세요.」에 PG 여기서는 안 돼요——오류를 격리하기 위해 저장점을 사용하지 않는 한
//     이 발언에。저장 포인트 없음，이제 남은 건「전액 롤백」이 옵션은。
//
//  2. **전체 롤백이 잘못된 이유**：푸시는 편리한 기능입니다，취약점 기록은 제품 자체입니다.。알림
//     테이블 문제（기존 라이브러리가 마이그레이션되지 않았습니다.、일시적인 디스크 오류）고위험 취약점은 라이브러리에 저장하면 안 됩니다.。그럼 고립은 이렇습니다.
//     오류、로그를 남겨보세요、복귀 false，평소대로 취약점 쓰기 제출을 허용하세요.——이 추진력을 잃는 대가는。
//     복귀 bool 대신 error 의도적이에요：호출자는 이를 쓰기 성공이나 실패에 영향을 미치는 오류로 간주해서는 안 됩니다.。
func RecordNotificationEventTx(ctx context.Context, tx *sql.Tx, kind string, findingID int64, snap notify.Snapshot) bool {
	raw, err := json.Marshal(snap)
	if err != nil {
		log.Printf("[notify] 푸시 이벤트를 직렬화하지 못했습니다. finding=%d: %v", findingID, err)
		return false
	}
	if _, err := tx.ExecContext(ctx, `SAVEPOINT notify_event`); err != nil {
		log.Printf("[notify] 저장점을 생성하지 못했습니다. finding=%d: %v", findingID, err)
		return false
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO notification_events(kind,finding_id,snapshot) VALUES($1,$2,$3)`,
		kind, findingID, string(raw)); err != nil {
		log.Printf("[notify] 푸시 이벤트를 작성하지 못했습니다. finding=%d（취약점 레코드는 영향을 받지 않습니다.）: %v", findingID, err)
		// 저장포인트로 롤백，거래를 다음에서 변경하세요. aborted 상태에서 저장됨。
		if _, rbErr := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT notify_event`); rbErr != nil {
			log.Printf("[notify] 저장점으로 롤백하지 못했습니다. finding=%d: %v", findingID, rbErr)
		}
		return false
	}
	// 저장점 해제，긴 거래에서 쓸데없는 세이브 포인트 축적을 피하세요。
	_, _ = tx.ExecContext(ctx, `RELEASE SAVEPOINT notify_event`)
	return true
}

// AddNotificationEvent 네 InsertNotificationEventTx 의 독립 트랜잭션 버전，공급자가 없습니다.
// 기존 거래에서 콜 포인트 사용（채널 등「테스트 메시지 보내기」，현실이 아니에요 finding）。
func (d *DB) AddNotificationEvent(ctx context.Context, kind string, findingID int64, snap notify.Snapshot) (int64, error) {
	raw, err := json.Marshal(snap)
	if err != nil {
		return 0, fmt.Errorf("알림 이벤트 스냅샷을 직렬화하지 못했습니다.: %w", err)
	}
	var id int64
	err = d.QueryRowContext(ctx, `INSERT INTO notification_events(kind,finding_id,snapshot) VALUES($1,$2,$3) RETURNING id`,
		kind, findingID, string(raw)).Scan(&id)
	return id, err
}

// FanOutPendingEvents 현재 활성화된 채널에 따라 분산되지 않은 취약점 이벤트를 전달 작업으로 확장합니다.，
// 이번 라운드에서 처리된 이벤트 수와 새로운 전달 수를 반환합니다.。
//
// 전체 작업 라운드가 하나의 트랜잭션으로 이루어집니다.：이벤트용 FOR UPDATE SKIP LOCKED 받기，동시에 실행되는 여러 프로세스
// 도 다른 줄을 받았습니다.（프로젝트에서 Archive Queue를 수집하는 데에도 동일한 방법이 사용됩니다.，또 만나요
// db/task_archives.go 님 completeNextArchiveJob）。
//
// 필터 매칭은 의도적으로 배치되었습니다. Go 쪽 대신 SQL：채널 필터 조건은 선택적 필드 집합입니다. JSONB，
// 사용 SQL 일치하는 조합을 6개 표현하면 쿼리를 유지 관리하기가 어려워집니다.，그리고 채널 수는「손으로 준비한 몇 조각」，
// 풀 로딩 후 메모리의 각 항목을 비교하는 것이 더 빠르고 테스트하기 쉽습니다.。
//
// 채널에 도달하지 않은 이벤트도 표시됩니다. fanned_out ——그렇지 않으면 보류 중인 컬렉션에 영원히 유지됩니다.，
// 각 tick 이(가) 다시 스캔되었습니다.。
func (d *DB) FanOutPendingEvents(ctx context.Context, limit int) (eventCount, deliveryCount int, err error) {
	if limit <= 0 {
		limit = 200
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback() //nolint:errcheck // 성공적으로 제출한 후, no-op

	channels, err := listEnabledNotificationChannelsTx(ctx, tx)
	if err != nil {
		return 0, 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, kind, finding_id, snapshot FROM notification_events
WHERE NOT fanned_out ORDER BY id FOR UPDATE SKIP LOCKED LIMIT $1`, limit)
	if err != nil {
		return 0, 0, err
	}
	var (
		events      []NotificationEvent
		parsedSnaps []notify.Snapshot
	)
	for rows.Next() {
		var ev NotificationEvent
		if err := rows.Scan(&ev.ID, &ev.Kind, &ev.FindingID, &ev.Snapshot); err != nil {
			rows.Close()
			return 0, 0, err
		}
		var snap notify.Snapshot
		// 스냅샷은 직접 작성했습니다，이론적으로 구문 분석이 가능해야 합니다.；파싱 실패로 인해 전달 과정이 차단되지는 않습니다.，
		// 하지만 이 이벤트는 필드가 모두 비어 있으므로 필터 조건이 있는 모든 채널에서 건너뛰게 됩니다.——차라리 글 하나 덜 밀고 싶네요
		// 잘못된 행 하나가 전체 대기열을 방해하지 않도록 하세요.。
		_ = json.Unmarshal(ev.Snapshot, &snap)
		// kind 라인의 값이 우선합니다.：스냅샷의 사본은 렌더링에 사용된 사본입니다.，이 이전 버전으로 작성되었을 수 있습니다.。
		snap.Kind = ev.Kind
		events = append(events, ev)
		parsedSnaps = append(parsedSnaps, snap)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	if len(events) == 0 {
		return 0, 0, tx.Commit()
	}

	type pending struct {
		eventID   int64
		channelID int64
	}
	var toInsert []pending
	for i, snap := range parsedSnaps {
		for _, ch := range channels {
			if !notify.Match(notify.ParseFilter(ch.Filter), snap) {
				continue
			}
			toInsert = append(toInsert, pending{eventID: events[i].ID, channelID: ch.ID})
		}
	}
	if len(toInsert) > 0 {
		var (
			vals []string
			args []any
		)
		for _, p := range toInsert {
			vals = append(vals, fmt.Sprintf("($%d,$%d)", len(args)+1, len(args)+2))
			args = append(args, p.eventID, p.channelID)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO notification_deliveries(event_id,channel_id) VALUES `+strings.Join(vals, ","), args...); err != nil {
			return 0, 0, err
		}
	}

	// 이번 이벤트 라운드를 전달됨으로 표시합니다.。채널에 닿지 않는 이벤트도 함께 표시됩니다.（함수 설명 보기）。
	ids := make([]string, 0, len(events))
	markArgs := make([]any, 0, len(events))
	for _, ev := range events {
		markArgs = append(markArgs, ev.ID)
		ids = append(ids, fmt.Sprintf("$%d", len(markArgs)))
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notification_events SET fanned_out=true WHERE id IN (`+strings.Join(ids, ",")+`)`, markArgs...); err != nil {
		return 0, 0, err
	}
	return len(events), len(toInsert), tx.Commit()
}

// listEnabledNotificationChannelsTx 트랜잭션의 활성 채널을 가져옵니다.。수량이 매우 적습니다.，
// 페이징 또는 캐싱 없음——캐싱이 도입됩니다.「변경된 구성은 언제 적용되나요?」이 추가적인 타이밍 문제。
func listEnabledNotificationChannelsTx(ctx context.Context, tx *sql.Tx) ([]*NotificationChannel, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, name, kind, config, mode, filter, rate_per_min
FROM notification_channels WHERE enabled ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*NotificationChannel{}
	for rows.Next() {
		var c NotificationChannel
		if err := rows.Scan(&c.ID, &c.Name, &c.Kind, &c.Config, &c.Mode, &c.Filter, &c.RatePerMin); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

// NotificationAssetNames 자산을 넣어 id 짧은 표시 이름으로 구문 분석됨，푸시 메시지의 경우。
//
// 반환 순서가 입력 매개변수와 일치합니다.、길이가 입력 매개변수보다 작을 수 있습니다.（존재하지 않습니다 id 을 건너뛰었습니다.）。매개변수 입력 순서를 다음과 같이 유지합니다.
// 동일한 취약점 메시지를 여러 번 전달하는 경우 자산의 순서를 안정화하기 위해——그렇지 않으면 재시도 후 수신된 메시지에서
// 자산의 순서가 변경되었습니다.，은 다음과 같이 잘못 읽혀집니다.「자산이 변경되었습니다.」。
func (d *DB) NotificationAssetNames(ctx context.Context, ids []int64) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ph, args := placeholders(1, ids)
	rows, err := d.QueryContext(ctx, `SELECT id, type, domain, ip, url, app_name, bundle_id FROM assets WHERE id IN (`+ph+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	labels := map[int64]string{}
	for rows.Next() {
		var (
			id                int64
			typ               string
			domain, ip, url   sql.NullString
			appName, bundleID sql.NullString
		)
		if err := rows.Scan(&id, &typ, &domain, &ip, &url, &appName, &bundleID); err != nil {
			return nil, err
		}
		labels[id] = assetDisplayName(typ, domain.String, ip.String, url.String, appName.String, bundleID.String)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ids))
	seen := map[int64]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if label, ok := labels[id]; ok && label != "" {
			out = append(out, label)
		}
	}
	return out, nil
}

// assetDisplayName 자산 유형별로 가장 인지도가 높은 로고를 선택하세요.。
// 빈 문자열을 반환합니다.，어떻게 제시할지 결정하는 것은 발신자 몫「이름을 검색할 수 없는 자산입니다.」——이 함수는 자리 표시자를 생성하지 않습니다.，
// 그렇지 않으면「자산#42」푸시 메시지에 이런 잡음이 섞이게 됩니다.，독자들은 그것이 진짜 도메인 이름이라고 생각했습니다.。
func assetDisplayName(typ, domain, ip, url, appName, bundleID string) string {
	pick := func(vals ...string) string {
		for _, v := range vals {
			if strings.TrimSpace(v) != "" {
				return v
			}
		}
		return ""
	}
	switch typ {
	case "root_domain", "subdomain":
		return domain
	case "ip":
		return ip
	case "app":
		return pick(appName, bundleID)
	case "service", "endpoint":
		return pick(url, domain, ip)
	default:
		return pick(domain, ip, url, appName)
	}
}

// SetFindingStatusWithNotify 취약점 처리 현황 업데이트，및 동일한 거래에 상태 변경 등록
// 푸시 이벤트。
//
// 복귀 from=변경 전 상태；found=취약점이 존재합니까?；notified=이벤트 등록이 잘 되었나요?。
//
// 의도적인 세 가지 행동：
//   - 실제로 상태가 변경되지 않은 경우 이벤트가 등록되지 않습니다.。프런트 엔드 서랍이 동일한 값을 반복적으로 제출합니다.、또는 자동화 스크립트
//     멱등성 재생，누르는 소리가 나지 않아야 합니다.。
//   - 취약점이 존재하지 않는 경우 반환됨 found=false 그리고 아무것도 쓰지 마세요，발신자가 번역함 404。
//   - 이벤트 등록 실패는 상태 업데이트에 영향을 미치지 않습니다.（또 만나요 RecordNotificationEventTx 에 대한 포인트 설명 저장），
//     그래서 notified=false 상태가 성공적으로 변경되었습니다.，발신자는 이로 인해 오류를 보고해서는 안 됩니다.。
func (d *DB) SetFindingStatusWithNotify(ctx context.Context, id int64, status string) (from string, found bool, notified bool, err error) {
	err = d.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		var txErr error
		from, found, _, notified, txErr = SetFindingStatusTx(ctx, tx, id, status)
		return txErr
	})
	return from, found, notified, err
}

// SetFindingStatusTx 에**발신자의 거래**취약점 상태 업데이트 및 상태 변경 푸시 이벤트 등록。
//
// 트랜잭션 수준 함수를 추출하는 목적은 상태를 변경하는 모든 경로가 동일한 의미 집합을 공유하도록 허용하는 것입니다.——이전에만
// patchFinding 운송 통지 버전，그리고**재시험의 결론은「고정됨」시간**（finding_retests
// 리 나티아오 `UPDATE findings SET status=...`）은 라이브러리에 직접 작성됩니다.，그래서 할당했어요
// `on_status_change` 채널은 이러한 유형의 상태 이전에 대해 푸시 알림을 전혀 받을 수 없습니다.：인터페이스 상태가 조용히 바뀌었습니다.，
// 운영 및 유지보수는 플랫폼 오픈 전까지는 알 수 없었습니다.。
//
// 복귀 from=변경 전 상태、found=취약점이 존재합니까?、changed=상태가 정말 바뀌었나요?、
// notified=이벤트 등록이 잘 되었나요?（등록 실패는 상태 업데이트에 영향을 미치지 않습니다.，또 만나요 RecordNotificationEventTx）。
func SetFindingStatusTx(ctx context.Context, tx *sql.Tx, id int64, status string) (from string, found bool, changed bool, notified bool, err error) {
	var (
		vulnclass, name, severity, summary string
		taskID                             sql.NullInt64
		assetIDs                           []byte
	)
	scanErr := tx.QueryRowContext(ctx, `SELECT vulnclass, name, severity, summary, task_id, asset_ids, status
FROM findings WHERE id=$1 FOR UPDATE`, id).
		Scan(&vulnclass, &name, &severity, &summary, &taskID, &assetIDs, &from)
	if scanErr == sql.ErrNoRows {
		return "", false, false, false, nil
	}
	if scanErr != nil {
		return "", false, false, false, scanErr
	}
	found = true
	if from == status {
		// 상태가 실제로 변하지 않으면 이벤트가 등록되지 않습니다.：같은 값 반복 제출、멱등성 재생을 해서는 안 됩니다.
		// 누르는 소리 발생。
		return from, true, false, false, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE findings SET status=$2 WHERE id=$1`, id, status); err != nil {
		return from, true, false, false, err
	}
	var assets []int64
	_ = json.Unmarshal(assetIDs, &assets)
	notified = RecordNotificationEventTx(ctx, tx, notify.EventFindingStatusChanged, id, notify.Snapshot{
		Kind:       notify.EventFindingStatusChanged,
		FindingID:  id,
		TaskID:     taskID.Int64,
		VulnClass:  vulnclass,
		Name:       name,
		Severity:   severity,
		Summary:    summary,
		AssetIDs:   assets,
		FromStatus: from,
		ToStatus:   status,
	})
	return from, true, true, notified, nil
}

// NotificationStats 은 알림 페이지 상단의 개요 개수입니다.。
type NotificationStats struct {
	Channels     int   `json:"channels"`
	ChannelsOn   int   `json:"channels_on"`
	Pending      int   `json:"pending"`
	Failed       int   `json:"failed"`
	SentToday    int   `json:"sent_today"`
	BacklogAgeMS int64 `json:"backlog_age_ms"` // 가장 오래된 보류 중인 배달 이후 경과된 시간(밀리초)
}

// NotificationStatsSnapshot 요약 알림 시스템 상태。
// BacklogAgeMS 네「푸시가 멈췄나요?」가장 직접적인 지표——보다 pending 계산이 훨씬 더 유용해요，
// 밀린 업무 때문에 3 기사 및 백로그 3 막대의 차이점은 다음과 같습니다. 3 이제 몇 초 남았습니다. 3 시간。
func (d *DB) NotificationStatsSnapshot(ctx context.Context) (*NotificationStats, error) {
	var s NotificationStats
	if err := d.QueryRowContext(ctx, `SELECT
    (SELECT count(*) FROM notification_channels),
    (SELECT count(*) FROM notification_channels WHERE enabled),
    (SELECT count(*) FROM notification_deliveries WHERE state IN ($1,$2)),
    (SELECT count(*) FROM notification_deliveries WHERE state=$3),
    (SELECT count(*) FROM notification_deliveries WHERE state=$4 AND sent_at >= date_trunc('day', now())),
    COALESCE((SELECT EXTRACT(EPOCH FROM (now() - min(created_at))) * 1000 FROM notification_deliveries WHERE state=$1), 0)::bigint`,
		NotifyStatePending, NotifyStateSending, NotifyStateFailed, NotifyStateSent).
		Scan(&s.Channels, &s.ChannelsOn, &s.Pending, &s.Failed, &s.SentToday, &s.BacklogAgeMS); err != nil {
		return nil, err
	}
	return &s, nil
}
