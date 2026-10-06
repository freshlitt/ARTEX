"use client";

import * as React from "react";
import { toast } from "sonner";
import { EyeIcon, GitCompareIcon, InfoIcon, PencilIcon, RotateCcwIcon, SaveIcon, Trash2Icon, XIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Textarea } from "@/components/ui/textarea";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import { Switch } from "@/components/ui/switch";
import { Separator } from "@/components/ui/separator";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { Markdown } from "@/components/markdown";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";
import type { Agent, AgentDetail, AgentTrigger, MCPServer, PromptVar, PromptVersion, Settings, SkillItem, Tool } from "@/lib/types";

// Traffic tools are host tools gated by the global 트래픽 캡처 switch: bindable, but
// only usable when capture is on. Keep this list in sync with traffic.SeedToolMetas.
const TRAFFIC_TOOL_KEYS = new Set(["traffic_search", "traffic_get"]);

// AgentEditor is the tabbed editor for one agent, used inside the agents-page
// drawer (and reused full-page for deep links). Tabs: 구성 및 프롬프트 단어 / MCP / Skill /
// Tools. Config + prompt save as before; visibility + tool bindings toggle live.
export function AgentEditor({ agentKey, onSaved }: { agentKey: string; onSaved?: () => void }) {
  const [detail, setDetail] = React.useState<AgentDetail | null>(null);
  const [versions, setVersions] = React.useState<PromptVersion[]>([]);
  const [variables, setVariables] = React.useState<PromptVar[]>([]);
  const [mcp, setMcp] = React.useState<MCPServer[]>([]);
  const [skills, setSkills] = React.useState<SkillItem[]>([]);
  const [tools, setTools] = React.useState<Tool[]>([]);
  const [loaded, setLoaded] = React.useState(false);
  const [viewVer, setViewVer] = React.useState<PromptVersion | null>(null);
  const [diffVer, setDiffVer] = React.useState<PromptVersion | null>(null);

  const [prompt, setPrompt] = React.useState("");
  const [mcpVisible, setMcpVisible] = React.useState<number[]>([]);
  const [skillVisible, setSkillVisible] = React.useState<string[]>([]);
  const [preview, setPreview] = React.useState("");
  const [maxTurns, setMaxTurns] = React.useState("0");
  const [runSecs, setRunSecs] = React.useState("600");
  // "" = 팔로우(구속되지 않음)；그렇지 않으면 profile id 문자열
  const [llmProfileId, setLlmProfileId] = React.useState("");
  const [llmProfiles, setLlmProfiles] = React.useState<NonNullable<AgentDetail["llm_profiles"]>>([]);
  const [webSearch, setWebSearch] = React.useState(false);
  const [interactiveShell, setInteractiveShell] = React.useState(false);
  const [wrapup, setWrapup] = React.useState("");
  const [wrapupDefault, setWrapupDefault] = React.useState("");
  const [wrapupTurns, setWrapupTurns] = React.useState("0");
  const [wrapupTurnsDefault, setWrapupTurnsDefault] = React.useState(5);
  // 작업 수준 시간 초과 종료 단어(만 worker/planner)
  const [ttSupported, setTtSupported] = React.useState(false);
  const [ttWrapup, setTtWrapup] = React.useState("");
  const [ttWrapupDefault, setTtWrapupDefault] = React.useState("");
  const [ttTurns, setTtTurns] = React.useState("0");
  const [ttTurnsDefault, setTtTurnsDefault] = React.useState(5);
  const [settings, setSettings] = React.useState<Settings | null>(null);

  React.useEffect(() => {
    api.mcpServers().then(setMcp).catch(() => {});
    api.skills().then(setSkills).catch(() => {});
    api.tools().then(setTools).catch(() => {});
    api.settings().then(setSettings).catch(() => {});
  }, []);
  // global gates: traffic tools need 트래픽 캡처, web search needs the master switch.
  const captureOn = !!settings?.traffic_capture;
  const webSearchGlobalOn = !!settings?.web_search_enabled;

  const reload = React.useCallback(() => {
    api
      .getAgent(agentKey)
      .then((d) => {
        setDetail(d);
        setPrompt(d.prompt ?? "");
        setVariables(d.variables ?? []);
        setVersions(d.versions ?? []);
        setMcpVisible(d.visibility?.mcp ?? []);
        setSkillVisible(d.visibility?.skill ?? []);
        setMaxTurns(String(d.agent?.max_turns ?? 0));
        setRunSecs(String(d.agent?.run_seconds ?? 600));
        setLlmProfileId(d.agent?.llm_profile_id != null ? String(d.agent.llm_profile_id) : "");
        setLlmProfiles(d.llm_profiles ?? []);
        setWebSearch(!!d.agent?.web_search);
        setInteractiveShell(!!d.agent?.interactive_shell);
        setWrapup(d.wrapup_prompt ?? "");
        setWrapupDefault(d.wrapup_default ?? "");
        setWrapupTurns(String(d.wrapup_max_turns ?? 0));
        setWrapupTurnsDefault(d.wrapup_max_turns_default ?? 5);
        setTtSupported(!!d.task_timeout_wrapup_supported);
        setTtWrapup(d.task_timeout_wrapup_prompt ?? "");
        setTtWrapupDefault(d.task_timeout_wrapup_default ?? "");
        setTtTurns(String(d.task_timeout_wrapup_max_turns ?? 0));
        setTtTurnsDefault(d.task_timeout_wrapup_max_turns_default ?? 5);
      })
      .catch(() => setDetail(null))
      .finally(() => setLoaded(true));
  }, [agentKey]);
  React.useEffect(() => {
    reload();
  }, [reload]);

  async function doPreview() {
    try {
      const r = await api.previewAgentPrompt(agentKey, prompt);
      setPreview(r.error ? "렌더링 오류：" + r.error : r.rendered);
    } catch (e) {
      setPreview("미리보기 실패：" + (e as Error).message);
    }
  }
  async function savePrompt() {
    try {
      const r = await api.saveAgentPrompt(agentKey, prompt);
      toast.success(`버전으로 저장되었습니다. v${r.version}`);
      reload();
      onSaved?.();
    } catch (e) {
      toast.error("저장 실패：" + (e as Error).message);
    }
  }
  async function resetPrompt() {
    try {
      const r = await api.resetAgentPrompt(agentKey);
      toast.success(`내장된 기본값으로 복원되었습니다.（v${r.version}）`);
      reload();
    } catch (e) {
      toast.error("복구 실패：" + (e as Error).message);
    }
  }
  async function saveWrapup() {
    try {
      const turns = Math.max(0, Math.floor(Number(wrapupTurns) || 0));
      await api.saveAgentWrapup(agentKey, wrapup, turns);
      toast.success(wrapup.trim() || turns > 0 ? "마무리 구성이 저장되었습니다.（다음에 실행하면 적용됩니다.）" : "삭제됨，은 내장된 기본값을 사용합니다.");
      reload();
    } catch (e) {
      toast.error("저장 실패：" + (e as Error).message);
    }
  }
  async function resetWrapup() {
    try {
      await api.resetAgentWrapup(agentKey);
      toast.success("내장된 기본값으로 복원되었습니다.");
      reload();
    } catch (e) {
      toast.error("복구 실패：" + (e as Error).message);
    }
  }
  async function saveTaskTimeoutWrapup() {
    try {
      const turns = Math.max(0, Math.floor(Number(ttTurns) || 0));
      await api.saveAgentTaskTimeoutWrapup(agentKey, ttWrapup, turns);
      toast.success("작업 시간 초과 종료 구성이 저장되었습니다.（다음에 실행하면 적용됩니다.）");
      reload();
    } catch (e) {
      toast.error("저장 실패：" + (e as Error).message);
    }
  }
  async function resetTaskTimeoutWrapup() {
    try {
      await api.resetAgentTaskTimeoutWrapup(agentKey);
      toast.success("내장된 기본값으로 복원되었습니다.");
      reload();
    } catch (e) {
      toast.error("복구 실패：" + (e as Error).message);
    }
  }
  async function saveConfig() {
    try {
      // 이것만 제출하세요 agent 실제 표시된 필드，표시되지 않는 항목은 피하세요.( goals 님 max_turns)기본값으로 덮어쓰기。
      const patch: Parameters<typeof api.saveAgentConfig>[1] = {
        llm_profile_id: llmProfileId === "" ? null : Number(llmProfileId),
      };
      if (showConfig) {
        patch.max_turns = Math.max(0, Math.floor(Number(maxTurns) || 0));
        patch.run_seconds = Math.max(0, Math.floor(Number(runSecs) || 0));
      }
      if (showWebSearch) patch.web_search = webSearch;
      if (showInteractiveShell) patch.interactive_shell = interactiveShell;
      await api.saveAgentConfig(agentKey, patch);
      toast.success("실행 중인 구성이 저장되었습니다.（즉시 효력 발생）");
      reload();
    } catch (e) {
      toast.error("저장 실패：" + (e as Error).message);
    }
  }
  // applyVis optimistically updates, persists, and toasts success/failure. On
  // failure it reverts to the prior selection so the UI never lies about state.
  async function applyVis(nextMcp: number[], nextSkill: string[], okMsg: string) {
    const prevMcp = mcpVisible;
    const prevSkill = skillVisible;
    setMcpVisible(nextMcp);
    setSkillVisible(nextSkill);
    try {
      await api.setAgentVisibility(agentKey, nextMcp, nextSkill);
      toast.success(okMsg);
      onSaved?.(); // refresh the list so the card's MCP/Skill counts stay in sync
    } catch (e) {
      setMcpVisible(prevMcp);
      setSkillVisible(prevSkill);
      toast.error("저장 실패：" + (e as Error).message);
    }
  }
  function toggleMcp(id: number) {
    const on = mcpVisible.includes(id);
    const name = mcp.find((m) => m.id === id)?.name ?? String(id);
    applyVis(
      on ? mcpVisible.filter((x) => x !== id) : [...mcpVisible, id],
      skillVisible,
      `${on ? "취소됨" : "켜짐"} MCP「${name}」표시됨`,
    );
  }
  function toggleSkill(name: string) {
    const on = skillVisible.includes(name);
    applyVis(
      mcpVisible,
      on ? skillVisible.filter((x) => x !== name) : [...skillVisible, name],
      `${on ? "취소됨" : "켜짐"} Skill「${name}」표시됨`,
    );
  }
  async function toggleTool(t: Tool) {
    const on = t.agents.includes(agentKey);
    const nextAgents = on ? t.agents.filter((k) => k !== agentKey) : [...t.agents, agentKey];
    // optimistic update
    setTools((ts) => ts.map((x) => (x.key === t.key ? { ...x, agents: nextAgents } : x)));
    try {
      await api.saveTool(t.key, {
        description: t.description,
        schema: t.schema,
        agents: nextAgents,
        enabled: t.enabled,
      });
      toast.success(`${on ? "약속 안함" : "바운드"}도구「${t.key}」`);
      onSaved?.(); // refresh the list so the card's 도구 count stays in sync
    } catch (e) {
      toast.error("저장 도구 바인딩에 실패했습니다.：" + (e as Error).message);
      reload();
      api.tools().then(setTools).catch(() => {});
    }
  }

  if (loaded && !detail) {
    return <div className="text-muted-foreground p-6 text-center text-sm">찾을 수 없음 Agent：{agentKey}</div>;
  }
  // config is meaningless for the conversational main agent and the fixed-budget
  // goals decomposer; every other agent (workers, custom assistants) honors it.
  const showConfig = agentKey !== "mainagent" && agentKey !== "goals";
  // web search applies to every conversational/executing agent except the one-shot
  // goals decomposer; it's gated by the global master switch.
  const showWebSearch = agentKey !== "goals";
  // interactive shell (지속됨 PTY 대화 도구 제품군) 제외하고 동일 goals 외부 agent 열려있습니다;글로벌 게이팅 없음。
  const showInteractiveShell = agentKey !== "goals";
  // 각 agent(포함 goals/mainagent)다들 어디론가 달려가고 있어요 LLM 에,그래서「기본 모델」모두 바인딩 agent 열려있습니다。
  const showLLM = true;
  // triggers (P3) only attach to custom agents.
  const isCustom = !!detail && !detail.agent?.builtin;

  return (
    <Tabs defaultValue="prompt" className="flex min-h-0 flex-1 flex-col">
      <TabsList className="mx-4 mt-2 w-fit">
        <TabsTrigger value="prompt">구성 및 프롬프트 단어</TabsTrigger>
        <TabsTrigger value="wrapup">마무리 프롬프트 단어</TabsTrigger>
        <TabsTrigger value="mcp">MCP</TabsTrigger>
        <TabsTrigger value="skill">Skill</TabsTrigger>
        <TabsTrigger value="tools">Tools</TabsTrigger>
        {isCustom && <TabsTrigger value="triggers">트리거</TabsTrigger>}
      </TabsList>

      {/* 구성 + 프롬프트 단어 */}
      <TabsContent value="prompt" className="min-h-0 flex-1 overflow-y-auto px-4 pb-4">
        <div className="grid gap-4">
          {(showLLM || showConfig || showWebSearch || showInteractiveShell) && (
            <div className="grid gap-3 rounded-md border p-3">
              {showLLM && (
                <div className="grid gap-1.5">
                  <Label htmlFor="llm-profile" className="text-xs">기본 모델（LLM 구성）</Label>
                  <div className="flex flex-wrap items-center gap-3">
                    <Select value={llmProfileId || "__follow__"} onValueChange={(v) => setLlmProfileId(v === "__follow__" ? "" : v)}>
                      <SelectTrigger id="llm-profile" className="h-8 w-72">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="__follow__">미션을 따르세요 / 전역 활성화 구성</SelectItem>
                        {llmProfiles.map((p) => (
                          <SelectItem key={p.id} value={String(p.id)}>
                            {p.name}（{p.model}）{p.is_default ? " · 기본값" : ""}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    <span className="text-muted-foreground max-w-md text-xs">
                      그렇기 때문이죠 Agent 바인딩이 수정되었습니다. LLM 구성（점「구성 저장」유효）。우선순위：Agent 바인딩 &gt; 임무/세션 지정 &gt; 글로벌 활성화。
                    </span>
                  </div>
                </div>
              )}
              <div className="flex flex-wrap items-end gap-3">
                {showConfig && (
                  <>
                    <div className="grid gap-1.5">
                      <Label htmlFor="max-turns" className="text-xs">최대 사이클 수（0=제한 없음）</Label>
                      <Input id="max-turns" type="number" min={0} className="h-8 w-32"
                        value={maxTurns} onChange={(e) => setMaxTurns(e.target.value)} />
                    </div>
                    <div className="grid gap-1.5">
                      <Label htmlFor="run-seconds" className="text-xs">실행시간（초，0=제한 없음）</Label>
                      <Input id="run-seconds" type="number" min={0} className="h-8 w-32"
                        value={runSecs} onChange={(e) => setRunSecs(e.target.value)} />
                    </div>
                  </>
                )}
                <Button size="sm" variant="outline" onClick={saveConfig}>
                  <SaveIcon /> 구성 저장
                </Button>
              </div>
              {showWebSearch && (
                <div className="flex items-center gap-3 border-t pt-3">
                  <Switch
                    id="web-search"
                    checked={webSearch}
                    disabled={!webSearchGlobalOn}
                    onCheckedChange={setWebSearch}
                  />
                  <div className="grid gap-0.5">
                    <Label htmlFor="web-search" className="text-sm">인터넷 검색</Label>
                    <span className="text-muted-foreground text-xs">
                      {webSearchGlobalOn
                        ? "그렇기 때문이죠 Agent 개봉 후（위를 클릭하여 저장하고 적용하세요.），가능 web_search 인터넷 검색"
                        : "들어가야 해「시스템 구성」네트워크 검색 활성화 및 백엔드 구성，여기에서 활성화할 수 있습니다."}
                    </span>
                  </div>
                </div>
              )}
              {showInteractiveShell && (
                <div className="flex items-center gap-3 border-t pt-3">
                  <Switch
                    id="interactive-shell"
                    checked={interactiveShell}
                    onCheckedChange={setInteractiveShell}
                  />
                  <div className="grid gap-0.5">
                    <Label htmlFor="interactive-shell" className="text-sm">인터랙티브 Shell</Label>
                    <span className="text-muted-foreground text-xs">
                      그렇기 때문이죠 Agent 개봉 후（위를 클릭하여 저장하고 적용하세요.），사용 가능한 지속성 PTY 대화 도구（shell_open/send/read/close/list）운전사 msfconsole/ssh/REPL 및 기타 대화형 프로그램
                    </span>
                  </div>
                </div>
              )}
            </div>
          )}

          <div className="grid gap-2">
            <Label className="text-muted-foreground text-xs">가변적（자리표시자를 삽입하려면 클릭하세요.，렌더링 시 런타임 데이터로 대체됨）</Label>
            <div className="flex flex-wrap gap-2">
              {variables.map((v) => (
                <Tooltip key={v.name}>
                  <TooltipTrigger asChild>
                    <button type="button" onClick={() => setPrompt((p) => `${p}{{.${v.name}}}`)}
                      className="hover:bg-muted inline-flex items-center gap-1 rounded-md border bg-muted/40 px-2 py-1 font-mono text-xs">
                      {`{{.${v.name}}}`}
                      <Badge variant="secondary" className="px-1 py-0 text-[10px]">{v.source}</Badge>
                    </button>
                  </TooltipTrigger>
                  <TooltipContent className="max-w-xs">
                    <p className="font-medium">{v.description}</p>
                    <p className="text-muted-foreground mt-1">예：{v.example}</p>
                  </TooltipContent>
                </Tooltip>
              ))}
              {variables.length === 0 && <span className="text-muted-foreground text-xs">（변수 없음）</span>}
            </div>
          </div>

          <Textarea className="font-mono text-xs" rows={16} value={prompt}
            placeholder="공백으로 두면 내장된 기본 프롬프트 단어가 사용됩니다." onChange={(e) => setPrompt(e.target.value)} />

          <div className="flex flex-wrap gap-2">
            <Dialog>
              <DialogTrigger asChild>
                <Button variant="outline" size="sm" onClick={doPreview}>
                  <EyeIcon /> 렌더링 미리보기
                </Button>
              </DialogTrigger>
              <DialogContent className="sm:max-w-2xl">
                <DialogHeader>
                  <DialogTitle>렌더링 미리보기</DialogTitle>
                  <DialogDescription>모두 {`{{.Var}}`} 이 샘플 값으로 백엔드로 대체되었습니다.。</DialogDescription>
                </DialogHeader>
                <div className="max-h-[60vh] overflow-auto rounded-md border bg-muted/30 p-3">
                  <Markdown text={preview} />
                </div>
              </DialogContent>
            </Dialog>
            <Button size="sm" onClick={savePrompt}>
              <SaveIcon /> 새 버전으로 저장
            </Button>
            <Button variant="outline" size="sm" onClick={resetPrompt}>
              <RotateCcwIcon /> 기본값 복원
            </Button>
          </div>


          <Separator />
          <div className="grid gap-2">
            <Label className="text-muted-foreground text-xs">버전 기록</Label>
            <ul className="grid gap-1">
              {versions.map((ver, i) => (
                <li key={ver.version} className="flex items-center gap-2 rounded-md px-1 py-0.5 text-xs hover:bg-muted/50">
                  <span className="font-mono shrink-0">v{ver.version}</span>
                  {i === 0 && <Badge variant="secondary" className="px-1.5 py-0 shrink-0">현재</Badge>}
                  <span className="text-muted-foreground truncate flex-1">{ver.note}</span>
                  {ver.ts && (
                    <span className="text-muted-foreground/60 shrink-0 tabular-nums">
                      {new Date(ver.ts).toLocaleDateString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" })}
                    </span>
                  )}
                  <Button variant="ghost" size="icon-sm" className="size-6 shrink-0" onClick={() => setViewVer(ver)}>
                    <EyeIcon className="size-3" />
                  </Button>
                  {i > 0 && versions[0] && (
                    <Button variant="ghost" size="icon-sm" className="size-6 shrink-0" onClick={() => setDiffVer(ver)}>
                      <GitCompareIcon className="size-3" />
                    </Button>
                  )}
                </li>
              ))}
              {versions.length === 0 && (
                <li className="text-muted-foreground text-xs">（아직 저장된 버전이 없습니다.，내장된 기본값 사용）</li>
              )}
            </ul>
          </div>

          {/* 버전 보기 dialog */}
          <Dialog open={!!viewVer} onOpenChange={(o) => { if (!o) setViewVer(null); }}>
            <DialogContent className="sm:max-w-2xl">
              <DialogHeader>
                <DialogTitle>
                  v{viewVer?.version}
                  {viewVer?.version === versions[0]?.version && (
                    <Badge variant="secondary" className="ml-2 px-1.5 py-0 align-middle">현재</Badge>
                  )}
                </DialogTitle>
                <DialogDescription>
                  {viewVer?.note || "（코멘트 없음）"}
                  {viewVer?.ts && (
                    <span className="ml-2 text-muted-foreground/60">
                      {new Date(viewVer.ts).toLocaleString("zh-CN")}
                    </span>
                  )}
                </DialogDescription>
              </DialogHeader>
              <pre className="bg-muted max-h-[55vh] overflow-auto whitespace-pre-wrap rounded-md p-3 font-mono text-xs">
                {viewVer?.template_text || "（비어 있음）"}
              </pre>
              <div className="flex gap-2 justify-end">
                {viewVer && viewVer.version !== versions[0]?.version && (
                  <Button variant="outline" size="sm" onClick={() => {
                    if (viewVer) { setDiffVer(viewVer); setViewVer(null); }
                  }}>
                    <GitCompareIcon className="mr-1 size-3.5" /> 현재 버전과 비교
                  </Button>
                )}
                <Button size="sm" onClick={() => {
                  if (viewVer) { setPrompt(viewVer.template_text); setViewVer(null); toast.success(`로드됨 v${viewVer.version} 편집자로 가기，확인 후 클릭「새 버전으로 저장」`); }
                }}>
                  편집기에 로드
                </Button>
              </div>
            </DialogContent>
          </Dialog>

          {/* 버전 비교 dialog */}
          <Dialog open={!!diffVer} onOpenChange={(o) => { if (!o) setDiffVer(null); }}>
            <DialogContent className="sm:max-w-3xl">
              <DialogHeader>
                <DialogTitle>버전 비교：v{diffVer?.version} → v{versions[0]?.version}（현재）</DialogTitle>
                <DialogDescription>
                  <span className="inline-flex items-center gap-3 text-xs">
                    <span className="rounded bg-red-500/15 px-1.5 py-0.5 text-red-600 dark:text-red-400">- 삭제</span>
                    <span className="rounded bg-green-500/15 px-1.5 py-0.5 text-green-600 dark:text-green-400">+ 새로운</span>
                  </span>
                </DialogDescription>
              </DialogHeader>
              <DiffView oldText={diffVer?.template_text ?? ""} newText={versions[0]?.template_text ?? ""} />
            </DialogContent>
          </Dialog>
        </div>
      </TabsContent>

      {/* 마무리 프롬프트 단어 */}
      <TabsContent value="wrapup" className="min-h-0 flex-1 overflow-y-auto px-4 pb-4">
        <div className="grid gap-3">
          <p className="text-muted-foreground text-xs leading-relaxed">
            이럴 때 Agent 왜냐하면<b>시간 초과</b>또는<b>걸음이 지쳤습니다.</b>종료 시，시스템에서 이 문단을 삽입합니다.「마무리 프롬프트 단어」라운드를 마치세요：
            먼저 식별되었지만 라이브러리에 포함되지 않은 콘텐츠를 다시 작성합니다.，또 다른 요약 출력（배드엔딩은 피하세요）。내장된 기본값을 사용하려면 비워 두세요.。
          </p>
          <div className="flex items-center gap-2">
            <Label className="text-xs">종료 프롬프트 텍스트</Label>
            {wrapup.trim() ? (
              <Badge variant="secondary" className="px-1.5 py-0">맞춤형</Badge>
            ) : (
              <Badge variant="outline" className="px-1.5 py-0">내장된 기본값 사용</Badge>
            )}
          </div>
          <Textarea
            className="font-mono text-xs"
            rows={10}
            value={wrapup}
            placeholder={wrapupDefault || "공백으로 두면 내장된 기본 닫기 프롬프트가 사용됩니다."}
            onChange={(e) => setWrapup(e.target.value)}
          />
          <div className="grid gap-1.5">
            <Label htmlFor="wrapup-turns" className="text-xs">
              마감 라운드 수（마감 단계에서 최대 몇 라운드까지 실행할 수 있나요?；0=내장된 기본값 사용 {wrapupTurnsDefault} 휠）
            </Label>
            <Input id="wrapup-turns" type="number" min={0} className="h-8 w-32"
              value={wrapupTurns} onChange={(e) => setWrapupTurns(e.target.value)} />
          </div>
          <div className="flex gap-2">
            <Button size="sm" onClick={saveWrapup}>저장</Button>
            <Button size="sm" variant="outline" onClick={resetWrapup}>기본값 복원</Button>
          </div>
          {wrapupDefault && (
            <>
              <Separator />
              <div className="grid gap-1.5">
                <Label className="text-muted-foreground text-xs">기본 내장（읽기 전용，참고로）</Label>
                <pre className="text-muted-foreground max-h-40 overflow-y-auto rounded-md border bg-muted/30 p-2 text-xs whitespace-pre-wrap">
                  {wrapupDefault}
                </pre>
              </div>
            </>
          )}

          {ttSupported && (
            <>
              <Separator className="my-2" />
              <p className="text-muted-foreground text-xs leading-relaxed">
                <b>작업 시간 초과가 종료되었습니다.</b>（이상 per-run 결말은<b>2개 세트</b>）：언제<b>미션 전체</b>시간 초과 제한에 도달했습니다.、거의 끝부분에 주사를 놓는다。
                그리고 per-run 의미가 반대되는 경우가 많습니다.（ planner：per-run 님이 말했어요“계획을 멈추지 마세요”，작업 시간 초과가 발생했습니다.“그쯤에서 멈춰라、최종 결정하세요”）。내장된 기본값을 사용하려면 비워 두세요.。
              </p>
              <div className="flex items-center gap-2">
                <Label className="text-xs">작업 시간 초과 종료 프롬프트 텍스트 텍스트</Label>
                {ttWrapup.trim() ? (
                  <Badge variant="secondary" className="px-1.5 py-0">맞춤형</Badge>
                ) : (
                  <Badge variant="outline" className="px-1.5 py-0">내장된 기본값 사용</Badge>
                )}
              </div>
              <Textarea
                className="font-mono text-xs"
                rows={10}
                value={ttWrapup}
                placeholder={ttWrapupDefault || "공백으로 두면 내장된 기본 작업 시간 초과 종료 프롬프트 단어가 사용됩니다."}
                onChange={(e) => setTtWrapup(e.target.value)}
              />
              <div className="grid gap-1.5">
                <Label htmlFor="tt-turns" className="text-xs">
                  마감 라운드 수（0=내장된 기본값 사용 {ttTurnsDefault} 휠）
                </Label>
                <Input id="tt-turns" type="number" min={0} className="h-8 w-32"
                  value={ttTurns} onChange={(e) => setTtTurns(e.target.value)} />
              </div>
              <div className="flex gap-2">
                <Button size="sm" onClick={saveTaskTimeoutWrapup}>저장</Button>
                <Button size="sm" variant="outline" onClick={resetTaskTimeoutWrapup}>기본값 복원</Button>
              </div>
              {ttWrapupDefault && (
                <div className="grid gap-1.5">
                  <Label className="text-muted-foreground text-xs">기본 내장（읽기 전용，참고로）</Label>
                  <pre className="text-muted-foreground max-h-40 overflow-y-auto rounded-md border bg-muted/30 p-2 text-xs whitespace-pre-wrap">
                    {ttWrapupDefault}
                  </pre>
                </div>
              )}
            </>
          )}
        </div>
      </TabsContent>

      {/* MCP 가시성 */}
      <TabsContent value="mcp" className="min-h-0 flex-1 overflow-y-auto px-4 pb-4">
        <p className="text-muted-foreground mb-3 text-xs">이것을 확인하세요 Agent 표시됨 MCP 서버。</p>
        <div className="grid gap-2">
          {mcp.map((m) => (
            <label key={m.id} className="flex items-center gap-2 rounded-md border p-2 text-sm">
              <Checkbox checked={mcpVisible.includes(m.id)} onCheckedChange={() => toggleMcp(m.id)} />
              {m.name}
              <span className="text-muted-foreground ml-auto text-xs">{m.transport}</span>
            </label>
          ))}
          {mcp.length === 0 && <span className="text-muted-foreground text-xs">（아직 없음 MCP）</span>}
        </div>
      </TabsContent>

      {/* Skill 가시성 */}
      <TabsContent value="skill" className="min-h-0 flex-1 overflow-y-auto px-4 pb-4">
        <p className="text-muted-foreground mb-3 text-xs">이것을 확인하세요 Agent 표시됨 Skill。</p>
        <div className="grid gap-2">
          {skills.map((s) => (
            <label key={s.name} className="flex items-center gap-2 rounded-md border p-2 text-sm">
              <Checkbox checked={skillVisible.includes(s.name)} onCheckedChange={() => toggleSkill(s.name)} />
              <span className="font-mono text-xs">{s.name}</span>
              {s.description && <span className="text-muted-foreground ml-auto truncate text-xs">{s.description}</span>}
            </label>
          ))}
          {skills.length === 0 && <span className="text-muted-foreground text-xs">（아직 없음 Skill）</span>}
        </div>
      </TabsContent>

      {/* Tools 바인딩 */}
      <TabsContent value="tools" className="min-h-0 flex-1 overflow-y-auto px-4 pb-4">
        <p className="text-muted-foreground mb-3 text-xs">이것을 바인딩하려면 선택하세요. Agent 용 내장 도구。</p>
        <div className="grid gap-2">
          {tools.map((t) => {
            const isTraffic = TRAFFIC_TOOL_KEYS.has(t.key);
            const gated = isTraffic && !captureOn; // traffic tools need 트래픽 캡처 on
            return (
              <label
                key={t.key}
                className={cn(
                  "flex items-center gap-2 rounded-md border p-2 text-sm",
                  gated && "opacity-60",
                )}
              >
                <Checkbox
                  checked={t.agents.includes(agentKey)}
                  disabled={gated}
                  onCheckedChange={() => toggleTool(t)}
                />
                <span className="font-mono text-xs">{t.key}</span>
                {isTraffic && (
                  <Badge variant="secondary" className="px-1 py-0 text-[9px]">교통</Badge>
                )}
                {!t.enabled && (
                  <Badge variant="outline" className="text-destructive px-1 py-0 text-[9px]">비활성화됨</Badge>
                )}
                {gated ? (
                  <span className="text-muted-foreground ml-auto text-xs">트래픽 캡처를 활성화해야 합니다.</span>
                ) : (
                  t.description && (
                    <span className="text-muted-foreground ml-auto line-clamp-1 max-w-[55%] text-xs">
                      {t.description}
                    </span>
                  )
                )}
              </label>
            );
          })}
          {tools.length === 0 && <span className="text-muted-foreground text-xs">（아직 도구가 없습니다）</span>}
        </div>
      </TabsContent>

      {/* 트리거(P3, 사용자 정의 전용 agent) */}
      {isCustom && (
        <TabsContent value="triggers" className="min-h-0 flex-1 overflow-y-auto px-4 pb-4">
          <AgentTriggersTab agentKey={agentKey} agent={detail?.agent} />
        </TabsContent>
      )}
    </Tabs>
  );
}

// ---------- Diff helpers ----------

type DiffLine = { type: "same" | "add" | "del"; text: string };

function computeDiff(oldText: string, newText: string): DiffLine[] {
  const a = oldText.split("\n");
  const b = newText.split("\n");
  const m = a.length;
  const n = b.length;
  const dp: number[][] = Array.from({ length: m + 1 }, () => new Array(n + 1).fill(0));
  for (let i = 1; i <= m; i++)
    for (let j = 1; j <= n; j++)
      dp[i][j] = a[i - 1] === b[j - 1] ? dp[i - 1][j - 1] + 1 : Math.max(dp[i - 1][j], dp[i][j - 1]);
  const result: DiffLine[] = [];
  let i = m;
  let j = n;
  while (i > 0 || j > 0) {
    if (i > 0 && j > 0 && a[i - 1] === b[j - 1]) {
      result.unshift({ type: "same", text: a[i - 1] });
      i--;
      j--;
    } else if (j > 0 && (i === 0 || dp[i][j - 1] >= dp[i - 1][j])) {
      result.unshift({ type: "add", text: b[j - 1] });
      j--;
    } else {
      result.unshift({ type: "del", text: a[i - 1] });
      i--;
    }
  }
  return result;
}

function DiffView({ oldText, newText }: { oldText: string; newText: string }) {
  const lines = React.useMemo(() => computeDiff(oldText, newText), [oldText, newText]);
  return (
    <pre className="max-h-[60vh] overflow-auto rounded-md border bg-muted/30 p-2 font-mono text-xs leading-5">
      {lines.map((l, idx) => (
        <div
          key={idx}
          className={cn(
            "whitespace-pre-wrap px-1",
            l.type === "del" && "bg-red-500/15 text-red-700 dark:text-red-400",
            l.type === "add" && "bg-green-500/15 text-green-700 dark:text-green-400",
            l.type === "same" && "text-muted-foreground",
          )}
        >
          <span className="select-none mr-1 opacity-50">{l.type === "del" ? "-" : l.type === "add" ? "+" : " "}</span>
          {l.text}
        </div>
      ))}
    </pre>
  );
}

// AgentTriggersTab manages a custom agent's P3 triggers: list + add + delete.
// Each trigger fires (타이밍/찾음finding/목표 달성/작업 시간 초과/공구콜，복수 선택 가능) → a new conversation runs
// in parallel with the base user message + auto context appended by the backend.
function AgentTriggersTab({ agentKey, agent }: { agentKey: string; agent?: Agent }) {
  const [triggers, setTriggers] = React.useState<AgentTrigger[]>([]);
  const [tools, setTools] = React.useState<Tool[]>([]);
  // 트리거 후 처리 전략(매 agent);초기값은 다음과 같습니다. agent detail,변경 사항을 저장합니다.。
  const [runMode, setRunMode] = React.useState<"serial" | "parallel">(agent?.trigger_run_mode ?? "serial");
  const [mergeMode, setMergeMode] = React.useState<"by_task" | "all" | "none">(agent?.trigger_merge_mode ?? "all");
  const [maxParallel, setMaxParallel] = React.useState(String(agent?.trigger_max_parallel ?? 5));
  React.useEffect(() => {
    setRunMode(agent?.trigger_run_mode ?? "serial");
    setMergeMode(agent?.trigger_merge_mode ?? "all");
    setMaxParallel(String(agent?.trigger_max_parallel ?? 5));
  }, [agent?.trigger_run_mode, agent?.trigger_merge_mode, agent?.trigger_max_parallel]);

  async function saveBehavior(patch: {
    trigger_run_mode?: "serial" | "parallel";
    trigger_merge_mode?: "by_task" | "all" | "none";
    trigger_max_parallel?: number;
  }) {
    try {
      await api.saveAgentConfig(agentKey, patch);
    } catch (e) {
      toast.error("정책을 저장하지 못했습니다.：" + (e as Error).message);
    }
  }
  const [onInterval, setOnInterval] = React.useState(false);
  const [intervalSec, setIntervalSec] = React.useState("60");
  const [onFinding, setOnFinding] = React.useState(false);
  const [onGoalMet, setOnGoalMet] = React.useState(false);
  const [onTaskTimeout, setOnTaskTimeout] = React.useState(false);
  const [onToolCall, setOnToolCall] = React.useState(false);
  const [onTaskCreate, setOnTaskCreate] = React.useState(false);
  const [intervalMsg, setIntervalMsg] = React.useState("");
  const [findingMsg, setFindingMsg] = React.useState("");
  const [goalMsg, setGoalMsg] = React.useState("");
  const [taskTimeoutMsg, setTaskTimeoutMsg] = React.useState("");
  const [toolCallMsg, setToolCallMsg] = React.useState("");
  const [taskCreateMsg, setTaskCreateMsg] = React.useState("");
  const [toolNames, setToolNames] = React.useState<string[]>([]);
  const [saving, setSaving] = React.useState(false);
  // null = 새로운 모드；아니요 null = 님이 편집 중입니다. id 에 대한 트리거。
  const [editingId, setEditingId] = React.useState<number | null>(null);

  const reload = React.useCallback(() => {
    api.agentTriggers(agentKey).then(setTriggers).catch(() => setTriggers([]));
  }, [agentKey]);
  React.useEffect(() => {
    reload();
  }, [reload]);
  React.useEffect(() => {
    api.tools().then(setTools).catch(() => setTools([]));
  }, []);

  function toggleTool(key: string) {
    setToolNames((prev) => (prev.includes(key) ? prev.filter((k) => k !== key) : [...prev, key]));
  }

  // resetForm 양식을 지우고 다음으로 돌아갑니다.「새로운」모드。
  function resetForm() {
    setEditingId(null);
    setOnInterval(false);
    setIntervalSec("60");
    setOnFinding(false);
    setOnGoalMet(false);
    setOnTaskTimeout(false);
    setOnToolCall(false);
    setOnTaskCreate(false);
    setIntervalMsg("");
    setFindingMsg("");
    setGoalMsg("");
    setTaskTimeoutMsg("");
    setToolCallMsg("");
    setTaskCreateMsg("");
    setToolNames([]);
  }

  // startEdit 기존 트리거를 양식에 입력합니다.,들어가세요「편집」모드。
  function startEdit(t: AgentTrigger) {
    setEditingId(t.id);
    setOnInterval(t.interval_sec > 0);
    setIntervalSec(t.interval_sec > 0 ? String(t.interval_sec) : "60");
    setOnFinding(t.on_finding);
    setOnGoalMet(t.on_goal_met);
    setOnTaskTimeout(t.on_task_timeout);
    setOnToolCall(t.on_tool_call);
    setOnTaskCreate(t.on_task_create);
    setIntervalMsg(t.interval_message);
    setFindingMsg(t.finding_message);
    setGoalMsg(t.goal_message);
    setTaskTimeoutMsg(t.task_timeout_message);
    setToolCallMsg(t.tool_call_message);
    setTaskCreateMsg(t.task_create_message);
    setToolNames(t.tool_names ?? []);
  }

  // submit 에 따르면 editingId 가자「새로운」또는「변경사항 저장」;편집하는 동안 이 트리거를 활성화된 상태로 유지하세요.。
  async function submit() {
    const n = onInterval ? Math.max(1, Math.floor(Number(intervalSec) || 0)) : 0;
    if (n === 0 && !onFinding && !onGoalMet && !onTaskTimeout && !onToolCall && !onTaskCreate) {
      toast.error("트리거 조건을 하나 이상 선택하세요.");
      return;
    }
    if (onToolCall && toolNames.length === 0) {
      toast.error("도구 호출 트리거는 하나 이상의 도구를 선택합니다.");
      return;
    }
    const body = {
      interval_sec: n,
      on_finding: onFinding,
      on_goal_met: onGoalMet,
      on_task_timeout: onTaskTimeout,
      on_tool_call: onToolCall,
      on_task_create: onTaskCreate,
      interval_message: intervalMsg.trim(),
      finding_message: findingMsg.trim(),
      goal_message: goalMsg.trim(),
      task_timeout_message: taskTimeoutMsg.trim(),
      tool_call_message: toolCallMsg.trim(),
      task_create_message: taskCreateMsg.trim(),
      tool_names: onToolCall ? toolNames : [],
    };
    setSaving(true);
    try {
      if (editingId != null) {
        const cur = triggers.find((x) => x.id === editingId);
        await api.updateTrigger(editingId, { ...body, enabled: cur?.enabled ?? true });
        toast.success("변경사항이 저장되었습니다.");
      } else {
        await api.createTrigger(agentKey, { ...body, enabled: true });
        toast.success("트리거가 추가되었습니다.");
      }
      resetForm();
      reload();
    } catch (e) {
      toast.error((editingId != null ? "저장 실패：" : "추가 실패：") + (e as Error).message);
    } finally {
      setSaving(false);
    }
  }
  async function toggleEnabled(t: AgentTrigger) {
    try {
      await api.updateTrigger(t.id, {
        enabled: !t.enabled,
        interval_sec: t.interval_sec,
        on_finding: t.on_finding,
        on_goal_met: t.on_goal_met,
        on_task_timeout: t.on_task_timeout,
        on_tool_call: t.on_tool_call,
        on_task_create: t.on_task_create,
        interval_message: t.interval_message,
        finding_message: t.finding_message,
        goal_message: t.goal_message,
        task_timeout_message: t.task_timeout_message,
        tool_call_message: t.tool_call_message,
        task_create_message: t.task_create_message,
        tool_names: t.tool_names,
      });
      reload();
    } catch (e) {
      toast.error("저장 실패：" + (e as Error).message);
    }
  }
  async function del(id: number) {
    try {
      await api.deleteTrigger(id);
      if (editingId === id) resetForm();
      reload();
    } catch (e) {
      toast.error("삭제 실패：" + (e as Error).message);
    }
  }

  function condLabel(t: AgentTrigger): string {
    const parts: string[] = [];
    if (t.interval_sec > 0) parts.push(`매 ${t.interval_sec}s`);
    if (t.on_finding) parts.push("찾음 finding");
    if (t.on_goal_met) parts.push("목표 달성");
    if (t.on_task_timeout) parts.push("작업 시간 초과");
    if (t.on_tool_call) parts.push(`공구콜(${t.tool_names.length})`);
    if (t.on_task_create) parts.push("태스크 생성");
    return parts.join(" · ") || "（무조건）";
  }

  return (
    <div className="grid gap-4">
      <p className="text-muted-foreground text-xs">
        트리거가 이를 사용자 정의합니다. Agent 자동 실행：발동될 때마다<b>실행할 새 세션을 만듭니다.</b>（에「대화」페이지가 표시됩니다.）。
        다중 선택 트리거 조건을 사용할 수 있습니다.；시스템은「이번에는 왜 발동됐나요? + 관련 업무/finding/대상」이 자동으로 추가됩니다.。
      </p>

      {/* 트리거 후 처리 전략 */}
      <div className="grid gap-3 rounded-md border p-3">
        <Label className="text-muted-foreground text-xs">트리거 후 처리 전략（트리거를 대기열에 추가하는 방법 결정/복합운전）</Label>
        <div className="flex flex-wrap items-center gap-4">
          <div className="grid gap-1">
            <Label className="text-xs">작동 모드</Label>
            <Select
              value={runMode}
              onValueChange={(v) => {
                const rm = v as "serial" | "parallel";
                setRunMode(rm);
                saveBehavior({ trigger_run_mode: rm });
              }}
            >
              <SelectTrigger size="sm" className="h-8 w-40">
                <SelectValue />
              </SelectTrigger>
              <SelectContent position="popper">
                <SelectItem value="serial">시리얼（대기중，하나씩）</SelectItem>
                <SelectItem value="parallel">병렬（각 동시 세션）</SelectItem>
              </SelectContent>
            </Select>
          </div>

          <div className="grid gap-1">
            <Label className="text-xs">병합 모드</Label>
            <Select
              value={mergeMode}
              disabled={runMode === "parallel"}
              onValueChange={(v) => {
                const mm = v as "by_task" | "all" | "none";
                setMergeMode(mm);
                saveBehavior({ trigger_merge_mode: mm });
              }}
            >
              <SelectTrigger size="sm" className="h-8 w-44">
                <SelectValue />
              </SelectTrigger>
              <SelectContent position="popper">
                <SelectItem value="by_task">작업별 병합</SelectItem>
                <SelectItem value="all">모두 하나로 병합</SelectItem>
                <SelectItem value="none">병합되지 않음</SelectItem>
              </SelectContent>
            </Select>
          </div>

          {runMode === "parallel" && (
            <div className="grid gap-1">
              <Label htmlFor="tr-maxpar" className="text-xs">최대 동시성（0=제한 없음）</Label>
              <Input
                id="tr-maxpar"
                type="number"
                min={0}
                className="h-8 w-28"
                value={maxParallel}
                onChange={(e) => setMaxParallel(e.target.value)}
                onBlur={() => {
                  const n = Math.max(0, Math.floor(Number(maxParallel) || 0));
                  setMaxParallel(String(n));
                  saveBehavior({ trigger_max_parallel: n });
                }}
              />
            </div>
          )}
        </div>
        <p className="text-muted-foreground text-xs">
          {runMode === "parallel"
            ? "병렬：트리거될 때마다 세션이 즉시 열리고 동시에 실행됩니다.，병합되지 않음；최대 동시성을 초과하면 대기 및 공석 대기가 트리거됩니다.。"
            : mergeMode === "by_task"
              ? "시리얼·작업별 병합：마찬가지예요 agent 하나씩 실행；대기열에 있는 동일한 작업의 이벤트 트리거가 하나의 세션으로 병합됩니다.。"
              : mergeMode === "all"
                ? "시리얼·모두 병합：마찬가지예요 agent 하나씩 실행；대기열을 가져올 때 현재 대기열에 있는 모든 트리거를 하나의 세션으로 병합합니다.。"
                : "시리얼·병합되지 않음：마찬가지예요 agent 하나씩 실행；각 트리거에는 자체 세션이 있습니다.。"}
        </p>
      </div>

      {/* 새로운 / 트리거 편집 */}
      <div className="grid gap-3 rounded-md border p-3">
        <Label className="text-muted-foreground text-xs">
          {editingId != null
            ? `트리거 편집 #${editingId}（변경 완료「변경사항 저장」）`
            : "새 트리거 추가（각 조건은 자체 사용자 메시지를 채울 수 있습니다.）"}
        </Label>

        {/* 타이밍 */}
        <div className="grid gap-1.5">
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={onInterval} onCheckedChange={(v) => setOnInterval(!!v)} /> 타이밍 트리거
          </label>
          {onInterval && (
            <div className="grid gap-1.5">
              <div className="flex items-center gap-2">
                <Label htmlFor="tr-interval" className="text-xs">매</Label>
                <Input id="tr-interval" type="number" min={1} className="h-8 w-24"
                  value={intervalSec} onChange={(e) => setIntervalSec(e.target.value)} />
                <span className="text-muted-foreground text-xs">초</span>
              </div>
              <Textarea className="text-xs" rows={2} value={intervalMsg}
                placeholder="예약된 트리거 시간에 전송됨 agent 말，：모든 업무를 점검한다" onChange={(e) => setIntervalMsg(e.target.value)} />
            </div>
          )}
        </div>

        {/* finding */}
        <div className="grid gap-1.5">
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={onFinding} onCheckedChange={(v) => setOnFinding(!!v)} /> 찾음 finding 일 때 트리거됩니다.
          </label>
          {onFinding && (
            <Textarea className="text-xs" rows={2} value={findingMsg}
              placeholder="찾음 finding 에게 보냄 agent 말（시스템에는 작업과 finding 세부사항）" onChange={(e) => setFindingMsg(e.target.value)} />
          )}
        </div>

        {/* 목표 달성 */}
        <div className="grid gap-1.5">
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={onGoalMet} onCheckedChange={(v) => setOnGoalMet(!!v)} /> 목표 달성 시 발동
          </label>
          {onGoalMet && (
            <Textarea className="text-xs" rows={2} value={goalMsg}
              placeholder="목표 달성 시 발행 agent 말（시스템에는 달성해야 할 작업과 목표가 제공됩니다.）" onChange={(e) => setGoalMsg(e.target.value)} />
          )}
        </div>

        {/* 작업 시간 초과 */}
        <div className="grid gap-1.5">
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={onTaskTimeout} onCheckedChange={(v) => setOnTaskTimeout(!!v)} /> 작업 시간이 초과되면 트리거됩니다.
          </label>
          {onTaskTimeout && (
            <Textarea className="text-xs" rows={2} value={taskTimeoutMsg}
              placeholder="작업 시간이 초과되면 전송됩니다. agent 말（시스템이 작업 번호와 대상을 첨부합니다.）" onChange={(e) => setTaskTimeoutMsg(e.target.value)} />
          )}
        </div>

        {/* 공구콜 */}
        <div className="grid gap-1.5">
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={onToolCall} onCheckedChange={(v) => setOnToolCall(!!v)} /> 도구가 호출될 때 트리거됩니다.
          </label>
          {onToolCall && (
            <div className="grid gap-1.5">
              <div className="text-muted-foreground text-xs">
                모니터링할 도구를 선택하세요.（적어도 하나는）；작업 실행 중에 이러한 도구를 사용할 때마다<b>통화 완료</b>이 실행됩니다。선택됨 {toolNames.length} 。
              </div>
              <div className="max-h-40 overflow-y-auto rounded-md border p-2">
                {tools.length === 0 && <span className="text-muted-foreground text-xs">（도구 목록이 비어 있습니다.）</span>}
                <div className="grid gap-1">
                  {tools.map((tool) => (
                    <label key={tool.key} className="flex items-start gap-2 text-xs">
                      <Checkbox className="mt-0.5" checked={toolNames.includes(tool.key)}
                        onCheckedChange={() => toggleTool(tool.key)} />
                      <span className="min-w-0">
                        <span className="font-medium">{tool.key}</span>
                        {tool.description && <span className="text-muted-foreground line-clamp-1"> {tool.description}</span>}
                      </span>
                    </label>
                  ))}
                </div>
              </div>
              <Textarea className="text-xs" rows={2} value={toolCallMsg}
                placeholder="도구가 호출될 때 전송됩니다. agent 말（시스템이 작업 정보를 첨부합니다.、도구 항목이 콘텐츠를 반환합니다.）" onChange={(e) => setToolCallMsg(e.target.value)} />
            </div>
          )}
        </div>

        {/* 태스크 생성 */}
        <div className="grid gap-1.5">
          <label className="flex items-center gap-2 text-sm">
            <Checkbox checked={onTaskCreate} onCheckedChange={(v) => setOnTaskCreate(!!v)} /> 작업이 생성될 때 트리거됩니다.
          </label>
          {onTaskCreate && (
            <Textarea className="text-xs" rows={2} value={taskCreateMsg}
              placeholder="작업이 생성될 때 전송됩니다. agent 말（시스템이 작업 번호와 대상을 첨부합니다.）" onChange={(e) => setTaskCreateMsg(e.target.value)} />
          )}
        </div>

        <div className="flex items-center gap-2">
          <Button size="sm" onClick={submit} disabled={saving}>
            <SaveIcon /> {editingId != null ? "변경사항 저장" : "트리거 추가"}
          </Button>
          {editingId != null && (
            <Button size="sm" variant="ghost" onClick={resetForm} disabled={saving}>
              <XIcon /> 편집 취소
            </Button>
          )}
        </div>
      </div>

      {/* 이미 트리거가 있습니다. */}
      <div className="grid gap-2">
        <Label className="text-muted-foreground text-xs">이미 트리거가 있습니다.</Label>
        {triggers.length === 0 && <span className="text-muted-foreground text-xs">（아직 없음）</span>}
        {triggers.map((t) => (
          <div
            key={t.id}
            className={cn(
              "flex items-start gap-2 rounded-md border p-2 text-sm",
              editingId === t.id && "border-primary bg-primary/5",
            )}
          >
            <Switch checked={t.enabled} onCheckedChange={() => toggleEnabled(t)} className="mt-0.5" />
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-1.5">
                <span className="font-medium">{condLabel(t)}</span>
                {!t.enabled && (
                  <Badge variant="outline" className="text-destructive px-1 py-0 text-[9px]">비활성화됨</Badge>
                )}
              </div>
              <div className="text-muted-foreground grid gap-0.5 text-xs">
                {t.interval_sec > 0 && t.interval_message && <div className="line-clamp-1">타이밍：{t.interval_message}</div>}
                {t.on_finding && t.finding_message && <div className="line-clamp-1">finding：{t.finding_message}</div>}
                {t.on_goal_met && t.goal_message && <div className="line-clamp-1">대상：{t.goal_message}</div>}
                {t.on_task_timeout && t.task_timeout_message && <div className="line-clamp-1">시간 초과：{t.task_timeout_message}</div>}
                {t.on_task_create && t.task_create_message && <div className="line-clamp-1">태스크 생성：{t.task_create_message}</div>}
                {t.on_tool_call && (
                  <>
                    <div className="line-clamp-1">도구：{t.tool_names.join("、") || "（선택되지 않음）"}</div>
                    {t.tool_call_message && <div className="line-clamp-1">메시지：{t.tool_call_message}</div>}
                  </>
                )}
              </div>
            </div>
            <Button variant="ghost" size="icon-sm" className="text-muted-foreground hover:text-foreground"
              onClick={() => startEdit(t)} title="편집">
              <PencilIcon className="size-3.5" />
            </Button>
            <Button variant="ghost" size="icon-sm" className="text-muted-foreground hover:text-destructive"
              onClick={() => del(t.id)} title="삭제">
              <Trash2Icon className="size-3.5" />
            </Button>
          </div>
        ))}
      </div>
    </div>
  );
}
