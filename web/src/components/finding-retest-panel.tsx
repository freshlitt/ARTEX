"use client";

import * as React from "react";

import Link from "next/link";

import { RotateCcwIcon } from "lucide-react";

import { FindingRetestDialog } from "@/components/finding-retest-dialog";
import { Markdown } from "@/components/markdown";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { api } from "@/lib/api";
import type { FindingRetest } from "@/lib/types";

const statusLabels = {
  pending: "시작 대기 중",
  running: "재시험 중",
  completed: "완료",
  failed: "재테스트 실패",
  stopped: "중지됨",
};
const verdictLabels = { reproduced: "아직 재현 가능", fixed: "고정됨", inconclusive: "확인불가" };

function active(r: FindingRetest) {
  return r.status === "pending" || r.status === "running";
}

export function FindingRetestPanel({
  findingId,
  findingName,
  readOnly = false,
  onCompleted,
}: {
  findingId: string;
  findingName?: string;
  readOnly?: boolean;
  onCompleted?: () => void;
}) {
  const [items, setItems] = React.useState<FindingRetest[] | null>(null);
  const [error, setError] = React.useState("");
  const [open, setOpen] = React.useState(false);
  const requestSeq = React.useRef(0);
  const previousItems = React.useRef<FindingRetest[]>([]);

  const load = React.useCallback(async () => {
    const seq = ++requestSeq.current;
    try {
      const rows = await api.findingRetests(findingId);
      if (seq !== requestSeq.current) return;
      const completed = previousItems.current.some(
        (item) => active(item) && rows.some((row) => row.id === item.id && !active(row)),
      );
      previousItems.current = rows;
      setItems(rows);
      setError("");
      if (completed) onCompleted?.();
    } catch (e) {
      if (seq === requestSeq.current) setError((e as Error).message);
    }
  }, [findingId, onCompleted]);

  React.useEffect(() => {
    void load();
    return () => {
      requestSeq.current++;
    };
  }, [load]);

  React.useEffect(() => {
    if (error || !items?.some(active)) return;
    const timer = setTimeout(() => void load(), 3000);
    return () => clearTimeout(timer);
  }, [error, items, load]);

  const running = items?.find(active);

  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-start justify-between gap-3">
        <div className="flex flex-col gap-1.5">
          <CardTitle>취약점 재테스트</CardTitle>
          <CardDescription>독립형 세션에서 현재 상태 확인，각 재시험의 결론과 증거를 보관하십시오.。</CardDescription>
        </div>
        {running?.conversation_id != null ? (
          <Button asChild variant="outline" size="sm">
            <Link href={`/chat?c=${running.conversation_id}`} title="진행 중인 재시험 세션 보기">
              <Spinner data-icon="inline-start" aria-hidden="true" />
              재시험 중
            </Link>
          </Button>
        ) : null}
        {!running && !readOnly ? (
          <Button size="sm" onClick={() => setOpen(true)} disabled={items === null || !!error}>
            <RotateCcwIcon data-icon="inline-start" />
            재테스트 시작
          </Button>
        ) : null}
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {error ? (
          <Alert variant="destructive">
            <AlertDescription>
              재테스트 기록을 로드하지 못했습니다.：{error}
              <Button variant="outline" size="sm" onClick={() => void load()}>
                다시 시도해보세요
              </Button>
            </AlertDescription>
          </Alert>
        ) : null}
        {!error && items === null ? <Skeleton className="h-16 w-full" /> : null}
        {!error && items?.length === 0 ? (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>아직 재검사 기록이 없습니다</EmptyTitle>
              <EmptyDescription>수리 배포 완료 후，재테스트를 시작하고 이전 증거와 새 증거를 비교할 수 있습니다.。</EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : null}
        {!error && items
          ? items.map((item) => (
              <div key={item.id} className="flex min-w-0 flex-col gap-2 rounded-lg border p-3">
                <div className="flex flex-wrap items-center gap-2">
                  <Badge
                    variant={item.status === "completed" && item.verdict === "reproduced" ? "destructive" : "secondary"}
                  >
                    {item.status === "completed" && item.verdict
                      ? verdictLabels[item.verdict]
                      : statusLabels[item.status]}
                  </Badge>
                  <span className="text-muted-foreground text-xs">
                    #{item.id} · {new Date(item.created_at).toLocaleString("zh-CN")}
                  </span>
                  {item.conversation_id != null ? (
                    <Button asChild variant="ghost" size="sm" className="ml-auto">
                      <Link href={`/chat?c=${item.conversation_id}`}>대화 보기</Link>
                    </Button>
                  ) : (
                    <span className="text-muted-foreground text-xs">대화가 삭제되었습니다</span>
                  )}
                </div>
                {item.status === "completed" && item.summary ? (
                  <p className="whitespace-pre-wrap break-words text-sm">{item.summary}</p>
                ) : null}
                {item.error ? (
                  <p className="whitespace-pre-wrap break-words text-destructive text-sm">{item.error}</p>
                ) : null}
                {item.notes ? (
                  <p className="whitespace-pre-wrap break-words text-muted-foreground text-xs">
                    추가 설명：{item.notes}
                  </p>
                ) : null}
                {item.status === "completed" && item.evidence ? (
                  <details className="min-w-0">
                    <summary className="cursor-pointer text-sm">증거 재시험</summary>
                    <div className="mt-3 overflow-x-auto">
                      <Markdown text={item.evidence} />
                    </div>
                  </details>
                ) : null}
              </div>
            ))
          : null}
      </CardContent>
      {open ? (
        <FindingRetestDialog
          findingId={findingId}
          findingName={findingName}
          onClose={() => setOpen(false)}
          onStarted={(retest) => {
            requestSeq.current++;
            const rows = [retest, ...previousItems.current.filter((item) => item.id !== retest.id)];
            previousItems.current = rows;
            setItems(rows);
            setError("");
            void load();
          }}
        />
      ) : null}
    </Card>
  );
}
