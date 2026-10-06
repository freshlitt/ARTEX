package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/notify"
)

// 이 문서에서는 푸시 기능의 엔드투엔드 동작을 다룹니다.：취약점 로깅 → 이벤트 → 파견 → 진짜머리 HTTP。
//
// 안전 참고사항：이러한 사용 사례**글로벌로 전화하지 마세요 Notifier.step()**，직접 제작하신 분에 한해
// 채널콜 stepRealtime/stepDigest。이유는 step() 은 라이브러리에서 활성화된 모든 채널을 트래버스합니다.——
// 실제 딩톡에서/Qiwei Robot 개발 라이브러리에서 테스트 실행，글로벌 step 테스트 기간을 유지합니다
// 생성된 취약점은 실제로 해당 그룹에 푸시됩니다.。채널별 호출은 테스트로 인해 생성된 가짜 수신 측에 미치는 영향을 엄격하게 제한합니다.。
//
// 정리하다：유스케이스가 종료되면 이 유스케이스에서 생성된 이벤트를 삭제합니다.（계단식 삭제 전송）및 채널，실제 채널에 대한 백로그를 남기지 마세요。
//
// 주장 구경：stepRealtime/stepDigest 반환값 없음、내부 로깅，그래서 여기서 주장하는 것은
// **관찰 가능한 외부 동작**（가짜 수신자는 무엇을 받았나요?、배송라인은 어떤 상태인가요?），함수 대신
// 반환 값——반환 값을 쌓는 것보다 실제 호출 경로에 더 가깝습니다.。

// notifyFixture 은 이 문서에서 사용 사례의 공용 장치입니다.。
type notifyFixture struct {
	s       *Server
	pg      *db.DB
	request func(method, path, body string) *httptest.ResponseRecorder
	n       *Notifier
	// 자체 제작 task/exploration：여기에 취약점을 기록하는 사용 사례，다른 사용 사례로부터 데이터 격리。
	taskID int64
	expID  int64
	// cleanupMark 이후 발생한 이벤트는 정리 시 함께 삭제됩니다.。
	cleanupMark int64
}

func newNotifyFixture(t *testing.T) *notifyFixture {
	t.Helper()
	// 이 파일의 모든 잘못된 수신자는 다음에서 실행됩니다. 127.0.0.1 에，배달 기본값은 루프백 주소를 거부합니다.
	// （수비 SSRF 동일 머신 서비스 및 클라우드 메타데이터 검색）。이 스위치를 명시적으로 켜는 테스트；
	// 경비병「기본적으로 거부됨」의 행동은 다음으로 인해 발생합니다. notify 포함됨 ssrf_test.go 재정의。
	t.Setenv(notify.AllowLocalTargetsEnv, "1")
	s, _, request := trafficEvidenceServer(t)
	pg := s.m.pg

	// 직접 만들어 보세요 task：공유 장치 trafficEvidenceServer 만들어졌다 task 못찾겠어요 exploration id，
	// 로깅하는 동안 취약점을 제공해야 합니다.。
	task, err := s.m.CreateTask("알림 푸시 테스트", "푸시 동작 검증", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := strconv.ParseInt(task.ID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pg.Exec(`DELETE FROM tasks WHERE id=$1`, taskID) })

	var mark int64
	if err := pg.QueryRow(`SELECT COALESCE(max(id),0) FROM notification_events`).Scan(&mark); err != nil {
		t.Fatal(err)
	}
	// 장치가 폐쇄 루프를 형성하도록 합니다.：만들어 보세요 fixture 이전에 존재했던 이벤트는 한번에 전달되는 것으로 표시됩니다.。
	//
	// 왜 해야 하는가：FanOutPendingEvents 네**글로벌**님，은 전달되지 않은 모든 이벤트를 라이브러리에 저장합니다.
	// 일치하는 모든 채널로 확장。및 공유 장치 trafficEvidenceServer 저도 허점을 기억하고 있어요
	// （반환하는 초기값입니다. finding），다른 사용 사례에도 잔여물이 있을 수 있습니다.。격리되지 않은 경우，
	// 이러한 가짜 이벤트는 이 사용 사례의 채널로 전달됩니다.，하자「해야지 N 배달」이런 주장은
	// 맞을 때도 있고 틀릴 때도 있어요——그리고 잘못된 방법은 Use Case 실행 순서에 따라 다릅니다.，직접적인 실패보다 확인하기가 더 어렵습니다.。
	if _, err := pg.Exec(`UPDATE notification_events SET fanned_out = true WHERE id <= $1 AND NOT fanned_out`, mark); err != nil {
		t.Fatal(err)
	}

	f := &notifyFixture{s: s, pg: pg, request: request, n: newNotifier(s), taskID: taskID, expID: task.ExpID, cleanupMark: mark}
	t.Cleanup(func() {
		if _, err := pg.Exec(`DELETE FROM notification_events WHERE id > $1`, f.cleanupMark); err != nil {
			t.Logf("알림 이벤트를 지우지 못했습니다.: %v", err)
		}
	})
	// 메인 스위치가 켜져 있어야 합니다.（다른 사용 사례에서는 비활성화될 수 있습니다.）。
	if err := pg.SetBool(settingNotifyEnabled, true); err != nil {
		t.Fatal(err)
	}
	return f
}

// record 실제 증거 작성 경로를 취하면 허점이 발생합니다.，복귀 finding id。
// 이 길은**동일한 거래**에 푸시 이벤트를 등록하세요.——은 정확히 이 함수의 중단점입니다.。
func (f *notifyFixture) record(t *testing.T, vulnclass, severity string) int64 {
	t.Helper()
	out, err := f.s.evidenceStore().Record(context.Background(), db.RecordFindingInput{
		TaskID:        f.taskID,
		ExplorationID: f.expID,
		Worker:        "test",
		VulnClass:     vulnclass,
		Name:          vulnclass,
		Severity:      severity,
		Summary:       vulnclass + " 요약",
		Evidence:      "poc",
	}, nil)
	if err != nil {
		t.Fatalf("취약점 로그 실패: %v", err)
	}
	return out.FindingID
}

// channel 채널 구성 다시 읽기（채널별 통화의 경우 stepX 사용）。
func (f *notifyFixture) channel(t *testing.T, id int64) *db.NotificationChannel {
	t.Helper()
	ch, err := f.pg.NotificationChannelByID(context.Background(), id)
	if err != nil {
		t.Fatalf("채널을 읽지 못했습니다.: %v", err)
	}
	return ch
}

// deliver 이벤트를 디스패치하고 지정된 채널에 한 라운드만 전달을 실행합니다.。
func (f *notifyFixture) deliver(t *testing.T, chID int64, baseURL string) {
	t.Helper()
	ctx := context.Background()
	if _, _, err := f.pg.FanOutPendingEvents(ctx, 500); err != nil {
		t.Fatalf("발송 실패: %v", err)
	}
	f.n.stepRealtime(ctx, f.channel(t, chID), 50, baseURL)
}

// createChannel 합격 HTTP 인터페이스 구축 채널，그런데, 인터페이스 자체의 검증 경로를 덮어쓰세요.。
func (f *notifyFixture) createChannel(t *testing.T, payload map[string]any) int64 {
	t.Helper()
	raw, _ := json.Marshal(payload)
	r := f.request("POST", "/api/notify/channels", string(raw))
	if r.Code != 200 {
		t.Fatalf("채널 생성 실패 %d: %s", r.Code, r.Body)
	}
	var res struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &res); err != nil || res.ID == 0 {
		t.Fatalf("채널을 만들면 예외가 반환됩니다.: %s (%v)", r.Body, err)
	}
	t.Cleanup(func() { f.pg.Exec(`DELETE FROM notification_channels WHERE id=$1`, res.ID) })
	return res.ID
}

// fakeWebhook 은 수신된 요청 본문을 기록하는 가짜 수신측입니다.。
type fakeWebhook struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []map[string]any
}

func newFakeWebhook(t *testing.T) *fakeWebhook {
	t.Helper()
	f := &fakeWebhook{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.bodies = append(f.bodies, body)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeWebhook) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.bodies)
}

func (f *fakeWebhook) body(t *testing.T, i int) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.bodies) {
		t.Fatalf("가짜 수신측은 수신만 합니다. %d 요청，첫 번째를 얻을 수 없습니다 %d 글", len(f.bodies), i)
	}
	return f.bodies[i]
}

func (f *fakeWebhook) last(t *testing.T) map[string]any {
	t.Helper()
	if f.count() == 0 {
		t.Fatal("가짜 수신측에서는 어떠한 요청도 받지 못했습니다.")
	}
	return f.body(t, f.count()-1)
}

// markdownText 요청 본문에서 텍스트 가져오기，다양한 회사의 필드명이 달라도 호환 가능：
// 딩톡 markdown 사용 `text`、ActionCard 사용 `text`、기업 위챗 markdown 사용 `content`。
func markdownText(t *testing.T, body map[string]any) string {
	t.Helper()
	for _, key := range []string{"markdown", "actionCard"} {
		section, ok := body[key].(map[string]any)
		if !ok {
			continue
		}
		for _, field := range []string{"text", "content"} {
			if s, ok := section[field].(string); ok && s != "" {
				return s
			}
		}
	}
	t.Fatalf("요청 본문에 식별 가능한 텍스트가 없습니다.: %v", body)
	return ""
}

// agePendingBatch 보류 중인 전달을 채널에 알림，요약 배치 만료를 테스트하는 데 사용됩니다.。
func (f *notifyFixture) agePendingBatch(t *testing.T, chID int64) {
	t.Helper()
	if _, err := f.pg.Exec(`UPDATE notification_deliveries SET created_at = now() - interval '2 hours'
WHERE channel_id=$1 AND state=$2`, chID, db.NotifyStatePending); err != nil {
		t.Fatal(err)
	}
}

func TestNotifyEndToEndRealtimeDelivery(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	chID := f.createChannel(t, map[string]any{
		"name":   "실시간 푸시",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": hook.URL},
	})
	f.record(t, "SQL주사", "high")
	f.deliver(t, chID, "")

	if hook.count() != 1 {
		t.Fatalf("이 발급되어야 합니다. 1 메시지，실제 %d", hook.count())
	}
	text := markdownText(t, hook.last(t))
	for _, want := range []string{"SQL주사", "위험도 높음", "요약"} {
		if !strings.Contains(text, want) {
			t.Fatalf("메시지 본문이 누락되었습니다. %q:\n%s", want, text)
		}
	}
	// 배송을 다음 주소로 전달해야 합니다. sent。
	var pending int
	if err := f.pg.QueryRow(`SELECT count(*) FROM notification_deliveries WHERE channel_id=$1 AND state <> $2`,
		chID, db.NotifyStateSent).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatalf("배송 후에도 사용 가능 %d 항목이 표시되지 않습니다 sent", pending)
	}
}

func TestNotifyChannelAPIMasksSecretsAndPreservesOnUpdate(t *testing.T) {
	f := newNotifyFixture(t)
	chID := f.createChannel(t, map[string]any{
		"name":   "마스크 활용 사례",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": "https://oapi.dingtalk.com/robot/send?access_token=abc123456", "secret": "SECabcdef123456"},
	})

	r := f.request("GET", "/api/notify/channels", "")
	if r.Code != 200 {
		t.Fatalf("채널 목록을 표시하지 못했습니다. %d: %s", r.Code, r.Body)
	}
	if strings.Contains(r.Body.String(), "abc123456") || strings.Contains(r.Body.String(), "SECabcdef123456") {
		t.Fatalf("인터페이스 에코에서 자격 증명이 유출되었습니다.: %s", r.Body)
	}
	var listed struct {
		Channels []struct {
			ID         int64          `json:"id"`
			Config     map[string]any `json:"config"`
			SecretKeys []string       `json:"secret_keys"`
		} `json:"channels"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	var mine *struct {
		ID         int64          `json:"id"`
		Config     map[string]any `json:"config"`
		SecretKeys []string       `json:"secret_keys"`
	}
	for i := range listed.Channels {
		if listed.Channels[i].ID == chID {
			mine = &listed.Channels[i]
		}
	}
	if mine == nil {
		t.Fatal("새로 생성된 채널이 목록에 나타나지 않습니다.")
	}
	if !notify.IsMasked(fmt.Sprint(mine.Config["webhook"])) || !notify.IsMasked(fmt.Sprint(mine.Config["secret"])) {
		t.Fatalf("자격 증명 필드는 마스크된 값이어야 합니다.: %v", mine.Config)
	}
	if len(mine.SecretKeys) == 0 {
		t.Fatal("인터페이스는 어떤 필드가 자격 증명인지 프런트 엔드에 알려야 합니다.")
	}

	// PATCH 이름만 바꾸세요 + 마스킹된 자격 증명 반환：실제 자격 증명은 그대로 유지되어야 합니다.。
	body, _ := json.Marshal(map[string]any{
		"name":   "이름 변경 후",
		"config": map[string]any{"webhook": fmt.Sprint(mine.Config["webhook"]), "secret": fmt.Sprint(mine.Config["secret"])},
	})
	if r := f.request("PATCH", fmt.Sprintf("/api/notify/channels/%d", chID), string(body)); r.Code != 200 {
		t.Fatalf("업데이트 실패 %d: %s", r.Code, r.Body)
	}
	cfg := f.channelConfig(t, chID)
	if cfg["webhook"] != "https://oapi.dingtalk.com/robot/send?access_token=abc123456" {
		t.Fatalf("마스크된 포스트백에는 실제 자격 증명이 포함됩니다.: %v", cfg["webhook"])
	}
	if cfg["secret"] != "SECabcdef123456" {
		t.Fatalf("마스크반환 secret 덮었다: %v", cfg["secret"])
	}
	if f.channel(t, chID).Name != "이름 변경 후" {
		t.Fatal("이름이 업데이트되지 않았습니다.")
	}

	// 명시적 지우기 secret 이 적용되어야 합니다.（은(와) 다릅니다「리턴마스크=변함없이 그대로 유지」）。
	body, _ = json.Marshal(map[string]any{"config": map[string]any{"secret": ""}})
	if r := f.request("PATCH", fmt.Sprintf("/api/notify/channels/%d", chID), string(body)); r.Code != 200 {
		t.Fatalf("클리어 secret 실패 %d: %s", r.Code, r.Body)
	}
	if _, still := f.channelConfig(t, chID)["secret"]; still {
		t.Fatal("빈 문자열을 지워야 합니다. secret")
	}
}

func (f *notifyFixture) channelConfig(t *testing.T, id int64) map[string]any {
	t.Helper()
	var cfg map[string]any
	if err := json.Unmarshal(f.channel(t, id).Config, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestNotifyChannelAPICreateValidation(t *testing.T) {
	f := newNotifyFixture(t)
	cases := []struct {
		name    string
		payload map[string]any
		wantSub string
	}{
		{"잘못된 유형입니다.", map[string]any{"name": "x", "kind": "nope", "config": map[string]any{}}, "잘못된 채널 유형"},
		{"이름이 없습니다.", map[string]any{"kind": notify.KindDingTalk, "config": map[string]any{"webhook": "https://e.com/h"}}, "채널 이름이 없습니다."},
		{"누락 webhook", map[string]any{"name": "x", "kind": notify.KindDingTalk, "config": map[string]any{}}, "Webhook"},
		{"webhook 계약은 불법입니다", map[string]any{"name": "x", "kind": notify.KindDingTalk, "config": map[string]any{"webhook": "file:///etc/passwd"}}, "Webhook 주소가 잘못되었습니다."},
		{"불법 모드", map[string]any{"name": "x", "kind": notify.KindDingTalk, "mode": "sometimes", "config": map[string]any{"webhook": "https://e.com/h"}}, "푸시 모드가 잘못되었습니다."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(tc.payload)
			r := f.request("POST", "/api/notify/channels", string(raw))
			if r.Code != 400 {
				t.Fatalf("이(가) 반환되어야 합니다. 400，받았어요 %d: %s", r.Code, r.Body)
			}
			if !strings.Contains(r.Body.String(), tc.wantSub) {
				t.Fatalf("오류 메시지에 언급되어야 합니다. %q，받았어요 %s", tc.wantSub, r.Body)
			}
		})
	}
	if r := f.request("DELETE", "/api/notify/channels/99999999", ""); r.Code != 404 {
		t.Fatalf("존재하지 않는 채널을 삭제해야 합니다. 404，받았어요 %d", r.Code)
	}
}

func TestNotifyFilterBlocksBelowThreshold(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	chID := f.createChannel(t, map[string]any{
		"name":   "심한 경우에만 해당",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": hook.URL},
		"filter": map[string]any{"min_severity": "critical"},
	})
	f.record(t, "위험도가 낮은 문제", "low")
	if _, _, err := f.pg.FanOutPendingEvents(context.Background(), 500); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.pg.QueryRow(`SELECT count(*) FROM notification_deliveries WHERE channel_id=$1`, chID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("임계값 미만의 취약점으로 인해 전달되어서는 안 됩니다.，받았어요 %d 글", n)
	}
	f.n.stepRealtime(context.Background(), f.channel(t, chID), 50, "")
	if hook.count() != 0 {
		t.Fatal("필터링된 취약점은 메시지를 내보내서는 안 됩니다.")
	}
}

func TestNotifyDigestBatchesMultipleFindingsIntoOneMessage(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	chID := f.createChannel(t, map[string]any{
		"name":   "요약 푸시",
		"kind":   notify.KindDingTalk,
		"mode":   db.NotifyModeDigest,
		"config": map[string]any{"webhook": hook.URL},
	})
	for i := 0; i < 3; i++ {
		f.record(t, fmt.Sprintf("취약점 요약%d", i+1), "high")
	}
	ctx := context.Background()
	if _, _, err := f.pg.FanOutPendingEvents(ctx, 500); err != nil {
		t.Fatal(err)
	}
	ch := f.channel(t, chID)

	// 만료되지 않음：보내지 않음。
	f.n.stepDigest(ctx, ch, 50, "")
	if hook.count() != 0 {
		t.Fatal("요약 배치가 만료되기 전에 전송되었습니다.")
	}

	// 숙성 배치 후：세 개의 메시지가 하나의 메시지로 합쳐졌습니다.。
	f.agePendingBatch(t, chID)
	f.n.stepDigest(ctx, ch, 50, "")
	if got := hook.count(); got != 1 {
		t.Fatalf("세 개의 메시지를 하나의 메시지로 요약해야 합니다.，실제로 보냈습니다. %d 글", got)
	}
	text := markdownText(t, hook.last(t))
	if !strings.Contains(text, "근처") || !strings.Contains(text, "3 취약점") {
		t.Fatalf("요약 메시지 수가 누락되었습니다./시간 창 카피라이팅:\n%s", text)
	}
	for i := 1; i <= 3; i++ {
		if !strings.Contains(text, fmt.Sprintf("취약점 요약%d", i)) {
			t.Fatalf("요약 메시지에 첫 번째 메시지가 누락되었습니다. %d 글:\n%s", i, text)
		}
	}
	// 동일한 배치를 공유해야 합니다. batch_id。
	var distinct, total int
	if err := f.pg.QueryRow(`SELECT count(DISTINCT batch_id), count(*) FROM notification_deliveries WHERE channel_id=$1`, chID).Scan(&distinct, &total); err != nil {
		t.Fatal(err)
	}
	if total != 3 || distinct != 1 {
		t.Fatalf("세 개의 배달이 하나를 공유해야 합니다. batch_id，받았어요 distinct=%d total=%d", distinct, total)
	}
}

func TestNotifyDisabledChannelDoesNotSend(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	chID := f.createChannel(t, map[string]any{
		"name":    "채널 비활성화",
		"kind":    notify.KindDingTalk,
		"enabled": false,
		"config":  map[string]any{"webhook": hook.URL},
	})
	f.record(t, "비활성화 중 취약점", "critical")
	if _, _, err := f.pg.FanOutPendingEvents(context.Background(), 500); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.pg.QueryRow(`SELECT count(*) FROM notification_deliveries WHERE channel_id=$1`, chID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("비활성화된 채널은 전송을 생성해서는 안 됩니다.，받았어요 %d 글", n)
	}
}

func TestNotifyStatusChangeDelivery(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	chID := f.createChannel(t, map[string]any{
		"name":   "상태변경 구독",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": hook.URL},
		"filter": map[string]any{"on_status_change": true},
	})
	finding := f.record(t, "상태 변경 사용 사례", "high")
	r := f.request("PATCH", fmt.Sprintf("/api/exploration/findings/%d", finding), `{"status":"fixed"}`)
	if r.Code != 200 {
		t.Fatalf("상태 변경 실패 %d: %s", r.Code, r.Body)
	}
	f.deliver(t, chID, "")

	// 2개는 있어야지：fixed 그거 상태변경이에요；finding_created 저것도 같은 라운드에 보내질 수도 있겠네요。
	// 상태 변경은 실제로 나중에 생성됩니다.，단, 순서에 구애받지 않음，전체 수량 검색。
	found := false
	for i := 0; i < hook.count(); i++ {
		text := markdownText(t, hook.body(t, i))
		if strings.Contains(text, "상태변화") && strings.Contains(text, "고정됨") {
			found = true
		}
	}
	if !found {
		t.Fatalf("받지 못했습니다.「상태변화 → 고정됨」의 메시지（합계 %d 글）", hook.count())
	}
}

func TestNotifyStatusChangeSuppressedByDefault(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	chID := f.createChannel(t, map[string]any{
		"name":   "상태 변경을 구독하지 않습니다.",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": hook.URL},
	})
	finding := f.record(t, "변경사항을 구독하지 않음", "high")
	if r := f.request("PATCH", fmt.Sprintf("/api/exploration/findings/%d", finding), `{"status":"false_positive"}`); r.Code != 200 {
		t.Fatalf("상태 변경 실패 %d: %s", r.Code, r.Body)
	}
	if _, _, err := f.pg.FanOutPendingEvents(context.Background(), 500); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.pg.QueryRow(`SELECT count(*) FROM notification_deliveries d
JOIN notification_events e ON e.id = d.event_id
WHERE d.channel_id=$1 AND e.kind=$2`, chID, notify.EventFindingStatusChanged).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("상태 변경을 구독하지 않은 채널은 상태 변경 전달을 받을 수 없습니다.，받았어요 %d 글", n)
	}
}

func TestNotifyTestMessageEndpoint(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	chID := f.createChannel(t, map[string]any{
		"name":   "테스트 송신",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": hook.URL},
	})
	if r := f.request("POST", fmt.Sprintf("/api/notify/channels/%d/test", chID), ""); r.Code != 200 {
		t.Fatalf("테스트 전송 실패 %d: %s", r.Code, r.Body)
	}
	if hook.count() != 1 {
		t.Fatalf("가짜 수신측은 수신을 받아야 합니다. 1 테스트 메시지，받았어요 %d", hook.count())
	}
	// 테스트 메시지는 한눈에 테스트임을 명확하게 식별할 수 있어야 합니다.，실제 취약점으로 착각할 수 없음。
	if text := markdownText(t, hook.last(t)); !strings.Contains(text, "테스트") {
		t.Fatalf("테스트 메시지는 테스트로 표시되어야 합니다.: %s", text)
	}
	// 구성이 깨지면 채널의 원래 오류가 사용자에게 진실되게 반환되어야 합니다.。
	badID := f.createChannel(t, map[string]any{
		"name":   "주소가 잘못되었습니다.",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": "http://127.0.0.1:1/hook"},
	})
	if r := f.request("POST", fmt.Sprintf("/api/notify/channels/%d/test", badID), ""); r.Code != 502 {
		t.Fatalf("배송실패시 응답 502，받았어요 %d: %s", r.Code, r.Body)
	}
}

func TestNotifyDeliveriesHistoryAndRetry(t *testing.T) {
	f := newNotifyFixture(t)
	// 은 실패해야 하는 주소를 가리킵니다.，제조 failed 배송。
	chID := f.createChannel(t, map[string]any{
		"name":   "다시 시도하지 못했습니다.",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": "http://127.0.0.1:1/hook"},
	})
	f.record(t, "푸시가 실패합니다.", "high")
	ctx := context.Background()
	if _, _, err := f.pg.FanOutPendingEvents(ctx, 500); err != nil {
		t.Fatal(err)
	}
	ch := f.channel(t, chID)
	// 재시도 예산 소진시까지 지속적인 투자。
	for i := 0; i < db.MaxNotifyAttempts; i++ {
		f.n.stepRealtime(ctx, ch, 50, "")
		if _, err := f.pg.Exec(`UPDATE notification_deliveries SET next_attempt_at = now() - interval '1 minute' WHERE channel_id=$1`, chID); err != nil {
			t.Fatal(err)
		}
	}
	var state string
	if err := f.pg.QueryRow(`SELECT state FROM notification_deliveries WHERE channel_id=$1`, chID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != db.NotifyStateFailed {
		t.Fatalf("재시도가 끝나면 failed，받았어요 %s", state)
	}

	r := f.request("GET", fmt.Sprintf("/api/notify/deliveries?channel_id=%d&state=failed", chID), "")
	if r.Code != 200 {
		t.Fatalf("기록 확인 실패 %d: %s", r.Code, r.Body)
	}
	var hist struct {
		Deliveries []struct {
			ID        int64  `json:"id"`
			State     string `json:"state"`
			LastError string `json:"last_error"`
			Attempts  int    `json:"attempts"`
			Title     string `json:"title"`
		} `json:"deliveries"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &hist); err != nil {
		t.Fatal(err)
	}
	if hist.Total != 1 || len(hist.Deliveries) != 1 {
		t.Fatalf("찾아야 함 1 배송 실패，받았어요 total=%d len=%d", hist.Total, len(hist.Deliveries))
	}
	if hist.Deliveries[0].LastError == "" {
		t.Fatal("실패사유를 이력에 포함시켜야 함，그렇지 않으면 사용자가 문제를 해결할 수 없습니다.")
	}
	if hist.Deliveries[0].Attempts < db.MaxNotifyAttempts {
		t.Fatalf("시도 횟수를 기록해야 합니다.，받았어요 %d", hist.Deliveries[0].Attempts)
	}
	if hist.Deliveries[0].Title != "푸시가 실패합니다." {
		t.Fatalf("기록을 통해 취약점 제목이 밝혀져야 합니다.，받았어요 %q", hist.Deliveries[0].Title)
	}

	// 수동 재전송：이(가) 반환되어야 합니다. pending 및 카운트 지우기。
	if r := f.request("POST", fmt.Sprintf("/api/notify/deliveries/%d/retry", hist.Deliveries[0].ID), ""); r.Code != 200 {
		t.Fatalf("재전송 실패 %d: %s", r.Code, r.Body)
	}
	var attempts int
	if err := f.pg.QueryRow(`SELECT state, attempts FROM notification_deliveries WHERE id=$1`, hist.Deliveries[0].ID).Scan(&state, &attempts); err != nil {
		t.Fatal(err)
	}
	if state != db.NotifyStatePending || attempts != 0 {
		t.Fatalf("다시 보낸 후에는 pending 그리고 attempts=0，받았어요 %s/%d", state, attempts)
	}
}

func TestNotifyMetaAndSettingsRoundTrip(t *testing.T) {
	f := newNotifyFixture(t)
	r := f.request("GET", "/api/notify/meta", "")
	if r.Code != 200 {
		t.Fatalf("meta 실패: %s", r.Body)
	}
	var meta struct {
		Kinds []struct {
			Kind       string   `json:"kind"`
			SecretKeys []string `json:"secret_keys"`
		} `json:"kinds"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	if len(meta.Kinds) != len(notify.Kinds()) {
		t.Fatalf("meta 은 모두 나열해야 합니다. %d 채널，받았어요 %d", len(notify.Kinds()), len(meta.Kinds))
	}
	for _, k := range meta.Kinds {
		if len(k.SecretKeys) == 0 {
			t.Errorf("채널 %s 자격 증명 필드가 보고되지 않았습니다.", k.Kind)
		}
	}

	// 세 가지 전역 설정 왕복。후행 슬래시는 정규화되어야 합니다.，그렇지 않으면 링크 철자가 표시됩니다. "//function/..."。
	if r := f.request("PUT", "/api/settings", `{"notify_public_base_url":"https://artex.example.com/","notify_digest_interval_min":15,"notify_enabled":true}`); r.Code != 200 {
		t.Fatalf("설정을 쓰지 못했습니다. %d: %s", r.Code, r.Body)
	}
	t.Cleanup(func() {
		f.pg.Exec(`DELETE FROM settings WHERE key IN ($1,$2)`, settingNotifyPublicBaseURL, settingNotifyDigestMinutes)
	})
	payload := f.s.settingsPayload()
	if payload["notify_public_base_url"] != "https://artex.example.com" {
		t.Fatalf("반송링크 주소가 표준화되어 있지 않습니다.: %v", payload["notify_public_base_url"])
	}
	if payload["notify_digest_interval_min"] != 15 {
		t.Fatalf("요약주기가 효과적이지 않습니다.: %v", payload["notify_digest_interval_min"])
	}

	// 잘못된 값은 거부되어야 합니다.。
	for _, body := range []string{
		`{"notify_public_base_url":"ftp://x"}`,
		`{"notify_digest_interval_min":0}`,
		`{"notify_digest_interval_min":99999}`,
	} {
		if r := f.request("PUT", "/api/settings", body); r.Code != 400 {
			t.Errorf("%s 이(가) 반환되어야 합니다. 400，받았어요 %d", body, r.Code)
		}
	}
}

// TestNotifyDeepLinkUsesPublicBaseURL 커버백 링크 접합：일치함 public_base_url 시간
// 단일 메시지에는 버튼을 사용해야 합니다. ActionCard，및 해당 링크는 취약점 세부정보 페이지를 가리킵니다.。
func TestNotifyDeepLinkUsesPublicBaseURL(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	chID := f.createChannel(t, map[string]any{
		"name":   "링크뒤로",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": hook.URL},
	})
	finding := f.record(t, "체인 취약점 복구", "high")
	f.deliver(t, chID, "https://artex.example.com")

	body := hook.last(t)
	card, _ := body["actionCard"].(map[string]any)
	if card == nil {
		t.Fatalf("다시링크 나오면 신청하세요 ActionCard，받았어요 msgtype=%v", body["msgtype"])
	}
	want := fmt.Sprintf("https://artex.example.com/function/findings/detail?id=%d", finding)
	if card["singleURL"] != want {
		t.Fatalf("뒤로가기 링크가 잘못됐네요\n기대 %s\n받았어요 %v", want, card["singleURL"])
	}
}

// TestNotifyNoDeepLinkWithoutBaseURL 리버스 커버리지：외부 주소가 할당되지 않은 경우 잘못된 링크가 생성되어서는 안 됩니다.
// （예를 들어 다음을 가리킵니다. localhost 또는 상대 경로），순수로 반환해야 함 markdown。
func TestNotifyNoDeepLinkWithoutBaseURL(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	chID := f.createChannel(t, map[string]any{
		"name":   "링크가 없습니다",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": hook.URL},
	})
	f.record(t, "백링크 취약점 없음", "high")
	f.deliver(t, chID, "")

	body := hook.last(t)
	if body["msgtype"] != "markdown" {
		t.Fatalf("외부 주소가 할당되지 않은 경우 전송되어야 합니다. markdown，받았어요 %v", body["msgtype"])
	}
	if text := markdownText(t, body); strings.Contains(text, "자세히 보기") {
		t.Fatalf("외부 주소가 할당되지 않은 경우 세부정보 링크가 표시되지 않아야 합니다.:\n%s", text)
	}
}

// TestNotifyDigestSegmentsAndDefersRemainder 네「침묵이 사라졌다」수정에 대한 엔드투엔드 증거。
//
// 요약 메시지에는 채널 길이 상한이 적용됩니다.（치웨이 4096 바이트），하나의 배치를 로드할 수 없는 경우 사용해야 합니다.**전체글을 클릭하세요**세분화：
// 본 글에 포함된 태그가 배송되었습니다.，나머지는 대기열로 돌아가서 다음을 기다립니다。이전 구현은 전체 배치를 표시하는 것이었습니다.
// 성공——잘린건 메시지에 없네요、도 실패 목록에 없습니다.，배송 이력도 성공을 보여주네요，
// 허점이 사라졌습니다。
//
// 네 가지를 주장하세요.：① 실제 탑재된 항목 개수만 표시됩니다. ② 나머지는 아직 전송 대기 중입니다. ③ 참가 연기
// **재시도 횟수가 소모되지 않았습니다.** ④ 한 라운드 더 나머지를 보내세요（막히지 않을 것입니다）。
func TestNotifyDigestSegmentsAndDefersRemainder(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	// 기업 위챗을 이용하세요：markdown 상한 4096 바이트，6개 채널 중 가장 타이트합니다.。
	chID := f.createChannel(t, map[string]any{
		"name":   "세그먼트 요약",
		"kind":   notify.KindWeCom,
		"mode":   db.NotifyModeDigest,
		"config": map[string]any{"webhook": hook.URL},
	})
	const total = 60
	// 제목을 길게 해주세요，보장 60 훨씬 뛰어넘는다 4096 바이트，필요한 세분화。
	longName := strings.Repeat("취약점 이름이 너무 깁니다.", 6)
	for i := 0; i < total; i++ {
		f.record(t, longName+strconv.Itoa(i+1), "high")
	}
	ctx := context.Background()
	if _, _, err := f.pg.FanOutPendingEvents(ctx, 500); err != nil {
		t.Fatal(err)
	}
	f.agePendingBatch(t, chID)
	ch := f.channel(t, chID)

	f.n.stepDigest(ctx, ch, 50, "")
	if hook.count() != 1 {
		t.Fatalf("메시지는 하나만 보내야 합니다，받았어요 %d", hook.count())
	}

	var sent, pending int
	if err := f.pg.QueryRow(`SELECT
    count(*) FILTER (WHERE state=$2),
    count(*) FILTER (WHERE state=$3)
  FROM notification_deliveries WHERE channel_id=$1`, chID, db.NotifyStateSent, db.NotifyStatePending).
		Scan(&sent, &pending); err != nil {
		t.Fatal(err)
	}
	if sent == 0 {
		t.Fatal("항목이 전달됨으로 표시되어야 합니다.")
	}
	if pending == 0 {
		t.Fatalf("일괄 %d 전부로드는 불가능합니다. 4096 바이트，배송이 좀 남았을 텐데요；sent=%d", total, sent)
	}
	if sent+pending != total {
		t.Fatalf("항목 수가 일치하지 않습니다.：sent=%d pending=%d total=%d（전달되지도 않았고 보류되지도 않았습니다.=졌다）", sent, pending, total)
	}
	// 메시지 텍스트에는 이 기사에 포함되지 않은 메시지 수를 사실대로 명시해야 합니다.。
	if text := markdownText(t, hook.last(t)); !strings.Contains(text, "나머지는") {
		t.Fatalf("이 문서에 포함되지 않은 항목이 있다는 메시지가 표시되어야 합니다.:\n%.400s", text)
	}

	// 연기된 참가 신청은 재시도 예산을 소모해서는 안 됩니다.：수신 시 attempts 이미 낙관적이다 +1，연기되면 다시 줄여주세요。
	var maxAttempts int
	if err := f.pg.QueryRow(`SELECT COALESCE(max(attempts),0) FROM notification_deliveries
WHERE channel_id=$1 AND state=$2`, chID, db.NotifyStatePending).Scan(&maxAttempts); err != nil {
		t.Fatal(err)
	}
	if maxAttempts > 0 {
		t.Fatalf("연기된 항목은 재시도를 소모해서는 안 됩니다.（그렇지 않으면 몇 줄만 지나면 실패로 판단됩니다.），받았어요 attempts=%d", maxAttempts)
	}

	// 수렴될 때까지 반복。주장하는 것은**드디어 다 배송됐어요**그리고 그 과정에서 실제로 여러 차례의 라운드가 있었습니다.——
	// 이게 더 좋아요「2차 배포가 끝났습니다」더 강하게：분할이 막히지 않는다는 것을 증명합니다.、은 나머지 항목을 버리지 않습니다.。
	rounds := 0
	for {
		var undelivered int
		if err := f.pg.QueryRow(`SELECT count(*) FROM notification_deliveries
WHERE channel_id=$1 AND state <> $2 AND state <> $3`, chID, db.NotifyStateSent, db.NotifyStateFailed).
			Scan(&undelivered); err != nil {
			t.Fatal(err)
		}
		if undelivered == 0 {
			break
		}
		rounds++
		if rounds > total+5 {
			t.Fatalf("분할된 전달이 수렴되지 않음：도망쳐버렸어 %d 아직 바퀴가 있어요 %d 보류 중", rounds, undelivered)
		}
		before := hook.count()
		f.n.stepDigest(ctx, ch, 50, "")
		if hook.count() == before {
			t.Fatalf("아니요. %d 라운드에 진전이 없습니다，남음 %d 바가 영구적으로 고정됩니다.", rounds, undelivered)
		}
	}
	if rounds < 2 {
		t.Fatalf("하나 4096 바이트 메시지를 로드할 수 없습니다. %d 긴 제목 취약점，여러 차례에 걸쳐 발행되어야 함，실제로만 사용함 %d 휠", total, rounds)
	}
	// 첫 라운드 이후 매 라운드는**퓨어헤어익스텐션**，채널에서 거부된 항목이 없습니다.。
	var failed int
	if err := f.pg.QueryRow(`SELECT count(*) FROM notification_deliveries WHERE channel_id=$1 AND state=$2`,
		chID, db.NotifyStateFailed).Scan(&failed); err != nil {
		t.Fatal(err)
	}
	if failed != 0 {
		t.Fatalf("가짜 수신자는 항상 성공을 반환합니다.，실패한 항목이 없어야 합니다.，받았어요 %d", failed)
	}
}

// TestNotifyBackoffTableMatchesAttemptBudget 은 드리프트 방지 주장입니다.。
//
// 예산 재시도（db.MaxNotifyAttempts）및 백오프 시퀀스 목록（notifyBackoff）두 개의 패키지를 분리합니다.：
// 전자는 상태머신의 전략이다.、후자가 엔진의 실행 비트입니다.。둘 중 하나만 변경된 경우——예를 들어 예산을 언급하세요. 5
// 탈출 장비를 추가하는 것을 잊어버린 경우가 많습니다.——코드는 오류를 보고하지 않습니다.，은(는) 4、5 마지막 간격을 사용하여 다시 시도합니다.，
// 은(는) 다음과 같이 동작합니다.「재시도 리듬이 설명할 수 없을 정도로 느립니다.」，확인해보면 이곳이 생각나기 힘드네요。
// 두 길이가 동일한지 확인，이것을 흘러가게 하라 CI 내부가 노출되어 있어요。
func TestNotifyBackoffTableMatchesAttemptBudget(t *testing.T) {
	if len(notifyBackoff) != db.MaxNotifyAttempts {
		t.Fatalf("백업 기어 수(%d)및 최대 시도 횟수(%d)일관성이 없다——하나를 변경하려면 다른 하나도 동시에 변경해야 합니다.",
			len(notifyBackoff), db.MaxNotifyAttempts)
	}
	// 백오프 간격은 단조롭고 감소하지 않아야 합니다.，그렇지 않으면 애쓰면 할수록 급박해지겠죠.，오히려 전류한계를 강화한다.。
	for i := 1; i < len(notifyBackoff); i++ {
		if notifyBackoff[i] < notifyBackoff[i-1] {
			t.Fatalf("백오프 간격은 단조롭고 감소하지 않아야 합니다.：아니요. %d 파일 %v < 아니요. %d 파일 %v",
				i, notifyBackoff[i], i-1, notifyBackoff[i-1])
		}
	}
}

// TestNotifyRateLimitDoesNotConsumeRetryBudget 자물쇠「토큰을 먼저 받고 그다음 받기」의 순서。
// 반대라면（먼저 리드하고 포기），현재 제한으로 인해 차단된 배송이 1회 카운트되었습니다 attempts，
// 순전한 기다림으로 인해 예산이 소진될 것입니다.，드디어 빠졌어요 failed。
func TestNotifyRateLimitDoesNotConsumeRetryBudget(t *testing.T) {
	// 토큰 버킷 자체만 테스트하세요.，필요하지 않음 Server（그것을 위해 하나도 만들어서는 안됩니다）。
	n := &Notifier{buckets: map[int64]*notifyBucket{}}
	now := time.Now()
	// 매분마다 1 글：버킷이 가득 찼을 때 최대값 1 글。
	if got := n.takeTokens(1, 1, notifyMaxSendsPerChannelPerTick, now); got != 1 {
		t.Fatalf("버킷이 가득 차면 1분마다 1 찍어야지 1 토큰，받았어요 %d", got)
	}
	if got := n.takeTokens(1, 1, notifyMaxSendsPerChannelPerTick, now.Add(time.Millisecond)); got != 0 {
		t.Fatalf("토큰 소진 후 즉시 반환해야 함 0，받았어요 %d", got)
	}
	if got := n.takeTokens(1, 1, notifyMaxSendsPerChannelPerTick, now.Add(30*time.Second)); got != 0 {
		t.Fatalf("하나의 토큰을 중간에 채워서는 안 됩니다.，받았어요 %d", got)
	}
	if got := n.takeTokens(1, 1, notifyMaxSendsPerChannelPerTick, now.Add(time.Minute)); got != 1 {
		t.Fatalf("1사이클 후에 보상해야 합니다. 1 토큰，받았어요 %d", got)
	}
	// 무제한 채널은 제한된 상한을 사용합니다.，무한한 백로그로 인해 단일 라운드가 지연되는 것을 방지하세요.。
	if got := n.takeTokens(2, 0, notifyUnlimitedBurstPerTick+10, now); got != notifyUnlimitedBurstPerTick {
		t.Fatalf("전류가 제한되지 않으면 각 라운드의 상한값을 반환해야 합니다. %d，받았어요 %d", notifyUnlimitedBurstPerTick, got)
	}
	// 채널 간 토큰 버킷은 서로 독립적입니다.。
	if got := n.takeTokens(1, 1, notifyMaxSendsPerChannelPerTick, now.Add(time.Millisecond)); got != 0 {
		t.Fatalf("채널 1 에 대한 버킷은 여전히 비어 있어야 합니다.，받았어요 %d", got)
	}
}

// TestNotifyTakeTokensKeepsUnusedTokens 자물쇠「가져가기만 하세요 want 」의 의미。
//
// 이전 구현에서는 전체 버킷을 비운 다음 호출자가 이를 잘랐습니다.，그래서 rate=100/min 채널이 꽉 찼습니다、
// 한 라운드만 5 글，나머지는 95 토큰은 직접 폐기됩니다.；이번 라운드에 보류 중인 배송이 없으면 채널도 차감됩니다.。
// 댓글이 주장하는 결과는 이렇습니다「백로그가 있을 때 한번에 플러시 가능 rate_per_min 글」어떠한 경우에도 그럴 수 없습니다。
func TestNotifyTakeTokensKeepsUnusedTokens(t *testing.T) {
	n := &Notifier{buckets: map[int64]*notifyBucket{}}
	now := time.Now()
	// 버킷이 처음에는 가득 찼습니다.（100），이번 라운드만 5 。
	if got := n.takeTokens(1, 100, 5, now); got != 5 {
		t.Fatalf("want=5 정확하게 찍어야함 5 토큰，받았어요 %d", got)
	}
	// 핵심 어설션：나머지는 95 은 여전히 버킷에 있어야 합니다.，빼앗기고 버리는 대신。
	// 시간이 흐르지 않음，보충이 아닌 재고에서만 얻을 수 있는지 확인하십시오.。
	if got := n.takeTokens(1, 100, 95, now); got != 95 {
		t.Fatalf("남은 토큰은 여전히 사용 가능해야 합니다.（기대 95），받았어요 %d——라운드 내내 버킷이 비워졌습니다.", got)
	}
	if got := n.takeTokens(1, 100, 1, now); got != 0 {
		t.Fatalf("물통이 소진되었습니다，이(가) 반환되어야 합니다. 0，받았어요 %d", got)
	}
	// want<=0 토큰이 차감되어서는 안 됩니다.（빈바퀴는 무료）。
	n2 := &Notifier{buckets: map[int64]*notifyBucket{}}
	if got := n2.takeTokens(1, 20, 0, now); got != 0 {
		t.Fatalf("want=0 이(가) 반환되어야 합니다. 0，받았어요 %d", got)
	}
	if got := n2.takeTokens(1, 20, 20, now); got != 20 {
		t.Fatalf("want=0 그 때 토큰을 소비하지 말았어야 했어요，아직 꽉 차 있어야 합니다 20，받았어요 %d", got)
	}
}

// TestDigestTickPlanDecouplesBatchSizeFromSendBudget 요약 모드의 두 가지 차원을 고정합니다.。
//
// 요약 배치의 크기가 각 라운드의 요청 예산과 연결되면，rate_per_min=20 채널은 각 채널에서만 사용할 수 있습니다.
// 3 초 tick 작성 완료 1 토큰，따라서 각 요약 메시지에는 다음 내용만 포함됩니다. 1 취약점——기능적으로 없음과 동일
// 요약，그리고 메시지 헤더에도 다음과 같은 내용이 있습니다.「근처 30 분 추가됨 1 취약점」。이 성능 저하로 인해 오류가 보고되지 않습니다.，
// 기존 End-to-End 사용 사례도 볼 수 없습니다.（그들은 수동으로 제공합니다 stepDigest 충분히 큰 걸 통과하세요 limit，
// 우회됨 step 의 할당량 계산），그럼 여기서 결정 자체를 직접적으로 주장해 보세요.。
func TestDigestTickPlanDecouplesBatchSizeFromSendBudget(t *testing.T) {
	tokens, claimLimit := digestTickPlan()
	// 일괄 = 메시지 = 부탁 하나만 = 토큰。토큰 단위는 메시지입니다.，취약점이 아님。
	if tokens != 1 {
		t.Fatalf("배치를 요약하고 하나의 메시지만 보냅니다.，정확하게 소비해야합니다 1 토큰，받았어요 %d", tokens)
	}
	if claimLimit != db.MaxDigestBatchSize {
		t.Fatalf("요약 배치 크기는 메모리 상한선이어야 합니다. db.MaxDigestBatchSize=%d，받았어요 %d",
			db.MaxDigestBatchSize, claimLimit)
	}
	// 주요 관계：배치 크기는 라운드당 요청 예산보다 훨씬 커야 합니다.。둘의 크기가 같으면，
	// 다시 설명해주세요「메시지 좀 보내주세요」그리고「한 번에 여러 허점 설치」하나의 숫자로 뒤섞여。
	if claimLimit <= notifyMaxSendsPerChannelPerTick {
		t.Fatalf("요약 배치 크기 %d 라운드당 요청 예산이 적용되지 않아야 합니다. %d 제약——"+
			"요청한 예산은 임대 계약에서 역으로 계산됩니다.「여러 가지 요청을 보냅니다」，그리고「한 번에 여러 허점 설치」은 2차원입니다.",
			claimLimit, notifyMaxSendsPerChannelPerTick)
	}
}

// TestNotifyTickBudgetFitsWithinLease 은 또 다른 드리프트 방지 주장입니다.。
//
// 단일 채널의 라운드당 배송 항목 수 상한（notifyMaxSendsPerChannelPerTick）임대 기간을 기준으로 합니다.：
// 한 라운드의 연속 배송에 필요한 최악의 시간은 다음과 같아야 합니다. < 임대，그렇지 않으면 임대가 발행되기 전에 마지막 몇 개의 임대가 만료됩니다.，
// 여러 인스턴스를 배포할 때 피어는 해당 인스턴스를 다시 가져옵니다.、반복보내기。이 세 개의 상수는 서로 다른 위치에 있습니다.，
// 어느 하나를 바꾸면 오류 없이 관계가 깨질 수 있습니다.——그러니 여기에 십자가에 못박으시오。
func TestNotifyTickBudgetFitsWithinLease(t *testing.T) {
	worst := time.Duration(notifyMaxSendsPerChannelPerTick) * notifySendTimeout
	if worst >= notifyLease {
		t.Fatalf("단일 채널에서 라운드의 최악의 시간 소모 %v 임대 계약을 충족하거나 초과해서는 안 됩니다. %v"+
			"（notifyMaxSendsPerChannelPerTick=%d × notifySendTimeout=%v）——"+
			"이 세 가지 상수 중 하나를 변경하는 경우 나머지 두 가지를 동시에 확인해야 합니다.",
			worst, notifyLease, notifyMaxSendsPerChannelPerTick, notifySendTimeout)
	}
}
