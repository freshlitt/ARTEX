package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/notify"
)

// 글로벌 설정 키（존재합니다 settings 키 값 테이블에서，테이블을 생성할 필요가 없습니다.）。
const (
	// settingNotifyEnabled 은 푸시 마스터 스위치입니다.。기본적으로 켜져 있음：유지관리 기간 동안 원클릭으로 출혈을 멈추는데 사용됩니다.，
	// 기능 활성화 조건 대신——실제 활성화 조건은 다음과 같습니다.「유통채널이 있나요?」。
	settingNotifyEnabled = "notify_enabled"
	// settingNotifyPublicBaseURL 은 취약점 세부정보에 대한 링크를 생성하는 외부 액세스 주소입니다.
	// （ https://artex.example.com）。비워두면 메시지에 링크 버튼이 표시되지 않습니다.。
	// 프로젝트에 재사용 가능한 외부 주소 구성이 없습니다.，그래서 새로운 아이템이 나왔습니다。
	settingNotifyPublicBaseURL = "notify_public_base_url"
	// settingNotifyDigestMinutes 은 요약 모드 기간입니다.（분）。
	settingNotifyDigestMinutes = "notify_digest_interval_min"
)

const (
	// notifyTick 은 배달 엔진의 폴링 간격입니다.。3 초는 이 엔진의 실시간 성능의 상한선입니다.，
	// 도요「취약점 로깅」에게「메시지가 도착했습니다 IM」사이의 지연의 주요 원인。
	notifyTick = 3 * time.Second
	// notifyLease 은 배송물 수령 시 임대 기간입니다.。단일 배송 중 최악의 시간보다 훨씬 길어야 합니다.
	// （notify 포함됨 HTTP 클라이언트 시간 초과 15 초），그렇지 않으면 같은 줄이 두 번 나타납니다.
	// dispatcher 동시배송。
	notifyLease = 3 * time.Minute
	// notifyFanOutPerTick 라운드당 전달되는 이벤트 수를 제한합니다.，처음 채널을 활성화할 때는 피하세요.
	// 전체 과거 백로그를 납품 업무로 한번에 확장。
	notifyFanOutPerTick = 200
	// notifyDefaultDigestMinutes 은 요약 기간의 기본값입니다.。
	notifyDefaultDigestMinutes = 30
	// notifyUnlimitedBurstPerTick 은 채널이 현재 제한을 설정하지 않은 경우 각 전달 라운드의 상한입니다.。
	// 존재의 의미는 막는 것이다.「한 채널이 전류 제한 없이 구성되었습니다. + 수천 개의 허점을 한 번에 스캔」넣어보세요
	// 단일 사이클이 오랫동안 차단됩니다.。
	notifyUnlimitedBurstPerTick = 50
	// notifyMaxSendsPerChannelPerTick 단일 채널이 각 라운드에서 전달할 수 있는 최대 항목 수입니다.。
	//
	// 이 상한은 다음에 의해 결정됩니다.**임대 기간**거꾸로：인수할 때 은행에 임대차 계약서를 줬어요.（notifyLease = 3 분），
	// 한 라운드에 연속 배송되는 품목의 개수가 너무 많아 최악의 경우 시간 소모가 임대를 초과하는 경우，임대가 발행되기 전에 마지막 몇 개의 임대가 만료되었습니다.。
	// 단일 프로세스 내에서는 중요하지 않습니다.（Run 싱글이에요 goroutine 직렬 실행，tick 재입장 불가），하지만**둘
	// 라이브러리와 함께 처리**시간，피어는 임대가 만료된 행을 검색하여 다시 보냅니다.，그래도 넣을게요
	// attempts 2배 증가、원래 프로세스가 전달되는 동안 실패한 것으로 판단됩니다.。
	//
	// 값：3 분 임대 / 30 초 단일 시간 초과 = 6 네**방금 임대가 끝났습니다**、마진 없음，
	// 수강불가；받아 5 최악의 상황에도 시간이 걸리도록 놔두세요 150 초를 따로 설정 30 초 여유。이 관계는 다음과 같이 표현됩니다.
	// TestNotifyTickBudgetFitsWithinLease 십자가에 못 박히심——변경 notifyLease、
	// notifySendTimeout 또는 이 값으로 인해 해당 어설션이 실패하게 됩니다.。
	notifyMaxSendsPerChannelPerTick = 5
	// notifySendTimeout 은 단일 전달에 대한 시간 초과입니다.。이전 상수의 값도 결정합니다.，
	// 둘의 곱은 다음을 초과할 수 없습니다. notifyLease，또 만나요 TestNotifyTickBudgetFitsWithinLease。
	notifySendTimeout = 30 * time.Second
)

// notifyBackoff 은 실패한 재시도에 대한 백오프 시퀀스입니다.，아래 첨자는 시도 횟수입니다.。
// 3 기회（처음 포함해서）그리고 db.MaxNotifyAttempts 해당，둘 다 같이 변경해야 합니다.。
var notifyBackoff = []time.Duration{
	time.Second,
	5 * time.Second,
	30 * time.Second,
}

// Notifier 은 취약점 푸시 전달 엔진입니다.。
//
// 그리고 Scheduler 동점，무소속으로 goroutine 달려라（또 만나요 server.New）。의도적으로 재사용하지 않음
// Scheduler 님 tick：푸시에 대한 실시간 요구 사항（3 초）트리거와는 업무리듬이 다릅니다，
// 그리고 둘의 실패는 서로 관련이 없습니다——푸시가 멈춰도 영향을 주지 않습니다. agent 트리거。
type Notifier struct {
	s  *Server
	pg *db.DB

	// mu 보호 buckets。채널 수가 적습니다.、낮은 경쟁률，뮤텍스 잠금이면 충분합니다，
	// 세밀한 구조를 도입하는 것은 가치가 없습니다.。
	mu      sync.Mutex
	buckets map[int64]*notifyBucket
}

// notifyBucket 은 단일 채널 토큰 버킷입니다.。
//
// 대신 토큰 버킷을 사용하세요.「1분마다 카운트 후 삭제됨」용 슬라이딩 창，후자의 경계효과가 심해서 그렇습니다：
// 창 끝에 가득 찼습니다. 20 글、다음번에 다시 보내드리겠습니다 20 글，플랫폼의 경우 1초 이내 40 글，
// 제한됩니다；토큰 버킷은 일정한 비율로 보충됩니다.，그런 비상상황은 당연히 피하세요。
type notifyBucket struct {
	tokens   float64
	lastFill time.Time
}

func newNotifier(s *Server) *Notifier {
	return &Notifier{s: s, pg: s.m.pg, buckets: map[int64]*notifyBucket{}}
}

// Run 루프 종료: ctx 끝。 server.New 한번 시작해 보세요。
func (n *Notifier) Run(ctx context.Context) {
	if n.pg == nil {
		return
	}
	t := time.NewTicker(notifyTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n.step(ctx)
		}
	}
}

// step 한 라운드를 달린다：새 이벤트 먼저 전달，기한 작업을 다시 전달。
//
// 단계가 실패하면 기록만 남깁니다.、루프를 중단하지 마십시오.——알림 시스템 오류가 프로세스 수준 문제로 확대되어서는 안 됩니다.。
// 각 tick 모두 독립이다，다음 라운드에도 자연스럽게 다시 도전해보겠습니다。
func (n *Notifier) step(ctx context.Context) {
	if !n.enabled() {
		return
	}
	if _, _, err := n.pg.FanOutPendingEvents(ctx, notifyFanOutPerTick); err != nil {
		log.Printf("[notify] 이벤트 전달 실패: %v", err)
		return
	}
	channels, err := n.pg.ListNotificationChannels(ctx)
	if err != nil {
		log.Printf("[notify] 채널을 읽지 못했습니다.: %v", err)
		return
	}
	baseURL := n.publicBaseURL()
	for _, ch := range channels {
		if !ch.IsEnabled() {
			continue
		}
		// 토큰 버킷의 측정 단위는**메시지 수**（동등 HTTP 요청횟수），취약점 개수가 아님。
		// 실시간 모드에서는 둘 다 동일합니다.（하나의 취약점, 하나의 메시지）；요약 모드의 전체 취약점 종합 배치
		// 메시지，그래서 하나의 토큰만 소모됩니다.。
		//
		// 두 모드 모두 토큰 버킷을 먼저 요청합니다.、그럼 금액대로 받아가세요——순서는 되돌릴 수 없습니다，그렇지 않으면 전류 제한에 의해 차단됩니다.
		// 배송 재시도 횟수를 소모했습니다.。
		now := time.Now()
		if ch.Mode == db.NotifyModeDigest {
			tokens, claimLimit := digestTickPlan()
			if n.takeTokens(ch.ID, ch.RatePerMin, tokens, now) <= 0 {
				continue
			}
			n.stepDigest(ctx, ch, claimLimit, baseURL)
			continue
		}
		allow := n.takeTokens(ch.ID, ch.RatePerMin, notifyMaxSendsPerChannelPerTick, now)
		if allow <= 0 {
			continue
		}
		n.stepRealtime(ctx, ch, allow, baseURL)
	}
}

// digestTickPlan 요약 채널의 현재 라운드의 토큰 소비 및 배치 크기 상한을 반환합니다.。
//
// 두 개의 반환 값은 다음과 같습니다.**두 개의 다른 차원**，독립된 기능인 이유가 바로 이것이다.：
//
//   - tokens 은 메시지 수입니다.。하나의 메시지로 결합된 여러 취약점、한 번만 보내주세요 HTTP 요청，소헝웨이 1。
//     rate_per_min 그럼 아직도 그렇군요 digest 유효（분당 요약 메시지 최대 개수）。
//   - claimLimit 이 배치에 설치된 최대 취약점 수입니다.。메모리 상한에만 적용됩니다.，예산 요구와 관련 없음。
//
// 일단 놔두기 위해서 rate_per_min 예 digest 유효，라운드별로 요청예산을 넣어주세요
// （notifyMaxSendsPerChannelPerTick，임대차에서 차감）배치 크기로 직접 전달합니다.。
// 결과는 다음과 같습니다. rate_per_min=20 님의 채널은 다음과 같습니다. 3 초 tick 추가된 내용만 있음 1 토큰，그럼 각각의 요약은
// 메시지만 설치됩니다. 1 취약점——digest 로 변질된다「요약본 포함 실시간 푸시」，독자는 다음과 같은 문자열을 받습니다.
// 「근처 30 분 추가됨 1 취약점」，그리고 db.MaxDigestBatchSize 연락 불가。
//
// 엔드투엔드 테스트에서는 이런 증상을 찾기가 쉽지 않습니다.（기존 사용 사례는 수동으로 충분히 큰 값을 전달합니다. limit 주다
// stepDigest，우회됨 step 의 할당량 계산），그래서 결정은 여기에 포함됩니다
// TestDigestTickPlanDecouplesBatchSizeFromSendBudget 직접 핀으로 고정。
func digestTickPlan() (tokens, claimLimit int) {
	return 1, db.MaxDigestBatchSize
}

// stepRealtime 특정 채널로부터 실시간 작업을 받고 전달합니다.，하나의 취약점, 하나의 메시지。
func (n *Notifier) stepRealtime(ctx context.Context, ch *db.NotificationChannel, allow int, baseURL string) {
	deliveries, err := n.pg.ClaimRealtimeDeliveries(ctx, ch.ID, allow, notifyLease)
	if err != nil {
		log.Printf("[notify] 실시간 배송을 받지 못했습니다. channel=%d: %v", ch.ID, err)
		return
	}
	if len(deliveries) == 0 {
		return
	}
	channel, cfg, ok := n.adapt(ch)
	if !ok {
		_ = n.pg.FailDeliveries(ctx, deliveryIDs(deliveries), fmt.Sprintf("채널 유형 %q 등록되지 않음", ch.Kind))
		return
	}
	for _, dl := range deliveries {
		msg, err := n.renderSingle(ctx, dl, baseURL)
		if err != nil {
			// 로컬 데이터 문제로 인해 렌더링이 실패했습니다.，다시 시도해도 나아지지 않을 것 같아요。
			_ = n.pg.FailDeliveries(ctx, []int64{dl.ID}, err.Error())
			continue
		}
		n.send(ctx, channel, cfg, msg, []*db.NotificationDelivery{dl})
	}
}

// stepDigest 특정 채널에서 보류 중인 전달을 하나의 메시지로 집계하고 배치가 만료되면 보냅니다.。
func (n *Notifier) stepDigest(ctx context.Context, ch *db.NotificationChannel, allow int, baseURL string) {
	window := n.digestInterval()
	due, err := n.pg.DigestBatchDue(ctx, ch.ID, window)
	if err != nil {
		log.Printf("[notify] 요약 배치 심사 실패 channel=%d: %v", ch.ID, err)
		return
	}
	if !due {
		return
	}
	deliveries, err := n.pg.ClaimDigestBatch(ctx, ch.ID, allow, notifyLease)
	if err != nil {
		log.Printf("[notify] 요약 배치를 받지 못했습니다. channel=%d: %v", ch.ID, err)
		return
	}
	if len(deliveries) == 0 {
		return
	}
	channel, cfg, ok := n.adapt(ch)
	if !ok {
		_ = n.pg.FailDeliveries(ctx, deliveryIDs(deliveries), fmt.Sprintf("채널 유형 %q 등록되지 않음", ch.Kind))
		return
	}
	msg, included, err := n.renderBatch(ctx, deliveries, baseURL, int(window.Minutes()))
	if err != nil {
		_ = n.pg.FailDeliveries(ctx, deliveryIDs(deliveries), err.Error())
		return
	}
	// 스냅샷이 깨졌습니다.、메시지 수신에 실패한 배달은 명시적으로 실패해야 합니다.。그렇지 않으면 그대로 유지됩니다.
	// included 외부、메시지도 실패 목록도 입력되지 않았습니다.——성공적으로 전송되면 상태는 다음과 같습니다.
	// 이후의 일괄 태그가 누락되었습니다.，언제나 멈춰라 sending 임대가 만료되고 반복적으로 청구될 때까지。
	if skipped := excludeDeliveries(deliveries, included); len(skipped) > 0 {
		reason := "이벤트 스냅샷을 구문 분석할 수 없습니다.，이 취약점은 메시지로 렌더링될 수 없습니다."
		if fErr := n.pg.FailDeliveries(ctx, deliveryIDs(skipped), reason); fErr != nil {
			log.Printf("[notify] 잘못된 스냅샷 전송 실패로 표시 channel=%s ids=%v: %v", ch.Kind, deliveryIDs(skipped), fErr)
		}
		log.Printf("[notify] 건너뛰기 %d 스냅샷을 분석하여 전달할 수 없습니다. channel=%d", len(skipped), ch.ID)
	}
	// 메시지 받은 사람만 건네주세요 send：included[i] 그리고 msg.Items[i] 엄격한 대응，
	// send 이 상응관계에 의지하라「이전에 설치된 채널 보고서 K 글」올바른 배송 라인에 도착했습니다。
	n.send(ctx, channel, cfg, msg, included)
}

// send 제출 및 결과에 따라 상태 이관。
//
// 동일한 배치로 배송됨（요약 모드에는 수십 개의 항목이 있을 수 있습니다.）전송 결과 공유：또는 배달、또는 일괄적으로 다시 시도하세요.。
// 항목별로 재시도하지 마세요——요약 메시지는 하나입니다.，일부를 다시 보내면 배치 의미가 혼동됩니다.。
//
// 유일한 예외는**채널 길이 상한으로 인한 분할**：채널 보고서에는 실제로 전자만 포함되어 있습니다. K 글，
// 그럼 먼저 K+1 은 다음 배치를 위해 저장되어야 합니다.，과 함께 성공적으로 표시되는 대신。그렇지 않으면 잘립니다.
// 해당 취약점은 뉴스에 나오지 않습니다.、도 실패 목록에 없습니다.，완전히 사라졌어요。
func (n *Notifier) send(ctx context.Context, channel notify.Channel, cfg map[string]any, msg notify.Message, deliveries []*db.NotificationDelivery) {
	// 단일배송에는 상한선이 있습니다，이번 라운드에서는 특정 채널이 막혀 나머지 모든 채널이 아래로 끌리는 현상을 방지합니다.。
	sendCtx, cancel := context.WithTimeout(ctx, notifySendTimeout)
	defer cancel()
	delivered, err := channel.Send(sendCtx, cfg, msg)
	if err == nil && delivered > 0 {
		if delivered > len(deliveries) {
			// 채널에서 신고하는 아이템 개수는 배송 개수를 초과할 수 없습니다.；실제로 그런 일이 발생했다면 렌더링 레이어가 잘못 계산했다는 의미입니다.，
			// 모두보내기를 누르고 이슈를 적어주세요，기록을 망치는 것보다는 낫지。
			log.Printf("[notify] 전달된 채널 보고서 수 %d 배송횟수를 초과했습니다. %d channel=%s，모두 배달된 것으로 처리",
				delivered, len(deliveries), channel.Kind())
			delivered = len(deliveries)
		}
		sent, rest := deliveries[:delivered], deliveries[delivered:]
		if err := n.pg.MarkDeliveriesSent(ctx, deliveryIDs(sent)); err != nil {
			log.Printf("[notify] 표시 전달 실패 channel=%s ids=%v: %v", channel.Kind(), deliveryIDs(sent), err)
		}
		if len(rest) > 0 {
			// 이 메시지는 채널의 최대 길이에 도달했습니다.：나머지는 바로 팀으로 복귀하겠습니다，다음으로 tick 갱신。
			// 사용 DeferDeliveries 대신 RescheduleDeliveries —— 이건 실패가 아니다，
			// 재시도 예산을 소모해서는 안 됩니다.（받았을 때 이미 낙관적이었습니다. +1 ，거기서는 줄어들겠죠）。
			if err := n.pg.DeferDeliveries(ctx, deliveryIDs(rest),
				fmt.Sprintf("이 메시지는 채널의 최대 길이에 도달했습니다.，배송전까지만 %d 글，나머지는 다음 배치를 위해 예약됩니다", delivered)); err != nil {
				log.Printf("[notify] 분할 갱신 대기열에 실패했습니다. channel=%s ids=%v: %v", channel.Kind(), deliveryIDs(rest), err)
			}
		}
		return
	}
	if err == nil {
		// 채널에서 오류를 보고하지 않았고 메시지가 몇 개 전달되었는지도 알려주지 않았습니다.。실패로 처리（물러가라），
		// 이 배달이 반복적으로 수신되지만 표시되지 않는 것을 방지합니다.。
		err = fmt.Errorf("채널에서 배송된 상품 개수를 보고하지 않았습니다.（delivered=%d）", delivered)
	}

	// 실패 처리**하나씩**결정，전체 배치의 최대 시도 횟수로 판단하는 대신。
	//
	//  `if maxAttempts(deliveries) >= MaxNotifyAttempts` 사형을 선고받다，하지만 배치에서는
	// 항목별 시도 횟수가 동일하지 않습니다.：두 번 재시도된 이전 배달（attempts=2）은 같은 배치에 넣습니다.
	// 새제품 배송（attempts=1）함께 드래그하세요 failed——새로운 취약점은 단 한번의 재시도 없이 영원히 사라집니다.，
	// 그리고「기존 산업이 신산업을 물속으로 끌고 들어가게 두지 마세요.」원래 의도는 정반대。
	permanent := notify.IsPermanent(err)
	var failIDs, exhaustedIDs []int64
	byDelay := map[time.Duration][]int64{}
	for _, dl := range deliveries {
		switch {
		case permanent:
			failIDs = append(failIDs, dl.ID)
		case dl.Attempts >= db.MaxNotifyAttempts:
			exhaustedIDs = append(exhaustedIDs, dl.ID)
		default:
			delay := notifyBackoff[min(dl.Attempts, len(notifyBackoff)-1)]
			byDelay[delay] = append(byDelay[delay], dl.ID)
		}
	}

	if len(failIDs) > 0 {
		if fErr := n.pg.FailDeliveries(ctx, failIDs, err.Error()); fErr != nil {
			log.Printf("[notify] 표시 실패 상태 오류 channel=%s ids=%v: %v", channel.Kind(), failIDs, fErr)
		}
	}
	if len(exhaustedIDs) > 0 {
		reason := fmt.Sprintf("다시 시도해보세요 %d 번 후에도 여전히 실패함: %s", db.MaxNotifyAttempts, err)
		if fErr := n.pg.FailDeliveries(ctx, exhaustedIDs, reason); fErr != nil {
			log.Printf("[notify] 표시 실패 상태 오류 channel=%s ids=%v: %v", channel.Kind(), exhaustedIDs, fErr)
		}
	}
	// 딜레이 그룹별로 재배열：만 3 파일 퇴각，당연히 그룹 수가 매우 적습니다.，각각의 메시지를 따로 보내지 않아도 됩니다.
	// UPDATE（그러면 되겠군요. 500 항목 일괄 생성 500 왕복）。
	for delay, group := range byDelay {
		if rErr := n.pg.RescheduleDeliveries(ctx, group, delay, err.Error()); rErr != nil {
			log.Printf("[notify] 재정렬 전달 실패 channel=%s ids=%v: %v", channel.Kind(), group, rErr)
		}
	}
	if len(failIDs)+len(exhaustedIDs) > 0 {
		log.Printf("[notify] 배송실패 channel=%d kind=%s 영구 장애=%d 재시도 횟수가 부족함=%d 다시 시도해야 함=%d: %s",
			deliveries[0].ChannelID, channel.Kind(), len(failIDs), len(exhaustedIDs), len(byDelay), err)
	}
}

// excludeDeliveries 복귀 all 은 여기 없습니다 keep 에 계신 분들（포인터 동일성으로 비교）。
// 은 알아내는 데 사용됩니다.「메시지 수신 실패」으로 배송——명시적으로 폐기해야 합니다.，회색지대에 머물 수는 없다。
func excludeDeliveries(all, keep []*db.NotificationDelivery) []*db.NotificationDelivery {
	inKeep := make(map[*db.NotificationDelivery]bool, len(keep))
	for _, dl := range keep {
		inKeep[dl] = true
	}
	var out []*db.NotificationDelivery
	for _, dl := range all {
		if !inKeep[dl] {
			out = append(out, dl)
		}
	}
	return out
}

// adapt 채널 구현을 가져오고 해당 구성을 구문 분석합니다.。
// 복귀 ok=false 은 유형이 등록되지 않았음을 의미합니다.，배송은 무한 재시도가 아닌 직접 실패로 판단해야 합니다.。
func (n *Notifier) adapt(ch *db.NotificationChannel) (notify.Channel, map[string]any, bool) {
	channel, ok := notify.Get(ch.Kind)
	if !ok {
		return nil, nil, false
	}
	var cfg map[string]any
	if len(ch.Config) > 0 {
		// 구성 구문 분석이 실패하면 빈 값을 제공합니다. map：채널 자체 Validate 보고하겠습니다「어떤 필드가 누락되었나요?」，
		// 그 오류는 것보다 낫습니다. JSON 구문 분석 오류는 사용자가 오류를 수정하도록 더 잘 안내할 수 있습니다.。
		_ = json.Unmarshal(ch.Config, &cfg)
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	return channel, cfg, true
}

// renderSingle 단일 취약점 메시지 렌더링。
func (n *Notifier) renderSingle(ctx context.Context, dl *db.NotificationDelivery, baseURL string) (notify.Message, error) {
	snap, err := parseSnapshot(dl)
	if err != nil {
		return notify.Message{}, err
	}
	item, err := n.itemFor(ctx, snap, baseURL)
	if err != nil {
		return notify.Message{}, err
	}
	return notify.Message{Items: []notify.Item{item}, HomeURL: baseURL}, nil
}

// renderBatch 렌더링 요약 메시지。스냅샷을 하나씩 분석해 보세요——한 줄이 깨지면 그 줄은 건너뛰시면 됩니다.，
// 전체 배치 요약을 끌어내리지 마십시오.。
//
// 반환 값 included 그리고 msg.Items **엄격한 일대일 대응**（아니요. i 배송 ↔ 아니요. i 항목）。
// 이 서신은 어려운 요구 사항입니다.：발신자 누르기「이전에 설치된 채널 보고서 K 글」결정하기 전에 K 배송
// 마크 전달됨。여기에서는 잘못된 스냅샷을 건너뛰었지만 건너뛴 전달은 건너뛰지 않은 경우 included 에서 삭제됨，
// 아래 첨자가 잘못되었습니다.——실패했어야 하는 잘못된 항목이 전달된 것으로 표시됩니다.，그리고 좋은 물건이 미배송으로 잘못 판단되어。
// 깨진 것들은 호출자에 의해 명시적으로 실패로 표시됩니다.，또 만나요 stepDigest。
func (n *Notifier) renderBatch(ctx context.Context, deliveries []*db.NotificationDelivery, baseURL string, windowMinutes int) (notify.Message, []*db.NotificationDelivery, error) {
	items := make([]notify.Item, 0, len(deliveries))
	included := make([]*db.NotificationDelivery, 0, len(deliveries))
	for _, dl := range deliveries {
		snap, err := parseSnapshot(dl)
		if err != nil {
			// 잘못된 스냅샷은 메시지가 들어가지 않습니다.，둘다 들어가지 않음 included——폐기는 발신자의 책임입니다
			// （명시적 태그 실패，섞는 대신「배달됨」혼란스러워）。
			log.Printf("[notify] 요약 배치에서 해결되지 않은 스냅샷 건너뛰기 delivery=%d: %v", dl.ID, err)
			continue
		}
		item, err := n.itemFor(ctx, snap, baseURL)
		if err != nil {
			return notify.Message{}, nil, err
		}
		items = append(items, item)
		included = append(included, dl)
	}
	if len(items) == 0 {
		return notify.Message{}, nil, fmt.Errorf("배치 요약 %d 모든 제출 내용을 구문 분석할 수 없습니다.", len(deliveries))
	}
	return notify.Message{
		Items:         items,
		Batch:         true,
		WindowMinutes: windowMinutes,
		HomeURL:       baseURL,
	}, included, nil
}

// itemFor 이벤트 스냅샷을 푸시할 항목으로 렌더링，실수로 자산 이름과 세부 정보를 링크로 다시 구문 분석합니다.。
func (n *Notifier) itemFor(ctx context.Context, snap notify.Snapshot, baseURL string) (notify.Item, error) {
	assets, err := n.pg.NotificationAssetNames(ctx, snap.AssetIDs)
	if err != nil {
		// 자산 이름 확인 실패로 인해 푸시가 방해되어서는 안 됩니다.：이름을 읽을 수 없는 것이 알림을 받지 못하는 것보다 훨씬 쉽습니다.，
		// 메시지에 자산 한 줄이 누락되었습니다.。
		log.Printf("[notify] 자산 이름을 구문 분석하지 못했습니다. finding=%d: %v", snap.FindingID, err)
	}
	item := notify.Item{
		FindingID:  snap.FindingID,
		Name:       snap.Name,
		VulnClass:  snap.VulnClass,
		Severity:   snap.Severity,
		Summary:    snap.Summary,
		Assets:     assets,
		FromStatus: snap.FromStatus,
		ToStatus:   snap.ToStatus,
	}
	if baseURL != "" {
		// 상세페이지 경로 보기 web/src/app/(main)/function/findings/detail/page.tsx，
		// 시작합니다 query 매개변수 id 취약점 읽기 id。
		item.DetailURL = fmt.Sprintf("%s/function/findings/detail?id=%d", baseURL, snap.FindingID)
	}
	return item, nil
}

// takeTokens 채널 토큰 버킷에서 가져옵니다.**대부분 want **토큰，실제 가져온 수량을 반환합니다.。
//
// 토큰 = 메시지（한번 HTTP 요청）。실시간 모드에서는 발신자가 원하는 만큼 메시지를 보낼 수 있습니다.；
// 요약 모드에서는 전체 취약점 배치에 대해 하나의 메시지만 전송됩니다.，합격 1。
//
// 버킷 용량은 채널의 분당 상한입니다.，일정한 속도로 보충。ratePerMin<=0 은 전류 제한이 없음을 의미합니다.，
// 제한적이지만 충분히 큰 값을 반환합니다.，무한 백로그로 인해 단일 사이클이 지연되는 것을 방지합니다.。
//
// want 이 상한값은 필수입니다.：없으면 버킷 전체를 비울 수 밖에 없습니다.，그리고 호출자 자신도 라운드당 상한선이 있습니다.，
// 추가 토큰은 사용할 수 없습니다.、은 다음 보충 전에 다시 허공에서 사라집니다.——저장된 버스트 용량에는 절대 도달하지 않습니다.，
// 리안「이번 라운드에는 보류 중인 배송이 없습니다.」금액을 차감하겠습니다.。
func (n *Notifier) takeTokens(channelID int64, ratePerMin, want int, now time.Time) int {
	if want <= 0 {
		return 0
	}
	if ratePerMin <= 0 {
		return min(want, notifyUnlimitedBurstPerTick)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	b := n.buckets[channelID]
	if b == nil {
		b = &notifyBucket{tokens: float64(ratePerMin), lastFill: now}
		n.buckets[channelID] = b
	}
	// 실시간 경과에 따라 보충，요금은 ratePerMin/60 초당。
	if elapsed := now.Sub(b.lastFill).Seconds(); elapsed > 0 {
		b.tokens = minF(float64(ratePerMin), b.tokens+elapsed*float64(ratePerMin)/60)
		b.lastFill = now
	}
	// 최소값을 추가하세요 epsilon 다시 모아보세요：토큰의 개수는 부동소수점 단위로 누적됩니다.，2회에 걸쳐 채워지면
	// 0.5 + 0.5 얻을 수 있습니다 0.9999999999，직접 int() 잘려집니다 0——
	// 버킷이 수학적으로 가득 차 있지만 토큰을 꺼낼 수 없습니다.。1e-9 토큰보다 훨씬 작습니다.，진짜 빚은 버리지 않겠다。
	take := min(int(b.tokens+1e-9), want)
	if take <= 0 {
		return 0
	}
	b.tokens -= float64(take)
	return take
}

// enabled 메인 스위치 읽기。
func (n *Notifier) enabled() bool {
	return n.pg.GetBool(settingNotifyEnabled, true)
}

// publicBaseURL 다시 연결하는 데 사용되는 외부 주소를 반환합니다.，후행 슬래시를 제거하세요.。
func (n *Notifier) publicBaseURL() string {
	v, ok, err := n.pg.GetSetting(settingNotifyPublicBaseURL)
	if err != nil || !ok {
		return ""
	}
	return trimTrailingSlash(v)
}

// digestInterval 요약기간으로 돌아가기，불법이거나 구성되지 않은 경우 기본값으로 폴백。
func (n *Notifier) digestInterval() time.Duration {
	v, ok, err := n.pg.GetSetting(settingNotifyDigestMinutes)
	if err != nil || !ok {
		return time.Duration(notifyDefaultDigestMinutes) * time.Minute
	}
	m := 0
	if _, err := fmt.Sscanf(v, "%d", &m); err != nil || m <= 0 {
		return time.Duration(notifyDefaultDigestMinutes) * time.Minute
	}
	return time.Duration(m) * time.Minute
}

// parseSnapshot 해당 이벤트의 스냅샷을 구문 분석하여 전달합니다.。
func parseSnapshot(dl *db.NotificationDelivery) (notify.Snapshot, error) {
	var snap notify.Snapshot
	if len(dl.Snapshot) == 0 {
		return snap, fmt.Errorf("배송 %d 의 이벤트 스냅샷이 비어 있습니다.", dl.ID)
	}
	if err := json.Unmarshal(dl.Snapshot, &snap); err != nil {
		return snap, fmt.Errorf("분석 및 전달 %d 의 이벤트 스냅샷이 실패했습니다.: %w", dl.ID, err)
	}
	if snap.Kind == "" {
		// 이벤트 유형은 이벤트 동작에 따라 달라집니다.，스냅샷의 내용은 이전 버전에서 작성한 것일 수 있습니다.。
		snap.Kind = dl.EventKind
	}
	return snap, nil
}

func deliveryIDs(deliveries []*db.NotificationDelivery) []int64 {
	out := make([]int64, 0, len(deliveries))
	for _, dl := range deliveries {
		out = append(out, dl.ID)
	}
	return out
}

func trimTrailingSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
