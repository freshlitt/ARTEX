package db

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Autumn-27/artex/notify"
)

// 이 문서의 사용 사례는 모두 연결됩니다. PostgreSQL（라이브러리가 없으면 건너뛰기）。이것들은 SQL 사용했어요
// FOR UPDATE SKIP LOCKED、make_interval、JSONB、여러 줄 IN(...) 자리 표시자 연결，
// 모두「컴파일은 통과했지만 실행 시 오류가 보고될 수 있습니다.」쓰는 법，검증된 것으로 간주하려면 실행해야 합니다.。

func notifyTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// newTestChannel 채널 구축，테스트 후 자동 삭제됨。
func newTestChannel(t *testing.T, d *DB, kind, mode string, filter string) *NotificationChannel {
	t.Helper()
	if filter == "" {
		filter = `{}`
	}
	ch := &NotificationChannel{
		Name:       "테스트 채널-" + t.Name(),
		Kind:       kind,
		Mode:       mode,
		Config:     json.RawMessage(`{"webhook":"https://example.com/hook"}`),
		Filter:     json.RawMessage(filter),
		RatePerMin: 100,
	}
	id, err := d.SaveNotificationChannel(context.Background(), ch)
	if err != nil {
		t.Fatalf("채널 생성 실패: %v", err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM notification_channels WHERE id=$1`, id) })
	ch.ID = id
	return ch
}

// addTestEvent 이벤트 직접 작성（통하지 않음 finding），테스트 발송 및 배송에 사용됩니다.。
func addTestEvent(t *testing.T, d *DB, kind string, findingID int64, snap notify.Snapshot) int64 {
	t.Helper()
	snap.Kind = kind
	snap.FindingID = findingID
	id, err := d.AddNotificationEvent(context.Background(), kind, findingID, snap)
	if err != nil {
		t.Fatalf("이벤트를 작성하지 못했습니다.: %v", err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM notification_events WHERE id=$1`, id) })
	return id
}

func TestNotificationAssetNamesResolvesAndPreservesOrder(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()

	// 세 가지 유형의 자산 각각에는 고유한 디스플레이 수준이 있습니다.：도메인 이름、IP、URL。
	insertAsset := func(query, value string) int64 {
		t.Helper()
		var id int64
		if err := d.QueryRow(query, value).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	domID := insertAsset(`INSERT INTO assets(type, domain) VALUES('subdomain',$1) RETURNING id`, "a.example.com")
	ipID := insertAsset(`INSERT INTO assets(type, ip) VALUES('ip',$1) RETURNING id`, "10.1.2.3")
	svcID := insertAsset(`INSERT INTO assets(type, url) VALUES('service',$1) RETURNING id`, "https://a.example.com/admin")
	t.Cleanup(func() {
		d.Exec(`DELETE FROM assets WHERE id IN ($1,$2,$3)`, domID, ipID, svcID)
	})

	// 들어오는 순서가 의도적으로 어긋나 있습니다.，존재하지 않는 항목이 포함되어 있습니다. id。
	got, err := d.NotificationAssetNames(ctx, []int64{svcID, 999999999, domID, ipID, svcID})
	if err != nil {
		t.Fatalf("자산 이름을 구문 분석하지 못했습니다.: %v", err)
	}
	want := []string{"https://a.example.com/admin", "a.example.com", "10.1.2.3"}
	if len(got) != len(want) {
		t.Fatalf("자산 이름 개수가 일치하지 않습니다.，기대 %v 받았어요 %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("주문/값이 일치하지 않습니다.，기대 %v 받았어요 %v", want, got)
		}
	}
}

// TestRecordNotificationEventTxUnwindsOnFailure 은 저장점 메커니즘의 핵심 사용 사례입니다.：
// 거래에서는 먼저 양보하세요 notification_events 의 글쓰기는 실패해야 합니다（임시로 상수 추가 false 제약），
// 주장 ① 이 기능은 보고합니다. false ② 거래가 입력되지 않았습니다. aborted 상태，후속 명령문은 계속 실행될 수 있습니다.。
//
// 세이브포인트가 없는 경우，PostgreSQL 은 전체 거래를 무효화합니다.，후속 문은 다음으로 시작됩니다.
// "current transaction is aborted" 실패——바로 그렇죠「알림 양식에 문제가 발생했습니다.
// 취약점을 데이터베이스에 저장할 수 없습니다.」의 오류 경로。
//
// 여기서는 의도적으로 사용했습니다. **ROLLBACK 대신 끝내기 COMMIT**：ALTER TABLE 에 PG 은 트랜잭션용입니다.，
// 제출 후，그 임시 제약은 영구적으로 유지됩니다 schema 내부，모든 후속 사용 사례를 함께 정의합니다.。
// 롤백을 자동으로 취소할 수 있습니다. DDL，수동 청소가 필요하지 않습니다.。어설션에는 다음 사항만 필요합니다.「거래가 아직 살아있습니다.」，
// 실제로 제출할 필요는 없습니다.。
func TestRecordNotificationEventTxUnwindsOnFailure(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()

	// 방어 청소：과거 작업으로 인해 이 제약 조건이 벗어난 경우，먼저 벗어보세요。
	if _, err := d.Exec(`ALTER TABLE notification_events DROP CONSTRAINT IF EXISTS notify_test_never`); err != nil {
		t.Fatal(err)
	}

	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback() //nolint:errcheck // 임시 제약 취소，함수 설명 보기

	// NOT VALID：이 이후에 작성된 행만 제한합니다.，도서관에 존재하는 역사적 사건을 확인하지 마세요.
	// （그렇지 않으면 기존 행을 위반하면 제약 조건이 추가되지 않습니다.）。
	if _, err := tx.ExecContext(ctx, `ALTER TABLE notification_events ADD CONSTRAINT notify_test_never CHECK (false) NOT VALID`); err != nil {
		t.Fatalf("임시 제약 조건을 추가하지 못했습니다.: %v", err)
	}
	if RecordNotificationEventTx(ctx, tx, notify.EventFindingCreated, 1, notify.Snapshot{Severity: "high"}) {
		t.Fatal("불가피한 실패의 제약에도 불구하고 보고서 쓰기 성공")
	}
	// 핵심 어설션：거래는 계속 사용할 수 있습니다。
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
		t.Fatalf("거래가 오염되었습니다.（저장 포인트가 적용되지 않습니다.）: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("롤백 실패: %v", err)
	}
	// 확인 DDL 롤백으로 취소됨，후속 사용 사례를 위한 여지를 남기지 마십시오.。
	var exists bool
	if err := d.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conname='notify_test_never')`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("롤백으로 인해 임시 제약 조건이 취소되지 않았습니다.，후속 사용 사례를 오염시킬 것입니다.")
	}
}

func TestFanOutRoutesEventsByFilter(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()

	all := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{}`)
	onlyCritical := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{"min_severity":"critical"}`)
	sqlOnly := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{"vulnclass_include":["SQL"]}`)

	highSQL := addTestEvent(t, d, notify.EventFindingCreated, 1001, notify.Snapshot{Severity: "high", VulnClass: "SQL주사"})
	lowXSS := addTestEvent(t, d, notify.EventFindingCreated, 1002, notify.Snapshot{Severity: "low", VulnClass: "XSS"})
	criticalXSS := addTestEvent(t, d, notify.EventFindingCreated, 1003, notify.Snapshot{Severity: "critical", VulnClass: "XSS"})

	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatalf("발송 실패: %v", err)
	}

	cases := []struct {
		name    string
		eventID int64
		channel int64
		want    bool
	}{
		{"모든 채널을 통해 수신됨 high", highSQL, all.ID, true},
		{"모든 채널을 통해 수신됨 low", lowXSS, all.ID, true},
		{"중요한 채널만 건너뜁니다. high", highSQL, onlyCritical.ID, false},
		{"진지한 채널을 통해서만 수신됨 critical", criticalXSS, onlyCritical.ID, true},
		{"만SQL채널 수신됨 SQL", highSQL, sqlOnly.ID, true},
		{"만SQL채널 건너뛰기 XSS", lowXSS, sqlOnly.ID, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var exists bool
			if err := d.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM notification_deliveries WHERE event_id=$1 AND channel_id=$2)`,
				tc.eventID, tc.channel).Scan(&exists); err != nil {
				t.Fatal(err)
			}
			if exists != tc.want {
				t.Fatalf("배송이 되나요?: 기대 %v 받았어요 %v", tc.want, exists)
			}
		})
	}

	// 다시 발송해도 중복 배송이 발생하지 않아야 합니다.（fanned_out 멱등성）。
	events, deliveries, err := d.FanOutPendingEvents(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if events != 0 || deliveries != 0 {
		t.Fatalf("발송된 이벤트를 다시 처리하면 안 됩니다.，받았어요 events=%d deliveries=%d", events, deliveries)
	}
}

// TestFanOutMarksEventsWithNoMatchingChannel 재정의「해당 이벤트가 어떤 채널에도 연결되지 않았습니다.」의 상황。
// 이러한 이벤트는 여전히 전달됨으로 표시되어야 합니다.，그렇지 않으면 보류 중인 컬렉션에 영원히 유지됩니다.、각 tick 다시 스캔。
func TestFanOutMarksEventsWithNoMatchingChannel(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()
	pick := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{"vulnclass_include":["일치하지 않는 유형"]}`)
	_ = pick

	ev := addTestEvent(t, d, notify.EventFindingCreated, 2001, notify.Snapshot{Severity: "high", VulnClass: "XSS"})
	_, deliveries, err := d.FanOutPendingEvents(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if deliveries != 0 {
		t.Fatalf("배송이 안되요，받았어요 %d", deliveries)
	}
	var fanned bool
	if err := d.QueryRowContext(ctx, `SELECT fanned_out FROM notification_events WHERE id=$1`, ev).Scan(&fanned); err != nil {
		t.Fatal(err)
	}
	if !fanned {
		t.Fatal("채널을 놓친 이벤트도 전달됨으로 표시되어야 합니다.，그렇지 않으면 무한히 스캔됩니다.")
	}
}

func TestClaimRealtimeDeliveriesHonorsLeaseAndMode(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()

	realtime := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{}`)
	digest := newTestChannel(t, d, notify.KindDingTalk, NotifyModeDigest, `{}`)

	addTestEvent(t, d, notify.EventFindingCreated, 3001, notify.Snapshot{Severity: "high", VulnClass: "XSS"})
	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatal(err)
	}

	// 실시간 수집만 얻어야함 realtime 채널，움직이면 안 된다 digest 채널。
	got, err := d.ClaimRealtimeDeliveries(ctx, realtime.ID, 10, time.Minute)
	if err != nil {
		t.Fatalf("수신 실패: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("받아야지 1 글，받았어요 %d", len(got))
	}
	if got[0].State != NotifyStateSending || got[0].Attempts != 1 {
		t.Fatalf("받으신 후에는 sending 그리고 attempts=1，받았어요 state=%s attempts=%d", got[0].State, got[0].Attempts)
	}
	// 연관적으로 로드된 렌더링 컨텍스트가 완료되어야 합니다.（채널 구성 + 이벤트 스냅샷 + finding id）。
	if got[0].Channel == nil || len(got[0].Channel.Config) == 0 {
		t.Fatal("결과 수신 시 채널 구성이 누락되었습니다.，렌더링이 실패합니다.")
	}
	if got[0].FindingID != 3001 {
		t.Fatalf("finding id 행사에서 나오지 않았습니다，받았어요 %d", got[0].FindingID)
	}

	// 임대가 만료되지 않았습니다.，두 번째 컬렉션은 비어 있어야 합니다.——이건「같은 줄은 두 번 사용하지 않습니다. dispatcher 동시배송」
	// 보증。
	again, err := d.ClaimRealtimeDeliveries(ctx, realtime.ID, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("임대기간 동안 반복해서 받지 말아야 합니다.，받았어요 %d 글", len(again))
	}

	// digest 실시간 픽업으로 채널 전달이 발생해서는 안 됩니다.。
	left, err := d.ClaimRealtimeDeliveries(ctx, digest.ID, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("실시간 수집이 안된다. digest 채널 전달，받았어요 %d 글", len(left))
	}
}

// TestClaimExpiredLeaseRecovers 충돌 자가 복구 재정의：전달 중에 프로세스가 종료되면 남겨집니다. sending
// 알았어，임대기간 만료 후 다시 수령이 가능해야 합니다，그렇지 않으면 이 배달은 항상 중단될 것입니다。
func TestClaimExpiredLeaseRecovers(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()
	ch := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{}`)
	addTestEvent(t, d, notify.EventFindingCreated, 4001, notify.Snapshot{Severity: "high"})
	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatal(err)
	}
	first, err := d.ClaimRealtimeDeliveries(ctx, ch.ID, 10, time.Minute)
	if err != nil || len(first) != 1 {
		t.Fatalf("처음으로 수집에 실패했습니다.: %v (%d 글)", err, len(first))
	}
	// 수동으로 임대를 과거로 푸시，시뮬레이션「임대가 만료되었습니다」。
	if _, err := d.Exec(`UPDATE notification_deliveries SET next_attempt_at = now() - interval '1 minute' WHERE id=$1`, first[0].ID); err != nil {
		t.Fatal(err)
	}
	second, err := d.ClaimRealtimeDeliveries(ctx, ch.ID, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 {
		t.Fatalf("임대가 만료되었습니다 sending 행을 다시 확보해야 합니다.，받았어요 %d 글", len(second))
	}
	if second[0].Attempts != 2 {
		t.Fatalf("재획득 시도 누적 횟수가 이루어져야 합니다.，받았어요 %d", second[0].Attempts)
	}
}

func TestClaimSkipsDisabledChannel(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()
	ch := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{}`)
	addTestEvent(t, d, notify.EventFindingCreated, 5001, notify.Snapshot{Severity: "high"})
	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatal(err)
	}
	// 비활성화하면 배송 대기 중인 재고가 다음으로 표시됩니다. skipped。
	if err := d.SetNotificationChannelEnabled(ctx, ch.ID, false); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := d.QueryRow(`SELECT state FROM notification_deliveries WHERE channel_id=$1`, ch.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != NotifyStateSkipped {
		t.Fatalf("비활성화된 채널의 배송 보류 중인 인벤토리는 다음과 같이 표시되어야 합니다. skipped，받았어요 %s", state)
	}
	got, err := d.ClaimRealtimeDeliveries(ctx, ch.ID, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("비활성화된 채널은 소유권을 주장해서는 안 됩니다.，받았어요 %d 글", len(got))
	}
}

func TestDigestBatchDueAndStableBatchID(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()
	ch := newTestChannel(t, d, notify.KindDingTalk, NotifyModeDigest, `{}`)
	for i := 0; i < 3; i++ {
		addTestEvent(t, d, notify.EventFindingCreated, int64(6000+i), notify.Snapshot{Severity: "high"})
	}
	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatal(err)
	}

	// 배치가 방금 생성되었습니다.、나이는 0，30 분은 만료되어서는 안 됩니다.。
	due, err := d.DigestBatchDue(ctx, ch.ID, 30*time.Minute)
	if err != nil {
		t.Fatalf("배치 만료를 확인하지 못했습니다.: %v", err)
	}
	if due {
		t.Fatal("새로 생성된 배치가 즉시 만료되어서는 안 됩니다.")
	}

	// 3개 배달의 생성시간을 함께 미루세요，주기가 충분한 배치를 시뮬레이션합니다.。
	if _, err := d.Exec(`UPDATE notification_deliveries SET created_at = now() - interval '40 minutes' WHERE channel_id=$1`, ch.ID); err != nil {
		t.Fatal(err)
	}
	due, err = d.DigestBatchDue(ctx, ch.ID, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !due {
		t.Fatal("기간을 초과한 배치는 만료된 것으로 판단한다.")
	}

	batch, err := d.ClaimDigestBatch(ctx, ch.ID, MaxDigestBatchSize, time.Minute)
	if err != nil {
		t.Fatalf("요약 배치를 받지 못했습니다.: %v", err)
	}
	if len(batch) != 3 {
		t.Fatalf("요약은 한번에 찍어야함 3 글，받았어요 %d 글", len(batch))
	}
	if batch[0].BatchID == nil {
		t.Fatal("배치를 요약할 때 반드시 작성해야 함 batch_id，그렇지 않으면 이 글들이 함께 게시되었다는 사실을 이력으로는 알 수 없습니다.")
	}
	firstBatchID := *batch[0].BatchID
	for _, dl := range batch {
		if dl.BatchID == nil || *dl.BatchID != firstBatchID {
			t.Fatalf("동일한 배치를 공유해야 합니다. batch_id，받았어요 %v vs %d", dl.BatchID, firstBatchID)
		}
	}

	// 이 배치를 보자**종합**일정 재조정 실패 후 재수락，batch_id 원래 값을 유지해야 합니다.（COALESCE 기능）：
	// 그렇지 않으면 한 번만 다시 시도하면 됩니다.「일괄적으로 보내드린 내용입니다」해당 사실은 삭제되었습니다。
	//
	// 한 줄이 아닌 전체 배치를 다시 정렬해야 합니다.——요약 메시지를 보낼 때 전송 엔진이 처리하는 방식입니다.
	// （하나의 메시지는 전체 배치를 나타냅니다.，성공과 실패를 공유합니다）。한 문장만 재배열하세요，나머지는 아직 임대기간 중이에요，
	// 당연히 타이틀을 되찾았을 때 그 아이템 하나만 얻었습니다.。
	allIDs := make([]int64, 0, len(batch))
	for _, dl := range batch {
		allIDs = append(allIDs, dl.ID)
	}
	if err := d.RescheduleDeliveries(ctx, allIDs, time.Second, "시뮬레이션 실패"); err != nil {
		t.Fatal(err)
	}
	// 임대를 과거로 미루세요，시뮬레이션 백오프 시간이 만료되었습니다.。
	if _, err := d.Exec(`UPDATE notification_deliveries SET next_attempt_at = now() - interval '1 minute' WHERE channel_id=$1`, ch.ID); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := d.ClaimDigestBatch(ctx, ch.ID, MaxDigestBatchSize, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(reclaimed) != 3 {
		t.Fatalf("리클레이머는 모든 것을 얻어야 한다 3 글，받았어요 %d", len(reclaimed))
	}
	if reclaimed[0].BatchID == nil || *reclaimed[0].BatchID != firstBatchID {
		t.Fatalf("다시 시도한 후 batch_id 은 원래 값을 유지해야 합니다. %d，받았어요 %v", firstBatchID, reclaimed[0].BatchID)
	}
}

func TestDeliveryStateTransitions(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()
	ch := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{}`)
	addTestEvent(t, d, notify.EventFindingCreated, 7001, notify.Snapshot{Severity: "high"})
	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatal(err)
	}
	got, err := d.ClaimRealtimeDeliveries(ctx, ch.ID, 10, time.Minute)
	if err != nil || len(got) != 1 {
		t.Fatalf("수신 실패: %v (%d)", err, len(got))
	}
	id := got[0].ID

	if err := d.RescheduleDeliveries(ctx, []int64{id}, time.Second, "네트워크 지터"); err != nil {
		t.Fatal(err)
	}
	var state, lastErr string
	if err := d.QueryRow(`SELECT state, last_error FROM notification_deliveries WHERE id=$1`, id).Scan(&state, &lastErr); err != nil {
		t.Fatal(err)
	}
	if state != NotifyStatePending || lastErr != "네트워크 지터" {
		t.Fatalf("재배치 후에는 pending 그리고 그 이유를 적어주세요，받았어요 state=%s err=%q", state, lastErr)
	}

	if err := d.FailDeliveries(ctx, []int64{id}, "재시도 횟수가 부족함"); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT state FROM notification_deliveries WHERE id=$1`, id).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != NotifyStateFailed {
		t.Fatalf("이어야 합니다. failed，받았어요 %s", state)
	}

	// 수동 재전송은 재시도 횟수를 지우고 즉시 만료되어야 합니다.，그렇지 않으면 이전에 실패한 예산이 상속됩니다.。
	if err := d.RetryNotificationDelivery(ctx, id); err != nil {
		t.Fatalf("재전송 실패: %v", err)
	}
	var attempts int
	var next time.Time
	if err := d.QueryRow(`SELECT state, attempts, next_attempt_at FROM notification_deliveries WHERE id=$1`, id).Scan(&state, &attempts, &next); err != nil {
		t.Fatal(err)
	}
	if state != NotifyStatePending || attempts != 0 {
		t.Fatalf("다시 보낸 후에는 pending 그리고 attempts=0，받았어요 state=%s attempts=%d", state, attempts)
	}
	if next.After(time.Now().Add(time.Second)) {
		t.Fatal("즉시 재전송이 가능해야 합니다.")
	}

	// 배송된 상품은 다시 보내면 안 됩니다.。
	if err := d.MarkDeliveriesSent(ctx, []int64{id}); err != nil {
		t.Fatal(err)
	}
	if err := d.RetryNotificationDelivery(ctx, id); err == nil {
		t.Fatal("배송된 상품은 재전송이 불가합니다.")
	}
}

func TestListNotificationDeliveriesPagingAndFilter(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()
	ch := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{}`)
	for i := 0; i < 5; i++ {
		addTestEvent(t, d, notify.EventFindingCreated, int64(8000+i), notify.Snapshot{Severity: "high", Name: "페이징 테스트"})
	}
	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ClaimRealtimeDeliveries(ctx, ch.ID, 10, time.Minute); err != nil {
		t.Fatal(err)
	}

	page1, total, err := d.ListNotificationDeliveries(ctx, NotificationDeliveryFilter{ChannelID: ch.ID, State: NotifyStateSending}, 1, 2)
	if err != nil {
		t.Fatalf("쿼리 실패: %v", err)
	}
	if total != 5 {
		t.Fatalf("합계는 다음과 같아야 합니다. 5，받았어요 %d", total)
	}
	if len(page1) != 2 {
		t.Fatalf(" 2 글，받았어요 %d", len(page1))
	}
	// 새로운 것 먼저：첫 페이지, 첫 번째 기사 id 은 두 번째 페이지의 첫 번째 항목보다 커야 합니다.。
	page2, _, err := d.ListNotificationDeliveries(ctx, NotificationDeliveryFilter{ChannelID: ch.ID, State: NotifyStateSending}, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 2 || page2[0].ID >= page1[0].ID {
		t.Fatalf("페이징 순서가 먼저 새로워져야 합니다.，받았어요 page1[0]=%d page2[0]=%d", page1[0].ID, page2[0].ID)
	}
	// 렌더링 컨텍스트는 기록과 함께 반환되어야 합니다.，그렇지 않으면 목록을 표시할 수 없습니다.「뭘 밀고 있는 거야?」。
	if page1[0].ChannelName == "" || page1[0].FindingID == 0 {
		t.Fatalf("기록 항목에 표시 필드가 없습니다.: %+v", page1[0])
	}

	// 상태별로 필터링：아니요 pending 님。
	pending, totalPending, err := d.ListNotificationDeliveries(ctx, NotificationDeliveryFilter{ChannelID: ch.ID, State: NotifyStatePending}, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if totalPending != 0 || len(pending) != 0 {
		t.Fatalf("그러면 안 돼요. pending 배송，받았어요 %d 글 (total=%d)", len(pending), totalPending)
	}
}

func TestSetFindingStatusWithNotifyOnlyEmitsOnRealChange(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()

	tk, err := d.CreateTask("알림 상태 변경 테스트", "대상", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(tk.ID)
	es := d.Exploration(tk.ExplorationID)
	f, err := es.RecordFinding(ctx, RecordFindingInput{
		TaskID: tk.ID, Worker: "test", VulnClass: "SQL주사", Name: "상태 변경 사용 사례",
		Severity: "high", Summary: "요약",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM notification_events WHERE finding_id=$1`, f.FindingID) })

	// 도서관에 드랍시 항목이 1개 등록되었습니다. finding_created 이벤트，먼저 기준으로 삼아보세요。
	var base int
	if err := d.QueryRow(`SELECT count(*) FROM notification_events WHERE finding_id=$1`, f.FindingID).Scan(&base); err != nil {
		t.Fatal(err)
	}
	if base < 1 {
		t.Fatal("취약점 로깅은 동일한 트랜잭션에 푸시 이벤트를 등록해야 합니다.")
	}

	// 같은 상태로 변경：이벤트가 발생하면 안 됩니다.（반복적인 제출 및 푸시 소음 방지）。
	from, found, notified, err := d.SetFindingStatusWithNotify(ctx, f.FindingID, "pending")
	if err != nil || !found {
		t.Fatalf("상태 설정 실패: found=%v err=%v", found, err)
	}
	if notified {
		t.Fatal("상태가 변경되지 않은 경우 푸시 이벤트를 등록하면 안 됩니다.")
	}
	if from != "pending" {
		t.Fatalf("변경 전 상태로 돌아가야 함 pending，받았어요 %q", from)
	}

	// 실제 변화：이벤트를 등록하고 녹화해야 합니다. from/to。
	from, found, notified, err = d.SetFindingStatusWithNotify(ctx, f.FindingID, "fixed")
	if err != nil || !found {
		t.Fatalf("상태 설정 실패: found=%v err=%v", found, err)
	}
	if !notified {
		t.Fatal("실제로 상태가 변경될 때 푸시 이벤트를 등록해야 합니다.")
	}
	if from != "pending" {
		t.Fatalf("from 이어야 합니다. pending，받았어요 %q", from)
	}
	var snapshot []byte
	if err := d.QueryRow(`SELECT snapshot FROM notification_events WHERE finding_id=$1 AND kind=$2`,
		f.FindingID, notify.EventFindingStatusChanged).Scan(&snapshot); err != nil {
		t.Fatalf("상태 변경 이벤트를 찾을 수 없습니다.: %v", err)
	}
	var snap notify.Snapshot
	if err := json.Unmarshal(snapshot, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.FromStatus != "pending" || snap.ToStatus != "fixed" {
		t.Fatalf("스냅샷의 상태 흐름이 잘못되었습니다.: %s → %s", snap.FromStatus, snap.ToStatus)
	}
	// 스냅샷은 렌더링에 필요한 필드를 가져와야 합니다.，그렇지 않으면 상태 변경 메시지는 빈 쉘이 됩니다.。
	if snap.VulnClass != "SQL주사" || snap.Severity != "high" || snap.Name != "상태 변경 사용 사례" {
		t.Fatalf("스냅샷에 렌더링 필드가 누락되었습니다.: %+v", snap)
	}
	var status string
	if err := d.QueryRow(`SELECT status FROM findings WHERE id=$1`, f.FindingID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "fixed" {
		t.Fatalf("상태가 다음으로 업데이트되어야 합니다. fixed，받았어요 %s", status)
	}

	// 존재하지 않는 취약점：found=false，보고된 오류가 없습니다.。
	if _, found, _, err := d.SetFindingStatusWithNotify(ctx, 999999999, "fixed"); err != nil || found {
		t.Fatalf("존재하지 않는 취약점은 반환되어야 함 found=false 맞네요，받았어요 found=%v err=%v", found, err)
	}
}

func TestNotificationStatsSnapshot(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()
	ch := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{}`)
	addTestEvent(t, d, notify.EventFindingCreated, 9001, notify.Snapshot{Severity: "high"})
	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatal(err)
	}
	stats, err := d.NotificationStatsSnapshot(ctx)
	if err != nil {
		t.Fatalf("통계 실패: %v", err)
	}
	if stats.Channels < 1 || stats.ChannelsOn < 1 {
		t.Fatalf("채널 수가 잘못되었습니다.: %+v", stats)
	}
	if stats.Pending < 1 {
		t.Fatalf("이 계산되어 배달 준비가 되어야 합니다.: %+v", stats)
	}
	// 새로 구축된 배달 백로그 수명은 에 가까워야 합니다. 0，음수 또는 큰 값 대신。
	if stats.BacklogAgeMS < 0 || stats.BacklogAgeMS > int64(time.Hour/time.Millisecond) {
		t.Fatalf("백로그 연령이 불법입니다.: %d ms", stats.BacklogAgeMS)
	}
	_ = ch
}

func TestNotificationChannelCRUDRoundTrip(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()

	ch := &NotificationChannel{
		Name:       "CRUD 왕복",
		Kind:       notify.KindEmail,
		Mode:       NotifyModeDigest,
		Config:     json.RawMessage(`{"host":"smtp.example.com","port":587,"from":"a@b.c","to":["x@y.z"]}`),
		Filter:     json.RawMessage(`{"min_severity":"medium","on_status_change":true}`),
		RatePerMin: 42,
	}
	id, err := d.SaveNotificationChannel(ctx, ch)
	if err != nil {
		t.Fatalf("신규 생성 실패: %v", err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM notification_channels WHERE id=$1`, id) })

	got, err := d.NotificationChannelByID(ctx, id)
	if err != nil {
		t.Fatalf("읽기 실패: %v", err)
	}
	if got.Mode != NotifyModeDigest || got.RatePerMin != 42 || got.Name != "CRUD 왕복" {
		t.Fatalf("왕복 필드가 일치하지 않습니다.: %+v", got)
	}
	if !got.IsEnabled() {
		t.Fatal("기본값을 활성화해야 합니다.")
	}
	var cfg map[string]any
	if err := json.Unmarshal(got.Config, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["host"] != "smtp.example.com" {
		t.Fatalf("구성이 라이브러리에 올바르게 로그인되지 않았습니다.: %v", cfg)
	}
	var filter notify.Filter
	if err := json.Unmarshal(got.Filter, &filter); err != nil {
		t.Fatal(err)
	}
	if filter.MinSeverity != "medium" || !filter.OnStatusChange {
		t.Fatalf("필터 조건이 올바르게 저장되지 않았습니다.: %+v", filter)
	}

	// 업데이트 후 다시 읽어보세요.。
	got.Name = "이름이 변경되었습니다."
	off := false
	got.Enabled = &off
	if _, err := d.SaveNotificationChannel(ctx, got); err != nil {
		t.Fatal(err)
	}
	after, err := d.NotificationChannelByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Name != "이름이 변경되었습니다." || after.IsEnabled() {
		t.Fatalf("업데이트가 적용되지 않았습니다.: %+v", after)
	}

	// 삭제 후 신고해야 함「이 존재하지 않습니다」소리 없는 성공 대신。
	if err := d.DeleteNotificationChannel(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := d.NotificationChannelByID(ctx, id); err != ErrNotificationChannelNotFound {
		t.Fatalf("기대 ErrNotificationChannelNotFound，받았어요 %v", err)
	}
	if err := d.DeleteNotificationChannel(ctx, id); err != ErrNotificationChannelNotFound {
		t.Fatalf("중복삭제신고가 존재하지 않습니다.，받았어요 %v", err)
	}
}

// TestSaveNotificationChannelKeepsExplicitZeroRate 잘못 쓴 곳은 잠그세요：
// **0 은 합법적인 구성입니다.，뜻「전류 제한 없음」，은 할 수 없습니다 db 레이어「지정되지 않음」기본값으로 덮어쓰기**。
//
// 연혁 bug：SaveNotificationChannel 로 작성 `if RatePerMin <= 0 { 기본값을 사용합니다. }`，
// 그래서 문서는、UI 팁、takeTokens 둘 다 누르세요「0=전류 제한 없음」설명，글쓰기 라이브러리 레이어만 조용히 으로 변경되었습니다.
// 20（딩톡/치웨이/Telegram）또는 100（페이슈）——운영자는 전류 제한이 완화되었다고 생각했습니다.、사실 막혔어요，
// 그리고 프롬프트가 없습니다。「지정되지 않음」그리고「명시적 0」차이점은 신체표현 요청뿐，
// 따라서 기본값은 server 레이어 채우기（또 만나요 notifyCreateChannel），db 레이어 전용 매장。
func TestSaveNotificationChannelKeepsExplicitZeroRate(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()

	// 명시적 0（전류 제한 없음）：그대로 저장해야 함。
	unlimited := &NotificationChannel{
		Name: "전류 제한 없음", Kind: notify.KindDingTalk, RatePerMin: 0,
		Config: json.RawMessage(`{"webhook":"https://example.com/h"}`),
	}
	id, err := d.SaveNotificationChannel(ctx, unlimited)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM notification_channels WHERE id=$1`, id) })
	got, err := d.NotificationChannelByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.RatePerMin != 0 {
		t.Fatalf("명시적 0 은 전류 제한이 없음을 의미합니다.，그대로 저장해야 함，받았어요 %d", got.RatePerMin)
	}
	if got.Mode != NotifyModeRealtime {
		t.Fatalf("기본 모드는 다음과 같아야 합니다. realtime，받았어요 %s", got.Mode)
	}

	// 음수값은 잘못된 입력입니다.，을 다른 값으로 조용히 변경하기보다는 거부해야 합니다.。
	bad := &NotificationChannel{
		Name: "음의 전류 제한", Kind: notify.KindDingTalk, RatePerMin: -1,
		Config: json.RawMessage(`{"webhook":"https://example.com/h"}`),
	}
	if _, err := d.SaveNotificationChannel(ctx, bad); err == nil {
		t.Fatal("음의 전류 제한은 거부되어야 합니다.")
	}
}

// TestDeleteChannelCascadesDeliveries 외래 키 잠금 동작：채널이 삭제되면 전송 이력도 사라집니다.
// （구성이 사라졌습니다.，역사를 해석할 수 없습니다.），하지만 사건 자체는 남겨야지——다른 채널에서도 인용될 수 있습니다.。
func TestDeleteChannelCascadesDeliveries(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()
	ch := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{}`)
	ev := addTestEvent(t, d, notify.EventFindingCreated, 9101, notify.Snapshot{Severity: "high"})
	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := d.QueryRow(`SELECT count(*) FROM notification_deliveries WHERE channel_id=$1`, ch.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before == 0 {
		t.Fatal("전제조건이 성립되지 않았습니다.：배송이 안됐어요")
	}
	if err := d.DeleteNotificationChannel(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := d.QueryRow(`SELECT count(*) FROM notification_deliveries WHERE channel_id=$1`, ch.ID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != 0 {
		t.Fatalf("채널 삭제 후 캐스케이드 방식으로 전송을 삭제해야 합니다.，아직은 %d 글", after)
	}
	var evExists bool
	if err := d.QueryRow(`SELECT EXISTS(SELECT 1 FROM notification_events WHERE id=$1)`, ev).Scan(&evExists); err != nil {
		t.Fatal(err)
	}
	if !evExists {
		t.Fatal("채널을 삭제한다고 해서 이벤트 자체도 삭제되어서는 안 됩니다.")
	}
}

// TestClaimDigestBatchHonorsCallerLimit 커버리지 감사에서 지적된 공백：
// 이전에 집계 채널이 토큰 버킷을 완전히 우회했습니다.——allow 은(는) takeTokens 공제되었으나 아무도 사용하지 않음，
// rate_per_min 예 digest 패턴이 적용되지 않습니다.。지금 limit 도 제약조건에 참여합니다.。
func TestClaimDigestBatchHonorsCallerLimit(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()
	ch := newTestChannel(t, d, notify.KindDingTalk, NotifyModeDigest, `{}`)
	for i := 0; i < 10; i++ {
		addTestEvent(t, d, notify.EventFindingCreated, int64(7000+i), notify.Snapshot{Severity: "high"})
	}
	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatal(err)
	}
	// 받아 limit=3：수신만 가능 3 글，나머지는 도서관에 있어요。
	got, err := d.ClaimDigestBatch(ctx, ch.ID, 3, time.Minute)
	if err != nil {
		t.Fatalf("수신 실패: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("발신자의 현재 한도만큼만 수신해야 함 3 글，받았어요 %d", len(got))
	}
	// limit=0 은 이번 라운드의 할당량이 소진되었음을 의미합니다.：하나도 받지 말아야지，오류도 보고하면 안 됩니다.。
	if got, err := d.ClaimDigestBatch(ctx, ch.ID, 0, time.Minute); err != nil || len(got) != 0 {
		t.Fatalf("한도는 0 은 다음과 같은 경우에 수집되어야 합니다. 0 오류 없이，받았어요 %d 글 err=%v", len(got), err)
	}
}

// TestFinishFindingRetestEmitsStatusChange 감사를 통해 파악된 무결성 격차를 커버합니다.：
// 재시험의 결론은「고정됨」시간，상태가 정말 바뀌었어요，그런데 그거 UPDATE 은 라이브러리에 직접 작성됩니다.、
// 알림이 포함된 우회 버전——그래서 할당했어요 on_status_change 님의 채널이 이 상태의 흐름을 담당합니다.
// 푸시 알림이 전혀 수신되지 않습니다，인터페이스 상태가 조용히 바뀌었습니다.，플랫폼을 열어야 운영과 유지보수를 알 수 있습니다。
//
// 이 사용 사례는 잠겨 있습니다.「상태가 변경되는 모든 경로는 상태 변경 이벤트를 등록해야 합니다.」。
func TestFinishFindingRetestEmitsStatusChange(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()

	tk, err := d.CreateTask("푸시 테스트를 다시 테스트하세요.", "대상", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(tk.ID)
	es := d.Exploration(tk.ExplorationID)
	f, err := es.RecordFinding(ctx, RecordFindingInput{
		TaskID: tk.ID, Worker: "test", VulnClass: "SQL주사", Name: "재시험 목표",
		Severity: "high", Summary: "요약",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM notification_events WHERE finding_id=$1`, f.FindingID) })

	// 재테스트 기록을 생성하고 바로 완료 상태로 푸시합니다.。
	rt, _, _, err := d.CreateFindingRetest(ctx, f.FindingID, "검토")
	if err != nil {
		t.Fatal(err)
	}
	if rt.ConversationID == nil {
		t.Fatal("재테스트는 세션과 연결되어야 합니다.")
	}
	// 재시험을 먼저 입력해야 합니다. running 그래야만 결론을 내릴 수 있다（실제 프로세스와 일치）。
	if ok, err := d.StartFindingRetest(ctx, rt.ID); err != nil || !ok {
		t.Fatalf("재테스트를 시작하지 못했습니다.: ok=%v err=%v", ok, err)
	}
	if err := d.RecordFindingRetestResult(ctx, *rt.ConversationID, "fixed", "고정됨", "증거"); err != nil {
		t.Fatal(err)
	}
	if err := d.FinishFindingRetest(rt.ID, "completed", ""); err != nil {
		t.Fatalf("재테스트를 종료하지 못했습니다.: %v", err)
	}

	var status string
	if err := d.QueryRow(`SELECT status FROM findings WHERE id=$1`, f.FindingID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != FindingFixed {
		t.Fatalf("재시험판정이 수리된 후에 상태가 되어야 한다. fixed，받았어요 %s", status)
	}

	// 핵심 어설션：상태 변경 이벤트가 있어야 합니다.，그리고 from/to 맞습니다。
	var snapshot []byte
	err = d.QueryRow(`SELECT snapshot FROM notification_events WHERE finding_id=$1 AND kind=$2 ORDER BY id DESC LIMIT 1`,
		f.FindingID, notify.EventFindingStatusChanged).Scan(&snapshot)
	if err != nil {
		t.Fatalf("재테스트 판정이 수정되어 상태변경 푸시 이벤트를 등록해야 합니다.（그렇지 않으면 일치합니다. on_status_change 님의 채널을 수신할 수 없습니다）: %v", err)
	}
	var snap notify.Snapshot
	if err := json.Unmarshal(snapshot, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.FromStatus != "pending" || snap.ToStatus != FindingFixed {
		t.Fatalf("스냅샷의 상태 흐름이 잘못되었습니다.: %s → %s", snap.FromStatus, snap.ToStatus)
	}
	// 스냅샷에는 렌더링에 필요한 필드가 있어야 합니다.，그렇지 않으면 푸시된 쉘이 비어 있게 됩니다.。
	if snap.Name != "재시험 목표" || snap.Severity != "high" {
		t.Fatalf("스냅샷에 렌더링 필드가 누락되었습니다.: %+v", snap)
	}
}
