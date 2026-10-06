package agent

import (
	"strings"

	"github.com/Autumn-27/artex/db"
)

// constraintBlock renders this task's operation constraints (task_constraints) as a
// high-priority block appended to the planner/worker system prompt. allow/deny are
// grouped; empty string when there are no constraints (or ts is nil). The framing
// deliberately puts these ABOVE the exploration/expansion heuristics so a declared
// boundary wins the tug-of-war against "chase another entry surface".
func constraintBlock(ts *db.ExplorationStore) string {
	if ts == nil {
		return ""
	}
	rows, err := ts.ListConstraints()
	if err != nil || len(rows) == 0 {
		return ""
	}
	var allow, deny []string
	for _, c := range rows {
		text := strings.TrimSpace(c.Text)
		if text == "" {
			continue
		}
		if c.Kind == "allow" {
			allow = append(allow, "- "+text)
		} else {
			deny = append(deny, "- "+text)
		}
	}
	if len(allow) == 0 && len(deny) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n【운영상의 제약（최우선 순위，아래의 모든 것 이상을 탐색하세요./표면 확장 휴리스틱；인텐트가 생성될 때마다、조치를 실행하기 전에 먼저 위반 여부를 자체적으로 확인해야 합니다.，위반시 진행하지 마세요）】：")
	if len(allow) > 0 {
		b.WriteString("\n허용되는 작업：\n")
		b.WriteString(strings.Join(allow, "\n"))
	}
	if len(deny) > 0 {
		b.WriteString("\n금지된 조작：\n")
		b.WriteString(strings.Join(deny, "\n"))
	}
	b.WriteString("\n（제약을 넘어 새로운 목표를 발견하다/새 포트/새 호스트，은 허가를 받는 것을 의미하지 않습니다.：위의 허용 범위에 속하지 않는 한，그렇지 않으면 다음과 같이 기록합니다. out-of-scope 사실을 알고 건너뛰기，인텐트를 도출하거나 이에 대한 작업을 수행할 수 없습니다.。）")
	return b.String()
}
