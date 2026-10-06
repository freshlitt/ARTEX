"use client";

import * as React from "react";

import {
  CheckCircle2Icon,
  DownloadIcon,
  ExternalLinkIcon,
  RefreshCwIcon,
  RotateCcwIcon,
  TriangleAlertIcon,
} from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { api, sseUrl } from "@/lib/api";
import type { UpdateCheck, UpdateProgress } from "@/lib/types";

/** 새 버전이 온라인에 출시될 때까지 기다리는 가장 긴 시간。업그레이드에는 세 번의 프로세스 시작이 필요합니다.（임시저장 → 옷을 입으세요 → 새 버전），
 *  초마다，느린 디스크와 Docker 컨테이너 재구성。 */
const RESTART_TIMEOUT_MS = 180_000;

function humanSize(n?: number): string {
  if (!n || n <= 0) return "";
  const units = ["B", "KB", "MB", "GB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

export function UpdateCard() {
  const [info, setInfo] = React.useState<UpdateCheck | null>(null);
  const [checking, setChecking] = React.useState(true);
  const [progress, setProgress] = React.useState<UpdateProgress | null>(null);
  // 그리고 progress 별도：임시저장이 완료되면 해당 프로세스는 사라지게 됩니다.，SSE 깨지겠습니다，이제 폴링으로 전환해야 합니다. /api/health。
  const [restarting, setRestarting] = React.useState(false);
  const [busy, setBusy] = React.useState(false);

  // quiet 백엔드 캐시 우회 여부도 결정합니다.：페이지 진입 시 자동 확인을 위한 캐시（방금 상단 열을 확인했습니다），
  // 사용자 매뉴얼 클릭「업데이트 확인」그런 다음 소스로 강제 복귀，그렇지 않으면 캐시가 만료될 때까지 새로 출시된 버전이 표시되지 않습니다.。
  const check = React.useCallback((quiet = false) => {
    setChecking(true);
    api
      .checkUpdate(!quiet)
      .then((r) => {
        setInfo(r);
        if (!quiet) {
          if (r.error) toast.error("업데이트 확인에 실패했습니다.：" + r.error);
          else if (r.has_update) toast.success(`새 버전 발견 ${r.latest}`);
          else if (r.comparable) toast.success("현재 최신버전입니다");
        }
      })
      .catch((e) => {
        if (!quiet) toast.error("업데이트 확인에 실패했습니다.：" + (e as Error).message);
      })
      .finally(() => setChecking(false));
  }, []);

  React.useEffect(() => {
    check(true);
  }, [check]);

  // 폴링 /api/health 버전번호가 변경될 때까지。
  //
  // 기준은 다음과 같아야 합니다."버전이 바뀌었어요"대신"접속 가능"：드레싱 과정에서 이전 버전이 잠시 다시 나타납니다.
  // （그때 나는 오직 artex.new 변경하고 바로 종료），연결성만 보면 성공을 잘못 판단할 수도 있습니다.。
  const waitForNewVersion = React.useCallback(async (fromVersion: string) => {
    setRestarting(true);
    const deadline = Date.now() + RESTART_TIMEOUT_MS;
    while (Date.now() < deadline) {
      await sleep(2000);
      try {
        const r = await fetch("/api/health", { cache: "no-store" });
        if (r.ok) {
          const j = (await r.json()) as { version?: string };
          if (j.version && j.version !== fromVersion) {
            toast.success(`업데이트됨 ${j.version}，페이지 새로고침 중`);
            await sleep(800);
            window.location.reload();
            return;
          }
        }
      } catch {
        // 재시작 창 내에서는 연결이 되지 않을 것으로 예상됩니다.，계속 폴링。
      }
    }
    setRestarting(false);
    toast.error("서비스 재시작을 기다리는 동안 시간이 초과되었습니다.。백엔드 로그를 확인하세요.，또는 확인 artex 네 합격했습니다 start.sh / start.bat 시작됨。");
  }, []);

  // 업데이트 진행 상황을 구독하세요.。SSE 떠나지 않음 Next 님 /api 다시 작성（해당 레이어는 버퍼링됩니다.，이벤트를 푸시할 수 없습니다.）。
  const openStream = React.useCallback(
    (fromVersion: string) => {
      const es = new EventSource(sseUrl("/api/update/stream"));
      es.onmessage = (ev) => {
        let p: UpdateProgress;
        try {
          p = JSON.parse(ev.data) as UpdateProgress;
        } catch {
          return;
        }
        setProgress(p);
        if (p.phase === "failed") {
          es.close();
          setBusy(false);
          toast.error("업데이트 실패：" + (p.error || p.message));
          return;
        }
        if (p.phase === "staged") {
          es.close();
          void waitForNewVersion(fromVersion);
        }
      };
      es.onerror = () => {
        // 프로세스가 종료될 때 SSE 연결이 끊어져야 합니다。진입해서 재시작 대기중이라면，이건 정상이에요，
        // 줘 /api/health 계속해서 폴링하여 결정하세요.。
        es.close();
      };
      return es;
    },
    [waitForNewVersion],
  );

  const doUpdate = () => {
    if (!info) return;
    const from = info.current;
    const ok = window.confirm(
      `업데이트 확인 ${info.latest}？\n\n` +
        "업데이트하면 프로그램이 다시 시작됩니다.，실행 중인 작업이 중단됩니다.。\n" +
        (info.mode === "docker"
          ? "\n주의：컨테이너 내 업데이트는 프로그램 자체만 대체합니다.，이미지를 업데이트하지 않습니다. playwright / nmap 및 기타 도구 체인；" +
            "새 버전이 새 도구에 의존하는 경우，대신 사용해 주세요 docker compose pull。"
          : ""),
    );
    if (!ok) return;

    setBusy(true);
    setProgress({ phase: "downloading", percent: 0, message: "준비중…" });
    const es = openStream(from);
    api.applyUpdate().catch((e) => {
      es.close();
      setBusy(false);
      setProgress(null);
      toast.error("업데이트를 시작하지 못했습니다.：" + (e as Error).message);
    });
  };

  const doRollback = () => {
    if (!info) return;
    if (
      !window.confirm(
        "이전 버전으로 롤백？\n\n프로그램이 다시 시작됩니다，실행 중인 작업이 중단됩니다.。\n주의：데이터베이스 구조는 롤백되지 않습니다.，이전 버전에서는 새 버전에서 작성한 데이터를 인식하지 못할 수 있습니다.。",
      )
    )
      return;
    const from = info.current;
    setBusy(true);
    api
      .rollbackUpdate()
      .then(() => {
        toast.success("이전 버전으로 전환되었습니다.，다시 시작하는 중…");
        void waitForNewVersion(from);
      })
      .catch((e) => {
        setBusy(false);
        toast.error("롤백 실패：" + (e as Error).message);
      });
  };

  const phase = progress?.phase;
  const showProgress = busy || restarting;
  // 다운로드 단계에서만 실제 백분율을 얻을 수 있습니다.（언론 Content-Length 수）。확인/압축을 푼다/재시작 대기 중
  // 지속시간을 알 수 없는 무대，진행률 표시줄이 채워지고 펄스 애니메이션이 추가되어 이를 나타냅니다."바쁘지만 얼마나 걸릴지 모르겠습니다."。
  const downloading = !restarting && phase === "downloading";
  const pct = downloading ? Math.max(progress?.percent ?? 0, 0) : 100;

  return (
    // 설정 페이지는 다중 열 폭포 흐름 레이아웃입니다.，카드 자체가 줄 간격을 담당하고 열 간 연결 끊김을 방지합니다.（또 만나요 page.tsx 님의 댓글）。
    <Card className="mb-4 break-inside-avoid md:mb-6">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <DownloadIcon className="size-4" />
          버전 및 업데이트
        </CardTitle>
        <CardDescription>님으로부터 GitHub 새 버전 확인 및 설치。업데이트하면 프로그램이 다시 시작됩니다.，실행 중인 작업이 중단됩니다.。</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <span className="text-muted-foreground">현재 버전</span>
          <Badge variant="secondary" className="font-mono">
            {info?.current ?? "…"}
          </Badge>
          {info && (
            <>
              <Badge variant="outline" className="font-mono">
                {info.os}/{info.arch}
              </Badge>
              <Badge variant="outline">{info.mode === "docker" ? "Docker" : "독립형 프로그램"}</Badge>
            </>
          )}
          {info?.latest && (
            <>
              <span className="text-muted-foreground">최신 버전</span>
              <Badge variant={info.has_update ? "default" : "secondary"} className="font-mono">
                {info.latest}
              </Badge>
            </>
          )}
          {info?.html_url && (
            <a
              href={info.html_url}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-1 text-xs text-muted-foreground underline-offset-4 hover:underline"
            >
              업데이트 로그 <ExternalLinkIcon className="size-3" />
            </a>
          )}
        </div>

        {info?.boot_notice && (
          <p className="flex items-start gap-2 rounded-md border border-amber-500/40 bg-amber-500/10 p-2 text-xs text-amber-700 dark:text-amber-400">
            <TriangleAlertIcon className="mt-0.5 size-3.5 shrink-0" />
            {info.boot_notice}
          </p>
        )}

        {info?.error && (
          <p className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-2 text-xs text-destructive">
            <TriangleAlertIcon className="mt-0.5 size-3.5 shrink-0" />
            연결할 수 없습니다 GitHub：{info.error}
            {"　"}위에서 전역 프록시를 구성하고 다시 시도할 수 있습니다.。
          </p>
        )}

        {info && !info.comparable && info.reason && <p className="text-xs text-muted-foreground">{info.reason}</p>}

        {info?.has_update && info.asset_available === false && (
          <p className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-2 text-xs text-destructive">
            <TriangleAlertIcon className="mt-0.5 size-3.5 shrink-0" />
            {info.latest} 제공되지 않음 {info.os}/{info.arch} 패키지 출시（없어짐 {info.asset}），자동으로 업데이트할 수 없습니다.。
          </p>
        )}

        {info?.has_update && info.asset_available !== false && (
          <p className="text-xs text-muted-foreground">
            다운로드하겠습니다 <span className="font-mono">{info.asset}</span>
            {info.size ? `（${humanSize(info.size)}）` : ""}，확인 SHA256 및 교체 전 연기 테스트，현재 버전 자동 유지 실패。
          </p>
        )}

        {info && !info.has_update && info.comparable && !info.error && (
          <p className="flex items-center gap-2 text-xs text-muted-foreground">
            <CheckCircle2Icon className="size-3.5 text-emerald-600" />
            현재 최신버전입니다。
          </p>
        )}

        {info?.mode === "docker" && info.has_update && (
          <p className="text-xs text-muted-foreground">
            Docker 아래의 업데이트는 프로그램 자체만 대체합니다.，이미지 업데이트 안함 playwright / nmap 및 기타 도구 체인，그리고
            <span className="font-mono"> docker compose up -d </span>
            컨테이너를 다시 빌드한 후 이미지와 함께 제공되는 버전이 반환됩니다.。이미지 업그레이드가 필요하시면 같이 해주세요.
            <span className="font-mono"> docker compose pull artex &amp;&amp; docker compose up -d artex</span>。
          </p>
        )}

        {showProgress && (
          <div className="space-y-1.5">
            <Progress value={pct} className={downloading ? undefined : "animate-pulse"} />
            <p className="text-xs text-muted-foreground">
              {restarting ? "다시 시작하고 새 버전을 적용하는 중입니다.，기다려주세요（페이지가 자동으로 새로 고쳐집니다.）…" : progress?.message}
            </p>
          </div>
        )}

        <div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" onClick={() => check(false)} disabled={checking || busy || restarting}>
            <RefreshCwIcon className={checking ? "size-4 animate-spin" : "size-4"} />
            업데이트 확인
          </Button>
          <Button
            size="sm"
            onClick={doUpdate}
            disabled={busy || restarting || !info?.has_update || info?.asset_available === false}
          >
            <DownloadIcon className="size-4" />
            {info?.has_update ? `업데이트됨 ${info.latest}` : "지금 업데이트하세요"}
          </Button>
          {info?.has_backup && (
            <Button variant="ghost" size="sm" onClick={doRollback} disabled={busy || restarting}>
              <RotateCcwIcon className="size-4" />
              이전 버전으로 롤백
            </Button>
          )}
        </div>

        <p className="text-xs text-muted-foreground">
          원클릭 업데이트 종속성 데몬 스크립트 다시 시작 프로그램。합격해주세요 <span className="font-mono">start.sh</span>（Windows 입니다
          <span className="font-mono"> start.bat</span>）시작 ARTEX；직접 실행 artex 온톨로지，프로그램 종료 후 자동으로 실행되지 않습니다.。
        </p>
      </CardContent>
    </Card>
  );
}
