import type { NewAssetType } from "@/lib/types";

const ASSET_TYPE_LABELS: Record<NewAssetType, string> = {
  app: "신청",
  endpoint: "인터페이스",
  ip: "IP",
  root_domain: "루트 도메인 이름",
  service: "서비스",
  subdomain: "하위 도메인 이름",
};

const TASK_ASSET_SOURCE_LABELS: Record<string, string> = {
  agent: "Agent 찾음",
  anchor: "칠판 앵커",
  api: "자산 API",
  company: "기업소속",
  legacy: "역사협회",
  manual: "수동으로 가입",
  system: "시스템 연관",
  task: "작업 초기화",
};

export function taskAssetTypeLabel(type: NewAssetType): string {
  return ASSET_TYPE_LABELS[type];
}

export function taskAssetSourceLabel(source: string): string {
  return TASK_ASSET_SOURCE_LABELS[source] ?? source;
}
