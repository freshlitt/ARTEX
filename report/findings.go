package report

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
)

// 검색 페이지「수출」렌더링 사용됨:배치를 넣어 findings 테이블 행이 요약으로 렌더링됩니다. Markdown、싱글 Markdown、
// 또는 CSV。JSON  server 레이어를 직접 사용 DTO 직렬화,여기 없어요。

// sortFindingsForExport 심각도 내림차순、시간을 역순으로 정렬,요약보고서의 그룹화와 일치。
func sortFindingsForExport(fs []*db.DBFinding) {
	sort.SliceStable(fs, func(i, j int) bool {
		ri, rj := sevRank[fs[i].Severity], sevRank[fs[j].Severity]
		if ri != rj {
			return ri < rj // sevRank 작을수록 심각하다
		}
		return fs[i].CreatedAt.After(fs[j].CreatedAt)
	})
}

// findingTitle 읽을 수 있는 취약점 제목을 가져옵니다.:이름 → 카테고리 → 「분류되지 않음」。
func findingTitle(f *db.DBFinding) string {
	return nz(f.Name, nz(f.VulnClass, "분류되지 않음"))
}

// FindingsMarkdown 배치를 넣어 findings 요약보고서로 통합(요약 + 심각도별로 그룹화됨,
// 각 항목에는 카테고리가 포함되어 있습니다./상태/할당된 작업/증거/상세보고)。
func FindingsMarkdown(fs []*db.DBFinding, generatedAt time.Time) string {
	items := append([]*db.DBFinding(nil), fs...)
	sortFindingsForExport(items)

	var b strings.Builder
	b.WriteString("# 취약점 발견 요약 보고서\n\n")
	fmt.Fprintf(&b, "- **생성 시간**：%s\n", generatedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "- **총 발견 개수**：%d \n\n", len(items))

	// 요약:각 심각도 수준의 개수。
	counts := map[string]int{}
	for _, f := range items {
		counts[f.Severity]++
	}
	b.WriteString("## 요약\n\n")
	b.WriteString("| 심각도 수준 | 수량 |\n| --- | --- |\n")
	for _, s := range []struct{ key, label string }{
		{"critical", "심각해요"}, {"high", "위험도 높음"}, {"medium", "중간 위험"}, {"low", "낮은 위험"},
	} {
		fmt.Fprintf(&b, "| %s | %d |\n", s.label, counts[s.key])
	}
	b.WriteString("\n")

	if len(items) == 0 {
		b.WriteString("_일치하는 취약점이 없습니다.。_\n")
		return b.String()
	}

	b.WriteString("## 취약점 세부정보\n\n")
	for i, f := range items {
		fmt.Fprintf(&b, "### %d. [%s] %s\n\n", i+1, strings.ToUpper(nz(f.Severity, "info")), findingTitle(f))
		if f.VulnClass != "" {
			fmt.Fprintf(&b, "- **카테고리**：%s\n", f.VulnClass)
		}
		fmt.Fprintf(&b, "- **상태**：%s\n", nz(f.Status, "pending"))
		if desc := strings.TrimSpace(f.TaskDescription); desc != "" {
			fmt.Fprintf(&b, "- **할당된 작업**：%s\n", desc)
		}
		fmt.Fprintf(&b, "- **발견의 시간**：%s\n\n", f.CreatedAt.Format("2006-01-02 15:04:05"))
		if s := strings.TrimSpace(f.Summary); s != "" {
			fmt.Fprintf(&b, "%s\n\n", s)
		}
		if e := strings.TrimSpace(f.Evidence); e != "" {
			fmt.Fprintf(&b, "**증거：**\n\n```\n%s\n```\n\n", e)
		}
		if rep := strings.TrimSpace(f.Report); rep != "" {
			b.WriteString("**상세보고：**\n\n")
			b.WriteString(rep)
			b.WriteString("\n\n")
		}
		b.WriteString(findingTrafficMarkdown(f, false))
		b.WriteString("---\n\n")
	}
	return b.String()
}

// SingleFindingMarkdown 단일 취약점을 독립적으로 렌더링 Markdown(「하나의 취약점, 하나의 파일」포장)。
func SingleFindingMarkdown(f *db.DBFinding, generatedAt time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# [%s] %s\n\n", strings.ToUpper(nz(f.Severity, "info")), findingTitle(f))
	if f.VulnClass != "" {
		fmt.Fprintf(&b, "- **카테고리**：%s\n", f.VulnClass)
	}
	fmt.Fprintf(&b, "- **심각도 수준**：%s\n", nz(f.Severity, "info"))
	fmt.Fprintf(&b, "- **상태**：%s\n", nz(f.Status, "pending"))
	if desc := strings.TrimSpace(f.TaskDescription); desc != "" {
		fmt.Fprintf(&b, "- **할당된 작업**：%s\n", desc)
	}
	fmt.Fprintf(&b, "- **발견의 시간**：%s\n", f.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "- **생성 시간**：%s\n\n", generatedAt.Format("2006-01-02 15:04:05"))
	if s := strings.TrimSpace(f.Summary); s != "" {
		fmt.Fprintf(&b, "## 개요\n\n%s\n\n", s)
	}
	if e := strings.TrimSpace(f.Evidence); e != "" {
		fmt.Fprintf(&b, "## 증거\n\n```\n%s\n```\n\n", e)
	}
	if rep := strings.TrimSpace(f.Report); rep != "" {
		b.WriteString("## 상세보고\n\n")
		b.WriteString(rep)
		b.WriteString("\n")
	}
	b.WriteString(findingTrafficMarkdown(f, true))
	return b.String()
}

var unsafeFilenameChars = regexp.MustCompile(`[^\p{Han}\p{L}\p{N}._-]+`)

// FindingFilename 입니다「하나의 취약점, 하나의 파일」안전 생성 .md 파일명,모양은 다음과 같습니다
// `critical_SQL주사_#123.md`。경로 구분 기호 및 제어 문자를 제거합니다.,피하세요 zip 경로가 잘못되었습니다.。
func FindingFilename(f *db.DBFinding) string {
	sev := nz(f.Severity, "info")
	title := findingTitle(f)
	name := fmt.Sprintf("%s_%s_#%d", sev, title, f.ID)
	name = unsafeFilenameChars.ReplaceAllString(name, "_")
	name = strings.Trim(name, "._")
	if name == "" {
		name = fmt.Sprintf("finding_%d", f.ID)
	}
	// 방어적:경로의 다른 레이어를 벗겨냅니다.,방지 zip slip。
	name = path.Base(name)
	if len(name) > 120 {
		name = name[:120]
	}
	return name + ".md"
}

// FindingsCSV 배치를 넣어 findings 로 렌더링됨 CSV(와 함께 UTF-8 BOM,편리함 Excel 중국어를 정확하게 인식함)。
// 큰 단락은 포함하지 않습니다. report/evidence 전문,요약 필드만 넣습니다.;전문이 필요합니다 Markdown/JSON 수출。
func FindingsCSV(fs []*db.DBFinding) []byte {
	items := append([]*db.DBFinding(nil), fs...)
	sortFindingsForExport(items)

	var buf bytes.Buffer
	buf.WriteString("\xEF\xBB\xBF") // UTF-8 BOM
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"ID", "이름", "카테고리", "심각도 수준", "상태", "할당된 작업", "발견의 시간", "개요", "교통 증거 건수", "교통 증거ID"})
	for _, f := range items {
		_ = w.Write([]string{
			fmt.Sprintf("%d", f.ID),
			findingTitle(f),
			f.VulnClass,
			nz(f.Severity, "info"),
			nz(f.Status, "pending"),
			f.TaskDescription,
			f.CreatedAt.Format("2006-01-02 15:04:05"),
			strings.TrimSpace(f.Summary),
			fmt.Sprint(len(f.TrafficBindings)), findingTrafficIDs(f),
		})
	}
	w.Flush()
	return buf.Bytes()
}

func findingTrafficIDs(f *db.DBFinding) string {
	ids := make([]string, 0, len(f.TrafficBindings))
	for _, b := range f.TrafficBindings {
		ids = append(ids, fmt.Sprint(b.ID))
	}
	return strings.Join(ids, ",")
}

func findingTrafficMarkdown(f *db.DBFinding, attachments bool) string {
	stale := f.Report != "" && f.EvidenceVersion != f.ReportEvidenceVersion
	if len(f.TrafficBindings) == 0 && !stale {
		return ""
	}
	var out strings.Builder
	out.WriteString("\n## 관련 교통 증거\n\n")
	fmt.Fprintf(&out, "증거 버전：%d；제본 수량：%d。\n\n", f.EvidenceVersion, len(f.TrafficBindings))
	if stale {
		out.WriteString("증거가 변경되었습니다，세부 보고서는 업데이트 예정。\n\n")
	}
	for i, b := range f.TrafficBindings {
		fmt.Fprintf(&out, "%d. **증거 #%d · %s** — `%s %s`，상태 코드 %d\n", i+1, b.ID, b.Role, b.Snapshot.Method, strings.ReplaceAll(b.Snapshot.URL, "`", "%60"), b.Snapshot.Status)
		if b.Note != "" {
			fmt.Fprintf(&out, "   %s\n", strings.ReplaceAll(b.Note, "\n", "\n   "))
		}
		if attachments {
			fmt.Fprintf(&out, "   [요청 메시지](evidence/%d/%d/request.http) · [응답 메시지](evidence/%d/%d/response.http)\n", f.ID, b.ID, f.ID, b.ID)
		}
	}
	out.WriteString("\n")
	return out.String()
}
