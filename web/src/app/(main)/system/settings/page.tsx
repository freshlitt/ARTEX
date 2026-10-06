"use client";

import * as React from "react";

import { CpuIcon, FlaskConicalIcon, KeyboardIcon, RadioTowerIcon, SearchIcon, ShieldAlertIcon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { api } from "@/lib/api";
import { CHAT_SEND_MODE_OPTIONS, type ChatSendMode, setChatSendMode, useChatSendMode } from "@/lib/chat-send-mode";
import type { Settings } from "@/lib/types";

import { UpdateCard } from "./_components/update-card";

export default function SystemSettingsPage() {
  const [trafficCapture, setTrafficCapture] = React.useState(false);
  const [agentTrafficBinding, setAgentTrafficBinding] = React.useState(false);
  const [webSearch, setWebSearch] = React.useState(false);
  const [backend, setBackend] = React.useState("ddgs");
  const [braveKeySet, setBraveKeySet] = React.useState(false);
  const [braveKeyInput, setBraveKeyInput] = React.useState("");
  const [tavilyKeySet, setTavilyKeySet] = React.useState(false);
  const [tavilyKeyInput, setTavilyKeyInput] = React.useState("");
  const [savingTavilyKey, setSavingTavilyKey] = React.useState(false);
  const [proxyInput, setProxyInput] = React.useState("");
  const [savingProxy, setSavingProxy] = React.useState(false);
  const [globalProxyInput, setGlobalProxyInput] = React.useState("");
  const [savingGlobalProxy, setSavingGlobalProxy] = React.useState(false);
  const [testing, setTesting] = React.useState(false);
  const [loaded, setLoaded] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [savingKey, setSavingKey] = React.useState(false);
  const [pyInterp, setPyInterp] = React.useState("");
  const [workers, setWorkers] = React.useState("3");
  const [savingWorkers, setSavingWorkers] = React.useState(false);
  // 연산제약 주입범위(둘 다 기본적으로 활성화되어 있습니다.)。
  const [injectPlanner, setInjectPlanner] = React.useState(true);
  const [injectWorker, setInjectWorker] = React.useState(true);
  // 실험적 기능:noa 컨텍스트 압축(기본값은 꺼짐)。
  const [noaCompaction, setNoaCompaction] = React.useState(false);
  // 순수한 프런트엔드 선호：떠나지 않음 /api/settings，직접 읽고 쓰기 localStorage。
  const sendMode = useChatSendMode();

  const apply = React.useCallback((s: Settings) => {
    setTrafficCapture(!!s.traffic_capture);
    setAgentTrafficBinding(!!s.agent_traffic_binding);
    setWebSearch(!!s.web_search_enabled);
    setBackend(s.web_search_backend || "ddgs");
    setBraveKeySet(!!s.brave_key_set);
    setTavilyKeySet(!!s.tavily_key_set);
    setProxyInput(s.web_search_proxy ?? "");
    setGlobalProxyInput(s.global_proxy ?? "");
    setPyInterp(s.python_interpreter ?? "");
    setWorkers(String(s.workers ?? 3));
    setInjectPlanner(s.constraints_inject_planner !== false);
    setInjectWorker(s.constraints_inject_worker !== false);
    setNoaCompaction(!!s.noa_compaction);
  }, []);

  const saveWorkers = () => {
    const n = Number(workers);
    if (!Number.isInteger(n) || n <= 0) {
      toast.error("동시성 수는 다음보다 커야 합니다. 0 정수");
      return;
    }
    setSavingWorkers(true);
    api
      .setSettings({ workers: n })
      .then((s) => {
        apply(s);
        toast.success("동시 작업이 저장되었습니다. agent 번호（나중에 시작한 작업에 유효）");
      })
      .catch((e) => toast.error("저장 실패：" + (e as Error).message))
      .finally(() => setSavingWorkers(false));
  };

  const savePython = () => {
    setSaving(true);
    api
      .setSettings({ python_interpreter: pyInterp.trim() })
      .then((s) => {
        apply(s);
        toast.success("저장되었습니다 Python 통역사 구성");
      })
      .catch((e) => toast.error("저장 실패：" + (e as Error).message))
      .finally(() => setSaving(false));
  };
  const detectPython = () => {
    setSaving(true);
    api
      .detectPython()
      .then((r) => setPyInterp(r.python_interpreter))
      .catch(() => undefined)
      .finally(() => setSaving(false));
  };

  React.useEffect(() => {
    api
      .settings()
      .then(apply)
      .catch(() => undefined)
      .finally(() => setLoaded(true));
  }, [apply]);

  const toggleTraffic = (v: boolean) => {
    setTrafficCapture(v); // optimistic
    setSaving(true);
    api
      .setSettings({ traffic_capture: v })
      .then(apply)
      .catch(() => setTrafficCapture(!v)) // revert on failure
      .finally(() => setSaving(false));
  };

  const toggleInjectPlanner = (v: boolean) => {
    setInjectPlanner(v); // optimistic
    api
      .setSettings({ constraints_inject_planner: v })
      .then(apply)
      .catch(() => setInjectPlanner(!v)); // revert on failure
  };

  const toggleAgentTrafficBinding = (v: boolean) => {
    setAgentTrafficBinding(v);
    setSaving(true);
    api
      .setSettings({ agent_traffic_binding: v })
      .then((s) => {
        apply(s);
        toast.success(v ? "켜짐 Agent 자동으로 트래픽 바인딩" : "폐쇄됨 Agent 자동으로 트래픽 바인딩");
      })
      .catch((e) => {
        setAgentTrafficBinding(!v);
        toast.error(`저장 실패：${(e as Error).message}`);
      })
      .finally(() => setSaving(false));
  };

  const toggleInjectWorker = (v: boolean) => {
    setInjectWorker(v); // optimistic
    api
      .setSettings({ constraints_inject_worker: v })
      .then(apply)
      .catch(() => setInjectWorker(!v)); // revert on failure
  };

  const toggleNoaCompaction = (v: boolean) => {
    setNoaCompaction(v); // optimistic
    api
      .setSettings({ noa_compaction: v })
      .then((s) => {
        apply(s);
        toast.success(v ? "켜짐 noa 컨텍스트 압축（후속 실행에 적용됩니다.）" : "폐쇄됨 noa 컨텍스트 압축（내장 압축 복원）");
      })
      .catch((e) => {
        setNoaCompaction(!v); // revert on failure
        toast.error(`저장 실패：${(e as Error).message}`);
      });
  };

  // Persist a web-search patch (enable and/or backend). Optimistic with refetch.
  const saveWebSearch = (patch: Partial<Settings>) => {
    setSaving(true);
    api
      .setSettings(patch)
      .then((s) => {
        apply(s);
        toast.success("네트워크 검색 구성이 저장되었습니다.");
      })
      .catch((e) => {
        toast.error("저장 실패：" + (e as Error).message);
        api
          .settings()
          .then(apply)
          .catch(() => undefined);
      })
      .finally(() => setSaving(false));
  };

  const saveBraveKey = () => {
    setSavingKey(true);
    api
      .setSettings({ brave_search_api_key: braveKeyInput })
      .then((s) => {
        apply(s);
        setBraveKeyInput("");
        toast.success("저장되었습니다 Brave API Key");
      })
      .catch((e) => toast.error("저장 실패：" + (e as Error).message))
      .finally(() => setSavingKey(false));
  };

  const saveTavilyKey = () => {
    setSavingTavilyKey(true);
    api
      .setSettings({ tavily_search_api_key: tavilyKeyInput })
      .then((s) => {
        apply(s);
        setTavilyKeyInput("");
        toast.success("저장되었습니다 Tavily API Key");
      })
      .catch((e) => toast.error("저장 실패：" + (e as Error).message))
      .finally(() => setSavingTavilyKey(false));
  };

  const saveProxy = () => {
    setSavingProxy(true);
    api
      .setSettings({ web_search_proxy: proxyInput.trim() })
      .then((s) => {
        apply(s);
        toast.success(proxyInput.trim() ? "수출 에이전트가 저장되었습니다." : "내보내기 프록시가 지워졌습니다.（직접접속으로 변경）");
      })
      .catch((e) => toast.error("저장 실패：" + (e as Error).message))
      .finally(() => setSavingProxy(false));
  };

  const saveGlobalProxy = () => {
    setSavingGlobalProxy(true);
    api
      .setSettings({ global_proxy: globalProxyInput.trim() })
      .then((s) => {
        apply(s);
        toast.success(globalProxyInput.trim() ? "글로벌 에이전트가 저장되었습니다." : "글로벌 프록시가 지워졌습니다.（직접접속으로 변경）");
      })
      .catch((e) => toast.error("저장 실패：" + (e as Error).message))
      .finally(() => setSavingGlobalProxy(false));
  };

  // Run a real "test" search ("test") against the CURRENT form values (backend +
  // proxy + entered key), falling back to saved values server-side. Toasts result.
  const runTest = () => {
    setTesting(true);
    api
      .testWebSearch({
        web_search_backend: backend,
        web_search_proxy: proxyInput.trim(),
        brave_search_api_key: braveKeyInput,
        tavily_search_api_key: tavilyKeyInput,
      })
      .then((r) => {
        if (r.ok) toast.success(`검색 테스트 성공 · ${r.backend} 복귀 ${r.count} 결과`);
        else toast.error("검색 테스트 실패：" + (r.error || "알 수 없는 오류"));
      })
      .catch((e) => toast.error("검색 테스트 실패：" + (e as Error).message))
      .finally(() => setTesting(false));
  };

  // brave-free selected but no key stored and none being entered → tool stays off.
  const braveNeedsKey = webSearch && backend === "brave-free" && !braveKeySet;

  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">시스템 구성</h1>
        <p className="text-muted-foreground text-sm">전역 런타임 스위치</p>
      </div>

      {/* 대신 여러 열 grid：네트워크 검색 카드가 다른 카드보다 몇 배나 높습니다.，선택한 백엔드에 따라 높이가 변경됩니다.（brave/tavily
          님 key 입력은 조건부 렌더링입니다.）。grid 가장 높은 것이 전체 행을 채웁니다.、옆에 큰 공백을 남겨주세요，
          여러 열은 내용의 높이에 따라 균형있게 자동으로 채워집니다.。카드 간격은 다음에 따라 다릅니다. mb 대신 gap——다중 열 레이아웃에서
          column-gap 열 사이의 간격만 유지하세요.，줄 간격은 하위 요소 자체에서 설정해야 합니다.。 */}
      <div className="columns-1 gap-4 md:gap-6 lg:columns-2">
        <UpdateCard />

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <RadioTowerIcon className="size-4" />
              트래픽 캡처
            </CardTitle>
            <CardDescription>
              개봉 후，모두 Agent 님 HTTP 모든 트래픽은 녹음 에이전트를 통해 데이터베이스에 기록됩니다.，그리고 Agent 주사 traffic_search / traffic_get
              도구 및 에이전트 구성（프롬프트 단어에 상담원 설명이 포함되어 있습니다.）。
              <br />
              닫기（기본값）일 때 트래픽이 기록되지 않습니다.：Agent
              <b>아니요</b>프록시 구성 및 트래픽 도구 가져오기，프롬프트 단어도<b>포함되지 않음</b>에이전트 관련 내용。전환 후 바로 재구축됩니다. Agent 유효。
            </CardDescription>
          </CardHeader>
          <CardContent className="flex items-center justify-between gap-4">
            <Label htmlFor="traffic-capture" className="text-sm font-normal text-muted-foreground">
              {trafficCapture ? "켜짐 · 이 트래픽을 기록하고 프록시를 삽입하는 중입니다." : "폐쇄됨 · 녹음이 없습니다、프록시 삽입 없음"}
            </Label>
            <Switch
              id="traffic-capture"
              checked={trafficCapture}
              disabled={!loaded || saving}
              onCheckedChange={toggleTraffic}
            />
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <RadioTowerIcon className="size-4" />
              Agent 자동으로 트래픽 바인딩
            </CardTitle>
            <CardDescription id="agent-traffic-binding-description">
              기본적으로 폐쇄됨。개봉 후，취약점이 데이터베이스에 입력되면 보고가 트리거됩니다. Agent 기존 내용을 확인하겠습니다. HTTP 요청/응답，해당 트래픽을 연관시킨 후 리포트 작성。
              <b>데이터 패키지 확인 및 추가 툴 호출이 늘어나게 됩니다. Token 소비。</b>
              <br />
              TCP、패킷이 캡쳐되지 않거나 일치하는 트래픽이 없더라도 정상적으로 보고될 수 있습니다.。이 스위치는 트래픽 캡처에 영향을 주지 않습니다.、저장된 증거 수동 제본 및 보기。 다음 라운드 Agent
              유효；새 자동 바인딩은 종료 후 즉시 거부됩니다.。
            </CardDescription>
          </CardHeader>
          <CardContent className="flex items-center justify-between gap-4">
            <Label htmlFor="agent-traffic-binding" className="text-sm font-normal text-muted-foreground">
              {agentTrafficBinding ? "켜짐 · 늘어납니다 Token 소비" : "폐쇄됨 · 수동 바인딩을 계속할 수 있습니다."}
            </Label>
            <Switch
              id="agent-traffic-binding"
              aria-describedby="agent-traffic-binding-description"
              checked={agentTrafficBinding}
              disabled={!loaded || saving}
              onCheckedChange={toggleAgentTrafficBinding}
            />
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <RadioTowerIcon className="size-4" />
              글로벌 에이전트
            </CardTitle>
            <CardDescription>
              모두 Agent 님<b>타겟 트래픽</b>네트워크를 종료하려면 이 에이전트를 사용하십시오.（출처 숨기기 IP / 도약대를 잡아라）。지원 <b>http / https / socks5</b>，가능{" "}
              <code>user:pass</code> 인증。비워두세요=직접 연결。
              <br />
              켜세요<b>트래픽 캡처</b>시간，녹음 대행 역할을 합니다.<b>업스트림</b>（모든 트래픽이 아직 저장되어 있습니다.，그럼 이 요원을 통해서 나가세요）；캡처가 꺼진 경우，직접 주입
              Agent 님 bash / WebFetch 네트워크 연결이 끊어졌습니다.。인터넷 검색 에이전트와 함께、LLM 에이전트는 서로 독립적입니다.。
              <br />
              <b>팁</b>：socks5 에<b>캡처 끄기</b>은 각 명령줄 도구를 사용하여 <code>ALL_PROXY</code> 지원（curl
              가능，일부 도구는 무시될 수 있습니다.）； 주로 사용하는 경우 socks5，트래픽 캡처를 활성화하는 것이 좋습니다.——이 경로는 다음에서 제공됩니다. MITM
              직접 전화하기，도구가 인식하지 못합니다.、안정적이고 효과적입니다.。
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-2">
            <Label htmlFor="global-proxy" className="text-sm font-normal text-muted-foreground">
              대리인 주소
            </Label>
            <div className="flex items-center gap-2">
              <Input
                id="global-proxy"
                autoComplete="off"
                placeholder="socks5://user:pass@host:1080 또는 http://host:port（비워두세요=직접 연결）"
                value={globalProxyInput}
                disabled={!loaded || savingGlobalProxy}
                onChange={(e) => setGlobalProxyInput(e.target.value)}
              />
              <Button type="button" onClick={saveGlobalProxy} disabled={!loaded || savingGlobalProxy}>
                저장
              </Button>
            </div>
            <p className="text-muted-foreground text-xs">
              {globalProxyInput.trim() ? "구성됨 · 모든 대상 트래픽이 이 프록시를 통해 나갑니다." : "구성되지 않음 · 대상 트래픽은 아웃바운드 네트워크에 직접 연결됩니다."}
            </p>
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <ShieldAlertIcon className="size-4" />
              연산 제약 주입
            </CardTitle>
            <CardDescription>
              개봉 후，할 일을 하나씩 넣어라<b>운영상의 제약</b>（작업 개요에서「운영상의 제약」에서 유지됩니다. allow/deny 항목）해당 철자법 Agent
              에 대한 시스템 프롬프트，탐사 경계의 틀을 잡는 데 사용됩니다.（「현재 포트만 테스트」「폭파금지」）。
              <br />
              은 주입을 제어할 수 있습니다. <b>기획자（planner）</b>그리고 <b>집행관（worker）</b>
              ；둘 다 기본적으로 활성화되어 있습니다.。전환이 즉시 적용됩니다.（다음 읽기），재구축 필요 없음 Agent。닫은 후 Agent 제약 조건이 더 이상 표시되지 않습니다.。
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <div className="flex items-center justify-between gap-4">
              <Label htmlFor="inject-planner" className="text-sm font-normal text-muted-foreground">
                플래너 주입（planner）{injectPlanner ? " · 켜짐" : " · 폐쇄됨"}
              </Label>
              <Switch
                id="inject-planner"
                checked={injectPlanner}
                disabled={!loaded}
                onCheckedChange={toggleInjectPlanner}
              />
            </div>
            <div className="flex items-center justify-between gap-4">
              <Label htmlFor="inject-worker" className="text-sm font-normal text-muted-foreground">
                실행자 주입（worker）{injectWorker ? " · 켜짐" : " · 폐쇄됨"}
              </Label>
              <Switch
                id="inject-worker"
                checked={injectWorker}
                disabled={!loaded}
                onCheckedChange={toggleInjectWorker}
              />
            </div>
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <FlaskConicalIcon className="size-4" />
              실험적 기능
            </CardTitle>
            <CardDescription>
              메커니즘은 아직 검증 중입니다.，기본적으로 폐쇄됨。변경될 수 있음 Agent 동작이 안정성에 영향을 미칠 수 있음，영향을 이해한 후 활성화하십시오.。
              <br />
              <b>noa 컨텍스트 압축</b>：긴 대화 내용을 적극적으로 압축하는 모델（norma v0.4.0）。오픈 후 플랫폼 접속 4가지 유형 Agent（
              <b>기획자 / 집행관 / 스승님 Agent / 대화</b>）대신 사용하세요 noa 상황을 이어받아，내장된 압축을 대체합니다.，
              압축된 원본 텍스트는 쉽게 역추적할 수 있도록 작업 작업 디렉터리에 보관됩니다.。전환이 즉시 적용됩니다.（후속 실행에 적용됩니다.），재구축 필요 없음 Agent；
              종료 후 즉시 내장 압축 복원。
            </CardDescription>
          </CardHeader>
          <CardContent className="flex items-center justify-between gap-4">
            <Label htmlFor="noa-compaction" className="text-sm font-normal text-muted-foreground">
              noa 컨텍스트 압축{noaCompaction ? " · 켜짐" : " · 폐쇄됨"}
            </Label>
            <Switch
              id="noa-compaction"
              checked={noaCompaction}
              disabled={!loaded}
              onCheckedChange={toggleNoaCompaction}
            />
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <SearchIcon className="size-4" />
              인터넷 검색
            </CardTitle>
            <CardDescription>
              웹 검색입니다<b>마스터 스위치 + 소스 구성</b>。개봉 후，에서만<b>각 Agent 구성</b>활성화 여부를 개별적으로 선택하세요.
              <b>web_search</b>（제목만 반환/링크/요약，텍스트를 크롤링하지 마세요.；가져온 사람 WebFetch 담당）。인터넷 검색<b>떠나지 않음</b>
              녹음요원，트래픽 캡처와 무관。
              <br />
              소스 선택사항 <b>DuckDuckGo（ddgs）</b>（필요없어요 Key）、<b>Brave（무료 버전）</b>（작성 필요 Brave API Key）、{" "}
              <b>Tavily</b>（작성 필요 Tavily API Key）또는 <b>DeepSeek</b>（현재 재사용 LLM 구성）。메인 스위치가 꺼진 경우，각각
              Agent 의 네트워크 검색 스위치를 사용할 수 없습니다.。
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <div className="flex items-center justify-between gap-4">
              <Label htmlFor="web-search" className="text-sm font-normal text-muted-foreground">
                {webSearch ? "메인 스위치가 켜져 있습니다. · 다양한 형태로 사용 가능 Agent 구성에서 별도로 활성화" : "폐쇄됨 · 각각 Agent 웹 검색을 활성화할 수 없습니다."}
              </Label>
              <Switch
                id="web-search"
                checked={webSearch}
                disabled={!loaded || saving}
                onCheckedChange={(v) => {
                  setWebSearch(v); // optimistic
                  saveWebSearch({ web_search_enabled: v });
                }}
              />
            </div>

            {webSearch && (
              <div className="flex items-center justify-between gap-4">
                <Label className="text-sm font-normal text-muted-foreground">출처 검색</Label>
                <Select
                  value={backend}
                  disabled={!loaded || saving}
                  onValueChange={(v) => {
                    setBackend(v); // optimistic
                    saveWebSearch({ web_search_backend: v });
                  }}
                >
                  <SelectTrigger className="w-48 shrink-0">
                    <SelectValue placeholder="소스 선택" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="ddgs">DuckDuckGo（ddgs · 무료 없음 Key）</SelectItem>
                    <SelectItem value="brave-free">Brave（무료 버전 · 필수 Key）</SelectItem>
                    <SelectItem value="tavily">Tavily（필수 Key）</SelectItem>
                    <SelectItem value="deepseek">DeepSeek（공식）</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            )}

            {webSearch && backend === "deepseek" && (
              <div className="border-border/60 bg-muted/30 flex flex-col gap-2 rounded-md border p-3">
                <p className="text-sm font-medium">DeepSeek 공식 인터넷 검색</p>
                <p className="text-muted-foreground text-xs leading-relaxed">
                  이 소스는 직접 재사용됩니다.<b>현재 활성화 중 LLM 구성</b>。그렇죠
                  <b>만 지원됨 DeepSeek 공식모델</b>，그리고 이 구성<b>필수 anthropic 동의</b>
                  ——DeepSeek 님 OpenAI 프로토콜 끝점이 서버측 검색을 지원하지 않습니다.。스위치 LLM 이 소스는 구성 후에 유효하지 않게 될 수 있습니다.。
                </p>
                <p className="text-muted-foreground text-xs leading-relaxed">
                  다른 소스와 달리，검색 <b>DeepSeek 서버 실행</b>：각 검색은 추가 모델 호출을 소비합니다.（생성됨 Token
                  수수료），검색요청<b>위의 수출대리점을 통하지 않고</b>，도요<b>트래픽 추적에 포함되지 않음</b>；결과 반환<b>제목과 링크만</b>
                  （요약 없음），문자가 필요한 경우 WebFetch 잡아。
                </p>
                <p className="text-muted-foreground text-xs leading-relaxed">
                  위의 조건을 충족하는지 확인하는 것은 귀하의 몫입니다.，시스템이 차단하지 않습니다；아래에서 확인 가능「테스트 검색」실제로 확인을 위해 버튼이 한 번만 실행됩니다.。
                </p>
              </div>
            )}

            {webSearch && backend === "brave-free" && (
              <div className="flex flex-col gap-2">
                <Label htmlFor="brave-key" className="text-sm font-normal text-muted-foreground">
                  Brave Search API Key
                  {braveKeySet && <span className="ml-2 text-xs text-emerald-500">구성됨</span>}
                </Label>
                <div className="flex items-center gap-2">
                  <Input
                    id="brave-key"
                    type="password"
                    autoComplete="off"
                    placeholder={braveKeySet ? "구성됨（변경하지 않으려면 비워 두세요.）" : "입력 Brave API Key"}
                    value={braveKeyInput}
                    disabled={!loaded || savingKey}
                    onChange={(e) => setBraveKeyInput(e.target.value)}
                  />
                  <Button
                    type="button"
                    onClick={saveBraveKey}
                    disabled={!loaded || savingKey || braveKeyInput.trim() === ""}
                  >
                    저장
                  </Button>
                </div>
                {braveNeedsKey && (
                  <p className="text-xs text-amber-500">
                    선택됨 Brave 이지만 아직 구성되지 않았습니다. Key —— 저장 중 Key 전에，검색 도구가 활성화되지 않습니다.。
                  </p>
                )}
                <p className="text-muted-foreground text-xs">
                  무료 버전 제한은 대략 2,000 회/달。가세요 https://brave.com/search/api/ 받기 Key。
                </p>
              </div>
            )}

            {webSearch && backend === "tavily" && (
              <div className="flex flex-col gap-2">
                <Label htmlFor="tavily-key" className="text-sm font-normal text-muted-foreground">
                  Tavily Search API Key
                  {tavilyKeySet && <span className="ml-2 text-xs text-emerald-500">구성됨</span>}
                </Label>
                <div className="flex items-center gap-2">
                  <Input
                    id="tavily-key"
                    type="password"
                    autoComplete="off"
                    placeholder={tavilyKeySet ? "구성됨（변경하지 않으려면 비워 두세요.）" : "입력 Tavily API Key（tvly-…）"}
                    value={tavilyKeyInput}
                    disabled={!loaded || savingTavilyKey}
                    onChange={(e) => setTavilyKeyInput(e.target.value)}
                  />
                  <Button
                    type="button"
                    onClick={saveTavilyKey}
                    disabled={!loaded || savingTavilyKey || tavilyKeyInput.trim() === ""}
                  >
                    저장
                  </Button>
                </div>
                {webSearch && backend === "tavily" && !tavilyKeySet && (
                  <p className="text-xs text-amber-500">
                    선택됨 Tavily 이지만 아직 구성되지 않았습니다. Key —— 저장 중 Key 전에，검색 도구가 활성화되지 않습니다.。
                  </p>
                )}
                <p className="text-muted-foreground text-xs">가세요 https://tavily.com 등록하고 받기 API Key。</p>
              </div>
            )}

            {webSearch && (
              <div className="flex flex-col gap-2">
                <Label htmlFor="ws-proxy" className="text-sm font-normal text-muted-foreground">
                  수출대행（선택사항）
                </Label>
                <div className="flex items-center gap-2">
                  <Input
                    id="ws-proxy"
                    autoComplete="off"
                    placeholder="http://host:port 또는 socks5://host:port（비워두세요=직접 연결）"
                    value={proxyInput}
                    disabled={!loaded || savingProxy}
                    onChange={(e) => setProxyInput(e.target.value)}
                  />
                  <Button type="button" onClick={saveProxy} disabled={!loaded || savingProxy}>
                    저장
                  </Button>
                </div>
                <p className="text-muted-foreground text-xs">
                  독립 수출 대리점，검색 엔드포인트에 액세스하는 데에만 사용됩니다.（VPN/SOCKS 등）。트래픽이 기록되어 있음 MITM 상담원은 관련이 없습니다.；네트워크를 사용할 수 없을 때 이 프록시를 통해 액세스。
                </p>
              </div>
            )}

            {webSearch && (
              <div className="flex items-center justify-between gap-4 border-t pt-4">
                <p className="text-muted-foreground text-xs">
                  현재 구성 사용（출처 + 대리인 + Key）실제 검색 한번「test」，사용 가능 여부 확인。
                </p>
                <Button
                  type="button"
                  variant="outline"
                  onClick={runTest}
                  disabled={!loaded || testing}
                  className="shrink-0"
                >
                  {testing ? "테스트 중…" : "테스트 검색"}
                </Button>
              </div>
            )}
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <RadioTowerIcon className="size-4" />
              사용자 정의 스크립트 · Python 통역사
            </CardTitle>
            <CardDescription>
              맞춤형 <b>script</b> 문자 도구를 실행하는 데 사용합니다. Python。부팅시 자동으로 감지됩니다.（python3 우선순위）；자필로 작성 가능합니다 venv /
              특정 버전에 대한 절대 경로，공백으로 두면 런타임 시 자동으로 감지됩니다.。
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <div className="flex items-center gap-2">
              <Input
                className="font-mono text-sm"
                placeholder="/usr/bin/python3（비워두세요=자동 감지）"
                value={pyInterp}
                disabled={!loaded || saving}
                onChange={(e) => setPyInterp(e.target.value)}
              />
              <Button variant="outline" onClick={detectPython} disabled={!loaded || saving}>
                재확인
              </Button>
              <Button onClick={savePython} disabled={!loaded || saving}>
                저장
              </Button>
            </div>
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <CpuIcon className="size-4" />
              작업 동시성 · Work Agent 번호
            </CardTitle>
            <CardDescription>
              각 작업이 동시에 실행되는 작업 agent 수량（기본값 3）。값이 클수록 동시 탐지 수가 많아집니다.、소비량이 많을수록。수정 후
              <b>나중에 시작한 작업에 유효</b>，실행 중인 작업은 영향을 받지 않습니다.。
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <div className="flex items-center gap-2">
              <Input
                type="number"
                min={1}
                className="w-32 font-mono text-sm"
                placeholder="3"
                value={workers}
                disabled={!loaded || savingWorkers}
                onChange={(e) => setWorkers(e.target.value)}
              />
              <Button onClick={saveWorkers} disabled={!loaded || savingWorkers}>
                저장
              </Button>
            </div>
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <KeyboardIcon className="size-4" />
              세션 입력 상자에서 키 위치를 보냅니다.
            </CardTitle>
            <CardDescription>
              대화 페이지 및 작업 내용 마스터 Agent 세션 입력 상자는 이 설정을 공유합니다.，선택 후 즉시 적용、저장할 필요가 없습니다.。
              <br />
              이 기본 설정<b>이 브라우저에만 존재합니다.</b>，계정과 동기화되지 않습니다，브라우저 변경이나 사이트 데이터 삭제 후 재설정이 필요합니다.。
            </CardDescription>
          </CardHeader>
          <CardContent className="flex items-center justify-between gap-4">
            <Label htmlFor="chat-send-mode" className="text-sm font-normal text-muted-foreground">
              전송방법
            </Label>
            <Select value={sendMode} onValueChange={(v) => setChatSendMode(v as ChatSendMode)}>
              <SelectTrigger id="chat-send-mode" className="w-72">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {CHAT_SEND_MODE_OPTIONS.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
