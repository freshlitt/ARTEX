import { type ClassValue, clsx } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

// 서랍/대화 상자(Sheet/Dialog)님 onInteractOutside 판단지원 꺼주세요。
//
// 배경:서랍 속에 Radix 탄성층(Select 드롭다운、DropdownMenu、Popover 등)예 portal 서랍으로
// 외부。탄성 레이어가 켜진 상태에서 마스크를 클릭하세요./서랍밖으로 치워두고 싶다,이번에는 pointerdown 입니다 Select 그리고 Sheet 둘
// DismissableLayer 동시에 처리됨;Select 먼저 닫고 예 discrete 이벤트、React 이 동기화됩니다. flush,그래서
// 당신 차례예요 Sheet 의 프로세서는 탄성층입니다. data-state 은(는) 다음 언어로 번역되었습니다. closed —— 에"지금"탄성층이 열려 있는지 확인하세요
// 당연히 신뢰할 수 없음(실측으로 확인함)。
//
// 올바른 접근 방식:Radix 님 pointerdown 모니터링은 버블링 단계입니다.;우리는 capture 무대(그보다 먼저)먼저
// "지금 혹시 탄성층이 열려있나요?"녹음해 보세요,onInteractOutside 이 레코드 값을 다시 읽고 종료 해제 여부를 결정하십시오.。
function isRadixOverlayOpenNow(): boolean {
  if (typeof document === "undefined") return false;
  return !!document.querySelector(
    [
      "[data-slot='select-trigger'][data-state='open']",
      "[data-slot='select-content'][data-state='open']",
      "[role='listbox'][data-state='open']",
      "[data-radix-popper-content-wrapper]",
      "[aria-expanded='true'][data-state='open']",
    ].join(","),
  );
}

let overlayOpenAtLastPointerDown = false;
if (typeof document !== "undefined") {
  document.addEventListener(
    "pointerdown",
    () => {
      overlayOpenAtLastPointerDown = isRadixOverlayOpenNow();
    },
    true, // capture:먼저 받아보세요 Radix 버블링 단계에서 pointerdown 프로세서 이전 기록
  );
}

// radixOverlayWasOpenAtPointerDown 복귀"지난번에 pointerdown 그런 게 있었나요? Radix 탄성층
// 열려 있는"。서랍/대화 상자가 나타납니다.:탄성 레이어가 켜진 상태에서 마스크를 클릭하세요. → 탄성층만 채취、너 자신에 관한 것이 아니야。
export function radixOverlayWasOpenAtPointerDown(): boolean {
  return overlayOpenAtLastPointerDown;
}

// copyText 클립보드에 텍스트 쓰기,성공 여부를 반환합니다.。
// 배경:navigator.clipboard 보안 컨텍스트에서만(HTTPS / localhost)가능;합격 IP + HTTP
// 액세스하면 undefined,이때 다운그레이드는 execCommand("copy")。
export async function copyText(text: string): Promise<boolean> {
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // 다운그레이드 계획을 계속 진행하세요.
    }
  }
  try {
    const textarea = document.createElement("textarea");
    textarea.value = text;
    textarea.style.position = "fixed";
    textarea.style.left = "-9999px";
    textarea.style.top = "0";
    document.body.appendChild(textarea);
    textarea.focus();
    textarea.select();
    const ok = document.execCommand("copy");
    document.body.removeChild(textarea);
    return ok;
  } catch {
    return false;
  }
}

export const getInitials = (str: string): string => {
  if (typeof str !== "string" || !str.trim()) return "?";

  return (
    str
      .trim()
      .split(/\s+/)
      .filter(Boolean)
      .map((word) => word[0])
      .join("")
      .toUpperCase() || "?"
  );
};

export function formatCurrency(
  amount: number,
  opts?: {
    currency?: string;
    locale?: string;
    minimumFractionDigits?: number;
    maximumFractionDigits?: number;
    noDecimals?: boolean;
  },
) {
  const { currency = "USD", locale = "en-US", minimumFractionDigits, maximumFractionDigits, noDecimals } = opts ?? {};

  const formatOptions: Intl.NumberFormatOptions = {
    style: "currency",
    currency,
    minimumFractionDigits: noDecimals ? 0 : minimumFractionDigits,
    maximumFractionDigits: noDecimals ? 0 : maximumFractionDigits,
  };

  return new Intl.NumberFormat(locale, formatOptions).format(amount);
}
