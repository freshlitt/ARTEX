package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

// assetInterceptCandidates 삽입할 자산 입력 항목을 추출합니다. 도메인 이름/IP/URL 후보 문자열，자산 가로채기 매칭에 사용됩니다.。
// URL 님 host 카테고리를 구분하겠습니다.，만드세요「지참만 하세요 URL」님의 서비스/엔드포인트 자산도 가능합니다. 도메인 이름/IP 규칙 적중。
func assetInterceptCandidates(item assetInputItem) (domains, ips, urls []string) {
	add := func(dst *[]string, s string) {
		if s = strings.TrimSpace(s); s != "" {
			*dst = append(*dst, s)
		}
	}
	add(&domains, item.Domain)
	for _, d := range item.BoundDomains {
		add(&domains, d)
	}
	add(&ips, item.IP)
	add(&ips, item.ServiceIP)
	add(&urls, item.URL)
	if item.URL != "" {
		if u, err := url.Parse(item.URL); err == nil {
			if h := u.Hostname(); h != "" {
				if net.ParseIP(h) != nil {
					add(&ips, h)
				} else {
					add(&domains, h)
				}
			}
		}
	}
	return domains, ips, urls
}

// assetInputLabel 삽입할 자산의 짧은 ID를 반환합니다.，은 설명 메시지를 가로채는 데 사용됩니다.。
func assetInputLabel(item assetInputItem) string {
	typ := strings.TrimSpace(item.Type)
	var target string
	switch {
	case strings.TrimSpace(item.Domain) != "":
		target = strings.TrimSpace(item.Domain)
	case strings.TrimSpace(item.URL) != "":
		target = strings.TrimSpace(item.URL)
	case strings.TrimSpace(item.IP) != "":
		target = strings.TrimSpace(item.IP)
	case strings.TrimSpace(item.ServiceIP) != "":
		target = strings.TrimSpace(item.ServiceIP)
	default:
		target = "(알 수 없음)"
	}
	if typ != "" {
		return fmt.Sprintf("[%s] %s", typ, target)
	}
	return target
}

// =====================================================================
// Unified asset insertion tools
// =====================================================================

// SetAssetStore wires the asset store and company store onto this ToolSet
// so the insert_assets, add_company_scope, and list_assets tools are active.
func (t *ToolSet) SetAssetStore(as *db.AssetStore, cs *db.CompanyStore) {
	t.as = as
	t.cs = cs
}

// assetInputItem is one element of the insert_assets "assets" array.
type assetInputItem struct {
	Type string `json:"type"` // root_domain|ip|subdomain|app|service|endpoint

	// ---- root_domain / subdomain ----
	Domain      string   `json:"domain"`
	ICP         string   `json:"icp"`
	RecordType  string   `json:"record_type"`
	RecordValue []string `json:"record_value"`

	// ---- ip ----
	IP           string           `json:"ip"`
	BoundDomains []string         `json:"bound_domains"`
	OpenPorts    []db.PortService `json:"open_ports"`

	// ---- app ----
	AppName     string `json:"app_name"`
	BundleID    string `json:"bundle_id"`
	Category    string `json:"category"`
	Description string `json:"description"`
	AppICP      string `json:"app_icp"`
	CompanyID   *int64 `json:"company_id"` // explicit company link (app only; others auto-attribute via scope)

	// ---- service (http) ----
	URL           string           `json:"url"`
	Technologies  []string         `json:"technologies"`
	StatusCode    *int             `json:"status_code"`
	ContentLength *int64           `json:"content_length"`
	PageTitle     string           `json:"page_title"`
	FaviconMMH3   string           `json:"favicon_mmh3"`
	Auth          []map[string]any `json:"auth"`
	ServiceName   string           `json:"service_name"`
	ServiceIP     string           `json:"service_ip"` // optional enrichment IP

	// ---- service (other) ----
	Port  int    `json:"port"`
	Proto string `json:"proto"`

	// ---- endpoint ----
	Method string           `json:"method"`
	Params []map[string]any `json:"params"`
}

// insertAssets is the unified insert_assets agent tool.
func (t *ToolSet) insertAssets() actool.CoreTool {
	return writeTool(
		"insert_assets",
		"새로 발견된 자산 일괄 등록，한 번에 여러 유형을 혼합할 수 있습니다.（type 열거 참조）。\n"+
			"유형별 필수항목：root_domain→domain；ip→ip（반드시 IPv4/IPv6，호스트 이름이 아님）；subdomain→domain；app→app_name；service(HTTP)→url；service(아니요HTTP)→service_name+port（ip/domain 하나 이상 입력하세요.）；endpoint→url+method。기타 필드의 의미는 해당 설명을 참조하세요.。\n"+
			"auth/technologies/params 은 추가 병합용입니다.(append)，원래 값을 덮어쓰지 마십시오.。\n"+
			"복귀：{results:[{index,id,type}], errors:[{index,error}]}",
		obj(map[string]any{
			// task_id 모델 노출 없음：worker 어느쪽에 속하나요? task 프로그램별 SetTaskID 권한 할당(또 만나요 handler)。
			"assets": map[string]any{
				"type":        "array",
				"description": "자산 배열，각 요소는 자산 레코드에 해당합니다.",
				"items": obj(map[string]any{
					"type": map[string]any{
						"type":        "string",
						"enum":        []string{"root_domain", "ip", "subdomain", "app", "service", "endpoint"},
						"description": "자산 유형",
					},
					// root_domain / subdomain
					"domain":      str("루트 도메인 이름 또는 하위 도메인 이름（root_domain/subdomain 필수）"),
					"icp":         str("ICP 등록번호（선택사항）"),
					"record_type": str("DNS 분석 유형：A/AAAA/CNAME/MX 등（subdomain 선택사항）"),
					"record_value": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "DNS 구문 분석 값 목록（subdomain 선택사항， [\"1.2.3.4\",\"2.3.4.5\"]）",
					},
					// ip
					"ip": str("IP 주소，반드시 IPv4/IPv6 주소，호스트 이름을 입력할 수 없습니다.（호스트 이름을 사용하세요. type=subdomain 님 domain 필드）；ip 필수 입력；service/endpoint 유형을 입력할 수 있습니다.，은 연결에 사용됩니다. IP"),
					"bound_domains": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "그게 IP 바인딩된 도메인 이름 목록（ip 선택사항을 입력하세요.）",
					},
					"open_ports": map[string]any{
						"type":        "array",
						"description": "열린 포트 목록（ip 선택사항을 입력하세요.）",
						"items": obj(map[string]any{
							"port":    intp("포트 번호"),
							"service": str("서비스 이름， http/ssh/mysql 등（선택사항）"),
						}, "port"),
					},
					// app
					"app_name":    str("애플리케이션 이름（app 필수 입력）"),
					"bundle_id":   str("Bundle ID（app 선택사항을 입력하세요.）"),
					"category":    str("적용 분류（선택사항）"),
					"description": str("애플리케이션 설명（선택사항）"),
					"app_icp":     str("신청 ICP 제출 중（선택사항）"),
					"company_id":  intp("회사 소속 id（app 선택사항을 입력하세요.；app 신뢰할 수 없음 scope 자동 귀속，명시적으로 지정해야 함。id  add_company_scope 복귀）"),
					// service (http)
					"url":         str("완료 URL，프로토콜 및 포트 포함（HTTP 서비스 필요；service_type 자동으로 설정됨 http）"),
					"status_code": intp("HTTP 응답 상태 코드， 200/301/403/404（선택사항）"),
					"content_length": map[string]any{
						"type":        "integer",
						"description": "HTTP 응답 본문 바이트 수（선택사항）",
					},
					"page_title":   str("페이지 <title> 내용（선택사항）"),
					"favicon_mmh3": str("favicon MMH3 해시（선택사항）"),
					"technologies": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "지문/기술 스택 목록， [\"Nginx\",\"Vue\",\"Bootstrap\"]（선택사항）",
					},
					"auth": map[string]any{
						"type":        "array",
						"description": "검색된 인증정보 목록，각 항목에는 다음이 포함됩니다. type/username/password 및 기타 분야（선택사항，추가하면 덮어쓰지 않습니다.）",
						"items":       map[string]any{"type": "object"},
					},
					// service (other，아니요 HTTP)
					"service_name": str("서비스 이름， ssh/mysql/redis（service 아니요 HTTP 일 때 필수）"),
					"port":         intp("포트 번호（service 아니요 HTTP 일 때 필수）"),
					// endpoint
					"method": str("HTTP 방법：GET/POST/PUT/PATCH/DELETE 등（endpoint 필수）"),
					"params": map[string]any{
						"type":        "array",
						"description": "요청 매개변수 목록，각 항목에는 다음이 포함됩니다. location(query/body/header/path)/name/value/type（선택사항，추가하면 덮어쓰지 않습니다.）",
						"items":       map[string]any{"type": "object"},
					},
				}, "type"),
			},
		}, "assets"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil {
				return actool.Errorf("insert_assets 활성화되지 않음: AssetStore 초기화되지 않음"), nil
			}
			var a struct {
				Assets []assetInputItem `json:"assets"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf("invalid input: " + err.Error()), nil
			}
			// task_id 프로그램에 의해 정식으로 할당됨(worker: SetTaskID)，모델 입력을 허용하지 않습니다.——모델 유출 방지/전송이 잘못되었습니다.
			// 결과적으로 자산이 작업으로 반환되지 않거나 잘못된 작업으로 반환됩니다.。작업 컨텍스트가 없는 호출자(auto/pentest/chat)그래요 t.taskID=0。
			taskID := t.taskID

			type result struct {
				Index int    `json:"index"`
				ID    int64  `json:"id"`
				Type  string `json:"type"`
			}
			type errEntry struct {
				Index int    `json:"index"`
				Error string `json:"error"`
			}

			var results []result
			var errs []errEntry

			// 자산 게이트 규칙의 일회성 로드；판독에 실패하면 판정을 건너뜁니다.（삽입을 막지 마세요）。
			// 차단 규칙 = 글로벌 ∪ 작업 수준 block；허용 규칙 = 작업 수준 allow。
			blockRules, _ := t.as.ListAssetInterceptRules()
			var allowRules []db.AssetInterceptRule
			if t.taskID > 0 {
				if tb, ta, err := t.as.TaskInterceptRulesSplit(t.taskID); err == nil {
					blockRules = append(blockRules, tb...)
					allowRules = ta
				}
			}

			for i, item := range a.Assets {
				// 자산 게이트：먼저 차단하고 허용，거부된 자산은 삽입이 금지됩니다.（건너뛰기 Upsert 및 그에 따른 부작용）。
				domains, ips, urls := assetInterceptCandidates(item)
				if d := db.EvaluateAssetGate(blockRules, allowRules, domains, ips, urls); !d.Allowed {
					errs = append(errs, errEntry{
						Index: i,
						Error: fmt.Sprintf("자산 %s %s，삽입 금지", assetInputLabel(item), d.Reason),
					})
					continue
				}

				typ := strings.TrimSpace(item.Type)
				var id int64
				var err error

				switch typ {
				case "root_domain":
					id, err = t.as.UpsertRootDomain(db.UpsertRootDomainReq{
						Domain: item.Domain,
						ICP:    item.ICP,
						TaskID: taskID,
					})

				case "ip":
					id, err = t.as.UpsertIP(db.UpsertIPReq{
						IP:           item.IP,
						BoundDomains: item.BoundDomains,
						OpenPorts:    item.OpenPorts,
						TaskID:       taskID,
					})

				case "subdomain":
					id, err = t.as.UpsertSubdomain(db.UpsertSubdomainReq{
						Domain:      item.Domain,
						RecordType:  item.RecordType,
						RecordValue: item.RecordValue,
						ICP:         item.ICP,
						TaskID:      taskID,
					})

				case "app":
					id, err = t.as.UpsertApp(db.UpsertAppReq{
						Name:        item.AppName,
						BundleID:    item.BundleID,
						Category:    item.Category,
						Description: item.Description,
						ICP:         item.AppICP,
						CompanyID:   item.CompanyID,
						TaskID:      taskID,
					})

				case "service":
					// distinguish HTTP vs other by presence of url
					if item.URL != "" {
						// agent may send "ip" or "service_ip" for the enrichment IP; accept both
						svcIP := item.ServiceIP
						if svcIP == "" {
							svcIP = item.IP
						}
						id, err = t.as.UpsertHTTPService(db.UpsertHTTPServiceReq{
							URL:           item.URL,
							Technologies:  item.Technologies,
							StatusCode:    item.StatusCode,
							ContentLength: item.ContentLength,
							PageTitle:     item.PageTitle,
							FaviconMMH3:   item.FaviconMMH3,
							Auth:          item.Auth,
							IP:            svcIP,
							TaskID:        taskID,
						})
					} else {
						id, err = t.as.UpsertOtherService(db.UpsertOtherServiceReq{
							Domain:      item.Domain,
							IP:          item.IP,
							Port:        item.Port,
							ServiceName: item.ServiceName,
							Auth:        item.Auth,
							TaskID:      taskID,
						})
					}

				case "endpoint":
					id, err = t.as.UpsertEndpoint(db.UpsertEndpointReq{
						URL:    item.URL,
						Method: item.Method,
						Params: item.Params,
						IP:     item.ServiceIP,
						TaskID: taskID,
					})

				default:
					errs = append(errs, errEntry{Index: i, Error: "unknown type: " + typ})
					continue
				}

				if err != nil {
					errs = append(errs, errEntry{Index: i, Error: err.Error()})
					continue
				}
				results = append(results, result{Index: i, ID: id, Type: typ})
				t.writes.Assets++
				t.anchorOwner(id)
				if taskID > 0 {
					var sourceNodeID *int64
					if t.ownerNode > 0 {
						nodeID := t.ownerNode
						sourceNodeID = &nodeID
					}
					summary := "Agent 합격 insert_assets 등록"
					if t.ownerNode > 0 {
						summary = fmt.Sprintf("Worker 의도 #%d 합격 insert_assets 등록", t.ownerNode)
					}
					_ = t.as.SetTaskAssetSource(taskID, id, "agent", summary, sourceNodeID)
				}
				// 테스트 범위 자동 입력(source='auto')：다만 그렇습니다 worker 이 항목은 최상위 수준에 명시적으로 삽입됩니다.，클릭하세요
				// 유형과 보수적 범위；side-effect 파생자산은 여기를 통과하지 않습니다.，그러므로 무작정 범위를 확장해서는 안 된다.。taskID=0 일 때 작업이 수행되지 않습니다.。
				// 커버리지 전환과는 관련이 없습니다.：task_scope 은 작업의 범위 경계입니다.(list/쿼리 필터링 기반)，
				// 커버리지 스위치는 지표 계산 시 분모로 사용할지 여부만 결정합니다.，범위 자체를 합산할지 여부를 결정할 수 없습니다.。
				{
					svcIP := item.ServiceIP
					if svcIP == "" {
						svcIP = item.IP
					}
					_ = t.as.AddAutoScope(taskID, typ, item.Domain, item.URL, svcIP)
				}
			}

			return jsonResult(map[string]any{
				"results": results,
				"errors":  errs,
			})
		},
	)
}

// addCompanyScope writes to company_scope table and triggers asset attribution.
func (t *ToolSet) addCompanyScope() actool.CoreTool {
	return writeTool(
		"add_company_scope",
		"도메인 이름을 넣어주세요/IP/CIDR/ICP제출 중/특정 회사에 추가된 기업 키워드【자산 범위】——도메인 이름、네트워크와ICP은 자동으로 히트 자산을 청구합니다.，키워드는 다음 사용자에게만 제공됩니다.Agent범위 프롬프트로。\n"+
			"고유한 회사명：company 없으면 새로 만드세요，이미 존재한다면 재사용하세요.(범위만 병합합니다.)。\n"+
			"scope 한 줄에 한 줄씩，시스템이 자동으로 식별합니다.：루트 도메인 이름 / URL / 싱글 IP / CIDR 네트워크 세그먼트 / ICP제출 중 / 기업 키워드。\n"+
			"꼭 드려요 reason 귀속의 기초를 설명하세요.(whois/인증서/ASN 등)。\n"+
			"가드레일：알몸을 거부하세요 TLD 및 너무 넓은 네트워크 세그먼트(IPv4접두사는 다음과 같아야 합니다./16-/32、IPv6접두사는 다음과 같아야 합니다./32-/128)，잘못된 줄은 건너뛰고 errors 복귀。",
		obj(map[string]any{
			"company": str("회사명(없으면 새로 만드세요、존재하면 재사용；이름이 독특하네요)"),
			"scope":   str("자산 범위，한 줄에 한 줄씩：도메인 이름 / URL / IP / CIDR / ICP제출 중 / 기업 키워드"),
			"reason":  str("귀속 기준(증거/출처)，꼭 입력해주세요"),
			"logo":    str("회사 아이콘 URL(선택사항；새 회사를 만들 때만 적용됩니다.)"),
		}, "company", "scope"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.cs == nil {
				return actool.Errorf("add_company_scope 활성화되지 않음: CompanyStore 초기화되지 않음"), nil
			}
			var a struct {
				Company string `json:"company"`
				Scope   string `json:"scope"`
				Reason  string `json:"reason"`
				Logo    string `json:"logo"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if strings.TrimSpace(a.Company) == "" {
				return actool.Errorf("company 은 비워둘 수 없습니다."), nil
			}
			companyID, _, err := t.cs.UpsertCompany(a.Company, a.Logo)
			if err != nil {
				return actool.Errorf("생성/업체 확보 실패: " + err.Error()), nil
			}
			lines := splitLines(a.Scope)
			added, skipped, invalid, errMsgs := t.cs.AddScope(companyID, lines, a.Reason)
			out := map[string]any{
				"company_id": companyID,
				"added":      added,
				"skipped":    skipped,
				"invalid":    invalid,
			}
			if len(errMsgs) > 0 {
				out["errors"] = errMsgs
			}
			return jsonResult(out)
		},
	)
}

// addTaskScope lets the plan agent add test scope to THE CURRENT TASK — the coverage
// denominator and the task's authorization edge. Worker discoveries are auto-scoped
// (precise host) by insertAssets; this tool is for DELIBERATELY WIDENING: pull a whole
// root domain or whole company into scope, or add a specific subdomain / ip.
func (t *ToolSet) addTaskScope() actool.CoreTool {
	return writeTool(
		"add_task_scope",
		"테스트 범위 추가【이번 임무는】——이 작업에 대한 권한 부여 경계입니다.，은 자산 테스트 커버리지의 분모이기도 합니다.。\n"+
			"kind 지원：company(회사 전체 명의의 자산) / root_domain(전체 루트 도메인，모든 하위 도메인 포함) / subdomain(정확한 단일 하위 도메인) / ip / cidr / icp / keyword。\n"+
			"설명：worker 호스트가 하나씩 발견되면 시스템에 의해 차단됩니다.【자동】범위에 추가(정확한 하위 도메인)；이 도구는 다음 작업에 사용됩니다.【주도적으로 확장에 나서세요】——루트 도메인 전체를 넣어주세요/회사 전체가 포함됩니다.，또는 특정 하위 도메인을 추가하세요./IP。\n"+
			"value：company 회사명을 전달하거나 id(회사가 이미 존재해야 합니다.)；root_domain/subdomain 도메인 이름 전달；ip/cidr 합격 IP 또는 네트워크 세그먼트；icp/keyword 등록번호 또는 회사 키워드를 전달하세요.。\n"+
			"꼭 드려요 reason 설명 근거(감사 가능)。다용도 entries 배열。",
		obj(map[string]any{
			"entries": map[string]any{"type": "array", "description": "배치：[{kind, value}]。kind∈company/root_domain/subdomain/ip/cidr/icp/keyword。", "items": map[string]any{"type": "object"}},
			"kind":    str("[싱글] company / root_domain / subdomain / ip / cidr / icp / keyword"),
			"value":   str("[싱글] 회사명 또는id / 도메인 이름 / IP / CIDR / ICP / 키워드"),
			"reason":  str("가입기준(감사용)，꼭 입력해주세요"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil {
				return actool.Errorf("add_task_scope 활성화되지 않음: AssetStore 초기화되지 않음"), nil
			}
			if t.taskID <= 0 {
				return actool.Errorf("add_task_scope 작업 컨텍스트가 필요합니다.(현재 없음 task)"), nil
			}
			type scopeEntry struct {
				Kind  string `json:"kind"`
				Value string `json:"value"`
			}
			var a struct {
				Entries    []scopeEntry `json:"entries"`
				scopeEntry              // 단일 모드
				Reason     string       `json:"reason"`
			}
			_ = json.Unmarshal(in, &a)
			items := a.Entries
			if len(items) == 0 {
				items = []scopeEntry{a.scopeEntry}
			}
			var added []map[string]any
			errs := map[string]string{}
			for i, e := range items {
				ts, err := t.as.AddAgentScope(t.taskID, strings.TrimSpace(e.Kind), e.Value, a.Reason, "agent")
				if err != nil {
					errs[strconv.Itoa(i)] = err.Error()
					continue
				}
				added = append(added, map[string]any{"kind": ts.Kind, "domain": ts.Domain, "net": ts.Net, "value": ts.Value, "company_id": ts.CompanyID})
			}
			out := map[string]any{"added": added}
			if len(errs) > 0 {
				out["errors"] = errs
			}
			return jsonResult(out)
		},
	)
}

// listUntestedAssets lets the plan agent pull the current + directly inherited
// scope's not-yet-tested assets on demand (filter by type, paginated).
func (t *ToolSet) listUntestedAssets() actool.CoreTool {
	return readTool(
		"list_untested_assets",
		"질의【본 업무와 직접적으로 관련된 업무】범위 내、아직 팩트 앵커에 포함되지 않은 자산（연결된 범위는 읽기 전용입니다.，보충검사를 받을지는 본인이 결정하세요，나는 당신을 위해 결정을 내리지 않습니다）。\n"+
			"자산 유형별 선택적 필터링：root_domain/subdomain/service/app/endpoint/ip。\n"+
			"페이징：page 님으로부터 1 이후、page_size 기본값 10。복귀 {assets:[{id,type,label}], total, page, page_size}。작업 컨텍스트에서만 사용 가능。",
		obj(map[string]any{
			"type":      str("자산 유형 필터링（선택사항）：root_domain/subdomain/service/app/endpoint/ip"),
			"page":      intp("페이지 번호，님으로부터 1 이후（기본값 1）"),
			"page_size": intp("페이지당 수량（기본값 10）"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil {
				return actool.Errorf("list_untested_assets 활성화되지 않음: AssetStore 초기화되지 않음"), nil
			}
			if t.taskID <= 0 || t.ts == nil {
				return actool.Errorf("list_untested_assets 작업 컨텍스트가 필요합니다."), nil
			}
			var a struct {
				Type     string `json:"type"`
				Page     int    `json:"page"`
				PageSize int    `json:"page_size"`
			}
			_ = json.Unmarshal(in, &a)
			if a.Page <= 0 {
				a.Page = 1
			}
			if a.PageSize <= 0 {
				a.PageSize = 10
			}
			offset := (a.Page - 1) * a.PageSize
			assets, total, err := t.as.ListUntestedAssetsWithSources(t.taskID, strings.TrimSpace(a.Type), a.PageSize, offset)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return jsonResult(map[string]any{
				"assets": assets, "total": total, "page": a.Page, "page_size": a.PageSize,
			})
		},
	)
}

// listAssets lets an agent query the asset table.
func (t *ToolSet) listAssets() actool.CoreTool {
	return readTool(
		"list_assets",
		"자산 라이브러리 쿼리：DSL 표현 검색，또는 누르세요. id/ids 직접 접속；지원 페이징。반품만 가능【본 업무와 직접적으로 관련된 업무】테스트 범위 내의 자산。\n"+
			"DSL：field=value 흐릿하다(ILIKE) | field==value 정확함 | field!=value 제외 | 숫자 필드 지원 > >= < <= | 벌거벗은 말들=전문이 흐릿하네요；AND/OR 조합(AND 높은 우선순위)，괄호로 그룹화 가능。자산 유형은 독립적입니다. type 매개변수，쓰지 마세요 DSL。\n"+
			"전송되지 않음 id/ids 시간 dsl 비어 있지 않아야 합니다.（무조건 전체 쿼리는 허용되지 않습니다.）。\n"+
			"사용 가능한 필드：domain(루트/서브/서비스 도메인 이름)、root_domain、ip、url、page_title、icp、service_name、app_name、method( GET/POST)、service_type(http|other)、record_type( A/CNAME)、technology(배열，=흐릿하다 ==정확함)、port/status_code/company_id(정수)。\n"+
			"예：status_code>=400 AND technology=shiro ；(port==80 OR port==443) AND technology=nginx",
		obj(map[string]any{
			"dsl":    str(`DSL 쿼리 표현（문법/필드에 대한 도구 설명을 참조하십시오.）。전송되지 않음 id/ids 은 비어 있어서는 안 됩니다.。`),
			"type":   str("자산 유형 필터링：root_domain|ip|subdomain|app|service|endpoint（독립필드，사용 가능 dsl 오버레이；혼자 type 쿼리가 부족합니다.，그래도 필요하다 dsl）"),
			"id":     intp("단일 자산을 직접 클릭하세요. id 받아（선택사항，그리고 dsl/type 상호 배타적）"),
			"ids":    map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "여러 자산을 직접 클릭하세요. id 받아（선택사항，그리고 dsl/type 상호 배타적）"},
			"limit":  intp("반환 상한，기본값 10（선택사항）"),
			"offset": intp("페이징 오프셋，기본값 0（선택사항）"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil {
				return actool.Errorf("list_assets 활성화되지 않음: AssetStore 초기화되지 않음"), nil
			}
			var a struct {
				DSL    string  `json:"dsl"`
				Type   string  `json:"type"`
				ID     int64   `json:"id"`
				IDs    []int64 `json:"ids"`
				Limit  int     `json:"limit"`
				Offset int     `json:"offset"`
			}
			_ = json.Unmarshal(in, &a)
			if a.Limit <= 0 {
				a.Limit = 10
			}

			var assets []*db.Asset
			var err error
			switch {
			case a.ID > 0:
				assets, err = t.as.GetByIDsInScope(t.taskID, []int64{a.ID})
			case len(a.IDs) > 0:
				assets, err = t.as.GetByIDsInScope(t.taskID, a.IDs)
			case a.DSL != "":
				assets, err = t.as.QueryDSLInScope(a.DSL, a.Type, t.taskID, a.Limit, a.Offset)
			default:
				return actool.Errorf("전송되지 않음 id/ids 시간 dsl 은 비워둘 수 없습니다.：모든 자산에 대한 무조건 조회는 허용되지 않습니다.，쿼리 조건을 제공해주세요"), nil
			}
			if err != nil {
				return actool.Errorf("DSL 오류: " + err.Error()), nil
			}
			return jsonResult(map[string]any{
				"count":  len(assets),
				"assets": assets,
			})
		},
	)
}

// listCompanies lets an agent enumerate companies (기업) with their scope + asset count.
func (t *ToolSet) listCompanies() actool.CoreTool {
	return readTool(
		"list_companies",
		"자산 라이브러리의 자산 나열【기업/회사】및 해당 자산 범위(scope)및 귀속자산수。어느 회사인지 확인하는데 사용됩니다.、"+
			"알겠습니다 company_id（insert_assets 관련 app、list_assets 언론 company_id 필터링 시 사용）。"+
			"선택사항 search 회사 이름으로 퍼지 필터(대소문자를 구분하지 않습니다.)，모두 돌아가려면 공백으로 남겨두세요.。",
		obj(map[string]any{
			"search": str("회사 이름으로 퍼지 필터(선택사항，대소문자를 구분하지 않습니다.)；모두 돌아가려면 공백으로 남겨두세요."),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.cs == nil {
				return actool.Errorf("list_companies 활성화되지 않음: CompanyStore 초기화되지 않음"), nil
			}
			var a struct {
				Search string `json:"search"`
			}
			_ = json.Unmarshal(in, &a)
			cos, err := t.cs.ListCompanies()
			if err != nil {
				return actool.Errorf("회사에 문의하지 못했습니다.: " + err.Error()), nil
			}
			q := strings.ToLower(strings.TrimSpace(a.Search))
			type companyOut struct {
				ID         int64    `json:"id"`
				Name       string   `json:"name"`
				AssetCount int      `json:"asset_count"`
				Scope      []string `json:"scope"`
			}
			out := make([]companyOut, 0, len(cos))
			for _, c := range cos {
				if q != "" && !strings.Contains(strings.ToLower(c.Name), q) {
					continue
				}
				scope := make([]string, 0, len(c.Scope))
				for _, r := range c.Scope {
					scope = append(scope, r.Raw)
				}
				out = append(out, companyOut{ID: c.ID, Name: c.Name, AssetCount: c.AssetCount, Scope: scope})
			}
			return jsonResult(map[string]any{"count": len(out), "companies": out})
		},
	)
}

// splitLines splits a multi-line string into non-empty trimmed lines.
func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// WorkerTools returns the tool set for a work agent.
func (t *ToolSet) WorkerTools() []actool.CoreTool {
	return []actool.CoreTool{
		// list_findings 예약됨：취약점을 보고하기 전에 이 작업을 확인하여 취약점을 확인하십시오.，동일한 취약점을 반복적으로 보고하지 마세요.。
		t.listFindings(),
		t.addFinding(), t.recordFact(),
		// asset management (handlers guard nil store internally)。
		// add_company_scope 아니요 worker：기업 자산의 범위를 정의하는 것은 계획에 속합니다./마스터 컨트롤/Auto 책임，worker 탐색만 실행。
		t.insertAssets(), t.listAssets(),
		// 크로스 work 검토：worker 다른 것을 재사용할 수도 있습니다. work 님의 관찰，업무 중복 방지。
		// search_all_worker_traces：먼저 알 필요는 없습니다 intent_id，키워드별로 전 세계적으로 히트를 찾는 단계；
		// get_worker_trace：항목 잠그기 work 다음 단계/지역 검색/전체 콘텐츠 받기。
		t.searchAllWorkerTraces(), t.getWorkerTrace(),
		// node_detail：worker 알겠습니다 intent_id/노드 id 노드의 전체 세부정보는 나중에 확인할 수 있습니다.（위 리뷰에 협조해주세요）。
		t.nodeDetail(),
		// 다음 도구는 아직【아니요】worker，이제 남은 건 planner/main（컨텍스트 읽기、크로스 work 리뷰는 기획의 몫，
		// worker 단 하나의 의도만 실행하고 다시 작성하세요.）：list_facts / list_companies / list_worker_traces。
	}
}

// MainAgentTools returns the human-interface tool set.
func (t *ToolSet) MainAgentTools() []actool.CoreTool {
	return []actool.CoreTool{
		t.graphOverview(), t.listFindings(), t.listFacts(), t.nodeDetail(),
		t.expandDigest(), // cold-digest §6.1
		t.getWorkerOutput(), t.getWorkerTrace(), t.searchAllWorkerTraces(), t.addHint(), t.addIntent(),
		// steer_work：사람은 달리는 의도를 통제할 수 있다(work)수정지침 실시간 주입（방해하지 마세요、진행 상황을 놓치지 마세요）。
		t.steerWorkTool(),
		// set_goals：사람들은 런타임에 이 작업에 새로운 최종 목표를 추가할 수 있습니다.（이를 토대로 기획자가 새로운 판단을 내렸는지 여부）。
		t.setGoals(),
		// set_constraints：사람들은 런타임 중에 이 작업을 추가할 수 있습니다./작업 제약 조건 변경（allow/deny），제약 planner/worker 의 탐색 경계。
		t.setConstraints(),
		// asset management (handlers guard nil store internally)
		t.insertAssets(), t.addCompanyScope(), t.listAssets(),
		t.addFinding(), t.recordFact(),
		t.addTaskScope(),
		// list_untested_assets：요청 시 본 업무 범위 내 미측정 자산 확인(유형+페이징)，스스로 결정하여 보충검사를 받아보세요。
		t.listUntestedAssets(),
	}
}

// AllDomainTools returns the union of all domain tools across all agent types,
// deduped by name (mainagent order wins). Used by the server to build a registry
// for injecting domain tools into agents (Auto, custom) that don't own a per-task
// ToolSet. The caller provides real stores; tools are callable at taskID=0 scope.
func (t *ToolSet) AllDomainTools() []actool.CoreTool {
	seen := map[string]bool{}
	var out []actool.CoreTool
	all := append(append(t.MainAgentTools(), t.PlannerTools()...), t.WorkerTools()...)
	for _, tool := range all {
		if !seen[tool.Name()] {
			seen[tool.Name()] = true
			out = append(out, tool)
		}
	}
	return out
}
