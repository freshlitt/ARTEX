"use client";

import * as React from "react";

import { CheckIcon, CopyIcon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { cn, copyText } from "@/lib/utils";

type CopyButtonProps = {
  // 복사할 텍스트;비어 있으면 버튼이 비활성화됩니다.。
  text: string | null | undefined;
  // 복사 성공 후 toast 카피라이팅,기본값「복사됨」。
  successMessage?: string;
  label?: React.ReactNode;
  size?: React.ComponentProps<typeof Button>["size"];
  variant?: React.ComponentProps<typeof Button>["variant"];
  className?: string;
};

// CopyButton 통일「클립보드에 복사」버튼:내장 성공/실패 피드백,그리고 HTTP 비보안 컨텍스트
// 아래 자동 다운그레이드(또 만나요 copyText)。
export function CopyButton({
  text,
  successMessage = "복사됨",
  label = "복사",
  size = "sm",
  variant = "outline",
  className,
}: CopyButtonProps) {
  const [copied, setCopied] = React.useState(false);
  const timer = React.useRef<ReturnType<typeof setTimeout> | null>(null);

  React.useEffect(() => {
    return () => {
      if (timer.current) clearTimeout(timer.current);
    };
  }, []);

  async function handleCopy() {
    if (!text) return;
    const ok = await copyText(text);
    if (ok) {
      setCopied(true);
      toast.success(successMessage);
      if (timer.current) clearTimeout(timer.current);
      timer.current = setTimeout(() => setCopied(false), 1500);
    } else {
      toast.error("복사 실패，복사할 텍스트를 수동으로 선택하세요.");
    }
  }

  return (
    <Button
      type="button"
      size={size}
      variant={variant}
      className={cn(className)}
      disabled={!text}
      onClick={handleCopy}
    >
      {copied ? <CheckIcon /> : <CopyIcon />}
      {label}
    </Button>
  );
}
