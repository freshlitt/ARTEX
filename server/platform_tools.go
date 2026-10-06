package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

// 플랫폼 운영 도구(내장 제공 Auto agent 사용):빌드/변경 skill、사용자 정의 도구、MCP。모두 host 도구,
// seed 들어가세요 tools 테이블、기본 바인딩 auto, hostTools 주사。기존 재사용 db/파일 시스템 로직。

func (s *Server) platformTools() []actool.CoreTool {
	return []actool.CoreTool{
		s.toolCreateSkill(),
		s.toolUpdateSkillFile(),
		s.toolCreateCustomTool(),
		s.toolUpdateCustomTool(),
		s.toolCreateMCP(),
		s.toolUpdateMCP(),
		s.toolDeleteAssetsByHost(),
	}
}

// platformToolKeys are the tool keys the Auto agent gets bound by default.
var platformToolKeys = []string{
	"create_skill", "update_skill_file",
	"create_custom_tool", "update_custom_tool",
	"create_mcp", "update_mcp",
	"delete_assets_by_host",
}

// ---- assets ----

// toolDeleteAssetsByHost hard-deletes every asset tied to one host (exact match).
// Platform-level (not a per-task tool): operates on the global, cross-task asset도서관.
func (s *Server) toolDeleteAssetsByHost() actool.CoreTool {
	return wrTool("delete_assets_by_host",
		"언론 host 자산을 정확하게 삭제하세요.：삭제하세요 host 의 도메인 이름/하위 도메인 이름，및 그에 따른 서비스(service)、인터페이스(endpoint)。\n"+
			"host 정확히 일치(소문자、공백을 제거하세요.)，흐릿하지 않게/와일드카드。\n"+
			"루트 도메인 이름을 전달하세요.( example.com)은 하위 도메인 이름과 서비스도 삭제합니다./인터페이스；하위 도메인 이름 전달( a.example.com)또는 IP 이것만 삭제하세요 host 자체 및 서비스/인터페이스。\n"+
			"⚠️ 완전 삭제、글로벌 자산 라이브러리에 대한 작업(업무 간 공유)、취소불가。",
		objSchema(map[string]any{
			"host": strParam("삭제 예정 host：도메인 이름/하위 도메인 이름/IP。정확히 일치， example.com 또는 a.example.com 또는 1.2.3.4"),
		}, "host"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			as := s.assetStore()
			if as == nil {
				return actool.Errorf("자산 라이브러리가 초기화되지 않았습니다."), nil
			}
			var a struct {
				Host string `json:"host"`
			}
			_ = json.Unmarshal(in, &a)
			if strings.TrimSpace(a.Host) == "" {
				return actool.Errorf("host 은 비워둘 수 없습니다."), nil
			}
			counts, err := as.DeleteByHost(a.Host)
			if err != nil {
				return actool.Errorf("삭제 실패: " + err.Error()), nil
			}
			var total int64
			for _, n := range counts {
				total += n
			}
			return jsonResult(map[string]any{
				"host":            a.Host,
				"deleted":         total,
				"deleted_by_type": counts,
			})
		})
}

// ---- skills ----

func (s *Server) toolCreateSkill() actool.CoreTool {
	return wrTool("create_skill",
		"새로 만들기 skill(쓰기 SKILL.md，agentskills.io 사양)。name 소문자/번호/하이픈。",
		objSchema(map[string]any{
			"name":         strParam("skill 이름(소문자로 시작，편지/번호/하이픈)"),
			"description":  strParam("skill 설명(필수，그것이 무엇인지 설명해주세요/언제 사용하나요?)"),
			"instructions": strParam("Markdown 텍스트 설명(선택사항)"),
		}, "name", "description"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct{ Name, Description, Instructions string }
			_ = json.Unmarshal(in, &a)
			if !validSkillName(a.Name) {
				return actool.Errorf("skill 이름이 불법입니다(소문자로 시작，문자로만 가능/번호/하이픈，≤64)"), nil
			}
			if strings.TrimSpace(a.Description) == "" {
				return actool.Errorf("description 필수"), nil
			}
			path := filepath.Join(s.skillDir, a.Name)
			if _, err := os.Stat(path); err == nil {
				return actool.Errorf("skill 이(가) 이미 존재합니다.: " + a.Name), nil
			}
			if err := os.MkdirAll(path, 0o755); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			var b strings.Builder
			b.WriteString("---\n")
			fmt.Fprintf(&b, "name: %s\n", a.Name)
			fmt.Fprintf(&b, "description: %s\n", a.Description)
			b.WriteString("---\n")
			if strings.TrimSpace(a.Instructions) != "" {
				b.WriteString(a.Instructions)
			} else {
				fmt.Fprintf(&b, "## %s\n\n1. \n2. \n3. \n", a.Name)
			}
			if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(b.String()), 0o644); err != nil {
				_ = os.RemoveAll(path)
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text("skill created: " + a.Name), nil
		})
}

func (s *Server) toolUpdateSkillFile() actool.CoreTool {
	return wrTool("update_skill_file",
		"쓰기/특정 내용을 커버 skill 내의 파일(기본값 SKILL.md)。스킬 내용 수정이나 스크립트 추가에 사용됩니다./견적。",
		objSchema(map[string]any{
			"name":    strParam("skill 이름"),
			"file":    strParam("상대 경로(선택사항，기본값 SKILL.md， scripts/run.py)"),
			"content": strParam("파일 전체 내용"),
		}, "name", "content"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct{ Name, File, Content string }
			_ = json.Unmarshal(in, &a)
			if !validSkillName(a.Name) {
				return actool.Errorf("skill 이름이 불법입니다"), nil
			}
			skillPath := filepath.Join(s.skillDir, a.Name)
			if _, err := os.Stat(skillPath); os.IsNotExist(err) {
				return actool.Errorf("skill 이 존재하지 않습니다: " + a.Name), nil
			}
			rel := strings.TrimSpace(a.File)
			if rel == "" {
				rel = "SKILL.md"
			}
			clean, msg := skillRelPath(rel)
			if msg != "" {
				return actool.Errorf("잘못된 경로: " + msg), nil
			}
			full := filepath.Join(skillPath, clean)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if err := os.WriteFile(full, []byte(a.Content), 0o644); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text("skill file written: " + a.Name + "/" + clean), nil
		})
}

// ---- custom tools ----

type customToolToolInput struct {
	Key         string          `json:"key"`
	Description string          `json:"description"`
	Kind        string          `json:"kind"`
	Exec        json.RawMessage `json:"exec"`
	Schema      json.RawMessage `json:"schema"`
	Agents      []string        `json:"agents"`
	Deferred    bool            `json:"deferred"`
	Enabled     *bool           `json:"enabled"`
}

func customToolSchema(keyDesc string) map[string]any {
	return objSchema(map[string]any{
		"key":         strParam(keyDesc),
		"description": strParam("모델에게 보낸 설명"),
		"kind":        strParam("shell | command | script(만Python) | http。shell=bash 환경 선언문(도구를 사용할 수 있다는 사실만 모델에 알립니다. bash 으로 직접 전화주세요，필요없어요 exec/schema)；나머지 3가지 유형을 제공해야 합니다. exec"),
		"exec":        map[string]any{"type": "object", "description": "실행 사양(shell 유형은 필요하지 않습니다.): command→{command}; script→{code}; http→{method,url,headers,body,proxy,use_recording_proxy}"},
		"schema":      map[string]any{"type": "object", "description": "매개변수 JSON-Schema(shell/command/script 비워둘 수 있습니다.; http 필수 및 포함됨 properties)"},
		"agents":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "바운드 agent key(선택사항)"},
		"deferred":    map[string]any{"type": "boolean", "description": "늦어졌나요?(shell 잘못된 유형；만 command/script/http 의 흔하지 않은 도구만 열어보세요.)"},
		"enabled":     map[string]any{"type": "boolean", "description": "활성화되어 있나요?(기본값 true)"},
	}, "key", "kind")
}

func toDBTool(a customToolToolInput) *db.Tool {
	enabled := true
	if a.Enabled != nil {
		enabled = *a.Enabled
	}
	return &db.Tool{
		Key: a.Key, Description: a.Description, Schema: a.Schema, Agents: a.Agents,
		Enabled: enabled, Kind: a.Kind, Exec: a.Exec, Deferred: a.Deferred,
	}
}

func (s *Server) toolCreateCustomTool() actool.CoreTool {
	return wrTool("create_custom_tool", "【중요】플랫폼에서 사용할 수 없는 일부 도구를 설치할 때，설치된 도구를 플랫폼에 넣으려면 이 도구를 호출하십시오.，플랫폼을 호출 가능하게 만듭니다.！사용자 정의 도구 만들기(shell/command/script/http)。shell=bash 환경 선언문，그냥 key+description+agents，필요없어요 exec/schema。",
		customToolSchema("도구 key(소문자로 시작，편지/번호/밑줄)"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a customToolToolInput
			_ = json.Unmarshal(in, &a)
			a.Key = strings.TrimSpace(a.Key)
			if !reToolKey.MatchString(a.Key) {
				return actool.Errorf("key 소문자로 시작해야 합니다.，소문자만 가능/번호/밑줄"), nil
			}
			if a.Kind != "shell" && a.Kind != "command" && a.Kind != "script" && a.Kind != "http" {
				return actool.Errorf("kind 해야지 shell / command / script / http"), nil
			}
			if a.Kind == "http" && !hasSchemaProps(a.Schema) {
				return actool.Errorf("http 도구는 매개변수를 제공해야 합니다. JSON Schema(비워둘 수 없습니다.)"), nil
			}
			if exist, _ := s.m.pg.GetTool(a.Key); exist != nil {
				return actool.Errorf("그게 key 이(가) 이미 존재합니다.: " + a.Key), nil
			}
			if err := s.m.pg.CreateCustomTool(toDBTool(a)); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text("custom tool created: " + a.Key), nil
		})
}

func (s *Server) toolUpdateCustomTool() actool.CoreTool {
	return wrTool("update_custom_tool", "기존 사용자 정의 도구 수정(언론 key)。",
		customToolSchema("수정될 사용자 정의 도구 key"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a customToolToolInput
			_ = json.Unmarshal(in, &a)
			existing, _ := s.m.pg.GetTool(a.Key)
			if existing == nil || existing.System {
				return actool.Errorf("사용자 정의 도구만 수정할 수 있습니다.: " + a.Key), nil
			}
			if a.Kind != "shell" && a.Kind != "command" && a.Kind != "script" && a.Kind != "http" {
				return actool.Errorf("kind 해야지 shell / command / script / http"), nil
			}
			if a.Kind == "http" && !hasSchemaProps(a.Schema) {
				return actool.Errorf("http 도구는 매개변수를 제공해야 합니다. JSON Schema(비워둘 수 없습니다.)"), nil
			}
			if err := s.m.pg.UpdateCustomTool(toDBTool(a)); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text("custom tool updated: " + a.Key), nil
		})
}

// ---- MCP ----

type mcpToolInput struct {
	ID        int64           `json:"id"`
	Name      string          `json:"name"`
	Transport string          `json:"transport"`
	Command   string          `json:"command"`
	Args      json.RawMessage `json:"args"`
	Env       json.RawMessage `json:"env"`
	URL       string          `json:"url"`
	Enabled   *bool           `json:"enabled"`
	Insecure  *bool           `json:"insecure"`
}

func mcpSchema(withID bool) map[string]any {
	props := map[string]any{
		"name":      strParam("MCP 서버 이름"),
		"transport": strParam("stdio | http / sse"),
		"command":   strParam("stdio 의 시작 명령( npx)"),
		"args":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "명령 매개변수 배열"},
		"env":       map[string]any{"type": "object", "description": "환경변수 {KEY:VALUE}"},
		"url":       strParam("http/sse 님 URL"),
		"enabled":   map[string]any{"type": "boolean", "description": "활성화되어 있나요?(기본값 true)"},
		"insecure":  map[string]any{"type": "boolean", "description": "http: 건너뛰기 TLS 인증서 확인(자체 서명된 인증서 시간 설정 true, 기본값 false)"},
	}
	required := []string{"name", "transport"}
	if withID {
		props["id"] = map[string]any{"type": "integer", "description": "수정 예정 MCP 서버 id"}
		required = []string{"id", "name", "transport"}
	}
	return objSchema(props, required...)
}

func (a mcpToolInput) toDB() *db.MCPServer {
	enabled := true
	if a.Enabled != nil {
		enabled = *a.Enabled
	}
	insecure := false
	if a.Insecure != nil {
		insecure = *a.Insecure
	}
	return &db.MCPServer{
		ID: a.ID, Name: a.Name, Transport: a.Transport, Command: a.Command,
		Args: a.Args, Env: a.Env, URL: a.URL, Enabled: enabled, Insecure: insecure,
	}
}

func (s *Server) toolCreateMCP() actool.CoreTool {
	return wrTool("create_mcp", "하나 만들기 MCP 서버(stdio/http/sse)。도구를 만든 후 다음을 눌러야 합니다. agent 공개 승인。",
		mcpSchema(false),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a mcpToolInput
			_ = json.Unmarshal(in, &a)
			a.ID = 0
			if strings.TrimSpace(a.Name) == "" || strings.TrimSpace(a.Transport) == "" {
				return actool.Errorf("name / transport 필수"), nil
			}
			id, err := s.m.pg.SaveMCP(a.toDB())
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text(fmt.Sprintf("mcp created: id=%d name=%s", id, a.Name)), nil
		})
}

func (s *Server) toolUpdateMCP() actool.CoreTool {
	return wrTool("update_mcp", "기존 항목 수정 MCP 서버(언론 id)。",
		mcpSchema(true),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a mcpToolInput
			_ = json.Unmarshal(in, &a)
			if a.ID == 0 {
				return actool.Errorf("id 필수"), nil
			}
			if _, err := s.m.pg.SaveMCP(a.toDB()); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text(fmt.Sprintf("mcp updated: id=%d", a.ID)), nil
		})
}
