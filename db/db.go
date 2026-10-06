// Package db is the PostgreSQL data source for ARTEX (이 이전 버전을 대체합니다. graph 단일 파일 SQLite)。
// 연결이 열립니다、신청 schema、그리고 seed 내장 agent 및 변수 디렉토리。
package db

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Autumn-27/artex/config"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" // pgx database/sql driver ("pgx")
)

//go:embed schema.sql
var schemaSQL string

const schemaMigrationLockKey int64 = 7337741001

var schemaDeadlockRetryDelays = [...]time.Duration{
	100 * time.Millisecond,
	250 * time.Millisecond,
	500 * time.Millisecond,
	time.Second,
}

type schemaExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func isPostgresDeadlock(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40P01"
}

func applySchemaWithRetry(ctx context.Context, execer schemaExecer, sleep func(time.Duration)) error {
	for attempt := 0; ; attempt++ {
		if _, err := execer.ExecContext(ctx, schemaSQL); err != nil {
			if !isPostgresDeadlock(err) || attempt >= len(schemaDeadlockRetryDelays) {
				return err
			}
			sleep(schemaDeadlockRetryDelays[attempt])
			continue
		}
		return nil
	}
}

// withSchemaMigrationLock pins the session-level lock to one checked-out
// connection. Running pg_advisory_lock through *sql.DB is incorrect because a
// later schema or unlock call may use a different pooled PostgreSQL session.
func withSchemaMigrationLock(ctx context.Context, sqlDB *sql.DB, action func(*sql.Conn) error) (err error) {
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, schemaMigrationLockKey); err != nil {
		return fmt.Errorf("advisory lock: %w", err)
	}
	defer func() {
		if _, unlockErr := conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, schemaMigrationLockKey); unlockErr != nil && err == nil {
			err = fmt.Errorf("advisory unlock: %w", unlockErr)
		}
	}()
	return action(conn)
}

// coordinateWithSchemaMigration makes long, multi-table archive transactions
// mutually exclusive with startup DDL while allowing ordinary runtime queries
// to continue normally.
func coordinateWithSchemaMigration(tx *sql.Tx) error {
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1)`, schemaMigrationLockKey); err != nil {
		return fmt.Errorf("coordinate with schema migration: %w", err)
	}
	return nil
}

// DSN resolves the PostgreSQL connection string and reports where it came from.
// Precedence: env ARTEX_PG_DSN > config file (config.json). There is no
// built-in default — it errors if neither source is configured.
func DSN() (dsn, source string, err error) {
	return config.PostgresDSN()
}

// DB wraps the shared *sql.DB. PG handles its own connection pool + concurrency
// (MVCC), so unlike the old SQLite store there is no process-wide write mutex.
type DB struct{ *sql.DB }

// ensureDatabase connects to the postgres system database and creates the target
// database if it does not exist. dsn must be a postgres:// URL.
func ensureDatabase(dsn string) error {
	u, err := url.Parse(dsn)
	if err != nil {
		return nil // unparseable DSN — let the normal Open fail with a clear error
	}
	dbName := strings.TrimPrefix(u.Path, "/")
	if dbName == "" || dbName == "postgres" {
		return nil
	}
	// connect to the postgres maintenance database instead
	adminDSN := *u
	adminDSN.Path = "/postgres"
	admin, err := sql.Open("pgx", adminDSN.String())
	if err != nil {
		return nil // best-effort; let Open surface the real error
	}
	defer admin.Close()
	if err := admin.Ping(); err != nil {
		return nil
	}
	var exists bool
	_ = admin.QueryRow(`SELECT true FROM pg_database WHERE datname=$1`, dbName).Scan(&exists)
	if !exists {
		if _, err := admin.Exec(`CREATE DATABASE "` + dbName + `"`); err != nil {
			return fmt.Errorf("create database %q: %w", dbName, err)
		}
	}
	return nil
}

// Open connects, applies the schema (idempotent), and seeds builtin rows.
func Open(dsn string) (*DB, error) {
	if err := ensureDatabase(dsn); err != nil {
		return nil, err
	}
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("ping postgres (%s): %w", config.Redact(dsn), err)
	}
	d := &DB{sqlDB}
	// pgx runs multi-statement Exec via the simple protocol when there are no args.
	// Keep the dedicated lock connection checked out until both DDL and seeding
	// finish so concurrent application instances cannot initialize out of order.
	err = withSchemaMigrationLock(context.Background(), sqlDB, func(conn *sql.Conn) error {
		if err := applySchemaWithRetry(context.Background(), conn, time.Sleep); err != nil {
			return fmt.Errorf("apply schema: %w", err)
		}
		if err := d.seedBuiltins(); err != nil {
			return fmt.Errorf("seed builtins: %w", err)
		}
		return nil
	})
	if err != nil {
		sqlDB.Close()
		return nil, err
	}
	return d, nil
}

// builtinAgent describes one of the fixed agents and its prompt-variable catalog.
type builtinAgent struct {
	key, name, role, desc string
	vars                  []promptVar
	interactiveShell      bool // CCB 중 기본 대화형 shell 스위치；ON CONFLICT 사용자의 후속 수동 전환에는 적용되지 않습니다.
	runSeconds            *int // CCB 1회 run 벽시계 상한(초)；nil=기본적으로 시드 사용(1200)，0=시간 제한 없음
}

type promptVar struct{ name, desc, example, source string }

// intp 복귀 v 에 대한 포인터，는 다음과 같이 사용됩니다. builtinAgent 선택항목( runSeconds)명시적 값。
func intp(v int) *int { return &v }

// builtinAgents mirrors docs §5(a). 내장 도구는 라이브러리에 포함되어 있지 않습니다.；여기에서만 seed agent + 가변 디렉터리。
// 참고：planner/worker/mainagent/auto 의 인터랙티브 shell 기본값은 아래부터 interactive_shell_default_v1
// 차단 통합설정 true（후속 조치를 존중합니다. toggle）；여기요 interactiveShell 필요할 때만 줘「CCB는 기본적으로 이를 엽니다.」님의 새로운 agent。
var builtinAgents = []builtinAgent{
	{"goals", "타겟 분해", "goals", "침투임무 목표를 여러 독립된 항목으로 세분화、검증 가능한 하위 목표。", []promptVar{
		{"EngagementDescription", "작업 설명（테스트 대상/배경）", "테스트 example.com 사이트", "exploration"},
		// Now 은 글로벌입니다 runtime 가변적(또 만나요 server.globalPromptVars),각각에 더 이상 없음 agent 디렉토리에
		// 정의가 중복되었습니다.,그렇지 않으면 withGlobalVars 추가 시 이름이 전역 항목과 충돌합니다.。
	}, false, nil},
	{"planner", "기획", "planner", "상황 읽기、목표를 정하라，다루지 않은 새로운 방향이 있는 경우에만 탐색 의도를 추가하세요.（작업당 하나의 계획 주기）。", []promptVar{
		{"Goal", "전체 미션 목표", "받아가세요 example.com 에 대한 관리자 권한", "exploration"},
		{"AssetSummary", "자산수/유형 분포 요약(선택사항)", "domain:3 ip:5 site:2", "distilled"},
	}, false, nil},
	{"mainagent", "스승님", "main", "인간-컴퓨터 인터페이스：진행상황을 관찰하세요，사람의 뜻을 이루어주라 hint 또는 우선순위가 높은 의도。", []promptVar{
		{"Goal", "현재 임무 목표", "받아가세요 example.com 에 대한 관리자 권한", "exploration"},
		{"AssetSummary", "개통상황 요약(선택사항)", "domain:3 ip:5", "distilled"},
		{"FindingsSummary", "확인된 취약점 요약(선택사항)", "high:1 medium:2", "distilled"},
	}, false, nil},
	{"worker", "실행", "worker", "의도 실행을 수신，발견한 사실을 올려보세요/취약점이 지식 그래프에 다시 기록된 후 중지。", []promptVar{
		{"ProxyAddr", "프록시 주소 기록(운전사 if 이중 카피 라이팅)", "127.0.0.1:8080", "runtime"},
		{"WorkerName", "worker 본인확인(선택사항)", "worker-1", "runtime"},
	}, false, nil},
	// Auto:내장「플랫폼 운영」agent。침투 조정 주기에 참여하지 않음,대화페이지에 이끌려,도구를 사용하여 플랫폼을 운영합니다.。
	{"auto", "Auto", "assistant", "플랫폼 운영보조：도구를 사용하여 작업 관리(빌드/보세요/잠시 멈춤/팁을 주세요)자산 포함，을 생성할 수 있습니다./수정 skill、사용자 정의 도구、MCP。", nil, false, nil},
	// 침투 테스트:내장「독립적 침투」agent。대화페이지에 이끌려,한 사람이 정찰부터 폐쇄까지 전체 침투 체인을 완료합니다.,스스로 계획하고 실행하고 스스로 검증하라。대화형은 기본적으로 활성화되어 있습니다. shell。
	{"pentest", "침투 테스트", "assistant", "독립적 침투 agent：정찰대원 1명→공격 표면 찾기→심층 활용→확인→체인 전체를 마무리하세요，직접 계획해보세요、직접 실행해 보세요、자기적대적 검증。", nil, true, intp(0)},
}

// seedBuiltins inserts the fixed built-in agents and their variable catalog (idempotent).
func (d *DB) seedBuiltins() error {
	for _, a := range builtinAgents {
		var agentID int64
		err := d.QueryRow(`
INSERT INTO agents(key, name, description, role, builtin, enabled, interactive_shell, run_seconds)
VALUES ($1, $2, NULLIF($3,''), $4, true, true, $5, COALESCE($6, 1200))
ON CONFLICT (key) DO UPDATE SET name = EXCLUDED.name, description = EXCLUDED.description
RETURNING id`, a.key, a.name, a.desc, a.role, a.interactiveShell, a.runSeconds).Scan(&agentID)
		if err != nil {
			return fmt.Errorf("agent %s: %w", a.key, err)
		}
		for _, v := range a.vars {
			if _, err := d.Exec(`
INSERT INTO agent_prompt_vars(agent_id, var_name, description, example, source)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (agent_id, var_name) DO UPDATE
  SET description = EXCLUDED.description, example = EXCLUDED.example, source = EXCLUDED.source`,
				agentID, v.name, v.desc, v.example, v.source); err != nil {
				return fmt.Errorf("agent %s var %s: %w", a.key, v.name, err)
			}
		}
	}
	// Drop catalog entries for variables that were renamed, so the white-list no
	// longer advertises a name templates can't resolve (EngagementTitle→Description).
	// 'Now' 각각에서 agent 디렉터리를 전역으로 승격 runtime 변수 뒤,오래된 도서관 goals 아직 한개 남았네요 'Now'
	// 은 전역 항목 이름과 충돌합니다.(프론트엔드 변수 목록 key 반복);다 같이 지워보세요。
	if _, err := d.Exec(`DELETE FROM agent_prompt_vars WHERE var_name IN ('EngagementTitle', 'CoverageGaps', 'Now')`); err != nil {
		return fmt.Errorf("cleanup renamed vars: %w", err)
	}
	// Default-on interactive_shell for the runtime agents (planner/worker/mainagent/auto)
	// ONCE — respects a later user toggle-off (guarded by a settings flag). goals(one-shot
	// decomposer) stays off. Runs after the column exists (schema applied before seed).
	if v, _, _ := d.GetSetting("interactive_shell_default_v1"); v != "true" {
		if _, err := d.Exec(`UPDATE agents SET interactive_shell=true WHERE key IN ('planner','worker','mainagent','auto')`); err != nil {
			return fmt.Errorf("seed interactive_shell defaults: %w", err)
		}
		_ = d.SetSetting("interactive_shell_default_v1", "true")
	}
	// Seed the built-in browser (Playwright) MCP once — DISABLED by default (사용자
	// 필요할 때 활성화하세요.), no proxy by default. The traffic-capture toggle injects/strips
	// the recording proxy + CA at runtime (server.Manager.syncBrowserMCPProxy).
	// Insert only if absent so we never clobber user edits (args/env/enabled/
	// visibility) on restart.
	if _, err := d.Exec(`
INSERT INTO mcp_servers(name, transport, command, args, env, enabled)
VALUES ('browser', 'stdio', 'npx', $1, '{}', false)
ON CONFLICT (name) DO NOTHING`,
		`["@playwright/mcp","--headless"]`); err != nil {
		return fmt.Errorf("seed browser mcp: %w", err)
	}
	// NOTE: the placeholder ScopeSentry data-source MCP (empty URL + empty X-API-Key,
	// disabled) is seeded directly in schema.sql §F so a raw `psql < schema.sql` init
	// also gets it. schema.sql is Exec'd on every startup, so it stays idempotent.
	if err := d.seedBuiltinSkillVisibility(); err != nil {
		return fmt.Errorf("seed skill visibility: %w", err)
	}
	if err := d.seedDefaultInterceptRules(); err != nil {
		return fmt.Errorf("seed intercept rules: %w", err)
	}
	if err := d.seedDefaultInterceptRulesV2(); err != nil {
		return fmt.Errorf("seed intercept rules v2: %w", err)
	}
	if err := d.seedDefaultInterceptRulesV3(); err != nil {
		return fmt.Errorf("seed intercept rules v3: %w", err)
	}
	if err := d.seedDefaultAssetInterceptRules(); err != nil {
		return fmt.Errorf("seed asset intercept rules: %w", err)
	}
	return nil
}

// seedDefaultAssetInterceptRules inserts the built-in asset blocklist (fuzzy
// domain matches for government / education sites) once on first startup. Gated
// by a settings flag so a user's later disable/delete is never resurrected on
// restart — same policy as the intercept-rule seed.
func (d *DB) seedDefaultAssetInterceptRules() error {
	if v, _, _ := d.GetSetting("asset_intercept_default_rules_v1"); v == "done" {
		return nil
	}
	rules := []struct {
		kind    string
		pattern string
		note    string
	}{
		{"fuzzy_domain", ".gov", "[내장] 정부 홈페이지 (.gov)"},
		{"fuzzy_domain", ".gov.cn", "[내장] 정부 홈페이지 (.gov.cn)"},
		{"fuzzy_domain", ".edu", "[내장] 교육 홈페이지 (.edu)"},
		{"fuzzy_domain", ".edu.cn", "[내장] 교육 홈페이지 (.edu.cn)"},
	}
	for _, r := range rules {
		if _, err := d.Exec(`
INSERT INTO asset_intercept_rules(enabled, kind, pattern, note, builtin)
VALUES (true, $1, $2, $3, true)
ON CONFLICT DO NOTHING`, r.kind, r.pattern, r.note); err != nil {
			return fmt.Errorf("asset rule %q: %w", r.pattern, err)
		}
	}
	return d.SetSetting("asset_intercept_default_rules_v1", "done")
}

// builtinSkillVisibility maps a shipped skill's directory name → the built-in
// agent keys that should see it by default. The skill FILES themselves live on the
// filesystem (SkillDir, loaded by norma at runtime); DB only carries this visibility
// binding. Skills omitted here (e.g. playwright-cli, scopesentry) ship invisible by
// default — the user turns them on per-agent when needed. scopesentry additionally
// declares `mcps: ScopeSentry`, which only takes effect once it's made visible and
// that MCP is enabled/configured.
var builtinSkillVisibility = map[string][]string{
	"api-recon": {"auto", "pentest", "worker"},
}

// seedBuiltinSkillVisibility binds the shipped built-in skills to their default
// agents. Insert-if-absent (ON CONFLICT DO NOTHING) so a user's later toggle-off is
// never resurrected on restart — matches the browser-MCP / intercept-rule seed policy.
func (d *DB) seedBuiltinSkillVisibility() error {
	for skillName, agentKeys := range builtinSkillVisibility {
		for _, key := range agentKeys {
			if _, err := d.Exec(`
INSERT INTO agent_skill_visibility(agent_id, skill_name, enabled)
SELECT id, $2, true FROM agents WHERE key=$1
ON CONFLICT (agent_id, skill_name) DO NOTHING`, key, skillName); err != nil {
				return fmt.Errorf("skill %s → agent %s: %w", skillName, key, err)
			}
		}
	}
	return nil
}

// seedDefaultInterceptRules inserts built-in safety intercept rules once on
// first startup. The seed is gated by a settings flag so user edits (disable,
// delete, re-order) are never overwritten on subsequent restarts.
func (d *DB) seedDefaultInterceptRules() error {
	if v, _, _ := d.GetSetting("intercept_default_rules_v1"); v == "done" {
		return nil
	}
	type rule struct {
		name     string
		target   string // tool_name | tool_input
		typ      string // string | regex
		pattern  string
		action   string
		message  string
		priority int
	}
	rules := []rule{
		// ── 시스템 파괴 명령 (priority 100) ──────────────────────────────────
		{
			name:     "[내장] 재귀적 강제삭제 rm -rf",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\brm\b.{0,80}(?:-[a-z]*r[a-z]*f[a-z]*|-[a-z]*f[a-z]*r[a-z]*|--recursive|--no-preserve-root)`,
			action:   "deny",
			message:  "재귀강제삭제 금지（rm -rf / rm --recursive），시스템이나 드론 환경에 영구적인 손상을 줄 수 있음",
			priority: 100,
		},
		{
			name:     "[내장] 시스템 키 디렉터리 삭제",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\brm\b[^"'\n]{0,60}["'\s](/|/etc|/bin|/usr|/boot|/var|/lib|/sys|/proc|/dev|/sbin|/root)`,
			action:   "deny",
			message:  "시스템 중요 경로 삭제 비활성화",
			priority: 100,
		},
		{
			name:     "[내장] 디스크 포맷 mkfs",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\bmkfs\b`,
			action:   "deny",
			message:  "디스크 포맷 금지（mkfs）",
			priority: 100,
		},
		{
			name:     "[내장] 디스크 장치 덮어쓰기 dd",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\bdd\b[^|\n]{0,100}\bof=\s*/dev/[a-zA-Z]`,
			action:   "deny",
			message:  "사용금지 dd 디스크 장치 덮어쓰기",
			priority: 100,
		},
		{
			name:     "[내장] Fork 폭탄",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `:\(\)\s*\{[^}]*:\|:`,
			action:   "deny",
			message:  "실행금지 Fork 폭탄",
			priority: 100,
		},
		{
			name:     "[내장] 종료하세요 / 다시 시작",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\b(?:shutdown|reboot|halt|poweroff|init\s+[06])\b`,
			action:   "deny",
			message:  "종료 또는 다시 시작 명령 실행 금지",
			priority: 100,
		},
		{
			name:     "[내장] 모든 프로세스 종료",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\bkill\s+-9\s+-1\b|\bkillall\s+-9\b`,
			action:   "deny",
			message:  "금지됨 kill -9 -1 또는 killall -9（모든 프로세스 종료）",
			priority: 100,
		},
		{
			name:     "[내장] 디스크 지우기 shred / wipe",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\b(?:shred|wipe)\b[^|\n]{0,80}/dev/[a-zA-Z]`,
			action:   "deny",
			message:  "디스크 장치에서 실행을 비활성화합니다. shred/wipe 삭제",
			priority: 100,
		},
		{
			name:     "[내장] 방화벽 규칙 지우기",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\biptables\s+(?:-F|--flush)\b|\bnft\s+flush\s+ruleset\b`,
			action:   "deny",
			message:  "방화벽 규칙 삭제 금지（iptables -F / nft flush）",
			priority: 100,
		},
		// ── 데이터베이스 파괴 작업 (priority 90) ─────────────────────────────────
		{
			name:     "[내장] SQL DROP DATABASE / TABLE / SCHEMA",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\bDROP\s+(?:DATABASE|TABLE|SCHEMA|INDEX|VIEW|TABLESPACE|USER|ROLE)\b`,
			action:   "deny",
			message:  "실행금지 DROP 작동，데이터베이스 개체가 되돌릴 수 없게 파괴될 수 있습니다.",
			priority: 90,
		},
		{
			name:     "[내장] SQL TRUNCATE",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\bTRUNCATE\s+(?:TABLE\s+)?\w`,
			action:   "deny",
			message:  "실행금지 TRUNCATE，데이터 테이블의 모든 데이터를 삭제하는 것이 가능합니다.",
			priority: 90,
		},
		{
			name:     "[내장] MongoDB drop / dropDatabase",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\.(?:dropDatabase|dropCollection|drop)\s*\(`,
			action:   "deny",
			message:  "실행금지 MongoDB drop 작동",
			priority: 90,
		},
		{
			name:     "[내장] Redis FLUSHALL / FLUSHDB",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\b(?:FLUSHALL|FLUSHDB)\b`,
			action:   "deny",
			message:  "실행금지 Redis FLUSHALL / FLUSHDB，캐시된 데이터를 모두 삭제하는 것이 가능합니다.",
			priority: 90,
		},
		// ── HTTP 파괴적인 요청 (priority 80) ──────────────────────────────────
		// Agent 보내기 DELETE 세 가지 일반적인 요청 방법：
		//   1. curl -X DELETE / --request DELETE（Bash 도구가 직접 실행되거나 스크립트를 작성합니다.）
		//   2. Python HTTP 클라이언트 .delete() 방법
		//   3. JS/일반 스크립트에서 method: 'DELETE' / method="DELETE"
		{
			name:     "[내장] curl / wget 보내기 DELETE 요청",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\bcurl\b[^|\n&;"]{0,300}(?:-X\s*DELETE|--request\s+DELETE|-XDELETE)|\bwget\b[^|\n&;"]{0,300}--method[=\s]+DELETE`,
			action:   "deny",
			message:  "합격금지 curl/wget 보내기 HTTP DELETE 요청，대상 시스템 데이터가 삭제될 수 있습니다.",
			priority: 80,
		},
		{
			name:     "[내장] Python HTTP 클라이언트 DELETE（requests/httpx/aiohttp）",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\b(?:requests|httpx|aiohttp|urllib\.request)\.delete\s*\(|session\.delete\s*\(|client\.delete\s*\(`,
			action:   "deny",
			message:  "사용금지 Python HTTP 클라이언트가 보냅니다. DELETE 요청",
			priority: 80,
		},
		{
			name:     "[내장] 스크립트 내 명령문 HTTP DELETE 방법（JS/일반）",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)axios\.delete\s*\(|method\s*[:=]\s*['"]DELETE['"]`,
			action:   "deny",
			message:  "스크립트 선언 및 전송을 금지합니다. HTTP DELETE 요청",
			priority: 80,
		},
		{
			name:     "[내장] 배치 클리어 / 인터페이스 경로 지우기",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)/(?:clear|wipe|flush|purge|truncate|drop|destroy|factory[-_]reset|reset[-_]all)(?:[/?#"'\s]|$)`,
			action:   "deny",
			message:  "클래스 인터페이스 일괄 삭제 또는 삭제 호출을 금지합니다.（/clear /wipe /flush /purge 등）",
			priority: 80,
		},
	}
	for _, r := range rules {
		if _, err := d.Exec(`
INSERT INTO intercept_rules(name, enabled, priority, match_target, match_type, pattern, action, message, timeout_enabled, timeout_seconds, timeout_action)
VALUES ($1, true, $2, $3, $4, $5, $6, $7, false, 60, 'deny')
ON CONFLICT DO NOTHING`,
			r.name, r.priority, r.target, r.typ, r.pattern, r.action, r.message,
		); err != nil {
			return fmt.Errorf("rule %q: %w", r.name, err)
		}
	}
	return d.SetSetting("intercept_default_rules_v1", "done")
}

// seedDefaultInterceptRulesV2 migrates the two safety patterns that used to be
// hard-coded in guard.go (destructive shell + data-exfil pipe) into ordinary
// intercept rules. Gated by its own flag so it also lands on DBs that already ran
// v1. Unlike the old guard.go floor, these are plain [내장] rules — the user can
// disable or delete them. The exfil rule ships DISABLED by default (its
// curl/wget/nc pipe pattern mis-fires on legitimate CTF/pentest reverse-shell and
// data-transfer pipes); enable it manually when exfil gating is actually wanted.
func (d *DB) seedDefaultInterceptRulesV2() error {
	if v, _, _ := d.GetSetting("intercept_default_rules_v2"); v == "done" {
		return nil
	}
	rules := []struct {
		name     string
		pattern  string
		action   string
		message  string
		enabled  bool
		priority int
	}{
		{
			name:     "[내장] 파괴적인 시스템 명령",
			pattern:  `(?i)\b(rm\s+-rf\s+/|mkfs|dd\s+if=|:\(\)\s*\{|shutdown|reboot|>\s*/dev/sd)`,
			action:   "deny",
			message:  "파괴적인 명령이 거부되었습니다.（rm -rf / / mkfs / dd / fork bomb / 종료하고 다시 시작하세요. / 디스크 장치 덮어쓰기）",
			enabled:  true,
			priority: 100,
		},
		{
			name:     "[내장] 데이터 유출 파이프라인",
			pattern:  `(?i)(curl|wget|nc|ncat)\b[^|]*\b(\|\s*(curl|wget|nc))`,
			action:   "deny",
			message:  "데이터 유출이 의심되는 파이프라인은 거부되었습니다（명령 출력 curl/wget/nc 사이드스토리）",
			enabled:  false,
			priority: 80,
		},
	}
	for _, r := range rules {
		if _, err := d.Exec(`
INSERT INTO intercept_rules(name, enabled, priority, match_target, match_type, pattern, action, message, timeout_enabled, timeout_seconds, timeout_action)
VALUES ($1, $2, $3, 'tool_input', 'regex', $4, $5, $6, false, 60, 'deny')
ON CONFLICT DO NOTHING`,
			r.name, r.enabled, r.priority, r.pattern, r.action, r.message,
		); err != nil {
			return fmt.Errorf("rule %q: %w", r.name, err)
		}
	}
	return d.SetSetting("intercept_default_rules_v2", "done")
}

// seedDefaultInterceptRulesV3 adds the delete-endpoint path rule. The v1 HTTP rules
// only catch the DELETE *method* (curl -X DELETE, requests.delete(, method:'DELETE'),
// and v1's path rule covers only /clear /wipe /flush /purge /truncate /drop /destroy
// /factory-reset /reset-all — so a plain `curl 'http://t/api/user/delete?id=1'` (a
// delete endpoint reached with GET/POST, which is how most web apps expose deletion)
// slipped through every built-in rule. Own flag so it also lands on DBs that already
// ran v1/v2, where editing the v1 seed would have no effect.
//
// The pattern deliberately requires a separator after the verb so /delivery,
// /details, /delta and /delegate do not match, while /deleteAll, /delete_user and
// /delete-user do. destroy is re-covered here because v1's rule does not allow a
// suffix (/destroyAll was missed).
//
// Exported as a package const only so the seeded regex is unit-testable without a DB.
const deleteEndpointPathPattern = `(?i)/(?:(?:delete|remove|unlink|erase|destroy)[-\w]*|del)(?:[/?#"'\s]|$)`

func (d *DB) seedDefaultInterceptRulesV3() error {
	if v, _, _ := d.GetSetting("intercept_default_rules_v3"); v == "done" {
		return nil
	}
	const name = "[내장] 클래스 인터페이스 경로 삭제"
	if _, err := d.Exec(`
INSERT INTO intercept_rules(name, enabled, priority, match_target, match_type, pattern, action, message, timeout_enabled, timeout_seconds, timeout_action)
SELECT $1, true, 80, 'tool_input', 'regex', $2, 'deny', $3, false, 60, 'deny'
WHERE NOT EXISTS (SELECT 1 FROM intercept_rules WHERE name = $1)`,
		name,
		deleteEndpointPathPattern,
		"클래스 삭제 인터페이스를 호출하는 것은 금지되어 있습니다.（/delete /remove /unlink /erase 등），어느 것을 사용해도 상관없습니다. HTTP 방법——대부분의 애플리케이션의 삭제 인터페이스가 사용됩니다. GET/POST 발동 가능，은 실제로 대상 데이터도 삭제합니다.",
	); err != nil {
		return fmt.Errorf("rule %q: %w", name, err)
	}
	return d.SetSetting("intercept_default_rules_v3", "done")
}
