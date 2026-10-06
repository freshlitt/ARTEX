"use client";

import * as React from "react";

import { getLocalStorageValue, setLocalStorageValue } from "@/lib/local-storage.client";

// 세션 입력 상자 보내기/줄 바꿈 키。순수한 프런트엔드 선호：넘어지기만 한다 localStorage，데이터베이스에 포함되지 않음、계정과 동기화되지 않습니다，
// 그래서 브라우저 변경시 재설정을 하셔야 합니다。또 만나요 issue #39——0.3.2 넣어보세요 Ctrl+Enter 다음으로 변경하여 보내기 Enter 보내기，
// 여기서는 기존 키 위치를 옵션으로 반환해줍니다.。
export type ChatSendMode = "enter" | "ctrl-enter";

export const CHAT_SEND_MODE_KEY = "artex_chat_send_mode";
export const DEFAULT_CHAT_SEND_MODE: ChatSendMode = "enter";

export const CHAT_SEND_MODE_OPTIONS: { value: ChatSendMode; label: string }[] = [
  { value: "enter", label: "Enter 보내기，Shift+Enter 줄 바꿈" },
  { value: "ctrl-enter", label: "Ctrl+Enter 보내기，Enter 줄 바꿈" },
];

function parseMode(raw: string | null): ChatSendMode {
  return raw === "ctrl-enter" || raw === "enter" ? raw : DEFAULT_CHAT_SEND_MODE;
}

// 동일한 탭 내의 구독자 모음。localStorage 님 storage 이벤트는 에서만 진행됩니다.「기타」탭 트리거，
// 이 페이지는 설정에서 변경한 후 변경해야 합니다. emit 같은 페이지의 입력창에 알림，그렇지 않으면 새로 고쳐야 적용됩니다.。
const listeners = new Set<() => void>();

function subscribe(listener: () => void) {
  listeners.add(listener);
  window.addEventListener("storage", listener);
  return () => {
    listeners.delete(listener);
    window.removeEventListener("storage", listener);
  };
}

// 은 문자열 리터럴을 반환합니다.，Object.is 값으로 비교，안 돼요 useSyncExternalStore 루프에 갇혀。
function getSnapshot(): ChatSendMode {
  return parseMode(getLocalStorageValue(CHAT_SEND_MODE_KEY));
}

// 서버에 없습니다. localStorage，기본값을 먼저 렌더링합니다.，hydrate 이후 getSnapshot 다시 정정합니다。
function getServerSnapshot(): ChatSendMode {
  return DEFAULT_CHAT_SEND_MODE;
}

export function useChatSendMode(): ChatSendMode {
  return React.useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
}

export function setChatSendMode(mode: ChatSendMode) {
  setLocalStorageValue(CHAT_SEND_MODE_KEY, mode);
  for (const listener of listeners) listener();
}

// shouldSubmitOnKey 키 입력을 보낼지 여부를 결정합니다.。
// isComposing / keyCode 229 문자를 선택하는 중국어 등의 입력방식입니다.，석방되어야 함，그렇지 않으면 Enter를 눌러 선택한 단어가 실수로 전송됩니다.。
// enter 패턴 제외만 Shift，그리고 0.3.2 의 동작은 그대로 일관됩니다.——설정을 바꾸지 않은 사용자도 같은 느낌입니다.。
// ctrl-enter 모드는 둘 다 허용합니다. Ctrl 그리고 Cmd（macOS）。
export function shouldSubmitOnKey(e: React.KeyboardEvent, mode: ChatSendMode): boolean {
  if (e.key !== "Enter") return false;
  if (e.nativeEvent.isComposing || e.nativeEvent.keyCode === 229) return false;
  if (mode === "ctrl-enter") return e.ctrlKey || e.metaKey;
  return !e.shiftKey;
}
