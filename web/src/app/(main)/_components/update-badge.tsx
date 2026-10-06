"use client";

import * as React from "react";

import Link from "next/link";

import { ArrowUpCircleIcon } from "lucide-react";

import { api } from "@/lib/api";

/**
 * 상단 컬럼"새 버전이 있습니다"팁：전체 페이지가 로드되면 한 번 확인하세요.，업데이트가 있으면 버전 번호 옆에 표시됩니다.，
 * 클릭하면 시스템 구성 페이지로 바로 이동합니다.「버전 및 업데이트」카드。
 *
 * 백엔드 쌍 GitHub 에 대한 쿼리 결과는 다음과 같습니다. 30 분 캐시，그러니 여기에 장착할 때마다 확인하시면 안전할 것 같습니다
 * ——미인증 GitHub API 만 60 회/시간/IP，캐시 레이어가 없으면，탭 더 열기
 * 할당량을 모두 사용합니다.，나중에 꼭 업데이트하고 싶었는데 못찾았네요.。
 *
 * 쿼리가 실패하면 자동으로 처리됩니다.：상단바는 오류를 보고하는 곳이 아닙니다.，사용자 항목 설정 페이지「업데이트 확인」이유를 알겠습니다。
 */
export function UpdateBadge() {
  const [latest, setLatest] = React.useState("");

  React.useEffect(() => {
    let alive = true;
    api
      .checkUpdate()
      .then((r) => {
        // has_update 이미 포함되어 있습니다."버전 번호는 비슷합니다."의 판단，개발 빌드에서는 이 프롬프트가 표시되지 않습니다.。
        if (alive && r.has_update && r.latest) setLatest(r.latest.replace(/^v(?=\d)/, ""));
      })
      .catch(() => {
        // 침묵：인터넷이 연결되지 않았습니다. / GitHub 전류가 제한되어 있어도 상단 표시줄에 오류가 표시되어서는 안 됩니다.。
      });
    return () => {
      alive = false;
    };
  }, []);

  if (!latest) return null;

  return (
    <Link
      href="/system/settings"
      title={`새 버전 발견 ${latest}，업데이트하려면 클릭하세요.`}
      className="inline-flex items-center gap-1.5 rounded-full bg-primary px-2.5 py-1 font-medium text-primary-foreground text-xs transition-opacity hover:opacity-90"
    >
      {/* 호흡점：상단바에 여러가지 요소가 있습니다，일반 텍스트는 쉽게 무시됩니다.，한눈에 볼 수 있게 해주는 애니메이션。 */}
      <span className="relative flex size-1.5">
        <span className="absolute inline-flex size-full animate-ping rounded-full bg-primary-foreground opacity-75" />
        <span className="relative inline-flex size-1.5 rounded-full bg-primary-foreground" />
      </span>
      <ArrowUpCircleIcon className="size-3.5" />
      <span className="hidden sm:inline">새 버전 {latest}</span>
      <span className="sm:hidden">새 버전</span>
    </Link>
  );
}
