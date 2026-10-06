-- ARTEX PostgreSQL schema (단일 데이터 소스)
-- 멱등성：반복 실행 가능（IF NOT EXISTS / OR REPLACE / DROP TRIGGER IF EXISTS）。

-- =====================================================================
-- 0. 일반：updated_at 트리거
-- =====================================================================
CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN NEW.updated_at = now(); RETURN NEW; END;
$$ LANGUAGE plpgsql;

-- 안전하다 text→inet 전환：잘못된 값이 반환되었습니다. NULL 던지는 대신 22P02。assets.ip 은 자유 텍스트입니다.
-- (Agent / 자산 API 호스트 이름을 적어주세요.)，알몸 이체 a.ip::inet 은 한 행의 더러운 데이터가 전체를 압도하게 만듭니다.
-- 기업 소유권 재계산 명세서가 손상되었습니다.。발신자용 try_inet(...) IS NULL 이 줄을 찾아 경고하세요.。
-- 필요없어요 pg_input_is_valid 필요하기 때문이죠. PG16+，이전 스톡 라이브러리와 호환되어야 합니다.。
CREATE OR REPLACE FUNCTION try_inet(value text) RETURNS inet AS $$
BEGIN
    RETURN value::inet;
EXCEPTION WHEN others THEN
    RETURN NULL;
END;
$$ LANGUAGE plpgsql IMMUTABLE STRICT;

-- =====================================================================
-- A. 자산 레이어：companies / assets / company_scope
-- =====================================================================

CREATE TABLE IF NOT EXISTS companies (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT NOT NULL,
    nkey       TEXT NOT NULL UNIQUE,
    logo       TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_companies_nkey ON companies(nkey);
DROP TRIGGER IF EXISTS trg_companies_upd ON companies;
CREATE TRIGGER trg_companies_upd BEFORE UPDATE ON companies
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE IF NOT EXISTS assets (
    id              BIGSERIAL PRIMARY KEY,
    type            TEXT NOT NULL CHECK (type IN (
                        'root_domain','ip','subdomain','app','service','endpoint'
                    )),
    company_id      BIGINT REFERENCES companies(id) ON DELETE SET NULL,
    -- explicit: caller/user selected the company; scope: derived from company_scope.
    -- Existing installations are conservatively migrated as explicit so a scope
    -- rebuild can never erase a historical manual association.
    company_source  TEXT NOT NULL DEFAULT 'explicit'
                    CHECK (company_source IN ('explicit','scope')),
    task_ids        BIGINT[] NOT NULL DEFAULT '{}',
    domain          TEXT,
    root_domain     TEXT,
    ip              TEXT,
    c_segment       CIDR,
    port            INTEGER CHECK (port BETWEEN 1 AND 65535),
    icp             TEXT,
    bound_domains   TEXT[]  NOT NULL DEFAULT '{}',
    open_ports      JSONB[] NOT NULL DEFAULT '{}',
    record_type     TEXT,
    record_value    TEXT[],
    bundle_id       TEXT,
    app_name        TEXT,
    category        TEXT,
    app_description TEXT,
    app_icp         TEXT,
    url             TEXT,
    service_type    TEXT CHECK (service_type IN ('http','other')),
    service_name    TEXT,
    favicon_mmh3    TEXT,
    status_code     INTEGER,
    content_length  BIGINT,
    page_title      TEXT,
    technologies    TEXT[]  NOT NULL DEFAULT '{}',
    auth            JSONB[] NOT NULL DEFAULT '{}',
    method          TEXT,
    params          JSONB[] NOT NULL DEFAULT '{}',
    extra           JSONB   NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_av2_root_domain  ON assets(domain) WHERE type = 'root_domain';
CREATE UNIQUE INDEX IF NOT EXISTS uq_av2_ip           ON assets(ip)     WHERE type = 'ip';
CREATE UNIQUE INDEX IF NOT EXISTS uq_av2_subdomain    ON assets(domain, COALESCE(record_type,'')) WHERE type = 'subdomain';
CREATE UNIQUE INDEX IF NOT EXISTS uq_av2_app_bundle   ON assets(bundle_id) WHERE type = 'app' AND bundle_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_av2_app_name     ON assets(app_name)  WHERE type = 'app' AND bundle_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_av2_service_http ON assets(url) WHERE type = 'service' AND service_type = 'http';
CREATE UNIQUE INDEX IF NOT EXISTS uq_av2_service_other
    ON assets(COALESCE(domain,''), COALESCE(ip,''), port, service_name) WHERE type = 'service' AND service_type = 'other';
CREATE UNIQUE INDEX IF NOT EXISTS uq_av2_endpoint     ON assets(url, method) WHERE type = 'endpoint';
CREATE INDEX IF NOT EXISTS idx_av2_company      ON assets(company_id)       WHERE company_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_av2_company_type ON assets(company_id, type) WHERE company_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_av2_task_ids     ON assets USING GIN(task_ids);
CREATE INDEX IF NOT EXISTS idx_av2_domain       ON assets(domain)      WHERE domain IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_av2_root_domain  ON assets(root_domain) WHERE root_domain IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_av2_ip           ON assets(ip)          WHERE ip IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_av2_c_segment    ON assets USING GIST(c_segment inet_ops) WHERE c_segment IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_av2_technologies ON assets USING GIN(technologies) WHERE type = 'service';
CREATE INDEX IF NOT EXISTS idx_av2_bound_domains ON assets USING GIN(bound_domains) WHERE type = 'ip';
CREATE INDEX IF NOT EXISTS idx_av2_open_ports   ON assets USING GIN(open_ports)    WHERE type = 'ip';
CREATE INDEX IF NOT EXISTS idx_av2_last_seen    ON assets(last_seen DESC);
CREATE INDEX IF NOT EXISTS idx_av2_type_seen    ON assets(type, last_seen DESC);
ALTER TABLE assets ADD COLUMN IF NOT EXISTS company_source TEXT;
UPDATE assets SET company_source = 'explicit' WHERE company_source IS NULL;
ALTER TABLE assets ALTER COLUMN company_source SET DEFAULT 'explicit';
ALTER TABLE assets ALTER COLUMN company_source SET NOT NULL;
ALTER TABLE assets DROP CONSTRAINT IF EXISTS assets_company_source_check;
ALTER TABLE assets ADD CONSTRAINT assets_company_source_check
    CHECK (company_source IN ('explicit','scope'));
DROP TRIGGER IF EXISTS trg_av2_upd ON assets;
CREATE TRIGGER trg_av2_upd BEFORE UPDATE ON assets
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE IF NOT EXISTS company_scope (
    id         BIGSERIAL PRIMARY KEY,
    company_id BIGINT NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('domain','ip','cidr','icp','keyword')),
    domain     TEXT,
    net        CIDR,
    value      TEXT,
    raw        TEXT NOT NULL,
    reason     TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_sv2_domain UNIQUE (company_id, domain),
    CONSTRAINT uq_sv2_net    UNIQUE (company_id, net),
    CONSTRAINT ck_company_scope_payload CHECK (
        (kind = 'domain' AND domain IS NOT NULL AND net IS NULL AND value IS NULL)
        OR (kind IN ('ip','cidr') AND domain IS NULL AND net IS NOT NULL AND value IS NULL)
        OR (kind IN ('icp','keyword') AND domain IS NULL AND net IS NULL AND value IS NOT NULL)
    )
);
-- Existing installations need the new text payload and expanded kind check.
ALTER TABLE company_scope ADD COLUMN IF NOT EXISTS value TEXT;
ALTER TABLE company_scope DROP CONSTRAINT IF EXISTS company_scope_kind_check;
ALTER TABLE company_scope ADD CONSTRAINT company_scope_kind_check
    CHECK (kind IN ('domain','ip','cidr','icp','keyword'));
ALTER TABLE company_scope DROP CONSTRAINT IF EXISTS ck_company_scope_payload;
ALTER TABLE company_scope ADD CONSTRAINT ck_company_scope_payload CHECK (
    (kind = 'domain' AND domain IS NOT NULL AND net IS NULL AND value IS NULL)
    OR (kind IN ('ip','cidr') AND domain IS NULL AND net IS NOT NULL AND value IS NULL)
    OR (kind IN ('icp','keyword') AND domain IS NULL AND net IS NULL AND value IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS idx_sv2_domain  ON company_scope(domain)   WHERE kind = 'domain';
CREATE INDEX IF NOT EXISTS idx_sv2_net     ON company_scope USING GIST(net inet_ops) WHERE kind IN ('ip','cidr');
CREATE UNIQUE INDEX IF NOT EXISTS uq_sv2_value ON company_scope(company_id, kind, value) WHERE kind IN ('icp','keyword');
CREATE INDEX IF NOT EXISTS idx_sv2_icp ON company_scope(value) WHERE kind = 'icp';
CREATE INDEX IF NOT EXISTS idx_sv2_company ON company_scope(company_id);

-- =====================================================================
-- B. 추론 및 탐색 레이어
-- =====================================================================
CREATE TABLE IF NOT EXISTS explorations (
    id          BIGSERIAL PRIMARY KEY,
    description TEXT,
    goal        TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'open'
                  CHECK (status IN ('open','achieved','failed')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- cold-digest (§2.3): per-task planner round counter — bumped once each time the
-- planner wakes and processes a round. Drives the ≥R cold-node debounce (measured in
-- this exploration's own rounds, not global node ids or wall-clock).
ALTER TABLE explorations ADD COLUMN IF NOT EXISTS round_no BIGINT NOT NULL DEFAULT 0;
DROP TRIGGER IF EXISTS trg_exp_upd ON explorations;
CREATE TRIGGER trg_exp_upd BEFORE UPDATE ON explorations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE IF NOT EXISTS exploration_nodes (
    id             BIGSERIAL PRIMARY KEY,
    exploration_id BIGINT NOT NULL REFERENCES explorations(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL,
    payload        JSONB NOT NULL DEFAULT '{}',
    priority       INT  NOT NULL DEFAULT 0,
    state          TEXT NOT NULL DEFAULT 'open',
    origin         TEXT,
    owner          TEXT,
    blocked_reason TEXT,
    delete_reason  TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at   TIMESTAMPTZ,
    CONSTRAINT ck_node_kind CHECK (kind IN ('begin','goal','intent','fact','finding','hint','digest')),
    CONSTRAINT ck_node_state CHECK (
        (kind='begin'   AND state IN ('open')) OR
        (kind='intent'  AND state IN ('open','running','paused','done','blocked','exhausted','stopped','deleted')) OR
        (kind='goal'    AND state IN ('open','met','abandoned')) OR
        (kind='fact'    AND state IN ('confirmed','dismissed','origin')) OR
        (kind='finding' AND state IN ('confirmed','dismissed')) OR
        (kind='hint'    AND state IN ('active','consumed')) OR
        (kind='digest'  AND state IN ('active','superseded'))
    )
);
ALTER TABLE exploration_nodes ADD COLUMN IF NOT EXISTS blocked_reason TEXT;
-- 허위삭제 의도(soft delete):state='deleted' 시간,delete_reason 사용자가 기재한 삭제 사유를 기록합니다.。
ALTER TABLE exploration_nodes ADD COLUMN IF NOT EXISTS delete_reason TEXT;
-- cold-digest (§2.3/§5.3): content_version bumps on any change that could alter a
-- digest body (summary/state/confidence); cold_since_round stamps the planner round
-- a node most recently went from "has a live downstream branch" to none (NULL = hot).
ALTER TABLE exploration_nodes ADD COLUMN IF NOT EXISTS content_version  INT    NOT NULL DEFAULT 0;
ALTER TABLE exploration_nodes ADD COLUMN IF NOT EXISTS cold_since_round BIGINT;
-- ck_node_kind: existing installs predate the 'digest' kind — recreate to allow it.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid='exploration_nodes'::regclass
          AND conname='ck_node_kind'
          AND pg_get_constraintdef(oid) NOT LIKE '%digest%'
    ) THEN
        ALTER TABLE exploration_nodes DROP CONSTRAINT ck_node_kind;
        ALTER TABLE exploration_nodes ADD CONSTRAINT ck_node_kind
            CHECK (kind IN ('begin','goal','intent','fact','finding','hint','digest'));
    END IF;
END $$;
-- ck_node_state: recreate when it lacks the 'paused' (older), 'superseded' (digest rev),
-- or 'deleted' (intent soft-delete rev) branches.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid='exploration_nodes'::regclass
          AND conname='ck_node_state'
          AND (pg_get_constraintdef(oid) NOT LIKE '%paused%'
               OR pg_get_constraintdef(oid) NOT LIKE '%superseded%'
               OR pg_get_constraintdef(oid) NOT LIKE '%deleted%')
    ) THEN
        ALTER TABLE exploration_nodes DROP CONSTRAINT ck_node_state;
        ALTER TABLE exploration_nodes ADD CONSTRAINT ck_node_state CHECK (
            (kind='begin'   AND state IN ('open')) OR
            (kind='intent'  AND state IN ('open','running','paused','done','blocked','exhausted','stopped','deleted')) OR
            (kind='goal'    AND state IN ('open','met','abandoned')) OR
            (kind='fact'    AND state IN ('confirmed','dismissed','origin')) OR
            (kind='finding' AND state IN ('confirmed','dismissed')) OR
            (kind='hint'    AND state IN ('active','consumed')) OR
            (kind='digest'  AND state IN ('active','superseded'))
        );
    END IF;
END $$;
CREATE INDEX IF NOT EXISTS idx_expnodes_part     ON exploration_nodes(exploration_id, kind);
CREATE INDEX IF NOT EXISTS idx_expnodes_frontier ON exploration_nodes(exploration_id, priority DESC)
    WHERE kind='intent' AND state='open';
DROP TRIGGER IF EXISTS trg_expnodes_upd ON exploration_nodes;
CREATE TRIGGER trg_expnodes_upd BEFORE UPDATE ON exploration_nodes
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE IF NOT EXISTS exploration_edges (
    exploration_id BIGINT NOT NULL REFERENCES explorations(id) ON DELETE CASCADE,
    src_id         BIGINT NOT NULL REFERENCES exploration_nodes(id) ON DELETE CASCADE,
    dst_id         BIGINT NOT NULL REFERENCES exploration_nodes(id) ON DELETE CASCADE,
    rel            TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (exploration_id, src_id, rel, dst_id),
    CONSTRAINT ck_edge_noself CHECK (src_id <> dst_id),
    CONSTRAINT ck_edge_rel CHECK (rel IN ('spawns','derived_from','yields','proves','covers'))
);
CREATE INDEX IF NOT EXISTS idx_expedges_src ON exploration_edges(src_id, rel);
CREATE INDEX IF NOT EXISTS idx_expedges_dst ON exploration_edges(dst_id, rel);
-- cold-digest (§1): the 'covers' relation (digest→member) postdates shipped installs,
-- whose rel CHECK is an inline auto-named constraint. Find and recreate it as ck_edge_rel.
DO $$
DECLARE cname text;
BEGIN
    SELECT conname INTO cname FROM pg_constraint
     WHERE conrelid='exploration_edges'::regclass AND contype='c'
       AND pg_get_constraintdef(oid) LIKE '%rel%'
       AND pg_get_constraintdef(oid) NOT LIKE '%covers%';
    IF cname IS NOT NULL THEN
        EXECUTE 'ALTER TABLE exploration_edges DROP CONSTRAINT '||quote_ident(cname);
        ALTER TABLE exploration_edges ADD CONSTRAINT ck_edge_rel
            CHECK (rel IN ('spawns','derived_from','yields','proves','covers'));
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS exploration_anchors (
    node_id   BIGINT NOT NULL REFERENCES exploration_nodes(id) ON DELETE CASCADE,
    asset_id  BIGINT NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    PRIMARY KEY (node_id, asset_id)
);
CREATE INDEX IF NOT EXISTS idx_anchor_asset ON exploration_anchors(asset_id);

-- task_constraints: operator-authored operation constraints (allow/deny) for a task.
-- Extracted by the goals decomposer at round 0 (from goal/description), editable at
-- runtime by the main agent + 개요「제약사항 관리」. Injected into the planner/worker system
-- prompt each round (config-gated) to keep exploration within the operator's boundary.
CREATE TABLE IF NOT EXISTS task_constraints (
    id             BIGSERIAL PRIMARY KEY,
    exploration_id BIGINT NOT NULL REFERENCES explorations(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL CHECK (kind IN ('allow','deny')),
    text           TEXT NOT NULL,
    origin         TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_task_constraints_exp ON task_constraints(exploration_id);

CREATE TABLE IF NOT EXISTS activity (
    id                 BIGSERIAL PRIMARY KEY,
    exploration_id     BIGINT NOT NULL REFERENCES explorations(id) ON DELETE CASCADE,
    node_id            BIGINT REFERENCES exploration_nodes(id) ON DELETE SET NULL,
    worker             TEXT,
    kind               TEXT,
    tool               TEXT,
    tool_use_id        TEXT,
    is_error           BOOLEAN NOT NULL DEFAULT false,
    summary            TEXT,
    detail             TEXT,
    metadata           JSONB NOT NULL DEFAULT '{}',
    input_tokens       INTEGER,
    output_tokens      INTEGER,
    cache_read_tokens  INTEGER,
    cache_write_tokens INTEGER,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE activity ADD COLUMN IF NOT EXISTS metadata JSONB NOT NULL DEFAULT '{}';
-- main_seg segments the main-agent session into resettable conversations: a new
-- main session bumps the segment so its transcript + activity start clean while the
-- task's graph/assets/goal are untouched. NULL == legacy rows == segment 0 (the
-- original session). Only worker='mainagent' rows carry it.
ALTER TABLE activity ADD COLUMN IF NOT EXISTS main_seg INTEGER;
CREATE INDEX IF NOT EXISTS idx_act_node  ON activity(exploration_id, node_id, id);
CREATE INDEX IF NOT EXISTS idx_act_since ON activity(exploration_id, id);
CREATE INDEX IF NOT EXISTS idx_act_tool_call ON activity(exploration_id, tool_use_id, id)
  WHERE kind IN ('tool_use', 'tool_result');
-- Main/Plan history pages filter by worker (both carry NULL node_id, so idx_act_node
-- can't distinguish them); this covers reverse pagination of those sessions.
CREATE INDEX IF NOT EXISTS idx_act_worker ON activity(exploration_id, worker, id);
-- Main-session pages filter by segment on top of worker='mainagent'; this partial
-- index covers reverse pagination within one segment.
CREATE INDEX IF NOT EXISTS idx_act_main_seg ON activity(exploration_id, main_seg, id)
    WHERE worker='mainagent';
-- Task-list polls aggregate result usage and find the latest event repeatedly.
-- Cover the token columns for index-only aggregation and the timestamp order for
-- per-exploration latest-activity lookups.
CREATE INDEX IF NOT EXISTS idx_act_result_usage ON activity(exploration_id)
    INCLUDE (input_tokens, output_tokens, cache_read_tokens, cache_write_tokens)
    WHERE kind='result';
CREATE INDEX IF NOT EXISTS idx_act_latest ON activity(exploration_id, created_at DESC);

-- main_sessions records the resettable main-agent conversation segments of a task.
-- Segment 0 (the original session) is implicit and never stored; this table holds
-- only the extra segments created by "새 세션 만들기" (seq >= 1). The current segment is
-- MAX(seq) or 0. Each segment gets its own transcript file + activity slice; the
-- task's exploration graph/assets/goal are shared and never reset.
CREATE TABLE IF NOT EXISTS main_sessions (
    exploration_id BIGINT NOT NULL REFERENCES explorations(id) ON DELETE CASCADE,
    seq            INTEGER NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (exploration_id, seq)
);

-- =====================================================================
-- C. LLM profiles
-- =====================================================================
CREATE TABLE IF NOT EXISTS settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS llm_profiles (
    id               BIGSERIAL PRIMARY KEY,
    name             TEXT NOT NULL UNIQUE,
    format           TEXT NOT NULL CHECK (format IN ('openai','anthropic','openai-responses')),
    base_url         TEXT,
    proxy            TEXT,
    model            TEXT NOT NULL,
    api_key          TEXT,
    api_key_hint     TEXT,
    rate_per_second  DOUBLE PRECISION NOT NULL DEFAULT 0,
    rate_per_minute  DOUBLE PRECISION NOT NULL DEFAULT 0,
    context_window_k INTEGER NOT NULL DEFAULT 0,
    -- 매개변수를 두 개의 독립적인 필드로 분할하는 방법을 생각해 보세요.：thinking_type=생각의 스위치(''/disabled/enabled)，
    -- reasoning_effort=사고강도(''/low/medium/high/xhigh/max)，서로 관여하지 않음。
    reasoning_effort TEXT NOT NULL DEFAULT '',
    thinking_type    TEXT NOT NULL DEFAULT '',
    is_default       BOOLEAN NOT NULL DEFAULT false,
    -- 폴링(장애 조치)매개변수，또 만나요 docs/LLM폴링 디자인.md：
    --   priority     주문，값이 클수록 먼저 선택됩니다.；구성 활성화(is_default)항상 체인 1위，은 이 값과 관련이 없습니다.。
    --   pool_exclude true=장애 조치 대상이 아닙니다.(은 아직 가능해요 agent/작업의 명시적 바인딩 사용)。
    priority         INTEGER NOT NULL DEFAULT 0,
    pool_exclude     BOOLEAN NOT NULL DEFAULT false,
    -- streaming=true(기본값)걷는 스타일 SSE；false 진짜요·비스트리밍(stream:false，일회용 JSON)。
    streaming        BOOLEAN NOT NULL DEFAULT true,
    -- 단일 답변의 출력 제한(token)。0=이 필드를 보내지 마십시오.，서버의 기본값에 따라 결정됩니다.——기존 동작 유지。
    -- 그리고 context_window_k(총 모델 용량，압축 임계값에 로컬로만 사용됩니다.)둘은 다른겁니다：이 값은 요청과 함께 전송됩니다.。
    max_tokens       INTEGER NOT NULL DEFAULT 0,
    -- 상한값을 출력하는 데 사용되는 요청 필드 이름은 무엇입니까?，전용 format='openai' 유효：
    --   ''                      = max_tokens(기본값，대부분의 게이트웨이와 호환 가능)
    --   'max_completion_tokens' = 새 필드；OpenAI 추론 모델(o 시리즈/GPT-5)그것만 인식하세요，
    --                             보내기 max_tokens 입니다 unsupported_parameter 거부。
    -- anthropic(max_tokens 필수)그리고 openai-responses(max_output_tokens)자체 필드 이름이 함께 제공됩니다.，은 이 값의 영향을 받지 않습니다.。
    max_tokens_field TEXT NOT NULL DEFAULT '',
    -- 사용자 정의 세션 헤더：비어 있지 않으면 각 요청에 이 이름을 가진 항목이 전달됩니다. HTTP 머리，헤드 값=현재 진행중 session id
    -- (chat 대화/worker 의도)。은 일부 버튼에 사용됩니다. session-id 헤더 프롬프트 캐시/고정 라우팅을 위한 게이트웨이。''=보내지 않음。
    session_header_key TEXT NOT NULL DEFAULT '',
    -- 적용 범위 재시도：회 0=전역 기본값 사용/-1=닫기/>0=값；간격 0=기본 지수로 물러남/>0=고정 밀리초。
    -- 세 그룹은 연결 재시도에 해당합니다.、빈 응답 재시도、마찬가지예요 provider 안전창 재시도，자세한 내용은 아래를 참고하세요 ALTER 에 메모를 남겨주세요.。
    retry_connect_attempts    INTEGER NOT NULL DEFAULT 0,
    retry_connect_interval_ms INTEGER NOT NULL DEFAULT 0,
    retry_empty_attempts      INTEGER NOT NULL DEFAULT 0,
    retry_empty_interval_ms   INTEGER NOT NULL DEFAULT 0,
    retry_stream_attempts     INTEGER NOT NULL DEFAULT 0,
    retry_stream_interval_ms  INTEGER NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_llm_one_default ON llm_profiles(is_default) WHERE is_default;
DROP TRIGGER IF EXISTS trg_llm_upd ON llm_profiles;
CREATE TRIGGER trg_llm_upd BEFORE UPDATE ON llm_profiles
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
-- 폴링 순서/제외 태그；기존 라이브러리 보충。기본값 0 / false = 모든 구성이 폴링에 참여합니다.。
ALTER TABLE llm_profiles ADD COLUMN IF NOT EXISTS priority     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE llm_profiles ADD COLUMN IF NOT EXISTS pool_exclude BOOLEAN NOT NULL DEFAULT false;
-- 흐름 스위치；기존 라이브러리 보충。기본값 true = 기존 스트리밍 동작 유지，이전 구성의 자동 업그레이드。
ALTER TABLE llm_profiles ADD COLUMN IF NOT EXISTS streaming    BOOLEAN NOT NULL DEFAULT true;
-- 놔주세요 format 수용에 제약이 있음 openai-responses(OpenAI Responses API)；기존 라이브러리 보충。
-- 매번 실행됨,멱등성:오래된 것부터 먼저 삭제하세요 CHECK 세 가지 값을 사용하여 새 항목을 만듭니다. CHECK。
ALTER TABLE llm_profiles DROP CONSTRAINT IF EXISTS llm_profiles_format_check;
ALTER TABLE llm_profiles ADD  CONSTRAINT llm_profiles_format_check
    CHECK (format IN ('openai','anthropic','openai-responses'));

-- 출력 상한 및 해당 필드 이름；기존 라이브러리 보충。기본값 0 / '' = 상한선이 전송되지 않았습니다.、계속 사용하세요 max_tokens 필드명，
-- 이전 구성 동작은 완전히 변경되지 않았습니다.。
ALTER TABLE llm_profiles ADD COLUMN IF NOT EXISTS max_tokens       INTEGER NOT NULL DEFAULT 0;
ALTER TABLE llm_profiles ADD COLUMN IF NOT EXISTS max_tokens_field TEXT    NOT NULL DEFAULT '';
-- 마찬가지예요 format：삭제 후 생성，시작할 때마다 멱등성을 보장하세요.。
ALTER TABLE llm_profiles DROP CONSTRAINT IF EXISTS llm_profiles_max_tokens_field_check;
ALTER TABLE llm_profiles ADD  CONSTRAINT llm_profiles_max_tokens_field_check
    CHECK (max_tokens_field IN ('','max_completion_tokens'));
ALTER TABLE llm_profiles DROP CONSTRAINT IF EXISTS llm_profiles_max_tokens_check;
ALTER TABLE llm_profiles ADD  CONSTRAINT llm_profiles_max_tokens_check
    CHECK (max_tokens >= 0);
-- 사용자 정의된 세션 헤더 이름；기존 라이브러리 보충。기본값 '' = 보내지 않음，이전 구성 동작은 변경되지 않습니다.。
ALTER TABLE llm_profiles ADD COLUMN IF NOT EXISTS session_header_key TEXT NOT NULL DEFAULT '';

-- 단일 구성 재시도 범위（또 만나요 docs/LLM디자인 재시도.md）。세 그룹 각각에 대해 한 쌍「회 + 고정 간격」，
-- 의미통합：회 0=전역 기본값 사용、-1=이 레이어를 닫고 다시 시도해 보세요.、>0=이 값을 사용하세요；간격 0=상속받다
-- 기본 지수 백오프、>0=대신 이 고정된 숫자(밀리초)를 사용하십시오.。모두 기본값 0，그래서 오래된 도서관은/이전 구성 동작은 변경되지 않습니다.。
--   connect = 연결 다시 시도（SDK doStream：연결 재설정/시간 초과/429/5xx，흐름이 시작되기 전에）
--   empty   = 빈 응답 재시도（SDK：없이 완료되었습니다. content block，만 openai 형식）
--   stream  = 마찬가지예요 provider 안전창 재시도（이 프로젝트 task_llm：출력이 전달되기 전 중단 재생）
ALTER TABLE llm_profiles ADD COLUMN IF NOT EXISTS retry_connect_attempts    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE llm_profiles ADD COLUMN IF NOT EXISTS retry_connect_interval_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE llm_profiles ADD COLUMN IF NOT EXISTS retry_empty_attempts      INTEGER NOT NULL DEFAULT 0;
ALTER TABLE llm_profiles ADD COLUMN IF NOT EXISTS retry_empty_interval_ms   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE llm_profiles ADD COLUMN IF NOT EXISTS retry_stream_attempts     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE llm_profiles ADD COLUMN IF NOT EXISTS retry_stream_interval_ms  INTEGER NOT NULL DEFAULT 0;
-- 마찬가지예요 format：삭제 후 생성，시작할 때마다 멱등성을 보장하세요.。최소 횟수 -1(닫기)，간격은 음수일 수 없습니다.。
ALTER TABLE llm_profiles DROP CONSTRAINT IF EXISTS llm_profiles_retry_check;
ALTER TABLE llm_profiles ADD  CONSTRAINT llm_profiles_retry_check CHECK (
    retry_connect_attempts >= -1 AND retry_empty_attempts >= -1 AND retry_stream_attempts >= -1
    AND retry_connect_interval_ms >= 0 AND retry_empty_interval_ms >= 0 AND retry_stream_interval_ms >= 0);

-- 스위치 분야에 대한 생각 thinking_type，예전 싱글에서 reasoning_effort 의미는 한 번에 분할됩니다.。
-- schema.sql 시작할 때마다 실행됨，따라서 마이그레이션은 한 번만 실행해야 합니다.：열이 아직 존재하지 않는 경우에만 백필，
-- 그렇지 않으면 시작할 때마다 사용자가 수동으로 설정한 조합을 덮어쓰게 됩니다.。늙었다 reasoning_effort 의미：
--   'off'                    → 명시적으로 종료  → thinking_type='disabled'，명확한 강도
--   'low/medium/high/max'    → 켜세요+힘 → thinking_type='enabled'，강도 유지
--   ''                       → 보내지 않음    → 둘 다 비어있습니다.(기본값)
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'llm_profiles' AND column_name = 'thinking_type'
    ) THEN
        ALTER TABLE llm_profiles ADD COLUMN thinking_type TEXT NOT NULL DEFAULT '';
        UPDATE llm_profiles SET thinking_type = 'enabled'
            WHERE reasoning_effort IN ('low','medium','high','max');
        UPDATE llm_profiles SET thinking_type = 'disabled', reasoning_effort = ''
            WHERE reasoning_effort = 'off';
    END IF;
END $$;

-- LLM 폴링 회로 차단기 상태：특정 구성이 계속 실패했습니다.(잔고가 부족해요/key 유효하지 않음/전류 제한)냉각에 들어간 후，냉각기간 동안
-- 폴링은 바로 건너뜁니다.。메모리 상태가 우선합니다.，여기서 라이브러리를 삭제하는 목적은 다시 시작한 후 냉각 창을 잃지 않기 위한 것입니다.——로드할 때
-- 만료된 행(open_until > now)，유통기한 지난 상품은 자연스럽게 반품됩니다"정상"，반개방 테스트에 대한 다음 호출을 기다립니다.。
CREATE TABLE IF NOT EXISTS llm_profile_health (
    profile_id  BIGINT PRIMARY KEY REFERENCES llm_profiles(id) ON DELETE CASCADE,
    fails       INTEGER NOT NULL DEFAULT 0,  -- 현재 연속실패 횟수(성공하면 지워짐)
    trips       INTEGER NOT NULL DEFAULT 0,  -- 차단기 누적 개수,냉각 시간 지수 백오프에 사용됩니다.
    open_until  TIMESTAMPTZ,                 -- 냉각 차단;NULL/만료됨 = 안 터졌어
    last_error  TEXT NOT NULL DEFAULT '',
    last_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =====================================================================
-- D. 태스크 레이어
-- =====================================================================
-- Global task categories are intentionally independent from task templates.
-- Deleting a category only moves its tasks back to the uncategorized bucket.
CREATE TABLE IF NOT EXISTS task_categories (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT NOT NULL,
    nkey       TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_task_categories_name ON task_categories(name, id);
DROP TRIGGER IF EXISTS trg_task_categories_upd ON task_categories;
CREATE TRIGGER trg_task_categories_upd BEFORE UPDATE ON task_categories
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE IF NOT EXISTS tasks (
    id             BIGSERIAL PRIMARY KEY,
    name           TEXT NOT NULL DEFAULT '',
    category_id    BIGINT REFERENCES task_categories(id) ON DELETE SET NULL,
    description    TEXT NOT NULL,
    goal           TEXT NOT NULL,
    exploration_id BIGINT NOT NULL UNIQUE
                     REFERENCES explorations(id) ON DELETE RESTRICT,
    status         TEXT NOT NULL DEFAULT 'created'
                     CHECK (status IN ('created','running','paused','done','failed','timeout')),
    paused         BOOLEAN NOT NULL DEFAULT false,
    queued         BOOLEAN NOT NULL DEFAULT false,
    queued_at      TIMESTAMPTZ,
    queue_mode     TEXT NOT NULL DEFAULT '',
    llm_profile_id BIGINT REFERENCES llm_profiles(id) ON DELETE SET NULL,
    active_llm_profile_id BIGINT REFERENCES llm_profiles(id) ON DELETE SET NULL,
    llm_chain_revision BIGINT NOT NULL DEFAULT 0,
    company_id     BIGINT REFERENCES companies(id) ON DELETE SET NULL,
    parent_ref     TEXT,
    timeout_seconds INTEGER NOT NULL DEFAULT 0,
    plan_heartbeat_seconds INTEGER NOT NULL DEFAULT 300,
    coverage_enabled BOOLEAN NOT NULL DEFAULT true,
    pinned_at      TIMESTAMPTZ,
    first_run_at   TIMESTAMPTZ,
    deadline_at    TIMESTAMPTZ,
    archived_at    TIMESTAMPTZ,
    deleted_at     TIMESTAMPTZ,
    completed_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_tasks_alive  ON tasks(created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status)          WHERE deleted_at IS NULL;
DROP TRIGGER IF EXISTS trg_tasks_upd ON tasks;
CREATE TRIGGER trg_tasks_upd BEFORE UPDATE ON tasks
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
-- planner 하트비트 트리거 간격(초);기존 라이브러리 보충。기본값 300s(5min)。또 만나요 docs/planner-trigger-impl-plan.md
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS plan_heartbeat_seconds INTEGER NOT NULL DEFAULT 300;
-- 동시성 제한이 일시 중지되었습니다.;기존 라이브러리 보충。true=동시성 제한으로 인해 대기 중입니다.、공석이 자동으로 시작될 때까지 기다리십시오.。
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS queued BOOLEAN NOT NULL DEFAULT false;
-- 자산보상 기능 스위치;기존 라이브러리 보충。true(기본값)=계산/테스트 커버리지 표시、자동 누적 테스트 범위、
-- 주다 agent 열려있습니다 add_task_scope/list_untested_assets;false=모두 닫기(또 만나요 task_scope.go)。
-- 재고작업 기본 true 원래 동작을 유지하세요.;company 관련(task_scope kind=company)은 이 스위치의 영향을 받지 않습니다.。
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS coverage_enabled BOOLEAN NOT NULL DEFAULT true;
-- queued_at makes admission FIFO reflect the actual enqueue order rather than the
-- task creation order. queue_mode distinguishes first bootstrap from resuming an
-- exploration that already owns goals/history.
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS queued_at TIMESTAMPTZ;
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS queue_mode TEXT NOT NULL DEFAULT '';
-- 선택적 작업 이름;기존 라이브러리 보충。빈 문자열=이름 없음,프런트 엔드에 표시할 때 설명으로 돌아갑니다.。
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS name TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS category_id BIGINT REFERENCES task_categories(id) ON DELETE SET NULL;
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS pinned_at TIMESTAMPTZ;
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS active_llm_profile_id BIGINT REFERENCES llm_profiles(id) ON DELETE SET NULL;
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS llm_chain_revision BIGINT NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS archived_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_tasks_category ON tasks(category_id, created_at DESC)
    WHERE deleted_at IS NULL AND category_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_tasks_pinned ON tasks(pinned_at DESC)
    WHERE deleted_at IS NULL AND pinned_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_tasks_archived ON tasks(archived_at DESC)
    WHERE archived_at IS NOT NULL;

-- Cold task archives retain only compact metadata in PostgreSQL. The complete
-- task payload lives in a versioned .tar.zst package under data/archives/tasks.
-- task_id stays unique so an operation can be retried safely after a restart.
CREATE TABLE IF NOT EXISTS task_archives (
    id                         BIGSERIAL PRIMARY KEY,
    task_id                    BIGINT NOT NULL UNIQUE REFERENCES tasks(id) ON DELETE CASCADE,
    state                      TEXT NOT NULL DEFAULT 'archive_queued' CHECK (state IN (
                                   'archive_queued','archiving','archive_failed','ready',
                                   'restore_queued','restoring','restore_failed',
                                   'delete_queued','deleting','delete_failed'
                               )),
    phase                      TEXT NOT NULL DEFAULT 'queued',
    progress                   INTEGER NOT NULL DEFAULT 0 CHECK (progress BETWEEN 0 AND 100),
    error                      TEXT NOT NULL DEFAULT '',
    warnings                   JSONB NOT NULL DEFAULT '[]',
    format_version             INTEGER NOT NULL DEFAULT 2,
    archive_path               TEXT NOT NULL DEFAULT '',
    sha256                     TEXT NOT NULL DEFAULT '',
    original_size              BIGINT NOT NULL DEFAULT 0,
    compressed_size            BIGINT NOT NULL DEFAULT 0,
    task_name                  TEXT NOT NULL DEFAULT '',
    task_description           TEXT NOT NULL DEFAULT '',
    task_goal                  TEXT NOT NULL DEFAULT '',
    original_status            TEXT NOT NULL DEFAULT '',
    category_id_snapshot       BIGINT,
    category_name_snapshot     TEXT NOT NULL DEFAULT '',
    source_task_ids            BIGINT[] NOT NULL DEFAULT '{}',
    remaining_timeout_seconds  BIGINT NOT NULL DEFAULT 0,
    data_counts                JSONB NOT NULL DEFAULT '{}',
    aggregate_stats            JSONB NOT NULL DEFAULT '{}',
    archived_at                TIMESTAMPTZ,
    requested_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                 TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE task_archives ALTER COLUMN format_version SET DEFAULT 2;
CREATE INDEX IF NOT EXISTS idx_task_archives_state ON task_archives(state, requested_at, id);
CREATE INDEX IF NOT EXISTS idx_task_archives_archived ON task_archives(archived_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_task_archives_sources ON task_archives USING GIN(source_task_ids);
DROP TRIGGER IF EXISTS trg_task_archives_upd ON task_archives;
CREATE TRIGGER trg_task_archives_upd BEFORE UPDATE ON task_archives
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Reusable task description/goal presets. nkey is the normalized, case-insensitive
-- identity used to reject visually equivalent duplicate names.
CREATE TABLE IF NOT EXISTS task_templates (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL,
    nkey        TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL,
    goal        TEXT NOT NULL,
    -- 기본 작업 분류；카테고리 삭제시 비워두세요（그리고 tasks.category_id 일관됨，차단 없음）。
    category_id     BIGINT REFERENCES task_categories(id) ON DELETE SET NULL,
    -- 기본 작업 수준 차단/규칙 스냅샷 허용(AssetInterceptRuleInput 배열)；템플릿 적용시 새로운 작업 주입。
    intercept_rules JSONB NOT NULL DEFAULT '[]',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- 기존 라이브러리 보충(출판 버전,갈레 벨트 IF NOT EXISTS)。
ALTER TABLE task_templates ADD COLUMN IF NOT EXISTS category_id BIGINT REFERENCES task_categories(id) ON DELETE SET NULL;
ALTER TABLE task_templates ADD COLUMN IF NOT EXISTS intercept_rules JSONB NOT NULL DEFAULT '[]';
CREATE INDEX IF NOT EXISTS idx_task_templates_updated ON task_templates(updated_at DESC, id DESC);
DROP TRIGGER IF EXISTS trg_task_templates_upd ON task_templates;
CREATE TRIGGER trg_task_templates_upd BEFORE UPDATE ON task_templates
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Direct, read-only task context inheritance. Relations are intentionally not
-- recursive: a task sees only the source tasks explicitly chosen at creation.
CREATE TABLE IF NOT EXISTS task_relations (
    task_id        BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    source_task_id BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (task_id, source_task_id),
    CONSTRAINT ck_task_relation_not_self CHECK (task_id <> source_task_id)
);
CREATE INDEX IF NOT EXISTS idx_task_relations_source ON task_relations(source_task_id);

-- Task/asset provenance supplements the legacy assets.task_ids association. The
-- array remains the compatibility source for existing query and cleanup paths;
-- this relation records how each association was obtained for operator review.
CREATE TABLE IF NOT EXISTS task_asset_links (
    task_id        BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    asset_id       BIGINT NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    source         TEXT NOT NULL DEFAULT 'system',
    source_summary TEXT NOT NULL DEFAULT '',
    source_node_id BIGINT REFERENCES exploration_nodes(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (task_id, asset_id)
);
CREATE INDEX IF NOT EXISTS idx_task_asset_links_asset ON task_asset_links(asset_id, task_id);
CREATE INDEX IF NOT EXISTS idx_task_asset_links_node ON task_asset_links(source_node_id)
    WHERE source_node_id IS NOT NULL;
DROP TRIGGER IF EXISTS trg_task_asset_links_upd ON task_asset_links;
CREATE TRIGGER trg_task_asset_links_upd BEFORE UPDATE ON task_asset_links
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Keep provenance rows synchronized when existing asset upsert paths append or
-- remove task ids. Detailed callers overwrite the generic source after upsert.
CREATE OR REPLACE FUNCTION sync_task_asset_links() RETURNS trigger AS $$
BEGIN
    INSERT INTO task_asset_links(task_id, asset_id, source, source_summary)
    SELECT task.id, NEW.id, 'system', '작업 실행 중 자동 연결'
    FROM unnest(NEW.task_ids) AS requested(task_id)
    JOIN tasks task ON task.id=requested.task_id AND task.deleted_at IS NULL
    ON CONFLICT (task_id, asset_id) DO NOTHING;

    DELETE FROM task_asset_links link
    WHERE link.asset_id=NEW.id
      AND NOT (link.task_id=ANY(NEW.task_ids));
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS trg_assets_task_links ON assets;
CREATE TRIGGER trg_assets_task_links AFTER INSERT OR UPDATE OF task_ids ON assets
    FOR EACH ROW EXECUTE FUNCTION sync_task_asset_links();

-- Existing installations receive an auditable legacy source without rewriting
-- task_ids. Ignore stale array ids that no longer resolve to a live task.
INSERT INTO task_asset_links(task_id, asset_id, source, source_summary)
SELECT task.id, asset.id, 'legacy', '과거과제자산협회에 의해 이관됨'
FROM assets asset
CROSS JOIN LATERAL unnest(asset.task_ids) AS requested(task_id)
JOIN tasks task ON task.id=requested.task_id AND task.deleted_at IS NULL
ON CONFLICT (task_id, asset_id) DO NOTHING;

-- Ordered task-level LLM failover chain. A quota-exhausted entry is skipped
-- until the user saves/resets the chain, which clears all failure state.
CREATE TABLE IF NOT EXISTS task_llm_profiles (
    task_id          BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    profile_id       BIGINT NOT NULL REFERENCES llm_profiles(id) ON DELETE CASCADE,
    position         INTEGER NOT NULL CHECK (position >= 0),
    status           TEXT NOT NULL DEFAULT 'ready'
                       CHECK (status IN ('ready','quota_exhausted')),
    last_error       TEXT,
    exhausted_at     TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (task_id, profile_id),
    UNIQUE (task_id, position)
);
CREATE INDEX IF NOT EXISTS idx_task_llm_profiles_order ON task_llm_profiles(task_id, position);
CREATE INDEX IF NOT EXISTS idx_task_llm_profiles_profile ON task_llm_profiles(profile_id, task_id);
CREATE INDEX IF NOT EXISTS idx_tasks_llm_profile ON tasks(llm_profile_id) WHERE llm_profile_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_tasks_active_llm_profile ON tasks(active_llm_profile_id) WHERE active_llm_profile_id IS NOT NULL;
DROP TRIGGER IF EXISTS trg_task_llm_profiles_upd ON task_llm_profiles;
CREATE TRIGGER trg_task_llm_profiles_upd BEFORE UPDATE ON task_llm_profiles
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- One-time-compatible backfill: old pinned tasks become one-entry chains. A user
-- can still clear the chain later because the update path also clears the legacy
-- llm_profile_id column, preventing this block from re-adding it on restart.
INSERT INTO task_llm_profiles(task_id, profile_id, position)
SELECT t.id, t.llm_profile_id, 0
FROM tasks t
WHERE t.llm_profile_id IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM task_llm_profiles x WHERE x.task_id=t.id)
ON CONFLICT DO NOTHING;
UPDATE tasks t
SET active_llm_profile_id = t.llm_profile_id
WHERE t.active_llm_profile_id IS NULL
  AND t.llm_profile_id IS NOT NULL
  AND EXISTS (SELECT 1 FROM task_llm_profiles x WHERE x.task_id=t.id AND x.profile_id=t.llm_profile_id);

-- 태스크 테스트 범위（자산 보장의 분모 + 권한 범위）。
--   자동입력(source='auto')：insertAssets 상단 버튼 worker 명시적으로 삽입된 자산 유형과 보수적인 범위
--     （root_domain→root_domain，subdomain/service/endpoint→subdomain(host)，ip→ip）；
--     side-effect 파생 자산이 범위를 벗어났습니다.（후크는 handler 최상위 수준，은 다음에서 파생됩니다. db 내부 레이어）。
--   agent 작성하세요(source='agent')：add_task_scope 추가 company/root_domain/subdomain/ip。
-- 취재 = 일치 active 알았어 assets（분모）에，은(는) fact 고정된 노드의 비율（분자）。
CREATE TABLE IF NOT EXISTS task_scope (
    id          BIGSERIAL PRIMARY KEY,
    task_id     BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL CHECK (kind IN ('company','root_domain','subdomain','ip','cidr','icp','keyword')),
    company_id  BIGINT REFERENCES companies(id) ON DELETE CASCADE,  -- kind='company'
    domain      TEXT,          -- root_domain / subdomain
    net         CIDR,          -- ip / cidr
    value       TEXT,          -- icp / keyword
    source      TEXT NOT NULL DEFAULT 'auto' CHECK (source IN ('auto','agent','manual')),
    reason      TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- 기존 라이브러리 업그레이드：작업 범위 확장，전사적 단일 텍스트 상자 인식 기능으로 일관성을 유지하세요.。
ALTER TABLE task_scope ADD COLUMN IF NOT EXISTS value TEXT;
ALTER TABLE task_scope DROP CONSTRAINT IF EXISTS task_scope_kind_check;
ALTER TABLE task_scope ADD CONSTRAINT task_scope_kind_check
    CHECK (kind IN ('company','root_domain','subdomain','ip','cidr','icp','keyword'));
-- 중복 제거：마찬가지예요 task 의 동일한 범위는 한 번만 저장됩니다.（자동 채우기 일괄 삽입은 멱등성에 의존합니다.）。
DROP INDEX IF EXISTS uq_task_scope;
CREATE UNIQUE INDEX IF NOT EXISTS uq_task_scope_v2 ON task_scope(
    task_id, kind, COALESCE(domain,''), COALESCE(net::text,''), COALESCE(company_id,0), COALESCE(value,''));
CREATE INDEX IF NOT EXISTS idx_ts_domain  ON task_scope(domain) WHERE kind IN ('root_domain','subdomain');
CREATE INDEX IF NOT EXISTS idx_ts_net     ON task_scope USING GIST(net inet_ops) WHERE kind IN ('ip','cidr');
CREATE INDEX IF NOT EXISTS idx_ts_company ON task_scope(company_id) WHERE kind = 'company';

-- =====================================================================
-- E. Agents / 프롬프트 워드 템플릿 / 가변 디렉터리
-- =====================================================================
CREATE TABLE IF NOT EXISTS agents (
    id                BIGSERIAL PRIMARY KEY,
    key               TEXT NOT NULL UNIQUE CHECK (key ~ '^[a-z][a-z0-9_]*$'),
    name              TEXT NOT NULL,
    description       TEXT,
    role              TEXT NOT NULL,
    builtin           BOOLEAN NOT NULL DEFAULT true,
    enabled           BOOLEAN NOT NULL DEFAULT true,
    llm_profile_id    BIGINT REFERENCES llm_profiles(id) ON DELETE SET NULL,
    current_prompt_id BIGINT,
    max_turns         INTEGER NOT NULL DEFAULT 0,
    run_seconds       INTEGER NOT NULL DEFAULT 1200,
    web_search        BOOLEAN NOT NULL DEFAULT false,
    interactive_shell BOOLEAN NOT NULL DEFAULT false,
    wrapup_prompt     TEXT NOT NULL DEFAULT '',
    wrapup_max_turns  INTEGER NOT NULL DEFAULT 0,
    task_timeout_wrapup_prompt    TEXT NOT NULL DEFAULT '',
    task_timeout_wrapup_max_turns INTEGER NOT NULL DEFAULT 0,
    trigger_run_mode     TEXT    NOT NULL DEFAULT 'serial'  CHECK (trigger_run_mode IN ('serial','parallel')),
    trigger_merge_mode   TEXT    NOT NULL DEFAULT 'all' CHECK (trigger_merge_mode IN ('by_task','all','none')),
    trigger_max_parallel INTEGER NOT NULL DEFAULT 5,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT agents_role_ck CHECK (role IN ('goals','main','planner','worker','assistant'))
);
-- 컬럼 마이그레이션 추가(출판 버전,기존 도서관 업그레이드 및 보완 목록;새 도서관 CREATE 포함됨。마이그레이션 없이 CHECK:오래된 재고는 안전합니다 + 백엔드는 진실을 밝히기 위해 화이트리스트를 작성합니다.)。
ALTER TABLE agents ADD COLUMN IF NOT EXISTS trigger_run_mode     TEXT    NOT NULL DEFAULT 'serial';
ALTER TABLE agents ADD COLUMN IF NOT EXISTS trigger_merge_mode   TEXT    NOT NULL DEFAULT 'all';
ALTER TABLE agents ADD COLUMN IF NOT EXISTS trigger_max_parallel INTEGER NOT NULL DEFAULT 5;
-- per-agent LLM 바인딩(agent 레벨 기본 모델):초판부터 위에 나열됨 CREATE 에,이거 ALTER 지지우쿠만이 진실을 말할 수 있도록(멱등성)。
ALTER TABLE agents ADD COLUMN IF NOT EXISTS llm_profile_id BIGINT REFERENCES llm_profiles(id) ON DELETE SET NULL;
-- run_seconds 싱글 run 벽시계 기본 600→1200:기본 목록만 변경(앞으로 새로 삽입되는 행에 영향을 줍니다.),기존 재고 라인을 이동하지 마십시오。
ALTER TABLE agents ALTER COLUMN run_seconds SET DEFAULT 1200;
CREATE INDEX IF NOT EXISTS idx_agents_llm_profile ON agents(llm_profile_id) WHERE llm_profile_id IS NOT NULL;
DROP TRIGGER IF EXISTS trg_agents_upd ON agents;
CREATE TRIGGER trg_agents_upd BEFORE UPDATE ON agents
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE IF NOT EXISTS agent_prompts (
    id            BIGSERIAL PRIMARY KEY,
    agent_id      BIGINT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    version       INT NOT NULL,
    template_text TEXT NOT NULL,
    note          TEXT,
    updated_by    TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (agent_id, version)
);
-- 루프 외래 키：agents.current_prompt_id → agent_prompts.id（은 두 테이블이 생성된 후 추가되어야 합니다.）
DO $$ BEGIN
    ALTER TABLE agents ADD CONSTRAINT fk_agents_curprompt
        FOREIGN KEY (current_prompt_id) REFERENCES agent_prompts(id) ON DELETE SET NULL;
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

CREATE TABLE IF NOT EXISTS agent_prompt_vars (
    id          BIGSERIAL PRIMARY KEY,
    agent_id    BIGINT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    var_name    TEXT NOT NULL,
    description TEXT,
    example     TEXT,
    source      TEXT NOT NULL CHECK (source IN ('exploration','runtime','distilled')),
    UNIQUE (agent_id, var_name)
);

-- =====================================================================
-- F. MCP 서비스
-- =====================================================================
CREATE TABLE IF NOT EXISTS mcp_servers (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    transport   TEXT NOT NULL CHECK (transport IN ('stdio','http','sse')),
    command     TEXT,
    args        JSONB NOT NULL DEFAULT '[]',
    env         JSONB NOT NULL DEFAULT '{}',
    url         TEXT,
    enabled     BOOLEAN NOT NULL DEFAULT true,
    insecure    BOOLEAN NOT NULL DEFAULT false,  -- http: 건너뛰기 TLS 인증서 확인(자체 서명된 인증서 시나리오, issue #108)
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Allow legacy MCP SSE servers on databases created before SSE support.
ALTER TABLE mcp_servers DROP CONSTRAINT IF EXISTS mcp_servers_transport_check;
ALTER TABLE mcp_servers ADD CONSTRAINT mcp_servers_transport_check
    CHECK (transport IN ('stdio','http','sse'));
-- 기존 데이터베이스를 보충하는 중입니다.(schema.sql 시작할 때마다 Exec)。
ALTER TABLE mcp_servers ADD COLUMN IF NOT EXISTS insecure BOOLEAN NOT NULL DEFAULT false;
DROP TRIGGER IF EXISTS trg_mcp_upd ON mcp_servers;
CREATE TRIGGER trg_mcp_upd BEFORE UPDATE ON mcp_servers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 기본 데이터 소스 자리 표시자：ScopeSentry 자산 동기화 MCP（주소와 증명서를 비워두세요、활성화되지 않음）。
-- 「자산 동기화」페이지에서 데이터 소스가 구성되었는지 감지합니다.；사용자가 페이지를 채웁니다. url 그리고 X-API-Key 그런 다음 활성화하세요.。
-- 누락된 경우에만 삽입하세요.，사용자가 구성한 내용을 덮어쓰지 마십시오./서버 활성화됨（schema.sql 시작할 때마다 Exec）。
INSERT INTO mcp_servers (name, transport, url, env, enabled)
VALUES ('ScopeSentry', 'http', NULL, '{"X-API-Key":""}', false)
ON CONFLICT (name) DO NOTHING;

CREATE TABLE IF NOT EXISTS mcp_tools_cache (
    id            BIGSERIAL PRIMARY KEY,
    server_id     BIGINT NOT NULL REFERENCES mcp_servers(id) ON DELETE CASCADE,
    tool_name     TEXT NOT NULL,
    description   TEXT,
    schema        JSONB,
    discovered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (server_id, tool_name)
);

-- =====================================================================
-- G. 가시성：agent × mcp / skill
-- =====================================================================
CREATE TABLE IF NOT EXISTS agent_visibility (
    agent_id      BIGINT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    resource_kind TEXT   NOT NULL CHECK (resource_kind IN ('mcp')),
    resource_id   BIGINT NOT NULL,
    mcp_tool_name TEXT   NOT NULL DEFAULT '',
    enabled       BOOLEAN NOT NULL DEFAULT true,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, resource_kind, resource_id, mcp_tool_name)
);
CREATE INDEX IF NOT EXISTS idx_vis_resource ON agent_visibility(resource_kind, resource_id);

CREATE TABLE IF NOT EXISTS agent_skill_visibility (
    agent_id   BIGINT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    skill_name TEXT   NOT NULL,
    enabled    BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, skill_name)
);
CREATE INDEX IF NOT EXISTS idx_askv_skill ON agent_skill_visibility(skill_name);

-- Skill 원장에게 전화하세요（또 만나요 db/skill_usage.go）。한번 Skill() 전화를 걸어보세요，치수만 기억하고 텍스트는 기억하지 않음。
-- 의도적으로 외래 키를 사용하지 않음：임무/세션이 삭제된 후에도 통계는 계속 유지되어야 합니다.（그리고 llm_usage 같은 이유），skill 그 자체가 그냥
-- 파일 시스템의 디렉터리 이름，해당 테이블이 없습니다.。
CREATE TABLE IF NOT EXISTS skill_usage (
    id             BIGSERIAL PRIMARY KEY,
    ts             TIMESTAMPTZ NOT NULL DEFAULT now(),
    skill          TEXT NOT NULL,
    agent_key      TEXT,
    task_id        BIGINT,
    exploration_id BIGINT,
    intent_id      BIGINT,
    session_id     TEXT,
    args_len       INTEGER NOT NULL DEFAULT 0,
    -- false = 존재하지 않는 모델로 명명된 모델 skill(놓쳤어요)。이러한 라인도 예약되어 있습니다：반영합니다"사용하고 싶은데 없어요"
    -- 노치，예 skill 기준。
    found          BOOLEAN NOT NULL DEFAULT true
);
CREATE INDEX IF NOT EXISTS idx_skill_usage_skill ON skill_usage(skill, ts DESC);
CREATE INDEX IF NOT EXISTS idx_skill_usage_task  ON skill_usage(task_id);

-- 공구 호출 원장（또 만나요 db/tool_usage.go）。실시간 CoreTool.Call 한 줄，기여 차원만 기억하세요.，
-- 도구 매개변수를 저장하거나 내용을 반환하지 않음。의도적으로 외래 키를 사용하지 않음，임무、세션 또는 사용자 정의 도구가 삭제된 후에도 통계는 계속 유지됩니다.。
CREATE TABLE IF NOT EXISTS tool_usage (
    id             BIGSERIAL PRIMARY KEY,
    ts             TIMESTAMPTZ NOT NULL DEFAULT now(),
    tool_key       TEXT NOT NULL,
    agent_key      TEXT,
    task_id        BIGINT,
    exploration_id BIGINT,
    intent_id      BIGINT,
    session_id     TEXT
);
CREATE INDEX IF NOT EXISTS idx_tool_usage_tool ON tool_usage(tool_key, ts DESC);
CREATE INDEX IF NOT EXISTS idx_tool_usage_task ON tool_usage(task_id);

-- =====================================================================
-- H. 내장 도구 디렉토리
-- =====================================================================
CREATE TABLE IF NOT EXISTS tools (
    key         TEXT PRIMARY KEY,
    system      BOOLEAN NOT NULL DEFAULT true,
    description TEXT    NOT NULL DEFAULT '',
    schema      JSONB   NOT NULL DEFAULT '{}',
    agents      JSONB   NOT NULL DEFAULT '[]',
    enabled     BOOLEAN NOT NULL DEFAULT true,
    kind        TEXT    NOT NULL DEFAULT 'builtin',
    exec        JSONB   NOT NULL DEFAULT '{}',
    deferred    BOOLEAN NOT NULL DEFAULT false,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
DROP TRIGGER IF EXISTS trg_tools_upd ON tools;
CREATE TRIGGER trg_tools_upd BEFORE UPDATE ON tools
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- =====================================================================
-- I. 대화（대화 페이지）
-- =====================================================================
CREATE TABLE IF NOT EXISTS conversations (
    id             BIGSERIAL PRIMARY KEY,
    agent_key      TEXT NOT NULL,
    title          TEXT NOT NULL DEFAULT '',
    llm_profile_id BIGINT REFERENCES llm_profiles(id) ON DELETE SET NULL,
    pinned_at      TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS pinned_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_conversations_llm_profile ON conversations(llm_profile_id) WHERE llm_profile_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_conversations_pinned ON conversations(pinned_at DESC) WHERE pinned_at IS NOT NULL;
DROP TRIGGER IF EXISTS trg_conversations_upd ON conversations;
CREATE TRIGGER trg_conversations_upd BEFORE UPDATE ON conversations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE IF NOT EXISTS conversation_activities (
    id                 BIGSERIAL PRIMARY KEY,
    conversation_id    BIGINT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    worker             TEXT,
    kind               TEXT,
    tool               TEXT,
    tool_use_id        TEXT,
    is_error           BOOLEAN NOT NULL DEFAULT false,
    summary            TEXT,
    detail             TEXT,
    input_tokens       INTEGER,
    output_tokens      INTEGER,
    cache_read_tokens  INTEGER,
    cache_write_tokens INTEGER,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_conv_act ON conversation_activities(conversation_id, id);
CREATE INDEX IF NOT EXISTS idx_conv_act_tool_call ON conversation_activities(conversation_id, tool_use_id, id)
  WHERE kind IN ('tool_use', 'tool_result');

-- =====================================================================
-- J. Agent 트리거
-- =====================================================================
CREATE TABLE IF NOT EXISTS agent_triggers (
    id                          BIGSERIAL PRIMARY KEY,
    agent_key                   TEXT NOT NULL,
    enabled                     BOOLEAN NOT NULL DEFAULT true,
    interval_sec                INTEGER NOT NULL DEFAULT 0,
    on_finding                  BOOLEAN NOT NULL DEFAULT false,
    on_goal_met                 BOOLEAN NOT NULL DEFAULT false,
    on_task_timeout             BOOLEAN NOT NULL DEFAULT false,
    on_tool_call                BOOLEAN NOT NULL DEFAULT false,
    on_task_create              BOOLEAN NOT NULL DEFAULT false,
    interval_message            TEXT NOT NULL DEFAULT '',
    finding_message             TEXT NOT NULL DEFAULT '',
    goal_message                TEXT NOT NULL DEFAULT '',
    task_timeout_message        TEXT NOT NULL DEFAULT '',
    tool_call_message           TEXT NOT NULL DEFAULT '',
    task_create_message         TEXT NOT NULL DEFAULT '',
    tool_names                  TEXT NOT NULL DEFAULT '',
    last_fire                   TIMESTAMPTZ,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_agent_triggers_agent ON agent_triggers(agent_key);
-- 컬럼 마이그레이션 추가(출판 버전,기존 도서관 업그레이드 및 보완 목록;새 도서관 CREATE 이미 이 열이 포함되어 있습니다.,ALTER 입니다 no-op)。멱등성,시작할 때마다 반복 실행 가능。
ALTER TABLE agent_triggers ADD COLUMN IF NOT EXISTS on_tool_call        BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE agent_triggers ADD COLUMN IF NOT EXISTS tool_call_message   TEXT    NOT NULL DEFAULT '';
ALTER TABLE agent_triggers ADD COLUMN IF NOT EXISTS tool_names          TEXT    NOT NULL DEFAULT '';
ALTER TABLE agent_triggers ADD COLUMN IF NOT EXISTS on_task_create      BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE agent_triggers ADD COLUMN IF NOT EXISTS task_create_message TEXT    NOT NULL DEFAULT '';
DROP TRIGGER IF EXISTS trg_agent_triggers_upd ON agent_triggers;
CREATE TRIGGER trg_agent_triggers_upd BEFORE UPDATE ON agent_triggers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE IF NOT EXISTS scheduler_state (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
);

-- =====================================================================
-- K. 차단 규칙
-- =====================================================================
CREATE TABLE IF NOT EXISTS intercept_rules (
    id              BIGSERIAL PRIMARY KEY,
    name            TEXT NOT NULL,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    priority        INTEGER NOT NULL DEFAULT 0,
    match_target    TEXT NOT NULL CHECK (match_target IN ('tool_name', 'tool_input')),
    match_type      TEXT NOT NULL CHECK (match_type IN ('string', 'regex')),
    pattern         TEXT NOT NULL,
    action          TEXT NOT NULL CHECK (action IN ('allow', 'deny', 'ask')),
    message         TEXT NOT NULL DEFAULT '',
    timeout_enabled BOOLEAN NOT NULL DEFAULT true,
    timeout_seconds INTEGER NOT NULL DEFAULT 60,
    timeout_action  TEXT    NOT NULL DEFAULT 'deny',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
DROP TRIGGER IF EXISTS trg_intercept_rules_upd ON intercept_rules;
CREATE TRIGGER trg_intercept_rules_upd BEFORE UPDATE ON intercept_rules
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE IF NOT EXISTS intercept_pending (
    id              BIGSERIAL PRIMARY KEY,
    rule_id         BIGINT REFERENCES intercept_rules(id) ON DELETE SET NULL,
    conversation_id BIGINT REFERENCES conversations(id) ON DELETE CASCADE,
    task_id         TEXT,
    agent_name      TEXT NOT NULL DEFAULT '',
    tool_name       TEXT NOT NULL,
    tool_input      JSONB NOT NULL DEFAULT '{}',
    status          TEXT NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'allowed', 'denied', 'timeout')),
    -- 판단이유:룰이 맞으면 룰이다 message;LLM 모델이 전체 판단 시 제시하는 간략한 이유(접두사 [모델])。
    reason          TEXT NOT NULL DEFAULT '',
    decided_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_intercept_pending_status ON intercept_pending(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_intercept_pending_task   ON intercept_pending(task_id, created_at DESC);
-- 기존 라이브러리 보충:reason 칼럼(출판 버전,꼭 가지고 가야겠어요 IF NOT EXISTS)。
ALTER TABLE intercept_pending ADD COLUMN IF NOT EXISTS reason TEXT NOT NULL DEFAULT '';
-- Detail payloads are lazy-loaded; NULL preserves the meaning of legacy history.
ALTER TABLE intercept_pending ADD COLUMN IF NOT EXISTS audit JSONB;
ALTER TABLE intercept_pending ADD COLUMN IF NOT EXISTS decision_source TEXT NOT NULL DEFAULT '';
UPDATE intercept_pending SET decision_source=CASE WHEN rule_id IS NOT NULL THEN 'rule'
 WHEN reason LIKE '[모델]%' THEN 'model' ELSE 'unknown' END WHERE decision_source='';

-- =====================================================================
-- L. 취약점 발견 지속성
-- =====================================================================
CREATE TABLE IF NOT EXISTS findings (
    id          BIGSERIAL PRIMARY KEY,
    task_id     BIGINT REFERENCES tasks(id) ON DELETE SET NULL,
    node_id     BIGINT REFERENCES exploration_nodes(id) ON DELETE SET NULL,
    vulnclass   TEXT NOT NULL DEFAULT '',
    -- 취약점 이름(읽을 수 있는 제목)；프런트 엔드가 비어 있으면 다시 표시됩니다. vulnclass。severity 값：
    -- critical 심각해요 / high 높음 / medium 에 / low 낮음（추가되지 않음 CHECK，그리고 status 꾸준히 server 화이트리스트 확인）。
    name        TEXT NOT NULL DEFAULT '',
    severity    TEXT NOT NULL DEFAULT '',
    summary     TEXT NOT NULL DEFAULT '',
    evidence    TEXT NOT NULL DEFAULT '',
    worker      TEXT NOT NULL DEFAULT '',
    asset_ids   JSONB NOT NULL DEFAULT '[]',
    -- 폐기상태：pending 보류 중 / in_progress 처리 중 / confirmed 확인됨 / resolved 처리됨 / fixed 고정됨 /
    -- false_positive 거짓양성 / ignored 무시 / duplicate 반복 / risk_accepted 위험 감수。
    -- 값이 추가되지 않았습니다. CHECK：예전 도서관은 맨 밑에 있어요 ALTER 보완 컬럼,CHECK 백필할 수 없습니다.,통합 server 사이드 화이트리스트 확인。
    status      TEXT NOT NULL DEFAULT 'pending',
    -- 취약점 상세 보고서(Markdown)；기본값은 비어 있습니다.,상세페이지만 읽을 수 있습니다./보여줘,피하기 위해 목록 인터페이스에 들어가지 마십시오. payload 확장。
    report      TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE findings ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'pending';
ALTER TABLE findings ADD COLUMN IF NOT EXISTS name   TEXT NOT NULL DEFAULT '';
ALTER TABLE findings ADD COLUMN IF NOT EXISTS report TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_findings_task ON findings(task_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_findings_time ON findings(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_findings_status ON findings(status, created_at DESC);
-- 「자산별」보기는 다음에 따라 다름 asset_ids @> '[<id>]' 역조사 발견,그런거 없어요 GIN 인덱스는 전체 테이블 스캔입니다.。
CREATE INDEX IF NOT EXISTS idx_findings_asset_ids ON findings USING GIN(asset_ids jsonb_path_ops);

-- 수동 재테스트는 독립적인 세션입니다.；결론은 원래 취약점 처리 상태와 별도로 저장됩니다.。
CREATE TABLE IF NOT EXISTS finding_retests (
    id BIGSERIAL PRIMARY KEY,
    finding_id BIGINT NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
    conversation_id BIGINT UNIQUE REFERENCES conversations(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','completed','failed','stopped')),
    verdict TEXT NOT NULL DEFAULT '' CHECK (verdict IN ('','reproduced','fixed','inconclusive')),
    notes TEXT NOT NULL DEFAULT '',
    snapshot JSONB NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    evidence TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_finding_retests_history ON finding_retests(finding_id, id DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_finding_retests_active ON finding_retests(finding_id)
    WHERE status IN ('pending','running');

-- 세션 삭제 및 재시험 기록 보관，끝나지 않은 재시험 직업도 취소해주세요。
CREATE OR REPLACE FUNCTION stop_deleted_conversation_retest() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE finding_retests SET status='stopped', error='재시험 세션이 삭제되었습니다.', finished_at=now()
    WHERE conversation_id=OLD.id AND status IN ('pending','running');
    RETURN OLD;
END;
$$;
DROP TRIGGER IF EXISTS trg_conversation_retest_delete ON conversations;
CREATE TRIGGER trg_conversation_retest_delete BEFORE DELETE ON conversations
    FOR EACH ROW EXECUTE FUNCTION stop_deleted_conversation_retest();

ALTER TABLE findings ADD COLUMN IF NOT EXISTS evidence_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE findings ADD COLUMN IF NOT EXISTS report_evidence_version BIGINT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS traffic_evidence_snapshots (
    id TEXT PRIMARY KEY,
    source_traffic_id TEXT NOT NULL,
    captured_at BIGINT NOT NULL,
    url TEXT NOT NULL,
    method TEXT NOT NULL,
    status INTEGER NOT NULL,
    content_type TEXT NOT NULL DEFAULT '',
    req_head TEXT NOT NULL,
    resp_head TEXT NOT NULL,
    req_hash TEXT NOT NULL,
    resp_hash TEXT NOT NULL,
    req_len BIGINT NOT NULL,
    resp_len BIGINT NOT NULL,
    unreferenced_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS finding_traffic_bindings (
    id BIGSERIAL PRIMARY KEY,
    finding_id BIGINT NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
    snapshot_id TEXT NOT NULL REFERENCES traffic_evidence_snapshots(id),
    role TEXT NOT NULL DEFAULT 'supporting',
    note TEXT NOT NULL DEFAULT '',
    position INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(finding_id, snapshot_id)
);
CREATE INDEX IF NOT EXISTS idx_finding_traffic_order ON finding_traffic_bindings(finding_id, position, id);
CREATE INDEX IF NOT EXISTS idx_finding_traffic_snapshot ON finding_traffic_bindings(snapshot_id);

-- =====================================================================
-- M. 백엔드 로그 지속성
-- =====================================================================
CREATE TABLE IF NOT EXISTS server_logs (
    id         BIGSERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    level      TEXT NOT NULL DEFAULT 'info',
    tag        TEXT NOT NULL DEFAULT '',
    text       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_server_logs_id ON server_logs(id DESC);

-- Independent /btw history and the latest provider-ready main checkpoint.
CREATE TABLE IF NOT EXISTS side_question_sessions (
    session_key TEXT PRIMARY KEY,
    conversation_id BIGINT REFERENCES conversations(id) ON DELETE CASCADE,
    task_id BIGINT REFERENCES tasks(id) ON DELETE CASCADE,
    exploration_id BIGINT REFERENCES explorations(id) ON DELETE CASCADE,
    intent_id BIGINT REFERENCES exploration_nodes(id) ON DELETE CASCADE,
    run_id BIGINT NOT NULL,
    version BIGINT NOT NULL,
    snapshot JSONB NOT NULL,
    generation BIGINT NOT NULL DEFAULT 0,
    CHECK ((conversation_id IS NOT NULL AND task_id IS NULL AND exploration_id IS NULL AND intent_id IS NULL)
        OR (conversation_id IS NULL AND task_id IS NOT NULL AND exploration_id IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS idx_side_sessions_conv ON side_question_sessions(conversation_id);
CREATE INDEX IF NOT EXISTS idx_side_sessions_task ON side_question_sessions(task_id);
CREATE INDEX IF NOT EXISTS idx_side_sessions_exp ON side_question_sessions(exploration_id);
CREATE INDEX IF NOT EXISTS idx_side_sessions_intent ON side_question_sessions(intent_id);

CREATE TABLE IF NOT EXISTS side_question_requests (
    id TEXT PRIMARY KEY,
    ordinal BIGSERIAL UNIQUE,
    session_key TEXT NOT NULL REFERENCES side_question_sessions(session_key) ON DELETE CASCADE,
    generation BIGINT NOT NULL,
    client_id TEXT NOT NULL,
    question TEXT NOT NULL,
    answer TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK(status IN ('running','completed','failed','cancelled','interrupted')),
    error TEXT NOT NULL DEFAULT '',
    model JSONB NOT NULL,
    snapshot_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    sequence BIGINT NOT NULL DEFAULT 0,
    usage JSONB NOT NULL DEFAULT '{}',
    UNIQUE(session_key,generation,client_id)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_side_request_running ON side_question_requests(session_key) WHERE status='running';
CREATE INDEX IF NOT EXISTS idx_side_requests_history ON side_question_requests(session_key,ordinal DESC);

-- Additive v3 archive fields; old archives restore these as empty objects.
ALTER TABLE side_question_sessions ADD COLUMN IF NOT EXISTS memory JSONB NOT NULL DEFAULT '{}';
ALTER TABLE side_question_requests ADD COLUMN IF NOT EXISTS context_info JSONB NOT NULL DEFAULT '{}';

-- =====================================================================
-- 자산 차단 규칙（글로벌 블랙리스트）
-- 독립 §K 명령 차단(intercept_rules)：intercept_rules 일치하는 도구 이름/입력 매개변수 텍스트，
-- 이 표는 일치합니다.「대상자산」——합동/도메인 이름이 모호함·IP·URL 그리고 CIDR 네트워크 세그먼트。
-- 규칙만 남음；특정 매칭/차단 논리는 다른 곳에서 구현됩니다.。
-- kind 7가지 유형：
--   exact_domain / exact_ip / exact_url  —— 합동 일치
--   fuzzy_domain / fuzzy_ip / fuzzy_url  —— 퍼지 매칭
--   cidr                                 —— CIDR 네트워크 세그먼트
-- =====================================================================
CREATE TABLE IF NOT EXISTS asset_intercept_rules (
    id          BIGSERIAL PRIMARY KEY,
    enabled     BOOLEAN NOT NULL DEFAULT true,
    kind        TEXT NOT NULL CHECK (kind IN (
                    'exact_domain', 'exact_ip', 'exact_url',
                    'fuzzy_domain', 'fuzzy_ip', 'fuzzy_url',
                    'cidr')),
    pattern     TEXT NOT NULL,
    note        TEXT NOT NULL DEFAULT '',
    builtin     BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_asset_intercept_enabled ON asset_intercept_rules(enabled);
DROP TRIGGER IF EXISTS trg_asset_intercept_rules_upd ON asset_intercept_rules;
CREATE TRIGGER trg_asset_intercept_rules_upd BEFORE UPDATE ON asset_intercept_rules
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- =====================================================================
-- 태스크 수준 자산 차단/허용 규칙
-- 그리고 글로벌 asset_intercept_rules 동형（kind/pattern/note/enabled），그런데 누르세요 task_id
-- 관련、작업 계단식으로 삭제；작업 생성 시 입력、작업 내용을 수정할 수 있습니다.。
-- action: 'block'=차단(테스트 금지)  'allow'=허용됨(화이트리스트)。
-- 집행판결：먼저 눌러주세요 차단 규칙(글로벌 ∪ 임무block) 일치，타격 금지；누락되었으며 작업이 존재합니다.
-- 활성화됨 allow 규칙 시간，뭐라도 쳐야지 allow 출시됩니다，그렇지 않으면「테스트가 허용되지 않습니다.」。
-- =====================================================================
CREATE TABLE IF NOT EXISTS task_intercept_rules (
    id          BIGSERIAL PRIMARY KEY,
    task_id     BIGINT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    enabled     BOOLEAN NOT NULL DEFAULT true,
    action      TEXT NOT NULL DEFAULT 'block' CHECK (action IN ('block','allow')),
    kind        TEXT NOT NULL CHECK (kind IN (
                    'exact_domain', 'exact_ip', 'exact_url',
                    'fuzzy_domain', 'fuzzy_ip', 'fuzzy_url',
                    'cidr')),
    pattern     TEXT NOT NULL,
    note        TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_task_intercept_task ON task_intercept_rules(task_id);
-- 기존 라이브러리 보충(이 테이블은 이 세션의 앞부분에서 생성되었습니다.、없음 action 칼럼)：지알리(와 함께 IF NOT EXISTS)。
ALTER TABLE task_intercept_rules ADD COLUMN IF NOT EXISTS action TEXT NOT NULL DEFAULT 'block';
DROP TRIGGER IF EXISTS trg_task_intercept_rules_upd ON task_intercept_rules;
CREATE TRIGGER trg_task_intercept_rules_upd BEFORE UPDATE ON task_intercept_rules
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- =====================================================================
-- M. 취약점 IM 푸시
--
-- 세 개의 테이블을 의도적으로 분리했습니다.，핵심은**폭발 반경**：취약점을 작성한 트랜잭션(RecordFindingTx，
-- 작업 행 잠금 보류)시각장애인은 한 번만 허용 INSERT，채널 테이블을 읽지 못함、사용자 필터링 규칙을 실행하지 않음。그렇지 않으면
-- 일치가 잘못되었습니다. webhook 필터링 조건으로 인해 오염이 발생할 수 있습니다./거래 중단，결과적으로 취약점을 저장할 수 없습니다.。
--
--   notification_channels   채널 인스턴스 구성(가변적、자격 증명이 있는 경우、UI 관리)
--   notification_events     사건사실(쓰기 취약한 트랜잭션 블라인드 삽입，렌더링 스냅샷 포함)
--   notification_deliveries 납품업무(대외업무 fan-out 생성됨，베어링 상태/다시 시도해보세요/배치)
-- =====================================================================

-- 채널 예시：마찬가지예요 kind 임의의 숫자로 구성 가능(「비상반」「일일그룹」DingTalk 로봇 각 1대씩)。
-- kind 값은 다음과 같이 지정됩니다. server 사이드 화이트리스트 확인，추가되지 않음 CHECK：그리고 findings.status 같은 이유，
-- 이후에 채널을 추가할 때 테이블 구조를 변경할 필요가 없습니다.。
CREATE TABLE IF NOT EXISTS notification_channels (
    id           BIGSERIAL PRIMARY KEY,
    name         TEXT NOT NULL,
    -- dingtalk 딩톡 / feishu 페이슈 / wecom 기업 위챗 / webhook 일반 / telegram / email
    kind         TEXT NOT NULL,
    enabled      BOOLEAN NOT NULL DEFAULT true,
    -- 자격 증명(일반 텍스트 저장，UI 마스크 에코；또 만나요 server 쪽 maskChannelSecrets)。6개 채널의 필드는 매우 다릅니다.，
    -- 통일 JSONB + Go 사이드프레스 kind 엄격한 검증，각 채널에 무리를 추가하지 마십시오. NULL 칼럼：
    --   dingtalk {webhook,secret}
    --   feishu   {webhook,secret}
    --   wecom    {webhook}
    --   webhook  {url,method,content_type,headers{},body_template}
    --   telegram {bot_token,chat_id,base_url}
    --   email    {host,port,username,password,from,to[],tls}
    config       JSONB NOT NULL DEFAULT '{}',
    -- 푸시 타이밍：realtime 치고 밀고 / digest 들어오는 배치는 글로벌 주기에 따라 하나로 요약됩니다.。
    mode         TEXT NOT NULL DEFAULT 'realtime',
    -- 필터 조건，모든 항목은 선택사항입니다.(기본값=필터링 없음)：
    --   min_severity       ''|low|medium|high|critical
    --   task_ids/asset_ids 빈 배열=제한 없음；비어 있지 않은 경우 교차점은 비어 있지 않아야 합니다.
    --   vulnclass_include/exclude 키워드 배열(대소문자를 구분하지 않는 하위 문자열)；include 비어 있음=다 받아보세요
    --   on_status_change   bool，만 realtime 패턴이 이해가 되네요
    filter       JSONB NOT NULL DEFAULT '{}',
    -- 분당 최대 전송 한도；0=전류 제한 없음。기본값 20 딩톡 정렬/Qiwei 공식 하드 제한。
    -- 제한을 초과해도 메시지가 손실되지 않습니다.，다음으로만 배송을 미루세요 tick。
    rate_per_min INTEGER NOT NULL DEFAULT 20,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
DROP TRIGGER IF EXISTS trg_notification_channels_upd ON notification_channels;
CREATE TRIGGER trg_notification_channels_upd BEFORE UPDATE ON notification_channels
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 사건사실。 RecordFindingTx / 상태 변경 트랜잭션**똑같습니다**쓰기，보장「취약점 로깅」
-- 그리고「푸시 작업이 존재합니다.」원자적 일관성——이 존재하지 않습니다. 제출이 완료되었지만 팀에 추가되지 않았습니다.、메시지가 영구적으로 손실되는 창。
-- snapshot 의도적으로 중복됨：해당 취약점은 추후 이름이 변경될 예정입니다./레벨 변경/상태 변경，푸시 내용이 반영되어야 합니다.「사건 당시」，
-- 그리고 fan-out 렌더링으로 다시 확인할 필요가 없습니다. findings/tasks/assets 여러 테이블。
-- finding 이벤트는 삭제 후 계단식으로 삭제되지 않습니다.：그리고 findings 테이블「삭제된 작업은 계속 독립적으로 유지됩니다.」의 의미는 일관됩니다.。
CREATE TABLE IF NOT EXISTS notification_events (
    id         BIGSERIAL PRIMARY KEY,
    -- finding_created | finding_status_changed
    kind       TEXT NOT NULL,
    finding_id BIGINT NOT NULL,
    snapshot   JSONB NOT NULL,
    -- fan-out 멱등성 태그：dispatcher 이벤트를 전달하려면 이 열을 클릭하세요.，처리완료 true。
    -- 행 삭제 대신 열 사용，해당 이벤트까지 배송 이력을 추적할 수 있도록。
    fanned_out BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_notification_events_pending
    ON notification_events(id) WHERE NOT fanned_out;

-- 납품업무：이벤트 × 활성화 채널 = 한 줄。fan-out 업무 외적으로 하세요，그래서 채널
-- 나중에 열면 기록이 업데이트되지 않습니다(그리고 agent_triggers 님「늦게 열림 trigger 역사를 만회하지 마세요」일관된 의미 체계，
-- 채널을 활성화할 때 일회성 새로 고침 기록 백로그를 방지하세요.)。
-- channel_id 계단식 삭제：채널 구성이 사라졌습니다，배송이력은 의미가 없습니다。
CREATE TABLE IF NOT EXISTS notification_deliveries (
    id          BIGSERIAL PRIMARY KEY,
    event_id    BIGINT NOT NULL REFERENCES notification_events(id) ON DELETE CASCADE,
    channel_id  BIGINT NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    -- pending 출발 준비 완료 / sent 보냄 / failed 재시도 횟수가 부족함(수동으로 재전송 가능) / skipped 채널 비활성화 또는 일괄 취소
    -- pending 출발 준비 완료 / sending 이 되었습니다. dispatcher 받기(임대가 만료되지 않았습니다.) / sent 보냄 /
    -- failed 소진되거나 영구적인 실패를 재시도합니다.(수동으로 재전송 가능) / skipped 채널이 비활성화되었습니다。값이 추가되지 않았습니다. CHECK，
    -- 그리고 findings.status 같은 이유， server 사이드 화이트리스트 확인。
    state       TEXT NOT NULL DEFAULT 'pending',
    attempts    INTEGER NOT NULL DEFAULT 0,
    -- 동시에「다음번엔 모을 수 있겠네요」그리고「임대 만료 시간」：받았을 때 미래로 밀면 임대가 된다.，
    -- 그래서「임대가 만료되지 않았습니다.」그리고「재시도 시간이 아직 도착하지 않았습니다.」동일한 조건식을 공유합니다.，추가 불필요
    -- lease_until 칼럼。프로세스 충돌이 남아 있습니다. sending 임대 만료로 인해 다음 라운드에서 길드를 다시 되찾을 예정입니다.。
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error  TEXT NOT NULL DEFAULT '',
    -- digest 모드가 배치와 공유됩니다.；realtime 헝웨이 NULL。전체 배치가 하나의 메시지로 렌더링된 다음 함께 배치됩니다. sent。
    batch_id    BIGINT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at     TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_notification_deliveries_due
    ON notification_deliveries(next_attempt_at) WHERE state='pending';
CREATE INDEX IF NOT EXISTS idx_notification_deliveries_history
    ON notification_deliveries(id DESC);
CREATE INDEX IF NOT EXISTS idx_notification_deliveries_batch
    ON notification_deliveries(batch_id) WHERE batch_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_notification_deliveries_channel
    ON notification_deliveries(channel_id, id DESC);
