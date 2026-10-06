"use client";

import { CheckIcon } from "lucide-react";

import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import type { NotificationFilter } from "@/lib/types";

// asText / inputType 은 이 파일의 보조 값입니다.（컨트롤 렌더링과 밀접한 관련이 있음），넣지 마세요 channel-fields。
import { type FieldDef, type FieldKind, SEVERITY_OPTIONS } from "./channel-fields";

// asText 모든 구성 값을 입력 상자에서 사용할 수 있는 문자열로 렌더링합니다.。
// config 님으로부터 JSON，값은 다음과 같을 수 있습니다. string / number / boolean / array / null，
// 여기만 신경쓰여요「글 상자에 넣어주실 수 있나요?」，특정 직렬화는 다음에 의해 수행됩니다. buildConfig 담당。
function asText(v: unknown): string {
  if (typeof v === "string") return v;
  if (v === null || v === undefined) return "";
  return String(v);
}

// inputType 필드 유형을 매핑할 대상 input 님 type 속성。
function inputType(kind: FieldKind): "text" | "password" | "number" {
  if (kind === "password") return "password";
  if (kind === "number") return "number";
  return "text";
}

// ConfigField 필드 정의에 따라 해당 컨트롤을 렌더링합니다.。
//
// 여기서 중요한 것은 마스크 필드 처리뿐입니다.：입력 상자**표시되지 않음**마스크 값 자체，한 줄만 표시
// 「저장되었습니다」팁。이렇게 하면 인터페이스에 규칙이 하나만 있습니다.——상자 안의 단어는 사용자가 입력합니다.，
// 빈 상자는 빈 값。만약에 "__masked__:…abc123" 입력창에 삽입，사용자들은 자기 자신을 위한 것이라고 생각할 것입니다.
// 자리 표시자 텍스트가 삭제되었습니다.，반대로 실수로 자격 증명을 삭제하는 것이 더 쉽습니다.。
export function ConfigField({
  def,
  value,
  isSecret,
  onChange,
}: {
  def: FieldDef;
  value: unknown;
  isSecret: boolean;
  onChange: (v: unknown) => void;
}) {
  const id = `n-cfg-${def.key}`;
  const raw = asText(value);
  // 백엔드에서 에코되는 마스크 값：모양은 다음과 같습니다 "__masked__:…abc123"，꼬리는 원래 값의 식별 가능한 조각입니다.。
  const masked = isSecret && raw.startsWith("__masked__");
  const maskedTail = masked ? (raw.split("…")[1] ?? "") : "";

  if (def.kind === "switch") {
    return (
      <div className="flex items-center gap-2 text-sm">
        <Switch checked={value === true} onCheckedChange={onChange} aria-label={def.label} />
        {def.label}
        {def.help && <span className="text-muted-foreground">（{def.help}）</span>}
      </div>
    );
  }

  if (def.kind === "select") {
    return (
      <div className="grid gap-2">
        <Label>{def.label}</Label>
        <Select value={raw || def.options?.[0]?.value} onValueChange={onChange}>
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {(def.options ?? []).map((o) => (
              <SelectItem key={o.value} value={o.value}>
                {o.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
    );
  }

  // 컨트롤은 필드 유형별로 전달됩니다.。사용 if 중첩된 삼항 대신 체인，여기서 구별해야 할 컨트롤 종류가 4가지이기 때문입니다.，
  // 3단계 삼항법을 읽을 때 멈추고 괄호를 세어야 합니다.。
  function control() {
    if (def.kind === "textarea" || def.kind === "kv") {
      return (
        <Textarea
          id={id}
          className="font-mono"
          placeholder={def.placeholder}
          value={masked ? "" : raw}
          onChange={(e) => onChange(e.target.value)}
        />
      );
    }
    if (def.kind === "list") {
      return (
        <Input
          id={id}
          value={Array.isArray(value) ? (value as string[]).join(", ") : raw}
          onChange={(e) => onChange(e.target.value)}
          placeholder={def.placeholder}
        />
      );
    }
    return (
      <Input
        id={id}
        className={def.kind === "text" ? "font-mono" : ""}
        type={inputType(def.kind)}
        placeholder={def.placeholder}
        value={masked ? "" : raw}
        onChange={(e) => onChange(e.target.value)}
      />
    );
  }

  const hint = masked ? (
    <p className="text-muted-foreground flex items-center gap-1 text-xs">
      <CheckIcon className="size-3" />
      저장되었습니다{maskedTail ? `（꼬리번호 ${maskedTail}）` : ""} · 새로운 값을 입력하면 덮어쓰게 됩니다.，항목을 삭제하려면 지우세요.
    </p>
  ) : (
    def.help && <p className="text-muted-foreground text-xs">{def.help}</p>
  );

  return (
    <div className="grid gap-2">
      <Label htmlFor={id}>{def.label}</Label>
      {control()}
      {hint}
    </div>
  );
}

// FilterSummary 필터 조건을 한 줄로 요약，카드를 펼칠 필요 없이 이 채널이 무엇을 홍보하는지 살펴보겠습니다.。
export function FilterSummary({ filter }: { filter: NotificationFilter }) {
  const parts: string[] = [];
  if (filter.min_severity) {
    parts.push(SEVERITY_OPTIONS.find((o) => o.value === filter.min_severity)?.label ?? filter.min_severity);
  }
  if (filter.vulnclass_include?.length) parts.push(`유형에는 다음이 포함됩니다. ${filter.vulnclass_include.length} 말씀`);
  if (filter.vulnclass_exclude?.length) parts.push(`제외 ${filter.vulnclass_exclude.length} 말씀`);
  if (filter.task_ids?.length) parts.push(`${filter.task_ids.length} 작업`);
  if (filter.asset_ids?.length) parts.push(`${filter.asset_ids.length} 자산`);
  if (filter.on_status_change) parts.push("상태변화 포함");
  if (parts.length === 0) {
    return <p className="text-muted-foreground text-sm">모든 취약점</p>;
  }
  return <p className="text-muted-foreground text-sm">{parts.join(" · ")}</p>;
}
