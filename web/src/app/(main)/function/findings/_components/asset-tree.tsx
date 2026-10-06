"use client";

import * as React from "react";

import {
  BuildingIcon,
  ChevronRightIcon,
  CircleDashedIcon,
  GlobeIcon,
  LayoutTemplateIcon,
  LinkIcon,
  type LucideIcon,
  NetworkIcon,
  RefreshCwIcon,
  SearchIcon,
  SmartphoneIcon,
} from "lucide-react";

import { Button } from "@/components/ui/button";
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group";
import type { FindingAssetKind, FindingAssetNode } from "@/lib/types";
import { cn } from "@/lib/utils";

// 아이콘은 자산 페이지의 유형 매핑을 상속합니다.,동일한 자산이 두 곳에서 동일하게 보입니다.。
const KIND_ICON: Record<FindingAssetKind, LucideIcon> = {
  company: BuildingIcon,
  root_domain: GlobeIcon,
  subdomain: GlobeIcon,
  ip: NetworkIcon,
  app: SmartphoneIcon,
  service: LayoutTemplateIcon,
  endpoint: LinkIcon,
  none: CircleDashedIcon,
};

const KIND_LABEL: Record<FindingAssetKind, string> = {
  company: "기업",
  root_domain: "루트 도메인 이름",
  subdomain: "하위 도메인 이름",
  ip: "IP",
  app: "신청",
  service: "서비스",
  endpoint: "인터페이스",
  none: "관련되지 않음",
};

// TreeNode 은 노드 배열로 구성된 트리입니다.。백엔드가 눌림「같은 아버지 밑에서 앞에 사람이 더 많다는 걸 알게 됐어요」정렬됨,
// 여기서는 배열 순서대로만 마운트하면 됩니다.。
interface TreeNode extends FindingAssetNode {
  children: TreeNode[];
  depth: number;
  /** 실제로 트리에 텍스트가 렌더링되었습니다.;완료 label 아직도 남아있습니다 label 내부(호버 팁 및 이동 경로에 사용됩니다.)。 */
  display: string;
}

function stripBrackets(host: string) {
  return host.startsWith("[") && host.endsWith("]") ? host.slice(1, -1) : host;
}

function parseAssetURL(raw: string): URL | null {
  try {
    return new URL(raw);
  } catch {
    return null;
  }
}

// hostOf 노드가 나타내는 호스트를 가져옵니다.:URL 받아 hostname,「host:port」받아 host,나머지는
// 태그 자체(루트 도메인 이름 / 하위 도메인 이름 / IP)。
function hostOf(label: string): string {
  const url = parseAssetURL(label);
  if (url) return stripBrackets(url.hostname);
  const hostPort = label.match(/^(.+):(\d+)$/);
  return stripBrackets(hostPort ? hostPort[1] : label);
}

// shortLabel 상위 노드와 반복되는 접두사를 제거합니다.。service / endpoint 님 label 완료되었습니다 URL,그리고
// 호스트 도메인 이름/IP 이전 줄은 이미 작성되었습니다. —— 깊은 노드는 본질적으로 좁습니다.,다시 host 반복,정말요
// 정보볼륨의 포트와 경로가 모두 차단되었습니다.。완전한 값이 여전히 존재합니다. title 빵가루와 함께。
function shortLabel(node: FindingAssetNode, parent?: FindingAssetNode): string {
  if (!parent) return node.label;

  // 하위 도메인 이름이 루트 도메인 이름 아래에 걸려 있습니다.:루트 도메인 이름 접미사 제거,자기 몫만 지키세요。
  if (node.kind === "subdomain" && node.label.endsWith(`.${parent.label}`)) {
    return node.label.slice(0, -(parent.label.length + 1)) || node.label;
  }
  if (node.kind !== "service" && node.kind !== "endpoint") return node.label;

  // 상위 태그는 자체 접두사입니다.(인터페이스가 동일하게 걸려 있습니다. URL 님의 서비스 하에、서비스가 계속 멈춥니다 IP 다음):바로 잘라버리세요。
  if (node.label.startsWith(parent.label)) {
    return node.label.slice(parent.label.length) || node.label;
  }

  // 그렇지 않으면 상위 노드만 실제로는 이것입니다. URL 은 호스트의 약어입니다.,그렇지 않으면 식별 정보가 손실됩니다.
  // (예를 들어 하위 도메인 이름 자산 행이 부족하여 서비스가 루트 도메인 이름에 직접 연결됩니다.,그러면 완전히 표시되어야 합니다. URL)。
  if (hostOf(node.label) !== hostOf(parent.label)) return node.label;

  const url = parseAssetURL(node.label);
  if (!url) return node.label;
  if (node.kind === "endpoint") return `${url.pathname}${url.search}` || "/";
  const scheme = url.protocol.replace(":", "");
  const port = url.port || (url.protocol === "https:" ? "443" : "80");
  return `${scheme} :${port}`;
}

export function buildAssetTree(nodes: FindingAssetNode[]): TreeNode[] {
  const byKey = new Map<string, TreeNode>();
  for (const node of nodes) {
    byKey.set(node.key, { ...node, children: [], depth: 0, display: node.label });
  }
  const roots: TreeNode[] = [];
  for (const node of nodes) {
    const current = byKey.get(node.key);
    if (!current) continue;
    const parent = node.parent ? byKey.get(node.parent) : undefined;
    // 상위 노드가 누락되었습니다.(잘려서 삭제됨)최상위로 올라갔을 때,전체 하위 트리가 사라지는 것을 방지。
    if (parent) {
      parent.children.push(current);
      current.display = shortLabel(node, parent);
    } else {
      roots.push(current);
    }
  }
  const setDepth = (node: TreeNode, depth: number) => {
    node.depth = depth;
    for (const child of node.children) setDepth(child, depth + 1);
  };
  for (const root of roots) setDepth(root, 0);
  return roots;
}

// assetPathOf 최상위 수준에서 이 노드까지의 경로를 반환합니다.,。각 수준에는 관련 항목만 표시됩니다.
// 이전 레벨의 증가분(display),전체 값은 다음에 남아 있습니다. label 내부。
export function assetPathOf(nodes: FindingAssetNode[], key: string | null): (FindingAssetNode & { display: string })[] {
  if (!key) return [];
  const byKey = new Map(nodes.map((n) => [n.key, n]));
  const path: FindingAssetNode[] = [];
  const seen = new Set<string>();
  let current = byKey.get(key);
  while (current && !seen.has(current.key)) {
    seen.add(current.key);
    path.unshift(current);
    current = current.parent ? byKey.get(current.parent) : undefined;
  }
  return path.map((node, index) => ({ ...node, display: shortLabel(node, path[index - 1]) }));
}

// filterTree 키워드로 필터링:히트 노드가 유지됩니다.,및 전체 상위 체인 유지(조상 자신도 그리워할 수 있다)。
// 적중 노드의 자손이 함께 유지됩니다.,계속 드릴링이 편리함。
function filterTree(nodes: TreeNode[], keyword: string): TreeNode[] {
  const kw = keyword.trim().toLowerCase();
  if (!kw) return nodes;
  const walk = (node: TreeNode): TreeNode | null => {
    const hit = node.label.toLowerCase().includes(kw);
    if (hit) return node;
    const children = node.children.map(walk).filter((c): c is TreeNode => c !== null);
    if (children.length === 0) return null;
    return { ...node, children };
  };
  return nodes.map(walk).filter((n): n is TreeNode => n !== null);
}

// collectKeys 하나씩 모아보세요(서브)나무에 있는 모든 것 key,「모든 일치 항목 펼치기」。
function collectKeys(nodes: TreeNode[], out: Set<string> = new Set()): Set<string> {
  for (const node of nodes) {
    out.add(node.key);
    collectKeys(node.children, out);
  }
  return out;
}

interface AssetTreeProps {
  nodes: FindingAssetNode[];
  selected: string | null;
  onSelect: (key: string | null) => void;
  loading?: boolean;
  truncated?: boolean;
  droppedKinds?: string[];
  /** 자산을 선택하지 않은 경우 오른쪽에 표시되는 총 결과 수,「모든 자산」그 대사。 */
  findingTotal: number;
  /** 자산 보기가 폴링되지 않습니다.,이 버튼을 누르거나 페이지 내에서 추가, 삭제, 수정을 하면 나무 개수가 새로 고쳐집니다.。 */
  onRefresh?: () => void;
}

export function AssetTree({
  nodes,
  selected,
  onSelect,
  loading,
  truncated,
  droppedKinds,
  findingTotal,
  onRefresh,
}: AssetTreeProps) {
  const [keyword, setKeyword] = React.useState("");
  const [expanded, setExpanded] = React.useState<Set<string>>(() => new Set());
  // 사용자가 수동으로 접은 노드를 기억하세요.,그러지 않으려면「기본적으로 최상위 수준 확장」새로고침할 때마다 다시 분산시킵니다.。
  const [collapsed, setCollapsed] = React.useState<Set<string>>(() => new Set());

  const roots = React.useMemo(() => buildAssetTree(nodes), [nodes]);
  const visible = React.useMemo(() => filterTree(roots, keyword), [roots, keyword]);

  // 검색 시 일치하는 모든 분기를 확장합니다.,그렇지 않고 접힌 노드에 히트아이템이 숨겨져 있으면 검색이 없다는 뜻입니다.。
  const searching = keyword.trim() !== "";
  const searchKeys = React.useMemo(() => (searching ? collectKeys(visible) : null), [searching, visible]);

  const isExpanded = React.useCallback(
    (node: TreeNode) => {
      if (searchKeys) return searchKeys.has(node.key);
      if (expanded.has(node.key)) return true;
      // 최상위 레벨은 기본적으로 한 레이어 확장됩니다.:레벨이 아무리 깊어도 사용자가 직접 클릭해야 합니다.,수천줄을 한꺼번에 퍼뜨리지 마세요。
      return node.depth === 0 && !collapsed.has(node.key);
    },
    [collapsed, expanded, searchKeys],
  );

  const toggle = React.useCallback(
    (node: TreeNode) => {
      const open = isExpanded(node);
      setExpanded((prev) => {
        const next = new Set(prev);
        if (open) next.delete(node.key);
        else next.add(node.key);
        return next;
      });
      setCollapsed((prev) => {
        const next = new Set(prev);
        if (open) next.add(node.key);
        else next.delete(node.key);
        return next;
      });
    },
    [isExpanded],
  );

  let emptyHint = "현재 필터에 자산과 관련된 결과가 없습니다.。";
  if (loading) emptyHint = "로딩 중…";
  else if (searching) emptyHint = "일치하는 자산이 없습니다.。";

  const rows: React.ReactNode[] = [];
  const pushRows = (list: TreeNode[]) => {
    for (const node of list) {
      const open = isExpanded(node);
      rows.push(
        <AssetTreeRow
          key={node.key}
          node={node}
          open={open}
          selected={selected === node.key}
          onToggle={() => toggle(node)}
          onSelect={() => onSelect(selected === node.key ? null : node.key)}
        />,
      );
      if (open && node.children.length > 0) pushRows(node.children);
    }
  };
  pushRows(visible);

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-2">
      <div className="flex items-center gap-1">
        <InputGroup className="flex-1">
          <InputGroupInput
            type="search"
            value={keyword}
            onChange={(event) => setKeyword(event.target.value)}
            placeholder="자산 필터링"
            aria-label="자산 필터링"
          />
          <InputGroupAddon>
            <SearchIcon aria-hidden="true" />
          </InputGroupAddon>
        </InputGroup>
        {onRefresh && (
          <Button
            size="icon"
            variant="ghost"
            className="size-8 shrink-0 text-muted-foreground"
            onClick={onRefresh}
            disabled={loading}
            aria-label="자산 트리 새로 고침"
            title="자산 트리 새로 고침"
          >
            <RefreshCwIcon className={cn("size-4", loading && "animate-spin")} />
          </Button>
        )}
      </div>

      <button
        type="button"
        onClick={() => onSelect(null)}
        className={cn(
          "flex items-center justify-between gap-2 rounded-md px-2 py-1.5 text-left text-sm",
          selected === null ? "bg-accent font-medium" : "hover:bg-accent/50",
        )}
      >
        <span>모든 자산</span>
        <span className="text-xs tabular-nums text-muted-foreground">{findingTotal}</span>
      </button>

      <div className="max-h-[24rem] min-h-0 flex-1 overflow-y-auto pr-2 lg:max-h-[calc(100vh-16rem)]">
        <div className="flex flex-col">
          {rows}
          {rows.length === 0 && <p className="px-2 py-8 text-center text-xs text-muted-foreground">{emptyHint}</p>}
        </div>
      </div>

      {truncated && (
        <p className="px-1 text-xs text-muted-foreground">
          자산이 너무 많습니다.，숨김{(droppedKinds ?? []).map((k) => KIND_LABEL[k as FindingAssetKind] ?? k).join(" / ")}
          레벨（카운트는 여전히 상위에 포함되어 있습니다.）。전체 레벨을 보려면 필터를 사용하거나 필터 상자 범위를 좁히세요.。
        </p>
      )}
    </div>
  );
}

function AssetTreeRow({
  node,
  open,
  selected,
  onToggle,
  onSelect,
}: {
  node: TreeNode;
  open: boolean;
  selected: boolean;
  onToggle: () => void;
  onSelect: () => void;
}) {
  const Icon = KIND_ICON[node.kind] ?? GlobeIcon;
  const hasChildren = node.children.length > 0;
  return (
    <div
      className={cn(
        "group flex items-center gap-1 rounded-md pr-1 text-sm",
        selected ? "bg-accent" : "hover:bg-accent/50",
      )}
      style={{ paddingLeft: `${node.depth * 10}px` }}
    >
      {hasChildren ? (
        <button
          type="button"
          onClick={onToggle}
          className="flex size-5 shrink-0 items-center justify-center rounded text-muted-foreground hover:text-foreground"
          aria-label={open ? "접다" : "펼치기"}
          aria-expanded={open}
        >
          <ChevronRightIcon className={cn("size-3.5 transition-transform", open && "rotate-90")} />
        </button>
      ) : (
        <span className="size-5 shrink-0" />
      )}
      <button
        type="button"
        onClick={onSelect}
        className="flex min-w-0 flex-1 items-center gap-1.5 py-1 text-left"
        title={`${KIND_LABEL[node.kind] ?? node.kind} · ${node.label}`}
      >
        <Icon className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
        <span className={cn("min-w-0 truncate", selected && "font-medium")}>{node.display}</span>
      </button>
      <span className="flex shrink-0 items-center gap-1 text-xs tabular-nums">
        {node.critical > 0 && (
          <span className="text-rose-600" title={`심각해요 ${node.critical}`}>
            {node.critical}
          </span>
        )}
        {node.high > 0 && (
          <span className="text-red-500" title={`위험도 높음 ${node.high}`}>
            {node.high}
          </span>
        )}
        <span className="text-muted-foreground" title={`합계 ${node.total} 찾았습니다`}>
          {node.total}
        </span>
      </span>
    </div>
  );
}
