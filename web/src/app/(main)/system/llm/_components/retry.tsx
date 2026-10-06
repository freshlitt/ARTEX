"use client";

// LLM 구성된 공유 재시도：5개 레이어는 각각의 레이어를 다시 시도합니다.「회 + 간격」。
//
// 내부에서 외부까지 5층：지안롄(SDK) → 빈 응답(SDK) → 마찬가지예요 provider 보안창 → 폴링 회로 차단기 → 다시 뛰고 싶은 마음。
// 처음 세 레이어는 끝점을 따릅니다.，따라서 각 모델 구성은 전역 기본값을 재정의할 수 있습니다.；마지막 두 레이어는 프로세스 수준입니다.，전역 복사본이 하나만 있습니다.。
//
// 모든 입력은 동일한 세트를 따릅니다.「비워두세요 = 구성되지 않음」의미，및 백엔드 db.RetryRule 일관됨：
//   회   비어 있음/0 = 내장된 기본값 사용 | -1 = 이 레이어를 닫고 다시 시도해 보세요. | >0 = 이 횟수만큼 사용하세요
//   간격   비어 있음/0 = 이 레이어의 원래 인덱스를 사용하여 후퇴합니다. | >0 = 대신 이 고정된 밀리초 간격을 사용하세요.

import * as React from "react";

import { Loader2Icon, SaveIcon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import type { LLMRetryOverride, LLMRetryPolicy, LLMRetryRule } from "@/lib/types";

export const ZERO_RULE: LLMRetryRule = { attempts: 0, interval_ms: 0 };
export const ZERO_OVERRIDE: LLMRetryOverride = {
  connect: ZERO_RULE,
  empty: ZERO_RULE,
  stream: ZERO_RULE,
};
const ZERO_POLICY: LLMRetryPolicy = {
  ...ZERO_OVERRIDE,
  breaker: ZERO_RULE,
  intent: ZERO_RULE,
};

type LayerMeta = {
  title: string;
  /** 이 수준의 재시도는 어디에서 발생합니까?、누가 집행할 것인가? */
  where: string;
  /** 어떤 종류의 오류가 이 수준으로 이어지나요?——상태 코드에만 해당，사람들이 추측하게 하지 마세요 */
  trigger: string;
  /** 비슷해 보이지만【아니요】이 층 발이 잘못됐네요，작성하지 마시고, 답변이 없으면 사실이라고 생각하세요. bug */
  skips?: string;
  desc: string;
  attemptsLabel: string;
  /** 횟수를 비워 놓았을 때의 기본값， */
  defAttempts: number;
  /** 간격이 비어 있는 경우의 기본 정책， */
  defInterval: string;
  /** 횟수를 채워주세요 -1 의미 */
  offHint: string;
};

export const RETRY_LAYERS = {
  connect: {
    title: "연결 다시 시도",
    where: "SDK · 알겠습니다 200 전에",
    trigger:
      "연결이 안되거나 아직 받지 못했어요 200：연결 재설정 / 읽기 및 쓰기 시간 초과 / DNS 실패 및 기타 네트워크 계층 오류，그리고 HTTP 408、429、500、502、503、504。",
    skips: "기타 상태 코드（400 / 401 / 403 / 404 / 413 / 422 등）모두 결정적 거부입니다.，재전송도 실패합니다.，직접 던져보세요。",
    desc: "같은 요청을 그대로 다시 보냅니다.。흐름이 시작되면（이미 받았어요 200），도중에 연결이 끊어지면 이 레이어에서 관리하지 않습니다.。",
    attemptsLabel: "재시도 횟수",
    defAttempts: 3,
    defInterval: "0.5s→1s→2s 색인（캡 8s）",
    offHint: "-1 = 다시는 시도하지 마세요，실패하면 바로 토해내라",
  },
  empty: {
    title: "빈 응답 재시도",
    where: "SDK · 만 openai 형식",
    trigger:
      "HTTP 200、finish_reason 정상입니다 stop，하지만 전체 응답에 단일 콘텐츠 블록이 포함되어 있지 않습니다.——게이트웨이 빈 프레임、사고 영역의 프레임 손실、샘플과 딸꾹질은 다음과 같습니다。",
    skips: "왜냐하면 max_tokens 내용이 없는 잘림은 계산되지 않습니다.（그건 출력 상한을 높여서 해결해야죠，다시 보내면 충돌만 발생합니다）。",
    desc: "전체 재전송 prompt，긴 맥락에서는 더 비쌉니다.，너무 많이 주는 건 적절하지 않아요。",
    attemptsLabel: "재시도 횟수",
    defAttempts: 2,
    defInterval: "0.5s→1s→2s 색인（캡 8s）",
    offHint: "-1 = 빈 응답을 그대로 직접 건네줍니다.",
  },
  stream: {
    title: "마찬가지예요 provider 안전창 재시도",
    where: "이 프로젝트 · 출력물이 전달되기 전",
    trigger:
      "스트림이 설정되었습니다.（알겠습니다 200）그런데 뭔가 문제가 생겼어요：연결이 도중에 끊어졌습니다、공급업체 overloaded、스트림 내에서 429 / 5xx 오류 이벤트——그리고 하나 token 아직 발신자에게 전달되지 않았습니다.。",
    skips:
      "할당량 소진（402 / insufficient_quota，구성 변경은 폴링에 맡기세요.）、문맥이 너무 깁니다.（413 / context length，압축에 맡겨두세요）、400 / 401 / 403 / 404 / 422 확실한 거절，다시 시도하지 마세요。",
    desc: "동일한 구성에서 동일한 요청을 재생합니다.。아직 출력물이 전달되지 않았기 때문에，재생은 모델 출력이나 도구 실행을 복제하지 않습니다.。",
    attemptsLabel: "재시도 횟수",
    defAttempts: 2,
    defInterval: "0.5s→1s 색인（캡 4s）",
    offHint: "-1 = 흐름을 끊고 재실행을 위해 외부 레이어에 직접 넘겨줍니다.",
  },
  breaker: {
    title: "폴링 회로 차단기",
    where: "이 프로젝트 · 프로세스 수준，글로벌 카피",
    trigger:
      "순간적인 실패（429、5xx、네트워크 오류）연속 누적이 임계값에 도달하면 퓨즈가 끊어집니다.；잔고가 부족해요（402）、키가 잘못되었습니다.（401 / 403）、모델이 존재하지 않습니다.（404）이러한 유형의 결정적 실패는 임계값을 확인하지 않습니다.，퓨즈가 처음으로 끊어졌습니다.。",
    skips: "성공하면 클리어됨，그래서 가끔 초안이 있는 구성은 융합될 때까지 천천히 구축되지 않습니다.。",
    desc: "퓨즈가 끊어진 후 냉각에 들어갑니다.，냉각 기간 동안 폴링에서는 이 구성을 직접 건너뜁니다.。상태가 해제되었습니다.，다시 시작해도 손실이 없습니다.。",
    attemptsLabel: "회로 퓨즈가 여러 번 연속 고장났습니다.",
    defAttempts: 3,
    defInterval: "1min→5min→30min 그라데이션",
    offHint: "-1 = 순간적인 실패는 절대로 융합되지 않습니다.（결정적 실패가 여전히 발생합니다.）",
  },
  intent: {
    title: "다시 뛰고 싶은 마음",
    where: "이 프로젝트 · 프로세스 수준，글로벌 카피",
    trigger:
      "처음 몇 레이어는 덮이지 않습니다.：worker 에게 model_error 종료——모든 내부 재시도가 소진되었습니다.，또는 출력 전달을 시작한 후 스트림 연결이 끊어집니다.（그 당시에는 다시 플레이하는 것이 안전하지 않았습니다.，다시 시작할 수 밖에 없어）。",
    skips: "폴링 구성을 통해 할당량 소진이 처리되었습니다.，여기서 재방송하지 마세요；작업이 일시 중지되었습니다. / 종료 / 피날레 진입하자마자 양보하세요，백오프 시간을 차지하지 않음。",
    desc: "전체 의도를 처음부터 다시 실행해 보세요.。가장 바깥쪽 레이어입니다，재실행은 내부 여러 레이어의 시간이 다시 배가된다는 의미입니다.。",
    attemptsLabel: "재방송 횟수",
    defAttempts: 2,
    defInterval: "고정됨 3s",
    offHint: "-1 = 재방송 없음，의도가 직접적으로 판단됩니다. blocked",
  },
} satisfies Record<string, LayerMeta>;

type LayerKey = keyof typeof RETRY_LAYERS;

/** 밀리초 인간의 말，은 입력 상자 옆에 에코하는 데에만 사용됩니다.，0을 세지 않도록。 */
function humanMs(ms: number) {
  if (!Number.isFinite(ms) || ms <= 0) return "";
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60_000) return `${Number((ms / 1000).toFixed(2))}s`;
  return `${Number((ms / 60_000).toFixed(2))}min`;
}

/** 제어되는 디지털 입력：빈 문자열 ↔ 0，중간상태（"-"、"1e"）현지 그대로 유지하세요，부모님을 방해하지 마세요。 */
function NumField({
  id,
  value,
  onChange,
  placeholder,
  min,
}: {
  id: string;
  value: number;
  onChange: (n: number) => void;
  placeholder: string;
  min: number;
}) {
  const [text, setText] = React.useState(value === 0 ? "" : String(value));
  // 상위 항목이 전체 값 집합을 변경했습니다.（정책 읽기、스위치 구성）계속 따라오세요；입력해도 여기로 안 옵니다.，
  // 그렇다면 value 은(는) 이미 로컬 텍스트와 같습니다. parse 이후의 결과。
  React.useEffect(() => {
    const incoming = value === 0 ? "" : String(value);
    setText((cur) => (Number(cur || 0) === value ? cur : incoming));
  }, [value]);
  return (
    <Input
      id={id}
      type="number"
      min={min}
      className="w-28 shrink-0"
      value={text}
      placeholder={placeholder}
      onChange={(e) => {
        setText(e.target.value);
        const n = Number(e.target.value);
        onChange(e.target.value.trim() === "" || !Number.isFinite(n) ? 0 : Math.trunc(n));
      }}
    />
  );
}

/** 한 수준에서 재시도를 위한 손잡이 2개。idPrefix 같은 페이지가 여러 번 나타날 때 저장하기 위해 사용됩니다. label 님 htmlFor。 */
export function RetryRuleFields({
  layer,
  idPrefix,
  value,
  onChange,
  compact,
}: {
  layer: LayerKey;
  idPrefix: string;
  value: LLMRetryRule;
  onChange: (r: LLMRetryRule) => void;
  /** true = 구성 서랍의 컴팩트 버전：확장설명 생략，그냥 묵으세요「어떤 오류가 발생하면 이 수준이 될까요?」이 문장은 */
  compact?: boolean;
}) {
  const meta = RETRY_LAYERS[layer];
  const human = humanMs(value.interval_ms);
  return (
    <div className={compact ? "grid gap-2" : "grid gap-3 rounded-lg border p-3"}>
      <div className="grid gap-0.5">
        <div className="flex flex-wrap items-baseline gap-2">
          <Label className="text-sm">{meta.title}</Label>
          <span className="text-muted-foreground text-xs">{meta.where}</span>
        </div>
        {/* 어떤 오류가 이 수준으로 이어지나요?，상태 코드에만 해당——손잡이를 채워넣었는데 효과가 안보이네요，오류가 이 수준에 전혀 해당되지 않을 가능성이 높습니다.。 */}
        <p className="text-muted-foreground text-xs">
          <span className="font-medium text-foreground">트리거</span>：{meta.trigger}
        </p>
        {!compact && meta.skips && (
          <p className="text-muted-foreground text-xs">
            <span className="font-medium text-foreground">이 층에는 가지 마세요</span>：{meta.skips}
          </p>
        )}
        {!compact && <p className="text-muted-foreground text-xs">{meta.desc}</p>}
      </div>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <div className="flex items-center gap-2">
          <Label htmlFor={`${idPrefix}-${layer}-n`} className="text-muted-foreground text-xs">
            {meta.attemptsLabel}
          </Label>
          <NumField
            id={`${idPrefix}-${layer}-n`}
            min={-1}
            value={value.attempts}
            placeholder={`기본값 ${meta.defAttempts}`}
            onChange={(n) => onChange({ ...value, attempts: n })}
          />
        </div>
        <div className="flex items-center gap-2">
          <Label htmlFor={`${idPrefix}-${layer}-ms`} className="text-muted-foreground text-xs">
            간격 ms
          </Label>
          <NumField
            id={`${idPrefix}-${layer}-ms`}
            min={0}
            value={value.interval_ms}
            placeholder="기본 후퇴"
            onChange={(n) => onChange({ ...value, interval_ms: n })}
          />
          <span className="text-muted-foreground text-xs">{human ? `고정됨 ${human}` : meta.defInterval}</span>
        </div>
      </div>
      {!compact && <p className="text-muted-foreground text-xs">비워두세요 = 기본값 사용；{meta.offHint}。</p>}
    </div>
  );
}

/** 모델 구성 서랍의 3개 레이어 적용 범위（3층 끝점을 따라가세요）。 */
export function ProfileRetryFields({
  value,
  onChange,
}: {
  value: LLMRetryOverride;
  onChange: (o: LLMRetryOverride) => void;
}) {
  return (
    <div className="grid gap-3 rounded-lg border p-3">
      <div className="grid gap-0.5">
        <Label className="text-sm">적용 범위 재시도</Label>
        <p className="text-muted-foreground text-xs">
          이 구성에만 적용됩니다.，재정의「재시도 및 백오프」의 전역 기본값。각 상자를 비워 두세요. = 전체적인 상황을 지켜봐주세요；횟수를 채워주세요 -1 = 이 레이어를 끄고 다시 시도해보세요；
          간격이 채워지면 지수 백오프 대신 고정 간격을 사용하십시오.。회로 차단기 및 의도 재실행은 프로세스 수준에 있습니다.，글로벌 페이지에서만 조정 가능。
        </p>
      </div>
      {(["connect", "empty", "stream"] as const).map((k) => (
        <div key={k} className="border-t pt-3 first:border-t-0 first:pt-0">
          <RetryRuleFields
            compact
            layer={k}
            idPrefix="pf"
            value={value[k]}
            onChange={(r) => onChange({ ...value, [k]: r })}
          />
        </div>
      ))}
    </div>
  );
}

/** 「재시도 및 백오프」tab：레이어 5의 전역 기본값。 */
export function RetryPolicyPanel() {
  const [policy, setPolicy] = React.useState<LLMRetryPolicy>(ZERO_POLICY);
  const [loading, setLoading] = React.useState(true);
  const [saving, setSaving] = React.useState(false);

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      const p = await api.llmRetryPolicy();
      setPolicy({ ...ZERO_POLICY, ...p });
    } catch (e) {
      toast.error(`읽기 재시도 정책 실패：${(e as Error).message}`);
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void load();
  }, [load]);

  async function save() {
    if (saving) return;
    setSaving(true);
    try {
      // 백엔드는 범위를 벗어난 값을 다시 범위로 고정하고 반환합니다.，반환된 값으로 직접 새로 고침，보이는 대로 보이는 대로 보인다。
      const saved = await api.saveLLMRetryPolicy(policy);
      setPolicy({ ...ZERO_POLICY, ...saved });
      toast.success("저장되었습니다，즉시 효력 발생（현재 호출에서는 여전히 이전 매개변수를 사용합니다.）");
    } catch (e) {
      toast.error(`저장 실패：${(e as Error).message}`);
    } finally {
      setSaving(false);
    }
  }

  const set = (k: LayerKey) => (r: LLMRetryRule) => setPolicy((p) => ({ ...p, [k]: r }));

  if (loading) {
    return (
      <div className="flex items-center gap-2 rounded-lg border border-dashed p-10 text-muted-foreground text-sm">
        <Loader2Icon className="size-4 animate-spin" /> 재시도 전략 읽기…
      </div>
    );
  }

  return (
    <div className="grid gap-4">
      <div className="rounded-lg border bg-muted/30 p-3 text-muted-foreground text-xs leading-relaxed">
        모델 호출이 실패하면 5단계의 재시도를 거칩니다.，내부에서 외부로：
        <span className="text-foreground"> 지안롄 → 빈 응답 → 마찬가지예요 provider 보안창 → 폴링 회로 차단기 → 다시 뛰고 싶은 마음</span>
        。내층이 소진되면 외층 차례，그럼 횟수는
        <span className="text-foreground">곱하기</span>
        님——모든 레이어를 채우십시오.，한 번의 불안으로 수십 개의 요청이 사라질 수 있습니다.。
        현재 기본값을 보려면 모두 비워 두세요.，이 페이지가 없는 것과 똑같은 동작입니다.。처음 3개 레이어는 각 모델 구성에서 개별적으로 재정의될 수 있습니다.。
      </div>

      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
        {(Object.keys(RETRY_LAYERS) as LayerKey[]).map((k) => (
          <RetryRuleFields key={k} layer={k} idPrefix="gl" value={policy[k]} onChange={set(k)} />
        ))}
      </div>

      <div className="flex gap-2">
        <Button onClick={save} disabled={saving}>
          {saving ? <Loader2Icon className="animate-spin" /> : <SaveIcon />}
          저장
        </Button>
        <Button variant="outline" onClick={() => setPolicy(ZERO_POLICY)} disabled={saving}>
          모두 기본값으로 복원
        </Button>
      </div>
    </div>
  );
}
