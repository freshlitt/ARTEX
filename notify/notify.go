// Package notify , 취약점 발견 실현 IM / 이메일 푸시 채널 적응 계층。
//
// 레이어링：이 패키지는**나뭇잎주머니**，표준 라이브러리에만 의존합니다.。데이터베이스를 인식하지 못합니다.、모르겠어요 server。채널 구성
// 에게 map[string]any 들어옴（해당 notification_channels.config 이거요 JSONB 칼럼），
// 푸시할 내용은 Message 들어옴。이렇게 분해하면 좋은 점은：서명 계산、UTF-8 잘림、일치하도록 필터링하세요.
// 실제 오류가 발생하기 쉬운 영역은 PostgreSQL 단일 테스트，호스트는 다음 사항만 수행하면 됩니다. server 사이드 안무。
//
// 동시성 계약：Channel 의 구현은 다음과 같습니다.**상태 없음**。똑같습니다 Channel 인스턴스는 여러 채널로 구성됩니다.
// （동일한 채널에 대한 여러 봇 인스턴스도）동시 재사용，모든 자격 증명은 다음에서 얻어야 합니다. cfg 전달된 매개변수，
// 허용되지 않음 webhook URL 등은 스스로 구현하는 필드에 캐시됩니다.。
package notify

// 채널 유형 식별。값은 둘 다입니다. notification_channels.kind 의 법적 수집， server 쪽
// 화이트리스트 확인（그리고 findings.status 같은 이유，필요없어요 DB CHECK，후속 채널 추가를 용이하게 합니다.）。
const (
	KindDingTalk = "dingtalk" // 딩톡 커스텀 로봇
	KindFeishu   = "feishu"   // 페이슈(포함 Lark)맞춤형 로봇
	KindWeCom    = "wecom"    // 기업 위챗 그룹 로봇
	KindWebhook  = "webhook"  // 일반 Webhook：사용자 정의 방법/머리/JSON 템플릿
	KindTelegram = "telegram" // Telegram Bot API
	KindEmail    = "email"    // SMTP 이메일
)

// 이벤트 종류，해당 notification_events.kind。
const (
	EventFindingCreated       = "finding_created"
	EventFindingStatusChanged = "finding_status_changed"
)

// InitKind 네 config 이 비어 있습니다. kind 의 비밀값。
const InitKind = KindDingTalk

// severityRank 취약성 수준을 비교 가능한 서수로 매핑。알 수 없는 레벨이 반환되었습니다. 0，그럼요
// min_severity 알 수 없는 수준을 차단하는 설정입니다.——의심스러우면 추천하지 마세요，오경보 방지 및 화면 새로고침。
var severityRank = map[string]int{
	"low":      1,
	"medium":   2,
	"high":     3,
	"critical": 4,
}

// SeverityRank 레벨의 서수를 반환합니다.；알 수 없는 레벨이 반환되었습니다. 0。
func SeverityRank(severity string) int { return severityRank[severity] }

// SeverityLabel 반품 emoji 의 중국어 수준 이름，메시지 제목 및 카드 색상에 사용됩니다.。
// 알 수 없는 레벨이 그대로 에코됩니다.，조작되지 않음。
func SeverityLabel(severity string) string {
	switch severity {
	case "critical":
		return "🔴 심각해요"
	case "high":
		return "🟠 위험도 높음"
	case "medium":
		return "🟡 중간 위험"
	case "low":
		return "🔵 낮은 위험"
	default:
		return severity
	}
}

// StatusLabel 처분현황을 중국어로 번역，은 상태 변경 메시지에 사용됩니다.。
func StatusLabel(status string) string {
	switch status {
	case "pending":
		return "보류 중"
	case "in_progress":
		return "처리 중"
	case "confirmed":
		return "확인됨"
	case "resolved":
		return "처리됨"
	case "fixed":
		return "고정됨"
	case "false_positive":
		return "거짓양성"
	case "ignored":
		return "무시"
	case "duplicate":
		return "반복"
	case "risk_accepted":
		return "위험 감수"
	default:
		return status
	}
}

// AtLeast 판결 severity 도달했나요? min 임계값。min 비어 있으면 임계값이 설정되지 않았음을 의미합니다.，모두 합격。
// 알 수 없는 메모 severity 의 서수는 다음과 같습니다. 0，은 null이 아닌 값으로 대체됩니다. min 거절（또 만나요 severityRank 댓글）。
func AtLeast(severity, min string) bool {
	if min == "" {
		return true
	}
	return SeverityRank(severity) >= SeverityRank(min)
}
