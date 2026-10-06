package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/notify"
)

// 이 파일은 푸시 기능입니다 HTTP 인터페이스。모든 경로가 중단되었습니다. requireAuth 이후（또 만나요 Handler()），
// 다른 관리 인터페이스와 일관성。

// notifyChannelDTO 은 채널의 외부 표현입니다.。
//
// Config 네**마스킹 후**구성：자격 증명 필드는 다음으로 대체됩니다. notify.MaskedPrefix 으로 시작하는 값。
// 프런트 엔드에서는 마스크 값을 그대로 다시 제출합니다.「이 필드는 변경되지 않았습니다.」，서버는 그에 따라 원래 값을 라이브러리에 유지합니다.
// （또 만나요 notify.MergeConfig）。
type notifyChannelDTO struct {
	ID         int64          `json:"id"`
	Name       string         `json:"name"`
	Kind       string         `json:"kind"`
	Enabled    bool           `json:"enabled"`
	Mode       string         `json:"mode"`
	Config     map[string]any `json:"config"`
	Filter     notify.Filter  `json:"filter"`
	RatePerMin int            `json:"rate_per_min"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	// SecretKeys 어떤 필드가 자격 증명인지 프런트 엔드에 알려줍니다.，이를 바탕으로 비밀번호 상자와「공백으로 두고 변경하지 마십시오.」의 팁。
	// 채널 자체에서 선언（notify.Channel.SecretKeys），프런트엔드는 채널 지식을 하드코딩하지 않습니다.。
	SecretKeys []string `json:"secret_keys"`
}

// notifyDeliveryDTO 은 배송 내역의 외부 표현입니다.。
type notifyDeliveryDTO struct {
	ID          int64      `json:"id"`
	FindingID   int64      `json:"finding_id,string"`
	EventKind   string     `json:"event_kind"`
	ChannelID   int64      `json:"channel_id"`
	ChannelName string     `json:"channel_name"`
	ChannelKind string     `json:"channel_kind"`
	State       string     `json:"state"`
	Attempts    int        `json:"attempts"`
	LastError   string     `json:"last_error"`
	BatchID     *int64     `json:"batch_id,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	SentAt      *time.Time `json:"sent_at,omitempty"`
	NextAttempt time.Time  `json:"next_attempt_at"`
	// 메시지 제목 요약，히스토리 목록을 펼치지 않고도 이 트윗이 무엇인지 살펴보자。
	Title    string `json:"title"`
	Severity string `json:"severity"`
}

func toNotifyChannelDTO(ch *db.NotificationChannel) notifyChannelDTO {
	var cfg map[string]any
	if len(ch.Config) > 0 {
		_ = json.Unmarshal(ch.Config, &cfg)
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	secrets := []string{}
	if c, ok := notify.Get(ch.Kind); ok {
		secrets = c.SecretKeys()
	}
	return notifyChannelDTO{
		ID:         ch.ID,
		Name:       ch.Name,
		Kind:       ch.Kind,
		Enabled:    ch.IsEnabled(),
		Mode:       ch.Mode,
		Config:     notify.MaskConfig(ch.Kind, cfg),
		Filter:     notify.ParseFilter(ch.Filter),
		RatePerMin: ch.RatePerMin,
		CreatedAt:  ch.CreatedAt,
		UpdatedAt:  ch.UpdatedAt,
		SecretKeys: secrets,
	}
}

func toNotifyDeliveryDTO(dl *db.NotificationDelivery) notifyDeliveryDTO {
	snap, _ := parseSnapshot(dl)
	dto := notifyDeliveryDTO{
		ID:          dl.ID,
		FindingID:   dl.FindingID,
		EventKind:   dl.EventKind,
		ChannelID:   dl.ChannelID,
		ChannelName: dl.ChannelName,
		ChannelKind: dl.ChannelKind,
		State:       dl.State,
		Attempts:    dl.Attempts,
		LastError:   dl.LastError,
		BatchID:     dl.BatchID,
		CreatedAt:   dl.CreatedAt,
		SentAt:      dl.SentAt,
		NextAttempt: dl.NextAttemptAt,
		Severity:    snap.Severity,
	}
	if snap.Name != "" {
		dto.Title = snap.Name
	} else {
		dto.Title = snap.VulnClass
	}
	return dto
}

// notifyMeta 알림 페이지에 필요한 정적 메타데이터 및 전역 설정을 반환합니다.，한 번의 요청으로 모든 것을 얻을 수 있습니다，
// 드롭다운 상자를 렌더링하기 위해 프런트 엔드에서 3개의 요청을 보내지 않도록 합니다.。
func (s *Server) notifyMeta(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	kinds := make([]map[string]any, 0, len(notify.Kinds()))
	for _, k := range notify.Kinds() {
		ch, _ := notify.Get(k)
		kinds = append(kinds, map[string]any{
			"kind":                 k,
			"default_rate_per_min": ch.DefaultRatePerMin(),
			"secret_keys":          ch.SecretKeys(),
		})
	}
	baseURL, _, _ := pg.GetSetting(settingNotifyPublicBaseURL)
	digest, _, _ := pg.GetSetting(settingNotifyDigestMinutes)
	stats, err := pg.NotificationStatsSnapshot(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"kinds":               kinds,
		"enabled":             pg.GetBool(settingNotifyEnabled, true),
		"public_base_url":     baseURL,
		"digest_interval_min": digest,
		"defaults": map[string]any{
			"digest_interval_min": notifyDefaultDigestMinutes,
		},
		"stats": stats,
	})
}

func (s *Server) notifyListChannels(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	channels, err := pg.ListNotificationChannels(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := make([]notifyChannelDTO, 0, len(channels))
	for _, ch := range channels {
		out = append(out, toNotifyChannelDTO(ch))
	}
	writeJSON(w, 200, map[string]any{"channels": out})
}

// notifyChannelRequest 은 새로운/채널 요청 본문 업데이트。
//
// 모든 비즈니스 분야는 포인터를 사용합니다.，구별하기「불합격」그리고「0 값이 전달되었습니다.」：PATCH 의미에 따라，
// 전달되지 않은 필드는 라이브러리에서 원래 값을 유지해야 합니다.。
type notifyChannelRequest struct {
	Name       *string        `json:"name"`
	Kind       *string        `json:"kind"`
	Enabled    *bool          `json:"enabled"`
	Mode       *string        `json:"mode"`
	Config     map[string]any `json:"config"`
	Filter     *notify.Filter `json:"filter"`
	RatePerMin *int           `json:"rate_per_min"`
}

func (s *Server) notifyCreateChannel(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	var req notifyChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "요청 본문이 잘못되었습니다. JSON: "+err.Error())
		return
	}
	if req.Kind == nil || !notify.ValidKind(*req.Kind) {
		writeErr(w, 400, fmt.Sprintf("잘못된 채널 유형，선택사항：%s", strings.Join(notify.Kinds(), " / ")))
		return
	}
	name := ""
	if req.Name != nil {
		name = strings.TrimSpace(*req.Name)
	}
	if name == "" {
		writeErr(w, 400, "채널 이름이 없습니다.")
		return
	}
	channel, _ := notify.Get(*req.Kind)
	if err := channel.Validate(req.Config); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ch := &db.NotificationChannel{
		Name:       name,
		Kind:       *req.Kind,
		Enabled:    req.Enabled,
		Mode:       db.NotifyModeRealtime,
		RatePerMin: channel.DefaultRatePerMin(),
	}
	if req.Mode != nil {
		if !db.ValidNotifyMode(*req.Mode) {
			writeErr(w, 400, "푸시 모드가 잘못되었습니다.，선택사항：realtime / digest")
			return
		}
		ch.Mode = *req.Mode
	}
	if req.RatePerMin != nil {
		// 값을 명시적으로 지정하면 해당 값이 사용됩니다.——포함 0，그런 뜻이에요「전류 제한 없음」，은 합법적인 구성입니다.。
		if *req.RatePerMin < 0 {
			writeErr(w, 400, "전류 제한 값은 음수일 수 없습니다.")
			return
		}
		ch.RatePerMin = *req.RatePerMin
	}
	// 만「필드 기본값」채널 기본값만 적용。기본값은 여기가 아닌 여기에서 결정해야 합니다. db 레이어：
	// 신체적인 구별능력만을 요구함「이 필드는 전달되지 않습니다.」그리고「명시적으로 통과됨 0」，그리고 둘은 전혀 다른 의미를 가지고 있습니다.
	// （전자=기본값 사용，후자=전류 제한 없음）。db 레이어 핸들 0 또한 불특정으로 처리됩니다.，은 무제한 구성에 접근할 수 없게 만듭니다.。
	if req.RatePerMin == nil {
		ch.RatePerMin = channel.DefaultRatePerMin()
	}
	if req.Filter != nil {
		// 쓰기 시 제한된 값으로 필터 필드를 확인하세요.（ min_severity）。자세히 보기 notify.Filter.Validate：
		// 임계값에 오타가 있으면 필터가 자동으로 실패하고 전체 푸시가 됩니다.，입구는 막아야함。
		if err := req.Filter.Validate(); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		raw, _ := json.Marshal(req.Filter)
		ch.Filter = raw
	}
	rawCfg, _ := json.Marshal(req.Config)
	ch.Config = rawCfg

	id, err := pg.SaveNotificationChannel(r.Context(), ch)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"id": id})
}

func (s *Server) notifyUpdateChannel(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "채널 id 유효하지 않음")
		return
	}
	current, err := pg.NotificationChannelByID(r.Context(), id)
	if err != nil {
		notifyChannelLookupErr(w, err)
		return
	}
	var req notifyChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "요청 본문이 잘못되었습니다. JSON: "+err.Error())
		return
	}

	// kind 수정 허용，그러나 유형을 변경한다는 것은 전체 자격 증명 필드를 바꾸는 것을 의미합니다.，이전 구성과 병합할 수 없습니다.。
	kind := current.Kind
	if req.Kind != nil {
		if !notify.ValidKind(*req.Kind) {
			writeErr(w, 400, fmt.Sprintf("잘못된 채널 유형，선택사항：%s", strings.Join(notify.Kinds(), " / ")))
			return
		}
		kind = *req.Kind
	}
	channel, _ := notify.Get(kind)

	var stored map[string]any
	if kind == current.Kind {
		if len(current.Config) > 0 {
			_ = json.Unmarshal(current.Config, &stored)
		}
	}
	if stored == nil {
		stored = map[string]any{}
	}
	// 사용 PrepareConfigUpdate 알몸 대신 MergeConfig：대상 주소가 변경되면 운영자는 다음을 수행해야 합니다.
	// 자격 증명 필드를 다시 지정하세요.，그렇지 않으면「주소만 바꾸세요、자격 증명 상속」은 도서관의 실제 자격 증명을 새 주소로 보냅니다.。
	merged, err := notify.PrepareConfigUpdate(kind, stored, req.Config)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := channel.Validate(merged); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	rawCfg, _ := json.Marshal(merged)

	ch := &db.NotificationChannel{
		ID:         id,
		Name:       current.Name,
		Kind:       kind,
		Enabled:    current.Enabled,
		Mode:       current.Mode,
		Config:     rawCfg,
		Filter:     current.Filter,
		RatePerMin: current.RatePerMin,
	}
	if req.Name != nil {
		if ch.Name = strings.TrimSpace(*req.Name); ch.Name == "" {
			writeErr(w, 400, "채널 이름은 비워둘 수 없습니다.")
			return
		}
	}
	if req.Enabled != nil {
		ch.Enabled = req.Enabled
	}
	if req.Mode != nil {
		if !db.ValidNotifyMode(*req.Mode) {
			writeErr(w, 400, "푸시 모드가 잘못되었습니다.，선택사항：realtime / digest")
			return
		}
		ch.Mode = *req.Mode
	}
	if req.RatePerMin != nil {
		if *req.RatePerMin < 0 {
			writeErr(w, 400, "전류 제한 값은 음수일 수 없습니다.")
			return
		}
		ch.RatePerMin = *req.RatePerMin
	}
	if req.Filter != nil {
		if err := req.Filter.Validate(); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		raw, _ := json.Marshal(req.Filter)
		ch.Filter = raw
	}

	// 가자 SetNotificationChannelEnabled 대신 SaveNotificationChannel 경로，
	// 는「비활성화」동시에 배송대기 재고는 다음과 같이 표시하세요. skipped，다시 활성화할 때 수신을 피하세요.
	// 오래된 백로그 메시지 일괄 처리。
	enabledChanged := ch.Enabled != nil && current.Enabled != nil && *ch.Enabled != *current.Enabled
	if enabledChanged {
		// 먼저 구성을 업데이트하고 데이터베이스에 로그인하세요.（이때 enabled 이전 값 사용，건너뛰기 논리를 미리 트리거하지 마세요.），
		// 그럼 따로 스위치를 꺼주세요。두 단계 사이에 동시성 창이 없습니다.：이 인터페이스는 이 두 필드를 변경할 수 있는 유일한 입구입니다.。
		prev := ch.Enabled
		ch.Enabled = current.Enabled
		if _, err := pg.SaveNotificationChannel(r.Context(), ch); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		if err := pg.SetNotificationChannelEnabled(r.Context(), id, *prev); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]any{"id": id})
		return
	}
	if _, err := pg.SaveNotificationChannel(r.Context(), ch); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"id": id})
}

func (s *Server) notifyDeleteChannel(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "채널 id 유효하지 않음")
		return
	}
	if err := pg.DeleteNotificationChannel(r.Context(), id); err != nil {
		notifyChannelLookupErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// notifyTestChannel 현재 저장된 구성을 사용하여 테스트 메시지를 보냅니다.。
//
// 채널에 직접 전화하기 Send 배달 대기열을 거치지 않고：테스트의 목적은 사용자에게 즉시 알리는 것입니다.「이 구성이 가능할까요?
// 보내주세요」，큐잉은 배송내역에서 결과를 숨기게 됩니다，사용자는 이를 다시 거쳐야 성공 여부를 알 수 있습니다.。
// 그러므로 이 인터페이스는**동기화**님，시간 초과의 상한은 다음에 의해 결정됩니다. notify 포함됨 HTTP 고객이 결정합니다.（15 초）。
func (s *Server) notifyTestChannel(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "채널 id 유효하지 않음")
		return
	}
	ch, err := pg.NotificationChannelByID(r.Context(), id)
	if err != nil {
		notifyChannelLookupErr(w, err)
		return
	}
	channel, ok := notify.Get(ch.Kind)
	if !ok {
		writeErr(w, 400, fmt.Sprintf("채널 유형 %q 등록되지 않음", ch.Kind))
		return
	}
	var cfg map[string]any
	if len(ch.Config) > 0 {
		_ = json.Unmarshal(ch.Config, &cfg)
	}
	if err := channel.Validate(cfg); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	msg := notifyTestMessage(s.notifierBaseURL(pg))
	start := time.Now()
	// 테스트 메시지가 하나뿐입니다.，여기서는 배송물 개수는 필요하지 않습니다.（최대 채널 길이는 단일 메시지에 대한 것입니다.
	// 잘림，세분화를 포함하지 않습니다.）。
	if _, err := channel.Send(r.Context(), cfg, msg); err != nil {
		// 채널에서 반환한 원래 오류를 사용자에게 사실대로 반환합니다.——이것이 구성을 디버그할 수 있는 유일한 단서입니다.。
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok":         true,
		"latency_ms": time.Since(start).Milliseconds(),
	})
}

// notifyTestMessage 테스트 메시지 구성。한눈에 알 수 있는 내용을 의도적으로 테스트로 활용：
// 받는 사람이 진짜 취약점으로 오해하면 안 된다.。
func notifyTestMessage(baseURL string) notify.Message {
	return notify.Message{
		Items: []notify.Item{{
			FindingID: 0,
			Name:      "테스트 메시지 · 채널 구성이 정상입니다.",
			VulnClass: "연결 테스트",
			Severity:  "low",
			Summary:   "이건 ARTEX 푸시 채널 테스트 메시지，수신은 채널 구성이 가능하다는 의미입니다.。",
			Assets:    []string{"artex.example.com"},
			DetailURL: baseURL,
		}},
		HomeURL: baseURL,
	}
}

// notifierBaseURL 링크를 다시 읽는 데 사용되는 외부 주소。
func (s *Server) notifierBaseURL(pg *db.DB) string {
	v, _, _ := pg.GetSetting(settingNotifyPublicBaseURL)
	return trimTrailingSlash(v)
}

func (s *Server) notifyListDeliveries(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	f := db.NotificationDeliveryFilter{
		State:     r.URL.Query().Get("state"),
		EventKind: r.URL.Query().Get("event_kind"),
	}
	if v := r.URL.Query().Get("channel_id"); v != "" {
		f.ChannelID = int64(atoiDefault(v, 0))
	}
	page := queryInt(r, "page", 1)
	pageSize := queryInt(r, "page_size", 50)
	items, total, err := pg.ListNotificationDeliveries(r.Context(), f, page, pageSize)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := make([]notifyDeliveryDTO, 0, len(items))
	for _, dl := range items {
		out = append(out, toNotifyDeliveryDTO(dl))
	}
	writeJSON(w, 200, map[string]any{"deliveries": out, "total": total, "page": page, "page_size": pageSize})
}

func (s *Server) notifyRetryDelivery(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, ok := pathInt(r, "id")
	if !ok {
		writeErr(w, 400, "배송 id 유효하지 않음")
		return
	}
	if err := pg.RetryNotificationDelivery(r.Context(), id); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// notifyChannelLookupErr 넣어보세요「채널이 존재하지 않습니다」로 번역됨 404，기타 오류 500。
func notifyChannelLookupErr(w http.ResponseWriter, err error) {
	if errors.Is(err, db.ErrNotificationChannelNotFound) {
		writeErr(w, 404, "알림 채널이 존재하지 않습니다.")
		return
	}
	writeErr(w, 500, err.Error())
}
