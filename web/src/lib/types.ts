// ARTEX domain model — types used across the UI.
// Derived from the functional spec (section 7: 주요 데이터 형태).

export type TaskStatus = "created" | "queued" | "running" | "paused" | "done" | "failed" | "timeout";
export type EngineMode = "exploring" | "paused" | "stalled" | "idle";

export interface Task {
  id: string;
  name?: string; // 선택적 작업 이름;비어 있음/기본값=이름 없음,표시 시 설명으로 돌아가기
  category_id?: number;
  category_name?: string;
  pinned?: boolean;
  pinned_at?: string | null;
  description: string;
  goal: string;
  status: TaskStatus;
  created_at: string;
  created_unix?: number; // created_at as unix seconds (run-duration calc)
  completed_at?: string; // RFC3339 finish time (done/failed); "" if unfinished
  completed_unix?: number; // completed_at as unix seconds (0/undef if unfinished)
  last_activity_unix?: number; // unix seconds of the last activity (0/undef if none)
  paused?: boolean;
  queued?: boolean;
  active?: boolean;
  in_flight?: number;
  findings?: { critical: number; high: number; medium: number; low: number }; // 등록된 취약점 수(심각도순으로 정렬)
  last_activity?: string;
  stalled?: boolean;
  goals_total?: number;
  goals_met?: number;
  engine_mode?: EngineMode;
  tokens?: TokenTotal; // whole-task token consumption
  llm_profile_id?: number; // LLM profile used; absent = default profile
  llm_profile_ids?: number[]; // ordered task-level failover chain
  active_llm_profile_id?: number; // profile used by the next LLM call
  llm_failover_state?: "default" | "ready" | "chain_exhausted" | string;
  llm_failover_reason?: string;
  source_task_ids?: string[]; // directly related tasks inherited as read-only context
  archive_blocked_by_task_id?: string; // live direct dependent that must be archived first
  company_ids?: number[]; // associated company scopes; current company assets join the task at creation
  coverage_enabled?: boolean; // 자산보상 기능 스위치(생성 시기,기본적으로 켜져 있음)；false=계산되지 않음/적용 범위를 표시하지 않음
}

export interface TaskCategory {
  id: number;
  name: string;
  task_count: number;
  created_at: string;
  updated_at: string;
}

export interface TaskTemplate {
  id: number;
  name: string;
  description: string;
  goal: string;
  category_id?: number | null; // 기본 카테고리；null/기본값=없음
  intercept_rules?: AssetInterceptRuleInput[]; // 기본 작업 수준 차단/허용 규칙
  created_at: string;
  updated_at: string;
}

export interface DeleteTaskOptions {
  delete_assets: boolean;
  delete_traffic: boolean;
  delete_files: boolean;
  delete_findings: boolean;
  delete_llm_records: boolean;
}

export interface DeleteTaskResult {
  deleted: string;
  assets_deleted: number;
  assets_detached: number;
  traffic_deleted: number;
  files_deleted: boolean;
  findings_deleted: number;
  llm_records_deleted: number;
  cleanup_warning?: string;
}

export type TaskArchiveState =
  | "archive_queued"
  | "archiving"
  | "archive_failed"
  | "ready"
  | "restore_queued"
  | "restoring"
  | "restore_failed"
  | "delete_queued"
  | "deleting"
  | "delete_failed";

export interface TaskArchiveTokenStats {
  calls?: number;
  input_tokens?: number;
  output_tokens?: number;
  cache_read_tokens?: number;
  cache_write_tokens?: number;
}

export interface TaskArchive {
  id: number;
  task_id: number;
  state: TaskArchiveState;
  phase: string;
  progress: number;
  error?: string;
  warnings?: string[];
  format_version: number;
  sha256?: string;
  original_size: number;
  compressed_size: number;
  task_name: string;
  task_description: string;
  task_goal: string;
  original_status: TaskStatus;
  category_id?: number;
  category_name?: string;
  source_task_ids: number[];
  remaining_timeout_seconds: number;
  data_counts: Record<string, number>;
  aggregate_stats: {
    tokens?: TaskArchiveTokenStats;
    skills?: Record<string, number>;
    tools?: Record<string, number>;
    findings?: Record<string, number>;
  };
  archived_at?: string;
  requested_at: string;
  created_at: string;
  updated_at: string;
}

export interface TaskArchivePage {
  items: TaskArchive[];
  total: number;
  page: number;
  size: number;
}

export interface ArchiveBatchItem {
  id: string;
  archive_id?: number;
  ok: boolean;
  queued: boolean;
  error?: string;
}

// ---- Asset graph (global, shared across tasks) ----
export type AssetType =
  | "company"
  | "domain"
  | "ip"
  | "port"
  | "service"
  | "site"
  | "endpoint"
  | "parameter"
  | "tech"
  | "credential"
  | "data";

export type NodeState = "observed" | "confirmed" | "tombstoned";

export interface AssetNode {
  id: string;
  type: AssetType;
  name: string;
  key: string; // nkey
  value?: string;
  company_id?: string; // 회사 자산에 속함 id；비어 있음=소유하지 않음
  state: NodeState;
  confidence: number; // 0..1
  attrs?: Record<string, unknown>;
  first_seen: string;
  last_seen: string;
}

export type AssetRel =
  | "owns"
  | "resolves"
  | "exposes"
  | "runs"
  | "serves"
  | "has_endpoint"
  | "has_param"
  | "fingerprinted"
  | "authenticates_as"
  | "reachable"
  | "has_subdomain";

export interface Edge {
  src: string;
  dst: string;
  rel: AssetRel | ExploreRel;
}

// Task asset view — server-side enriched, paginated.
export interface TaskAssetRef {
  id: string;
  name?: string;
  key: string;
  attrs?: Record<string, unknown>;
}

export interface TaskAssetItem extends AssetNode {
  techs?: TaskAssetRef[];
  auth?: TaskAssetRef[];
  params?: TaskAssetRef[];
}

export interface TaskAssetView {
  counts: Record<string, number>;
  total: number;
  items: TaskAssetItem[];
}

// ---- New unified asset model (new backend) ----
export type NewAssetType = "root_domain" | "ip" | "subdomain" | "app" | "service" | "endpoint";

export interface Asset {
  id: number;
  type: NewAssetType;
  company_id?: number;
  task_ids: number[];
  domain?: string;
  root_domain?: string;
  ip?: string;
  c_segment?: string;
  port?: number;
  icp?: string;
  bound_domains?: string[];
  open_ports?: { port: number; service?: string }[];
  record_type?: string;
  record_value?: string[] | string;
  bundle_id?: string;
  app_name?: string;
  category?: string;
  app_description?: string;
  app_icp?: string;
  url?: string;
  service_type?: string;
  service_name?: string;
  favicon_mmh3?: string;
  status_code?: number;
  content_length?: number;
  page_title?: string;
  technologies?: string[];
  auth?: Record<string, unknown>[];
  method?: string;
  params?: Record<string, unknown>[];
  extra?: Record<string, unknown>;
  last_seen: string;
  task_source?: string;
  task_source_summary?: string;
  task_source_node_id?: number;
}

export interface IntentAsset {
  intent_id: number | string;
  asset_id: number;
  type: NewAssetType;
  label: string;
  source: string;
  source_summary: string;
  source_node_id?: number;
  source_task_id: number;
  inherited: boolean;
}

export interface TaskAssetMutation {
  requested: number;
  attached: number;
  existing: number;
}

export interface TaskAssetScopeMutation {
  requested: number;
  assets_linked: number;
  assets_existing: number;
  scopes_added: number;
  scopes_existing: number;
}

// ---- Asset coverage graph (per task) ----
// 강제 유도「자산 커버리지 맵」노드。key 만：자산="a:<id>"、회사="c:<id>"、
// 자산 행이 없는 루트 도메인 이름="r:<domain>"。in_scope=false 은 배선에만 사용되는 회색 컨텍스트 노드입니다.。
export interface CoverageGraphNode {
  key: string;
  kind: "company" | "root_domain" | "subdomain" | "ip" | "service" | "app" | "endpoint";
  label: string;
  tested: boolean;
  in_scope: boolean;
  asset_id?: number;
  company_id?: number;
  domain?: string;
  root_domain?: string;
  ip?: string;
  url?: string;
  port?: number;
  service_type?: string;
  app_name?: string;
  page_title?: string;
  status_code?: number;
}

export interface CoverageGraphEdge {
  src: string;
  dst: string;
}

export interface CoverageGraphData {
  nodes: CoverageGraphNode[];
  edges: CoverageGraphEdge[];
}

// 이 작업 탐색 맵의 자산과 관련된 의도/사실/찾음（오버레이 그래프 노드 서랍용）。
export interface CoverageAssetRef {
  id: number;
  kind: string;
  state: string;
  summary: string;
  source_task_id?: string;
  inherited?: boolean;
}
export interface CoverageAssetRefs {
  intents: CoverageAssetRef[];
  facts: CoverageAssetRef[];
  findings: CoverageAssetRef[];
}

// ---- Workspace file manager (workDir) ----
export interface WorkspaceEntry {
  name: string;
  path: string; // workspace-relative, forward slashes
  dir: boolean;
  size: number;
  mtime: number; // unix millis
}
export interface WorkspaceListing {
  path: string;
  entries: WorkspaceEntry[];
}
export interface WorkspaceFile {
  path: string;
  size: number;
  binary: boolean;
  too_large?: boolean;
  content?: string;
}

// 태스크 테스트 범위의 항목 1개（적용 범위 분모 + 권한 범위）。
export interface TaskScopeRow {
  id: number;
  task_id: number;
  kind: "company" | "root_domain" | "subdomain" | "ip" | "cidr" | "icp" | "keyword";
  company_id?: number;
  company_name?: string; // 백엔드 JOIN companies 분석，만 kind=company 귀중한
  domain?: string;
  net?: string;
  value?: string;
  source: "auto" | "agent" | "manual";
  reason?: string;
}

export type CompanyScopeKind = "domain" | "ip" | "cidr" | "icp" | "keyword";

// 새로운 기업을 추가할 때 제출된 구조화 자산 범위 규칙。
export interface CompanyScopeRule {
  kind: CompanyScopeKind;
  value: string;
}

// 자산 범위 작성 결과。errors 은 이 제출물에서 잘못된 줄입니다.；warnings 은 이 제출물과 관련이 없습니다.、
// 그러나 기여 결과가 기대에 미치지 못하게 만드는 기존 데이터 문제가 있습니다.（ ip 이 필드는 호스트 이름의 자산을 저장합니다.）。
export interface CompanyScopeMutation {
  added: number;
  skipped: number;
  invalid: number;
  errors?: string[];
  warnings?: string[];
}

// 회사의 자산 범위 규칙 중 하나（유일한 진실 소스에 대한 귀속）。
export interface ScopeRow {
  id: number;
  company_id: number;
  kind: CompanyScopeKind;
  domain?: string; // kind=domain 때로는 가치가 있습니다
  net?: string; // kind=ip|cidr 때로는 가치가 있습니다
  value?: string; // kind=icp|keyword 은 백엔드에서 직접 반환될 수 있습니다.
  raw: string; // 원래 사용자 입력，표시 및 백필용
  reason?: string;
}

// 기업：type=company 의 자산 노드 + 아이콘 + 자산수 + 자산 범위 규칙。
export interface Company {
  id: number;
  name: string;
  logo?: string; // 원격 아이콘 URL；비어있는 경우 앞부분에 이름의 첫 글자를 사용합니다.
  asset_count: number;
  scope?: ScopeRow[];
}

// ---- Exploration graph (per task) ----
export type ExploreKind = "task" | "begin" | "goal" | "intent" | "fact" | "finding" | "hint" | "digest";
export type GoalState = "open" | "met" | "abandoned";
export type IntentState = "open" | "running" | "paused" | "done" | "blocked" | "exhausted" | "stopped";
export type FindingState = "confirmed" | "dismissed";
export type HintState = "active" | "consumed";
export type ExploreRel = "spawns" | "derived_from" | "yields" | "proves" | "covers";

export interface TaskNode {
  id: string;
  type: ExploreKind;
  payload?: string;
  priority: number; // 0..10
  state: string; // GoalState | IntentState | FindingState | HintState
  origin: string;
  ts: string;
  source_task_id?: string;
  inherited?: boolean;
  delete_reason?: string; // 허위삭제 의도(state='deleted')일 때 삭제 이유
}

// 공지사항 게시판 페이지:생성된 순서대로 페이지가 매겨진 노드 + 이 페이지에 관련된 가장자리 + Edge의 반대쪽 끝에 있는 노드(refs,언론 id 색인),
// 이렇게 하면 모든 방송에서 명확하게 설명할 수 있습니다.「어디서 나온 말인가요?、무엇이 생산되나요?」,전체 그림을 끌어내리기보다는。
export interface ExplorationNodePage {
  items: TaskNode[];
  total: number;
  page: number;
  size: number;
  edges: Edge[];
  refs: Record<string, TaskNode>;
  // 노드 id → 이 노드에 고정된 자산(공지사항 게시판을 확장하면 표시됩니다.,이 페이지의 노드와 해당 이웃을 포함합니다.)。
  assets: Record<string, FindingAsset[]>;
}

export interface ExplorationNodeQuery {
  page?: number;
  size?: number;
  kinds?: ExploreKind[];
  states?: string[];
  q?: string;
  order?: "asc" | "desc";
}

// 목표관리카드용 목표(백엔드에는 payload 분할 text/vulnclass)。
export interface TaskGoal {
  id: string;
  text: string;
  vulnclass?: string;
  state: string; // GoalState
  origin?: string;
  ts: string;
}

// 제약 관리 카드에 대한 작업 제약(allow=허용됨 / deny=금지됨)。
export type ConstraintKind = "allow" | "deny";
export interface TaskConstraint {
  id: string;
  kind: ConstraintKind;
  text: string;
  origin?: string;
  ts?: string;
}

// ---- Findings ----
export type Severity = "critical" | "high" | "medium" | "low";

// 취약점 처리 현황:보류 중 / 처리 중 / 확인됨 / 처리됨 / 고정됨 / 거짓양성 / 무시 / 반복 / 위험 감수。
export type FindingStatus =
  | "pending"
  | "in_progress"
  | "confirmed"
  | "resolved"
  | "fixed"
  | "false_positive"
  | "ignored"
  | "duplicate"
  | "risk_accepted";

// FindingAsset 은 취약점에 묶인 자산입니다.(백엔드에서 사전 렌더링됨 label)。
export interface FindingAsset {
  id: string;
  type: string;
  label: string;
}

export interface Finding {
  traffic_count?: number;
  evidence_version?: number;
  report_evidence_version?: number;
  report_stale?: boolean;
  id: string;
  finding_id?: string; // 독립 findings 테이블의 행 id,상태 업데이트 핸들(작업의 이전 노드가 누락되었을 수 있습니다.)
  vulnclass: string;
  name?: string; // 취약점 이름;비어 있으면 디스플레이가 다음으로 돌아갑니다. vulnclass
  severity: Severity;
  status: FindingStatus;
  summary: string;
  evidence: string;
  report?: string; // 상세보고(Markdown);세부정보 인터페이스만 반환됩니다.,목록이 비어있습니다.
  intent_id?: string;
  param_id?: string;
  task_id?: string;
  task_description?: string;
  source_task_id?: string;
  inherited?: boolean;
  assets?: FindingAsset[];
  ts: string;
}

// FindingsPage 은 검색 목록의 서버측 페이징 응답입니다.。
export interface FindingsPage {
  items: Finding[];
  total: number;
  page: number;
  page_size: number;
}

export interface FindingGroup {
  task_id: string | number | null;
  task_name?: string; // 선택적 작업 이름;비어 있음/기본값=이름 없음
  task_description: string;
  task_status: string;
  count: number;
  critical: number;
  high: number;
  medium: number;
  low: number;
  last_found_at: string;
}

export interface FindingGroupsPage {
  items: FindingGroup[];
  total: number;
  finding_total: number;
  page: number;
  page_size: number;
}

export interface FindingDeepenResponse {
  task_id: string;
  intent_id: string;
  state: IntentState;
  queued: boolean;
}

// FindingStats 은 전체 테이블 집계의 발견입니다.(통계 카드 + 취약점 유형 드롭다운),서버측 컴퓨팅,페이징의 영향을 받지 않음。
export interface FindingStats {
  total: number;
  pending: number;
  critical: number;
  high: number;
  medium: number;
  low: number;
  vulnclasses: string[];
  tasks: FindingTaskOption[];
}

// FindingTaskOption 은 검색 페이지입니다「작업별」드롭다운 목록에서 항목 필터링:취약한 작업(빈 설명은 작업이 삭제되었음을 나타냅니다.,
// 프런트 엔드 롤백 디스플레이 id)및 취약점 수。
export interface FindingTaskOption {
  id: string | number;
  name?: string; // 선택적 작업 이름;비어 있음/기본값=이름 없음
  description: string;
  count: number;
}

// FindingQuery 은 검색 목록 페이징입니다./필터/매개변수 정렬。
export interface FindingQuery {
  page: number;
  pageSize: number;
  severity?: "all" | Severity;
  status?: "all" | FindingStatus;
  vulnclass?: string;
  task?: string; // 임무 id;"all"/비어 있음 = 작업별로 필터링하지 않음
  query?: string;
  sort?: "severity" | "time";
  // 자산 트리 노드 key;노드를 선택하세요 = 전체 하위 트리를 선택합니다.。비어 있음 = 자산으로 필터링하지 않음。
  assetScope?: string;
}

// ---- Findings by asset (자산 보기) ----
export type FindingAssetKind = "company" | "root_domain" | "subdomain" | "ip" | "service" | "app" | "endpoint" | "none";

// FindingAssetNode 은 자산 트리의 노드입니다.。key 모양은 다음과 같습니다 a:<id>(자산)、c:<id>(기업)、
// r:<domain>(라이브러리에 자산 행의 루트 도메인 이름이 없습니다.)、__none__(연결되지 않은 자산)。
export interface FindingAssetNode {
  key: string;
  parent?: string;
  kind: FindingAssetKind;
  label: string;
  asset_id?: number;
  company_id?: number;
  self: number; // 자산에 직접 연결된 발견 수
  total: number; // 한후손、검색 결과에 따라 중복 항목을 제거합니다.
  critical: number;
  high: number;
  medium: number;
  low: number;
  last_found_at: string;
}

export interface FindingAssetTree {
  nodes: FindingAssetNode[];
  finding_total: number;
  truncated: boolean;
  dropped_kinds?: string[];
}

// FINDING_UNASSIGNED_ASSET 및 백엔드 db.FindingUnassignedAsset 해당。
export const FINDING_UNASSIGNED_ASSET = "__none__";

// ---- Activity / sessions ----
export type ActivityKind =
  | "tool_use"
  | "tool_result"
  | "text"
  | "thinking"
  | "result"
  | "user"
  | "intent" // LLM-generated exploration objective leading a worker session (UI-synthesized)
  | "round" // planner round boundary marker (engine-emitted)
  | "usage" // live cumulative token usage (per model turn); not rendered
  | "llm_switch" // automatic/manual task-level LLM switch
  | "llm_failover" // task-level provider switch / chain exhaustion audit event
  | "intercept_request"; // user-approval request from the intercept layer

// ChatAttachment 은 한 번 업로드한 파일입니다.:path 이 세션과 관련/태스크 작업 디렉터리(그렇죠 agent 님 CWD)。
export interface ChatAttachment {
  name: string;
  path: string;
  size: number;
  abs?: string; // 절대 경로(scope=staging 임시업로드시 돌아가기;작업을 생성하기 전에 설명에 적어주세요.)
}

export interface Activity {
  seq: number;
  intent_id?: string;
  worker: string; // session owner: planner | mainagent | work#1 ...
  ts: string;
  kind: ActivityKind;
  tool?: string;
  tool_use_id?: string;
  is_error?: boolean;
  summary: string;
  detail?: string;
  metadata?: {
    llm_transition?: LLMTransition;
  };
  source_task_id?: string;
  inherited?: boolean;
  main_seg?: number; // main-agent conversation segment (present only on worker="mainagent" rows)
  // token usage (present only on kind='result')
  input_tokens?: number;
  output_tokens?: number;
  cache_read_tokens?: number;
  cache_write_tokens?: number;
}

export interface LLMAuditProfile {
  id: number;
  name: string;
  format: string;
  model: string;
}

export interface LLMTransition {
  mode: "automatic" | "manual" | "exhausted";
  reason: string;
  previous?: LLMAuditProfile;
  next?: LLMAuditProfile;
}

export interface TaskLLMResolution {
  profile_id?: number;
  name: string;
  format: string;
  model: string;
  source: "task_chain" | "agent_binding" | "global_profile" | "environment" | "global";
  available: boolean;
  reason?: string;
}

export interface TaskLLMResolutions {
  mainagent: TaskLLMResolution;
  planner: TaskLLMResolution;
  worker: TaskLLMResolution;
}

// ---- Agent triggers (P3 일정 조정 중，사용자 정의 전용 agent) ----
export interface AgentTrigger {
  id: number;
  agent_key: string;
  enabled: boolean;
  interval_sec: number; // 타이밍:매 N 초(0=부정기적으로)
  on_finding: boolean; // 모든 작업 검색 finding 일 때 트리거됩니다.
  on_goal_met: boolean; // 작업이 목표에 도달하면 트리거됩니다.
  on_task_timeout: boolean; // 작업 시간이 초과되면 트리거됩니다.
  on_tool_call: boolean; // 선택한 도구는(실행 완료)일 때 트리거됩니다.
  on_task_create: boolean; // 작업이 생성되면 트리거됩니다.
  interval_message: string; // 각 트리거 조건에 대한 독립적인 사용자 메시지
  finding_message: string;
  goal_message: string;
  task_timeout_message: string;
  tool_call_message: string;
  task_create_message: string;
  tool_names: string[]; // on_tool_call 선택한 도구 key(적어도 하나는)
  last_fire?: string;
}

// ---- Conversations (chat page) ----
export interface ActiveFindingRetest {
  id: number;
  finding_id: string;
  conversation_id: number;
  status: "pending" | "running";
}

export interface FindingRetest {
  id: number;
  finding_id: number;
  conversation_id: number | null;
  status: "pending" | "running" | "completed" | "failed" | "stopped";
  verdict: "" | "reproduced" | "fixed" | "inconclusive";
  notes: string;
  summary: string;
  evidence: string;
  error: string;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
}

export interface Conversation {
  id: number;
  running?: boolean; // live server state, returned with the conversation list
  agent_key: string;
  title: string;
  llm_profile_id?: number;
  pinned?: boolean;
  pinned_at?: string | null;
  created_at: string;
  updated_at: string;
}

// ---- Backend logs (/logs page) ----
export interface LogLine {
  seq: number;
  db_id?: number; // server_logs.id; present for DB-persisted lines
  ts: string;
  level: "info" | "warn" | "error";
  tag: string;
  text: string;
}

export type SessionRole = "mainagent" | "planner" | "worker" | "system";
export type SessionStatus = "running" | "paused" | "done" | "blocked" | "exhausted" | "pending" | "stopped" | "deleted";

// Daily token aggregate bucket (GET /api/tokens/daily).
export interface DailyTokenBucket {
  date: string; // "YYYY-MM-DD"
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

// Per-worker token usage (GET /api/exploration/tokens).
export interface TokenUsage {
  worker: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

export interface SessionTokenUsage {
  session: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

export interface BatchControlItem {
  id: string;
  ok: boolean;
  status?: string;
  queued?: boolean;
  error?: string;
}

// 작업별 일괄변경 분류 결과。작업이 삭제된 경우에만 실패가 발생할 수 있습니다.，카테고리 작성 자체가 원자적입니다.。
export interface BatchCategoryItem {
  id: string;
  ok: boolean;
  error?: string;
}

// Whole-task (all agents) token aggregate.
export interface TokenTotal {
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

// Global per-profile token spend from the llm_usage ledger (GET /api/tokens/usage).
export interface ProfileUsage {
  profile_name: string;
  calls: number;
  tasks: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

// One (profile, UTC day) token bucket for the dashboard's daily chart (new source).
export interface ProfileDayUsage {
  profile_name: string;
  date: string; // YYYY-MM-DD
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
}

// Response of GET /api/tokens/usage — the dashboard's "new" (llm_usage) token view.
export interface UsageStats {
  by_profile: ProfileUsage[];
  daily: ProfileDayUsage[];
}

// Per-model token usage for one task (GET /api/llm/records/by-model), from the
// always-on llm_usage metering ledger. calls = number of LLM calls on this model.
export interface ModelTokenStat {
  model: string;
  calls: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

export interface Session {
  id: string;
  role: SessionRole;
  title: string;
  status: SessionStatus;
  live: boolean;
  last_activity: string;
  intent_id?: string;
  source_task_id?: string;
  inherited?: boolean;
  seg?: number; // main-agent session: which conversation segment (0 = original)
}

// ---- Security ----
export interface AuditEntry {
  ts: string;
  tool: string;
  action: "allow" | "block";
  reason?: string;
  command?: string;
}

export interface Audit {
  entries?: AuditEntry[];
  attributions?: Record<string, number>;
}

// ---- Traffic ----
export interface TrafficExchange {
  id: string;
  ts: string;
  host: string;
  method: string;
  url: string;
  status: number;
  content_type: string;
  resp_len: number;
}

export interface TrafficResp {
  enabled: boolean;
  proxy?: string;
  count?: number; // global total (unfiltered)
  total?: number; // rows matching the current filter (for pagination)
  page?: number;
  size?: number;
  exchanges?: TrafficExchange[];
}

// Full raw request/response of one exchange (lazy-loaded on row select).
export interface TrafficDetail {
  req: string;
  resp: string;
}

// One distinct recorded host with its exchange count (target picker).
export interface TrafficHost {
  host: string;
  count: number;
}

// ---- App settings (runtime toggles) ----
export interface Settings {
  traffic_capture: boolean;
  agent_traffic_binding: boolean; // Agent 트래픽 증거 자동 바인딩，기본적으로 폐쇄됨；수동 바인딩에는 영향을 주지 않습니다.
  llm_record: boolean; // LLM 녹음 스위치（기본값은 꺼짐）；닫으면 아무것도 기록하지 않습니다. LLM 전화주세요
  // Web search. brave_key_set / tavily_key_set reflect whether a key is stored
  // (the values are never returned). On PUT, send the corresponding field to set/clear.
  web_search_enabled: boolean;
  web_search_backend: string; // "ddgs" | "brave-free" | "tavily" | "deepseek"
  brave_key_set: boolean;
  tavily_key_set: boolean;
  // write-only: only sent on PUT to store/clear the key.
  brave_search_api_key?: string;
  tavily_search_api_key?: string;
  // 독립 수출 대리점(http/https/socks5)，은 검색 엔드포인트에 액세스하는 데 사용됩니다.；트래픽이 기록되어 있음 MITM 상담원은 관련이 없습니다.。비어 있음=직접 연결。
  web_search_proxy?: string;
  // 글로벌 수출 대행(http/https/socks5，가능 user:pass)，모든 타겟 트래픽이 통과합니다.。트래픽 캡처가 활성화된 경우
  // MITM 업스트림；캡처가 꺼진 상태에서 직접 주입합니다. agent 님 bash/WebFetch。비어 있음=직접 연결。
  global_proxy?: string;
  python_interpreter?: string; // 사용자 정의 스크립트 도구 python 통역사 경로(비어 있음=런타임 감지)
  workers?: number; // 동시작업 agent 번호(기본값3)；나중에 시작한 작업에 유효
  // 작업 동시성 상한:동시에「달리고 있다」。닫기=제한 없음;새 작업을 연 후 제한을 초과하면 대기열에 추가됩니다.,공간이 확보되면 자동으로 시작됩니다.。
  task_concurrency_enabled?: boolean; // 기본값 false
  task_concurrency_limit?: number; // 개봉 후 기본값 5
  // LLM 폴링(장애 조치)。기본값은 꺼짐；개봉 후「지정된 모델이 없습니다.」님 agent 은 현재 구성에서 사용할 수 없습니다.
  // （잔고가 부족해요/key 유효하지 않음/전류 제한/서비스 예외）자동으로 다음 구성으로 전환됩니다.。
  llm_pool_enabled?: boolean; // 기본값 false
  // 은 지정된 구성에 바인딩됩니다. agent/작업이 실패하면 폴링 체인으로 다시 돌아가나요?。기본값 false = 바인딩은 독점을 의미합니다.。
  llm_pool_bind_fallback?: boolean;
  // 연산제약 주입범위(둘 다 기본적으로 활성화되어 있습니다.):임무를 받아라 allow/deny 제약조건 철자 해당 agent 에 대한 시스템 프롬프트。
  constraints_inject_planner?: boolean;
  constraints_inject_worker?: boolean;
  // 실험적 기능:noa 모델 기반 컨텍스트 압축(기본값은 꺼짐)。오픈 후 플랫폼 접속 4가지 유형 agent(planner/
  // worker/스승님 agent/대화) noa 컨텍스트 압축 인수,은 내장된 compaction;매 run 한 번 읽어보세요,예
  // 이후에 시작됨 run 유효。
  noa_compaction?: boolean;
  // ---- 취약점 IM 푸시（채널 자체가 독립적인 리소스입니다.，또 만나요 /api/notify/*，여기에는 전역 구성이 세 개만 있습니다.）----
  notify_enabled?: boolean; // 푸시 마스터 스위치，기본적으로 켜져 있음；유지관리 기간 동안 원클릭으로 출혈을 멈추는 데 사용됩니다.
  notify_public_base_url?: string; // 취약점 상세링크 외부 접속 주소；비어 있음=메시지가 링크를 가져오지 않습니다.
  notify_digest_interval_min?: number; // 요약 모드 주기（분），기본값 30
}

// ---- 취약점 IM 푸시 ----

// NotificationFilter 은 채널의 필터 조건입니다.，모든 항목은 선택사항입니다.，기본값은 필터링 없음입니다.。
// 백엔드가 모든 필드를 확인하지는 않습니다.：구성이 비정상일 때 누르세요.「히트」처리 중（나는 밀어붙이는 것보다 더 밀어붙이는 편이 낫다）。
export interface NotificationFilter {
  min_severity?: string; // "" | low | medium | high | critical
  task_ids?: number[]; // 비어 있음=제한 없음；비어 있지 않은 요구 사항은 취약점이 속한 작업과 겹쳐야 합니다.
  asset_ids?: number[]; // 비어 있음=제한 없음；비어 있지 않은 조건에는 취약점 고정 자산과의 교차가 필요합니다.
  vulnclass_include?: string[]; // 비어 있음=다 받아보세요；비어 있지 않은 경우 취약점 유형은 모든 키워드와 일치해야 합니다.（대소문자를 구분하지 않는 하위 문자열）
  vulnclass_exclude?: string[]; // 키워드가 일치하면 제외（제외가 포함보다 우선합니다.）
  on_status_change?: boolean; // 취약점 처리 상태 변경 이벤트도 수신할지 여부
}

// NotificationChannel 은 채널 인스턴스입니다.。config 의 필드는 다음과 같습니다. kind 다양함，
// 및 자격 증명 필드는 다음으로 대체됩니다. "__masked__" 으로 시작하는 마스크 값——그대로 돌려준다는 뜻「변화 없음」。
export interface NotificationChannel {
  id: number;
  name: string;
  kind: string;
  enabled: boolean;
  mode: "realtime" | "digest";
  config: Record<string, unknown>;
  filter: NotificationFilter;
  rate_per_min: number;
  created_at: string;
  updated_at: string;
  // secret_keys 은 백엔드에서 채널 유형별로 제공됩니다.，프런트 엔드는 비밀번호 상자를 렌더링하고「공백으로 두고 변경하지 마십시오.」팁，
  // 채널 지식을 하드코딩하지 마세요.。
  secret_keys: string[];
}

// NotificationKind 네 /api/notify/meta 반환된 채널 유형 메타데이터。
export interface NotificationKind {
  kind: string;
  default_rate_per_min: number;
  secret_keys: string[];
}

export interface NotificationMeta {
  kinds: NotificationKind[];
  enabled: boolean;
  public_base_url: string;
  digest_interval_min: string;
  defaults: { digest_interval_min: number };
  stats: {
    channels: number;
    channels_on: number;
    pending: number;
    failed: number;
    sent_today: number;
    backlog_age_ms: number;
  };
}

// NotificationDelivery 은 배송기록입니다，배송내역 및 재전송 실패에 사용됩니다.。
export interface NotificationDelivery {
  id: number;
  finding_id: string;
  event_kind: string; // finding_created | finding_status_changed
  channel_id: number;
  channel_name: string;
  channel_kind: string;
  state: "pending" | "sending" | "sent" | "failed" | "skipped";
  attempts: number;
  last_error: string;
  batch_id?: number;
  created_at: string;
  sent_at?: string;
  next_attempt_at: string;
  title: string;
  severity: string;
}

// ---- LLM config ----
export interface LLMProfile {
  id: string;
  name: string;
  format: "openai" | "anthropic" | "openai-responses";
  base_url?: string;
  proxy?: string;
  model: string;
  api_key_hint?: string;
  rate_per_second: number;
  rate_per_minute: number;
  context_window_k?: number;
  // 생각의 스위치(thinking.type): ""=보내지 않음(기본값) | "disabled"=닫기 | "enabled"=켜세요
  thinking_type?: string;
  // 사고강도: ""=보내지 않음(기본값) | "low"/"medium"/"high"/"xhigh"/"max"
  reasoning_effort?: string;
  is_default: boolean;
  // 폴링 순서：값이 클수록 먼저 선택됩니다.。활성화 구성은 항상 체인의 헤드입니다.，은 이 값과 관련이 없습니다.。
  priority?: number;
  // true = 장애 조치 대상이 아닙니다.（은 아직 가능해요 agent/작업의 명시적 바인딩 사용）。
  pool_exclude?: boolean;
  // true（기본값）= 스트리밍(SSE) | false = 맞아요·비스트리밍(stream:false，일회성 반품)。
  streaming?: boolean;
  // 단일 답변의 출력 제한(token)。0 = 이 필드를 보내지 마십시오.，서버의 기본값에 따라 결정됩니다.。
  // 참고 및 context_window_k 구별하세요：후자는 모델의 전체 용량입니다.，압축 임계값에 로컬로만 사용됩니다.。
  max_tokens?: number;
  // 상한값에는 어떤 요청 필드 이름이 사용됩니까?，만 format="openai" 의미있는：
  // ""=max_tokens(기본값) | "max_completion_tokens"(OpenAI 추론모델은 그것만 인식한다.)
  max_tokens_field?: string;
  // 사용자 정의된 세션 헤더 이름：각 요청은 비어 있지 않을 때 이를 가져옵니다. HTTP 머리，헤드 값=현재 세션/의도적 session id。
  // ""=보내지 않음。은 다음을 누르는 데 사용됩니다. session-id 헤더 프롬프트 캐시/고정 라우팅을 위한 게이트웨이。
  session_header_key?: string;
  // 이 구성에는 재시도가 포함됩니다.（지안롄/빈 응답/마찬가지예요 provider 보안창）。비워두세요/모두 0 = 글로벌 전략을 따르세요。
  retry?: LLMRetryOverride;
}

// ---- LLM 재시도 전략 ----
// 한 수준에서 재시도를 위한 손잡이 2개。둘 다「0 = 구성되지 않음」：
//   attempts    0=기본 횟수 사용 | -1=이 레이어를 닫고 다시 시도해 보세요. | >0=재시도 횟수
//   interval_ms 0=기본 지수 백오프 사용 | >0=대신 이 고정된 밀리초 간격을 사용하세요.
export interface LLMRetryRule {
  attempts: number;
  interval_ms: number;
}

// 싱글 LLM 커버 가능한 레이어 3개 구성（모두「끝점을 따라가세요」다시 시도）。
export interface LLMRetryOverride {
  connect: LLMRetryRule; // 연결 다시 시도：연결 재설정/시간 초과/429/5xx，흐름이 시작되기 전에
  empty: LLMRetryRule; // 빈 응답 재시도：완료되었으나 내용이 없습니다.（만 openai 형식）
  stream: LLMRetryRule; // 마찬가지예요 provider 안전창 재시도：출력이 전달되기 전 중단 재생
}

// 글로벌 전략 = 위 3개 레이어의 기본값 + 두 레이어에는 전역 레이어만 있습니다.：
//   breaker 폴링 회로 차단기（attempts=여러 차례 연속 순시 퓨즈 고장，interval_ms=고정 냉각 시간）
//   intent  다시 뛰고 싶은 마음（worker 에게 model_error 끝나고 다시 달려보자는 마음으로）
export interface LLMRetryPolicy extends LLMRetryOverride {
  breaker: LLMRetryRule;
  intent: LLMRetryRule;
}

// ---- LLM 폴링（장애 조치）----
// 폴링 체인에 구성된 위치 및 상태。state:
//   ok       정상
//   degraded 연속 오류가 발생했지만 회로 차단기 임계값에 도달하지 않았습니다.
//   tripped  이 터졌어요，대기시간 동안 건너뛰었습니다.（cooldown_secs 은 남은 초입니다.）
export interface LLMPoolMember {
  profile_id: string;
  name: string;
  model: string;
  format: string;
  priority: number;
  active: boolean; // 현재 활성화된 구성인가요?（항상 체인의 리더）
  excluded: boolean; // pool_exclude：폴링에 참여하지 않습니다.
  state: "ok" | "degraded" | "tripped";
  fails: number;
  trips: number;
  cooldown_secs: number;
  last_error?: string;
  last_at?: string;
}

export interface LLMPoolStatus {
  enabled: boolean;
  bind_fallback: boolean;
  chain: LLMPoolMember[];
}

// ---- Agents ----
export interface Agent {
  id: string;
  key: string; // 기본 제공 goals/planner/mainagent/worker；사용자 정의로 사용자 정의 key
  name: string;
  description?: string;
  role: string;
  builtin: boolean;
  enabled: boolean;
  llm_profile_id?: number | null; // 바운드 LLM 구성；null/absent = 미션을 따르세요/대화/글로벌
  max_turns?: number; // 0 = 무제한
  run_seconds?: number; // worker 작동하는 벽시계 한 개의 상한선(초)；0 = 무제한
  web_search?: boolean; // 네트워크 검색 활성화 여부(시스템 전역 스위치에 의해 게이트됨)
  interactive_shell?: boolean; // 대화형 활성화 여부 shell(지속됨 PTY 대화 도구 제품군)
  // P3 트리거 후 처리 전략(사용자 정의 전용 agent 의미있는)
  trigger_run_mode?: "serial" | "parallel"; // 직렬 대기열 / 각 트리거는 동시 세션을 트리거합니다.
  trigger_merge_mode?: "by_task" | "all" | "none"; // 만 serial：동일한 작업으로 병합 / 모두 병합 / 병합되지 않음
  trigger_max_parallel?: number; // 만 parallel：매 agent 동시성 제한；0=제한 없음
  // 제본 수량(목록 인터페이스만 반환됩니다.)：표시됨 MCP / 표시됨 Skill / 바인딩 도구
  mcp_count?: number;
  skill_count?: number;
  tool_count?: number;
}

export interface PromptVar {
  name: string;
  description: string;
  example: string;
  source: "exploration" | "runtime" | "distilled";
}

export interface PromptVersion {
  version: number;
  ts: string;
  note: string;
  template_text: string;
}

export interface AgentDetail {
  agent: Agent;
  prompt: string;
  variables: PromptVar[];
  versions: PromptVersion[];
  visibility: { mcp: number[]; skill: string[] };
  // 바인딩 가능 LLM 구성 후보(「기본 모델」드롭다운)；현재 바인딩 보기 agent.llm_profile_id
  llm_profiles?: { id: number; name: string; model: string; is_default: boolean }[];
  wrapup_prompt?: string; // 종료 프롬프트 단어를 저장했습니다.(비어 있음=내장된 기본값 사용)
  wrapup_default?: string; // 기본 닫기 프롬프트 단어 내장(자리 표시자/기본값 복원)
  wrapup_max_turns?: number; // 저장된 마감 라운드 수(0=내장된 기본값 사용)
  wrapup_max_turns_default?: number; // 내장된 기본 마감 라운드 수( "0=기본값N" 팁)
  // 작업 수준 시간 초과 종료 단어(만 worker/planner，task_timeout_wrapup_supported=true 인 경우에만 표시됩니다.)
  task_timeout_wrapup_supported?: boolean;
  task_timeout_wrapup_prompt?: string;
  task_timeout_wrapup_default?: string;
  task_timeout_wrapup_max_turns?: number;
  task_timeout_wrapup_max_turns_default?: number;
}

// ---- MCP ----
export interface MCPServer {
  id: number;
  name: string;
  transport: "stdio" | "http" | "sse";
  command?: string;
  args: string[];
  env: Record<string, string>;
  url?: string;
  enabled: boolean;
  insecure?: boolean; // http: skip TLS cert verification (self-signed servers)
  tools?: string[]; // mcp_tools_cache (names only, for the count)
}

export interface MCPTool {
  name: string;
  description: string;
}

// ---- Skills ----
// Fields align with the agentskills.io open specification.
// description covers both "what the skill does" and "when to use it".
export interface SkillItem {
  name: string; // unique key = directory name
  description?: string; // required per spec; covers what + when to use
  license?: string; // optional: SPDX identifier or free text
  compatibility?: string; // optional: environment requirements
  mcps?: string[]; // MCP server names this skill unlocks on load
  files: string[]; // files in the skill directory
  // 통화 통계（skill_usage 원장）。전화한 적 없음 skill：calls=0、last_used 기본값。
  calls: number;
  tasks: number; // 로드한 작업 수（chat 세션수는 계산되지 않습니다.）
  usage_agents: string[]; // 로드했어요 agent key
  last_used?: string;
}

// SkillCall 한번이에요 Skill() 전화주세요（싱글 skill 님의 최근 통화 목록）。
export interface SkillCall {
  ts: string;
  agent_key: string;
  task_id: number; // 0 = 비작업 시나리오（대화 세션）
  session_id: string;
  args_len: number;
}

// MissingSkill 이름이 있지만 존재하지 않습니다. skill —— "사용하고 싶은데 없어요"노치。
export interface MissingSkill {
  skill: string;
  calls: number;
  agents: string[];
  last_used?: string;
}

// ---- Tools (내장 도구 디렉토리) ----
// key + handler live in Go; only these fields are page-editable. system tools lock
// the key and the parameter *structure* (name/type/required) — the per-param
// description/default and the agent binding are what move.
export interface Tool {
  key: string;
  system: boolean;
  description: string;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  schema: Record<string, any>; // full JSON-Schema (object with properties)
  agents: string[]; // bound agent keys
  enabled: boolean;
  kind?: "builtin" | "shell" | "command" | "script" | "http"; // 사용자 정의 도구 유형
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  exec?: Record<string, any>; // 사용자 정의 도구 실행 사양(kind!=builtin)
  deferred?: boolean; // schema 지연(SearchExtraTools/ExecuteExtraTool)
  calls?: number; // persistent runtime invocation count (older APIs may omit it)
}

// ---- Stats ----
export interface Stats {
  assets: number;
  engine_mode: EngineMode;
  llm_configured: boolean;
  roe_enabled: boolean;
  findings_confirmed: number;
  active_task?: Partial<Task>;
}

// ---- Intercept Rules ----
export type InterceptAction = "allow" | "deny" | "ask";
export type InterceptMatchTarget = "tool_name" | "tool_input";
export type InterceptMatchType = "string" | "regex";

export interface InterceptRule {
  id: number;
  name: string;
  enabled: boolean;
  priority: number;
  match_target: InterceptMatchTarget;
  match_type: InterceptMatchType;
  pattern: string;
  action: InterceptAction;
  message: string;
  timeout_enabled: boolean;
  timeout_seconds: number;
  timeout_action: "deny" | "allow";
  created_at: string;
  updated_at: string;
}

// ---- Asset Intercept Rules（자산 가로채기：글로벌 블랙리스트） ----
export type AssetInterceptKind =
  | "exact_domain"
  | "exact_ip"
  | "exact_url"
  | "fuzzy_domain"
  | "fuzzy_ip"
  | "fuzzy_url"
  | "cidr";

// action 작업 수준 규칙에만 해당：block=차단(테스트 금지) allow=허용됨(화이트리스트)。
export type AssetInterceptAction = "block" | "allow";

// 태스크 수준 자산 차단/허용규칙 항목 항목（작업 생성、업무 내용 편집 및 활용）。
export interface AssetInterceptRuleInput {
  action: AssetInterceptAction;
  kind: AssetInterceptKind;
  pattern: string;
  note: string;
  enabled: boolean;
}

export interface AssetInterceptRule {
  id: number;
  enabled: boolean;
  action?: AssetInterceptAction; // 전역 규칙에는 이 필드가 없습니다.（Hengwei가 차단합니다.）；작업 수준 규칙 구분 block/allow
  kind: AssetInterceptKind;
  pattern: string;
  note: string;
  builtin: boolean;
  created_at: string;
  updated_at: string;
}

export interface InterceptPending {
  decision_source?: "rule" | "model" | "unknown" | "";
  id: number;
  rule_id?: number;
  conversation_id?: number;
  task_id?: string;
  agent_name: string;
  tool_name: string;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  tool_input: Record<string, any>;
  status: "pending" | "allowed" | "denied" | "timeout";
  reason: string; // 규칙 message 혹은 모델 판단 이유(모델결정영역 [모델] 접두사)
  decided_at?: string;
  created_at: string;
}

// JudgeConfig: 전체 모델 승인(차단 규칙이 적용되지 않은 경우에만 모델에 의해 판단됩니다.)의 전역 구성。
export interface JudgeConfig {
  enabled: boolean;
  profile_id: number; // 0 = 활성화를 따르세요/기본 구성
  prompt: string; // 판정 프롬프트 단어;GET 설정하지 않으면 백엔드가 기본 제공 템플릿의 전체 텍스트를 백필합니다.
  timeout_seconds: number; // 모델 호출 시간 초과
  fail_action: "allow" | "ask" | "deny"; // 모델 오류/시간 초과/해결할 수 없는 경우 대체
  ask_timeout_seconds: number; // 모델 판단 ask 수동으로 전환 후 승인 대기 시간 초과
  ask_timeout_action: "allow" | "deny"; // 승인 시간 초과 후 기본 작업
}

// JudgeUsage: 전체 모델 승인(judge 채널)의 축적 token 복용량 + 근처 N 요일별 순서。
export interface JudgeDayUsage {
  date: string; // YYYY-MM-DD (UTC)
  calls: number;
  input_tokens: number;
  output_tokens: number;
}
export interface JudgeUsage {
  calls: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  daily: JudgeDayUsage[];
}

export interface InterceptApprovalFilter {
  status?: InterceptPending["status"];
  decision_source?: "rule" | "model" | "unknown";
}

// InterceptApprovalRow enriches InterceptPending with conversation/task and rule context.
export interface InterceptApprovalRow extends InterceptPending {
  conv_title: string; // "" if no linked conversation
  conv_agent_key: string; // "" if no linked conversation
  rule_name: string; // "" if rule was deleted
}

// ── 자산 동기화 (ScopeSentry 데이터 소스) ──────────────────────────────────────────────
export interface SSProject {
  id: string; // MongoDB ObjectID — used as filter.project
  name: string;
  logo?: string;
  AssetCount?: number;
  tag?: string;
}

export interface SSTask {
  id: string;
  name: string; // used as filter.task
  status?: number;
  progress?: number;
  creatTime?: string;
  endTime?: string;
}

// ConvTokenSummary — one conversation's token total (+ profile/date) for merging
// chat usage into the dashboard token stats. GET /api/tokens/conversations.
export interface ConvTokenSummary {
  llm_profile_id: number | null;
  created_at: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

// ---- Command recording (Bash execution history) ----
export interface CommandRecord {
  id: number;
  exploration_id: number;
  worker: string;
  tool: string;
  command: string; // raw tool input (JSON)
  output: string;
  is_error: boolean;
  created_at: string;
}

// 단일 도구의 호출 통계（/commands/stats）；errors 은 실패 횟수입니다.。
export interface ToolStat {
  tool: string;
  total: number;
  errors: number;
}

// ---- LLM recording ----
export interface LLMRecordItem {
  id: number;
  ts: string;
  model: string;
  profile_name: string;
  session_id: string;
  task_id: string;
  worker: string;
  latency_ms: number;
  input_tokens: number;
  output_tokens: number;
  cache_read: number;
  cache_write: number;
  status: string;
  error?: string;
}

export interface LLMRecordDetail extends LLMRecordItem {
  request_body: string;
  response_body: string;
  // provider 실제로 보내고 받았습니다 HTTP 원문：요청사항은 buildBody() 전송 완료 body（도구 포함
  // schema），답변 원본입니다 SSE 프레임。위 request_body/response_body 은 정규화된 뷰입니다.，
  // 도구를 폐기했습니다. schema 그리고 tool_use 차단。이전 기록이 비어 있습니다.。
  raw_request?: string;
  raw_response?: string;
}

// One distinct task with its LLM-record count (task picker on the records page).
export interface LLMTask {
  task_id: string;
  count: number;
}

// The exact JSON sent to the review model, retained for all model verdicts.
export interface InterceptReviewInput {
  version: number;
  background?: {
    // worker_summary is retained only for immutable v2/v3 snapshots.
    source: "user_message" | "worker_summary";
    text: string;
    truncated?: boolean;
  };
  // Version 1 snapshots are immutable and remain readable in historical audits.
  task?: {
    task_id: number;
    description: string;
    goal: string;
    constraints: { id: number; kind: string; text: string; origin: string; created_at: number }[];
    truncated?: boolean;
  };
  working_directory?: string;
  worker_intent?: string;
  turn_input?: string;
  background_truncated?: boolean;
  // Legacy v1/v2 snapshots only; v3 never sends execution history.
  history?: {
    tool_use_id: string;
    tool: string;
    arguments_preview: string;
    result: string;
    status: "succeeded" | "failed";
    truncated?: boolean;
  }[];
  history_truncated?: boolean;
  correlation?: "exact" | "ambiguous" | "unavailable";
  tool_name: string;
  arguments: Record<string, unknown>;
}

// Immutable review snapshot plus separately recorded execution outcome.
export interface InterceptAudit {
  model_input?: InterceptReviewInput;
  model_input_digest?: string;
  run_id?: string;
  tool_use_id?: string;
  correlation: "exact" | "ambiguous" | "unavailable";
  input_digest: string;
  user_message: string;
  user_truncated?: boolean;
  context:
    | { kind: string; tool?: string; tool_use_id?: string; text: string; is_error?: boolean; truncated?: boolean }[]
    | null;
  context_truncated?: boolean;
  captured_at: string;
  model_fallback?: boolean;
  initial_action: "allow" | "ask" | "deny";
  initial_reason: string;
  effective_action?: "allow" | "deny";
  decision_reason?: string;
  rule_name?: string;
  config_digest?: string;
  profile_id?: number;
  execution_status: "not_started" | "not_executed" | "awaiting_result" | "succeeded" | "failed" | "unknown";
  output?: string;
  output_truncated?: boolean;
  execution_ended_at?: string;
}
export interface InterceptDetail extends InterceptApprovalRow {
  audit: InterceptAudit | null;
}

export type TrafficEvidenceRole = "baseline" | "proof" | "verification" | "supporting";
export interface TrafficEvidenceRef {
  traffic_id: string;
  role?: TrafficEvidenceRole;
  note?: string;
}
export interface TrafficEvidenceSnapshot {
  id: string;
  source_traffic_id: string;
  captured_at: number;
  url: string;
  method: string;
  status: number;
  content_type: string;
  req_head?: string;
  resp_head?: string;
  req_hash: string;
  resp_hash: string;
  req_len: number;
  resp_len: number;
}
export interface FindingTrafficBinding {
  id: string;
  finding_id: string;
  snapshot_id: string;
  role: TrafficEvidenceRole;
  note: string;
  position: number;
  created_at: string;
  snapshot: TrafficEvidenceSnapshot;
}
export interface FindingTraffic {
  finding_id: string;
  version: number;
  report_version: number;
  bindings: FindingTrafficBinding[];
}
export interface EvidenceBodyPreview {
  content: string;
  offset: number;
  total: number;
  next_offset: number;
  truncated: boolean;
  binary: boolean;
}
export interface FindingTrafficDetail {
  binding: FindingTrafficBinding;
  request: EvidenceBodyPreview;
  response: EvidenceBodyPreview;
}

/** GET /api/update/check —— 현재 버전은 다음과 같습니다. GitHub 최신 공식 버전 비교 결과。 */
export interface UpdateCheck {
  /** 현재 실행중인 버전；다음과 같이 개발 및 제작되었습니다. "dev" 또는 git describe 의 접미사 형식。 */
  current: string;
  /** 실행 상태。docker 교체는 컨테이너의 쓰기 가능한 레이어에서만 작동합니다.，컨테이너를 다시 빌드하면 이미지 버전이 반환됩니다.。 */
  mode: "docker" | "binary";
  os: string;
  arch: string;
  repo: string;
  /** 롤백 가능한 이전 버전이 있나요?（artex.old）。 */
  has_backup: boolean;
  /** 이 시작 중 자동 업데이트 부트스트래핑 결론（옷을 갈아입지 못했습니다. / 롤백 등），아무 일도 일어나지 않으면 비어 있음。 */
  boot_notice?: string;
  rolled_back?: boolean;
  /** 질의 GitHub 실패가 발생한 경우 이유를 설명해주세요.，현재 다음 필드는 모두 사용할 수 없습니다.。 */
  error?: string;
  latest?: string;
  notes?: string;
  html_url?: string;
  published_at?: string;
  /** 현재 플랫폼에 해당하는 릴리스 패키지 이름，그리고 Release 정말 가져오셨나요?。 */
  asset?: string;
  asset_available?: boolean;
  size?: number;
  has_update?: boolean;
  /** 두 당사자의 버전 번호가 비슷합니까?；다음과 같이 개발 및 제작되었습니다. false，현재 원클릭 업데이트가 비활성화되어 있습니다.。 */
  comparable?: boolean;
  /** comparable 입니다 false 일 때의 설명。 */
  reason?: string;
}

/** /api/update/stream 님이 푸시한 업데이트 진행 상황。 */
export interface UpdateProgress {
  phase: "idle" | "downloading" | "verifying" | "extracting" | "staged" | "failed";
  /** 다운로드 단계만 의미가 있음（0-100）；남은 단계는 -1。 */
  percent: number;
  message: string;
  version?: string;
  error?: string;
}

// Original execution selected from an approval, never submitted to the reviewer.
export interface InterceptExecution {
  conversation_id: number | null;
  task_id: string | null;
  session: string;
  seq: number;
  items: Activity[];
}
