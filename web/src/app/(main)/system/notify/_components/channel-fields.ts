// 채널 필드 테이블 및 구성 값에 대한 분석 도구。
//
// 이 페이지는 페이지와 분리되어 있습니다.**데이터**：각 채널에 어떤 필드가 있는지 설명합니다.、
// 어떤 컨트롤을 사용해야 합니까?，및 구성 값에 대한 텍스트 양식（JSON）의 양방향 변환。
// 별도의 파일을 배치한 후，새 채널을 추가하려면 여기로 이동하세요.，페이지 자체는 수정할 필요가 없습니다.。
// 표시 이름 및 채널 유형 소개。카피라이팅에만 영향을 주기 때문에 프론트엔드에 넣어주세요，백엔드는 알 필요가 없습니다.。
export const KIND_LABEL: Record<string, string> = {
  dingtalk: "딩톡",
  feishu: "페이슈",
  wecom: "기업 위챗",
  webhook: "일반 Webhook",
  telegram: "Telegram",
  email: "이메일",
};

// 각 채널에 대한 구성 필드 정의。
//
// 프런트엔드 필드 테이블을 일부러 여기에 두었습니다.，백엔드 문제를 허용하는 대신 schema：백엔드는 다음 작업만 담당합니다.
// Validate（필수/형식），UI 필요한 것은 레이아웃과 컨트롤 타입，둘은 같은 것에 집중하지 않는다.。
// 유일한 결합 지점은 secret_keys —— 비밀번호 상자로 렌더링되어야 하는 필드는 백엔드에서 제공됩니다.，
// 채널 구현만이 어떤 값이 자격 증명으로 계산되는지 알기 때문에（기업 전체 WeChat Webhook 은 자격 증명입니다.，
// 그리고 딩딩도 그 중 하나일 뿐이에요 secret）。채널을 추가할 때 여기에 항목을 하나 줄이면 양식이 공백으로 만들어집니다.，
// 자동 오류 없음（다음은 hasFields 이 메시지를 표시합니다.）。
export type FieldKind = "text" | "password" | "number" | "select" | "textarea" | "switch" | "kv" | "list";
export interface FieldDef {
  key: string;
  label: string;
  kind: FieldKind;
  placeholder?: string;
  help?: string;
  options?: { value: string; label: string }[];
}
export const CHANNEL_FIELDS: Record<string, FieldDef[]> = {
  dingtalk: [
    {
      key: "webhook",
      label: "Webhook 주소",
      kind: "text",
      placeholder: "https://oapi.dingtalk.com/robot/send?access_token=...",
    },
    {
      key: "secret",
      label: "서명 키",
      kind: "password",
      help: "로봇 안전 설정 선택「서명 추가」일 때 작성하세요.；선택「맞춤 키워드」또는 보안 설정이 켜져 있지 않은 경우 공백으로 두십시오.",
    },
  ],
  feishu: [
    {
      key: "webhook",
      label: "Webhook 주소",
      kind: "text",
      placeholder: "https://open.feishu.cn/open-apis/bot/v2/hook/...",
    },
    { key: "secret", label: "서명 확인 키", kind: "password", help: "로봇이 켜져 있습니다.「서명 확인」일 때 작성하세요.，그렇지 않으면 비워두세요" },
  ],
  wecom: [
    {
      key: "webhook",
      label: "Webhook 주소",
      kind: "text",
      placeholder: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=...",
    },
  ],
  webhook: [
    { key: "url", label: "대상 URL", kind: "text", placeholder: "https://your-endpoint.example.com/hook" },
    {
      key: "method",
      label: "요청방법",
      kind: "select",
      options: [
        { value: "POST", label: "POST（요청 본문 포함）" },
        { value: "PUT", label: "PUT（요청 본문 포함）" },
        { value: "PATCH", label: "PATCH（요청 본문 포함）" },
        { value: "GET", label: "GET（요청 본문 없음）" },
      ],
    },
    { key: "headers", label: "사용자 정의 요청 헤더", kind: "kv", help: "각 라인 KEY=VALUE，예를 들면 Authorization=Bearer xxx" },
    {
      key: "body_template",
      label: "요청 본문 템플릿",
      kind: "textarea",
      help:
        "내장된 기본 템플릿을 사용하려면 비워 두세요.。가변적：{{.Title}} {{.Batch}} {{.Count}} {{.HomeURL}} {{.SentAt}}，" +
        "그리고 range .Items  .Name/.VulnClass/.Severity/.Summary/.Assets/.DetailURL/.StatusLabel。" +
        "문자열을 삽입하는 데 사용하십시오 {{json .Xxx}} 대신 {{.Xxx}}，그렇지 않으면 제목의 따옴표가 깨집니다. JSON。",
    },
  ],
  telegram: [
    { key: "bot_token", label: "Bot Token", kind: "password", placeholder: "123456:ABC-DEF..." },
    { key: "chat_id", label: "Chat ID", kind: "text", placeholder: "-1001234567890" },
    {
      key: "base_url",
      label: "API 주소",
      kind: "text",
      placeholder: "https://api.telegram.org",
      help: "공식 주소를 사용하려면 공백으로 남겨두세요.；자체 제작 Bot API 역생성시 채워주세요",
    },
  ],
  email: [
    { key: "host", label: "SMTP 서버", kind: "text", placeholder: "smtp.example.com" },
    {
      key: "port",
      label: "포트",
      kind: "number",
      placeholder: "587",
      help: "587 가자 STARTTLS；465 부탁드려요「암시적 TLS」열려있습니다",
    },
    { key: "username", label: "계정", kind: "text" },
    { key: "password", label: "비밀번호 / 인증코드", kind: "password" },
    { key: "from", label: "보내는 사람", kind: "text", placeholder: "artex@example.com" },
    { key: "to", label: "수신자", kind: "list", help: "쉼표로 구분된 여러 주소" },
    { key: "tls", label: "암시적 TLS", kind: "switch", help: "465 포트 열림；587 폐쇄하세요（은 자동으로 STARTTLS）" },
  ],
};

export const SEVERITY_OPTIONS = [
  { value: "", label: "제한 없음" },
  { value: "low", label: "저위험 이상" },
  { value: "medium", label: "약간 위험함 이상" },
  { value: "high", label: "고위험 이상" },
  { value: "critical", label: "심한 경우에만 해당" },
];

export type ChannelForm = {
  name: string;
  kind: string;
  mode: "realtime" | "digest";
  enabled: boolean;
  ratePerMin: string;
  config: Record<string, unknown>;
  minSeverity: string;
  includeText: string;
  excludeText: string;
  taskIDsText: string;
  assetIDsText: string;
  onStatusChange: boolean;
};

export const emptyForm = (kind: string): ChannelForm => ({
  name: "",
  kind,
  mode: "realtime",
  enabled: true,
  ratePerMin: "",
  config: {},
  minSeverity: "",
  includeText: "",
  excludeText: "",
  taskIDsText: "",
  assetIDsText: "",
  onStatusChange: false,
});

// parseKV 분석「각 라인 KEY=VALUE」의 텍스트 필드。
export function parseKV(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of text.split("\n")) {
    const t = line.trim();
    if (!t) continue;
    const i = t.indexOf("=");
    if (i > 0) out[t.slice(0, i).trim()] = t.slice(i + 1).trim();
  }
  return out;
}
// parseIDs 쉼표를 구문 분석하세요./공백으로 구분됨 id 목록。
export function parseIDs(text: string): number[] {
  return text
    .split(/[\s,，]+/)
    .map((s) => s.trim())
    .filter(Boolean)
    .map((s) => Number(s))
    .filter((n) => Number.isFinite(n) && n > 0);
}
// parseKeywords 구문 분석 라인/쉼표로 구분된 키워드 목록（취약점 유형 이름에는 공백이 포함될 수 있습니다.，그래서 줄이나 쉼표로 잘라요）。
export function parseKeywords(text: string): string[] {
  return text
    .split(/[\n,，]+/)
    .map((s) => s.trim())
    .filter(Boolean);
}
