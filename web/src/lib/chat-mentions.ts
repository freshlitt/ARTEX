export const mentionKinds = [
  { kind: "finding", label: "취약점", alias: "finding" },
  { kind: "asset", label: "자산", alias: "asset" },
  { kind: "company", label: "기업", alias: "company" },
  { kind: "endpoint", label: "인터페이스", alias: "api" },
  { kind: "ip", label: "IP", alias: "ip" },
  { kind: "app", label: "애플리케이션", alias: "app" },
  { kind: "root_domain", label: "도메인 이름", alias: "domain" },
  { kind: "subdomain", label: "하위 도메인 이름", alias: "subdomain" },
  { kind: "service", label: "서비스", alias: "service" },
] as const;

export type MentionKind = (typeof mentionKinds)[number]["kind"];
export interface ChatMention {
  kind: MentionKind;
  id: number;
  label: string;
  description: string;
}

export function activeMention(value: string, caret: number) {
  const before = value.slice(0, caret);
  const start = before.lastIndexOf("@");
  if (start < 0 || (start > 0 && /[\w.+/-]/.test(before[start - 1]))) return null;
  const query = before.slice(start + 1);
  if (/[[\]\r\n@]/.test(query) || query.length > 220) return null;
  return { start, end: caret, query };
}

export function mentionSearch(query: string) {
  const text = query.trimStart().toLowerCase();
  for (const item of mentionKinds) {
    for (const alias of [item.label.toLowerCase(), item.alias]) {
      if (text === alias || text.startsWith(`${alias} `) || (/[^a-z]/.test(alias) && text.startsWith(alias))) {
        return { kind: item.kind, query: query.trimStart().slice(alias.length).trim(), categories: [] };
      }
    }
  }
  const categories = mentionKinds.filter(
    (item) => item.label.toLowerCase().startsWith(text) || item.alias.startsWith(text),
  );
  return { kind: "" as const, query: query.trim(), categories };
}

export function mentionToken(item: ChatMention) {
  const kind = mentionKinds.find((entry) => entry.kind === item.kind)?.label ?? "자산";
  const label = item.label
    .replace(/[[\]]/g, (char) => (char === "[" ? "（" : "）"))
    .replace(/\s+/g, " ")
    .slice(0, 100);
  return `@[${kind}#${item.id} ${label}]`;
}

export function selectedMentions(value: string) {
  return [...value.matchAll(/@\[(취약점|자산|기업|인터페이스|IP|애플리케이션|도메인 이름|하위 도메인 이름|서비스|\u6f0f\u6d1e|\u8d44\u4ea7|\u4f01\u4e1a|\u63a5\u53e3|\u5e94\u7528|\u57df\u540d|\u5b50\u57df\u540d|\u670d\u52a1)#([0-9]+)(?: ([^\]\r\n]*))?\]/g)].map(
    (match) => ({
      token: match[0],
      label: `${match[1]} #${match[2]}${match[3] ? ` · ${match[3]}` : ""}`,
      start: match.index,
    }),
  );
}
