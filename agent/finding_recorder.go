package agent

import (
	"context"

	"github.com/Autumn-27/artex/db"
)

// FindingRecorder is injected by the host; agents never synthesize or copy
// evidence bodies themselves. Its implementation owns the atomic write.
type FindingRecorder interface {
	Record(context.Context, db.RecordFindingInput, []db.TrafficRef) (*db.RecordedFinding, error)
}

// Tool-use guidance is appended without replacing the user's editable prompt.
// It does not require capture or claim that unavailable traffic tools exist.
const findingTrafficGuidance = "\n\n**취약점 트래픽 증거（선택사항）**：전화주세요 report_finding 취약점 보고 시，누군가 검토하고 취약점 결론이 뒷받침된다는 것을 확인한 경우 HTTP 요청/응답，가능 traffic_refs 반복되는 순서대로 true로 바인딩 ID；도메인 이름과 시간은 후보자 심사에만 사용됩니다.，연결이 없는 것으로 추정됩니다.。TCP 기다리지 마세요 HTTP 취약점、컬렉션이 없거나 정확히 일치하는 경우 생략 또는 통과 []，에 evidence 명령 출력 유지、로그 및 기타 검증 가능한 증거，구속력이 없는 이유를 설명하는 것이 좋습니다.。추측하지 마세요 ID，패치 패킷에 대해서만 탐지를 반복하지 마십시오.。"

func (t *ToolSet) SetFindingRecorder(r FindingRecorder)   { t.findingRecorder = r }
func (w *Worker) SetFindingRecorder(r FindingRecorder)    { w.findingRecorder = r }
func (p *Planner) SetFindingRecorder(r FindingRecorder)   { p.findingRecorder = r }
func (m *MainAgent) SetFindingRecorder(r FindingRecorder) { m.findingRecorder = r }
