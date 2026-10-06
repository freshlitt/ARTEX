package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"text/template"
	"time"
)

// webhookChannel 은 보편적이다 Webhook 어댑터：사용자 정의 URL、방법、요청 헤더는 다음과 같습니다. JSON 템플릿。
// 존재하면 이 기능이 불필요해집니다. Slack / Mattermost / Discord / 각 자체 구축 시스템에 대한 구현 작성——
// 이러한 플랫폼은 모두 구성 가능한 템플릿으로 처리할 수 있습니다.。
type webhookChannel struct{}

func (webhookChannel) Kind() string { return KindWebhook }

// 일반 Webhook 공식적인 제한 없음，복귀 0 은 기본적으로 전류 제한이 없음을 의미합니다.，상대방의 역량에 따라 사용자가 맞춤화。
func (webhookChannel) DefaultRatePerMin() int { return 0 }

// 마스크 url 그리고 headers：대상 주소 자체에 다음이 포함되는 경우가 많습니다. token，사용자 정의 헤더에는 일반적으로 인증 자격 증명이 포함됩니다.，
// 둘 다 인터페이스 에코에 나타납니다.，그럼 차단해야겠네요。
// 편집 중에 헤더 중 하나를 변경하려는 경우의 가격입니다.，그룹 헤더를 다시 채워야 합니다.（마스크 값은 다음과 같이 해석됩니다.「원래 값을 유지하세요.」）——
// 의도적인 선택입니다：차라리 한 번 더 작성하고 싶습니다.，자격 증명을 브라우저에 표시하지도 않습니다.。
func (webhookChannel) SecretKeys() []string { return []string{"url", "headers"} }

// 목적지는 url。변경 url 입장을 다시 밝혀야 한다 headers —— 그렇지 않으면 원본 Authorization 머리
// 은 새로운 주소로 그대로 발송됩니다.，마스크 우회의 주요 경로입니다.。
func (webhookChannel) DestinationKeys() []string { return []string{"url"} }

// webhookDefaultTemplate 은 템플릿이 작성되지 않은 경우의 요청 본문입니다.：간단한 것 JSON 구조，
// 대부분을 다룹니다.「하나 받음 JSON 저장」이 자체 제작한 수신단。
const webhookDefaultTemplate = `{
  "title": {{json .Title}},
  "batch": {{.Batch}},
  "count": {{.Count}},
  "items": [
{{- range $i, $it := .Items}}
{{- if $i}},{{end}}
    {
      "finding_id": {{$it.FindingID}},
      "name": {{json $it.Name}},
      "vulnclass": {{json $it.VulnClass}},
      "severity": {{json $it.Severity}},
      "summary": {{json $it.Summary}},
      "assets": {{json $it.Assets}},
      "detail_url": {{json $it.DetailURL}}
    }
{{- end}}
  ]
}`

// webhookTemplateData 은 사용자 템플릿에 노출되는 컨텍스트입니다.。
type webhookTemplateData struct {
	Title   string
	Batch   bool
	Count   int
	Items   []webhookItem
	HomeURL string
	// SentAt 배달 시간입니다（RFC3339），수신측 녹음용。
	SentAt string
}

type webhookItem struct {
	FindingID     int64
	Name          string
	VulnClass     string
	Severity      string
	SeverityLabel string
	Summary       string
	Assets        []string
	DetailURL     string
	FromStatus    string
	ToStatus      string
	// StatusLabel 은 사람이 읽을 수 있는 상태 변경 설명입니다.，「보류 중 → 고정됨」；상태 변화가 없을 때 비어 있음。
	StatusLabel string
}

func (webhookChannel) Validate(cfg map[string]any) error {
	raw := cfgString(cfg, "url")
	if raw == "" {
		return errors.New("대상이 누락되었습니다. URL")
	}
	if err := validateHTTPURL(raw); err != nil {
		return fmt.Errorf("대상 URL 유효하지 않음: %w", err)
	}
	if m := strings.ToUpper(cfgString(cfg, "method")); m != "" && m != http.MethodGet && m != http.MethodPost && m != http.MethodPut && m != http.MethodPatch {
		return fmt.Errorf("지원되지 않는 방법 %s（가능 GET/POST/PUT/PATCH）", m)
	}
	if tpl := cfgString(cfg, "body_template"); tpl != "" {
		if _, err := parseWebhookTemplate(tpl); err != nil {
			return fmt.Errorf("요청 본문 템플릿 구문 오류: %w", err)
		}
	}
	return nil
}

func (c webhookChannel) Send(ctx context.Context, cfg map[string]any, m Message) (int, error) {
	if err := c.Validate(cfg); err != nil {
		return 0, Permanent(err)
	}
	method := strings.ToUpper(cfgString(cfg, "method"))
	if method == "" {
		method = http.MethodPost
	}

	// GET 요청 본문 없음：내용을 넣어주세요 query 템플릿 기능을 넘어서，역시 일치하지 않습니다 GET 의미，
	// 그래서 GET 다음에만 적합합니다.「히트가 후크를 트리거합니다.」이런 수신단。
	var payload any
	if method != http.MethodGet {
		body, err := renderWebhookBody(cfgString(cfg, "body_template"), m)
		if err != nil {
			return 0, Permanent(err)
		}
		// 템플릿은 문자열 형태로 렌더링됩니다. JSON，여기로 변환하세요 json.RawMessage 있는 그대로 보냄，
		// 이중 이스케이프를 피하고 사용자가 신중하게 구성한 구조를 JSON 문자열。
		if !json.Valid([]byte(body)) {
			return 0, Permanent(errors.New("요청 본문 템플릿 렌더링 결과가 올바른 JSON이 아닙니다"))
		}
		payload = json.RawMessage(body)
	}

	headers := cfgMap(cfg, "headers")
	if ct := cfgString(cfg, "content_type"); ct != "" {
		// 재정의 허용，하지만 넣어 headers 나중에 신청하세요，명시적 구성이 우선인지 확인하세요.。
		if headers == nil {
			headers = map[string]string{}
		}
		headers["Content-Type"] = ct
	}
	if _, err := doJSON(ctx, method, cfgString(cfg, "url"), headers, payload); err != nil {
		return 0, err
	}
	// 일반 Webhook 텍스트를 자르지 마세요.（받는 쪽은 사용자 자신의 서비스，볼륨은 다음과 같이 지정됩니다. body_template 결정），
	// 따라서 전체 배치가 배송된 것으로 간주됩니다.。
	return len(m.Items), nil
}

// renderWebhookBody 사용자 템플릿 사용（또는 기본 템플릿）렌더링 요청 본문。
func renderWebhookBody(tpl string, m Message) (string, error) {
	if strings.TrimSpace(tpl) == "" {
		tpl = webhookDefaultTemplate
	}
	t, err := parseWebhookTemplate(tpl)
	if err != nil {
		return "", fmt.Errorf("요청 본문 템플릿 구문 오류: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, newWebhookTemplateData(m)); err != nil {
		return "", fmt.Errorf("요청 본문 템플릿을 렌더링하지 못했습니다.: %w", err)
	}
	return buf.String(), nil
}

// parseWebhookTemplate 구문 분석 템플릿。
//
// missingkey=zero 누락하자 map 오류를 보고하는 대신 키가 0 값으로 렌더링됩니다.——하지만 이 파일의 컨텍스트는 구조입니다.，
// 주요 기능은 .Items 이 비어 있습니다. range 오류 없음。정말 경계해야 할 것은 .Items 입니다 nil。
func parseWebhookTemplate(tpl string) (*template.Template, error) {
	return template.New("body").Funcs(webhookTemplateFuncs).Option("missingkey=zero").Parse(tpl)
}

// webhookTemplateFuncs 은 템플릿에 노출되는 도우미 함수입니다.。
var webhookTemplateFuncs = template.FuncMap{
	// json 모든 값을 직렬화합니다. JSON。
	//
	// 이 기능은 좋은 기능이 아니라 꼭 필요한 기능입니다：빼주세요，사용자는 쓰기만 가능합니다. {{.Title}} 직접 보간，
	// 취약점 제목에 따옴표나 줄 바꿈이 있는 한，전체 요청 본문이 더 이상 합법적이지 않습니다. JSON——수신측에서는
	// 거부，및 오류 메시지는 다음을 가리킵니다.「JSON 구문 분석 실패」，제목에 따옴표가 있는 줄은 몰랐네요.。
	"json": func(v any) (string, error) {
		raw, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(raw), nil
	},
	// jsons 에 익숙합니다. JSON 다른 단락에 조각이 포함되어 있습니다. JSON 문자열 값 내부（문자열 이스케이프 레이어를 수행합니다.）。
	"jsons": func(v any) (string, error) {
		raw, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		quoted, err := json.Marshal(string(raw))
		if err != nil {
			return "", err
		}
		// 바깥쪽 따옴표를 제거하세요.：인용부호 추가 여부는 발신자가 결정합니다.。
		return string(quoted[1 : len(quoted)-1]), nil
	},
}

func newWebhookTemplateData(m Message) webhookTemplateData {
	d := webhookTemplateData{
		Title:   markdownTitle(m),
		Batch:   m.Batch,
		Count:   len(m.Items),
		HomeURL: m.HomeURL,
		SentAt:  time.Now().Format(time.RFC3339),
		Items:   make([]webhookItem, 0, len(m.Items)),
	}
	for _, it := range m.Items {
		wi := webhookItem{
			FindingID:     it.FindingID,
			Name:          it.Name,
			VulnClass:     it.VulnClass,
			Severity:      it.Severity,
			SeverityLabel: SeverityLabel(it.Severity),
			Summary:       it.Summary,
			Assets:        append([]string{}, it.Assets...),
			DetailURL:     it.DetailURL,
			FromStatus:    it.FromStatus,
			ToStatus:      it.ToStatus,
		}
		if it.IsStatusChange() {
			wi.StatusLabel = StatusLabel(it.FromStatus) + " → " + StatusLabel(it.ToStatus)
		}
		d.Items = append(d.Items, wi)
	}
	return d
}
