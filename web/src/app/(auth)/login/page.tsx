"use client";

import { useEffect, useRef, useState } from "react";

import { useRouter } from "next/navigation";

import { AlertTriangle, ShieldCheck } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogClose, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import { auth } from "@/lib/auth";

export default function LoginPage() {
  const router = useRouter();
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [checking, setChecking] = useState(true);
  const [agreed, setAgreed] = useState(false);
  const [termsOpen, setTermsOpen] = useState(false);
  const [readToEnd, setReadToEnd] = useState(false);
  const termsBodyRef = useRef<HTMLDivElement>(null);

  // 용어 하단으로 스크롤（스크롤 없이 완전히 표시될 수 있는 상황도 포함）클릭 가능「동의한다」。
  function handleTermsScroll() {
    const el = termsBodyRef.current;
    if (!el) return;
    if (el.scrollTop + el.clientHeight >= el.scrollHeight - 8) setReadToEnd(true);
  }

  useEffect(() => {
    if (!termsOpen) return;
    // 열 때 재설정，그리고 한 화면에 처리하는 내용이 부족하네요、스크롤 시나리오를 실행할 수 없습니다.。
    setReadToEnd(false);
    const el = termsBodyRef.current;
    if (el && el.scrollHeight <= el.clientHeight + 8) setReadToEnd(true);
  }, [termsOpen]);

  useEffect(() => {
    // 로그인하고 메인 인터페이스에 직접 들어갔습니다.（정적 내보내기에는 없음 middleware 이 점프를 해주세요）。
    const token = auth.getToken();
    if (token) {
      // localStorage 아직 자격 증명이 있을 수 있지만 cookie 이 사라졌습니다。먼저 동기화하세요.，새로 요청하세요，
      // 서버측 가드 또는 라우팅 캐시가 아직 실행 중인 점프를 반환하지 않도록 방지하세요. checking 상태에 대한 로그인 페이지。
      auth.setToken(token);
      window.location.replace("/function/tasks");
      return;
    }
    api
      .authStatus()
      .then(({ initialized }) => {
        if (!initialized) router.replace("/setup");
      })
      .catch(() => setError("백엔드 서비스에 연결할 수 없습니다."))
      .finally(() => setChecking(false));
  }, [router]);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!agreed) {
      setError("먼저 읽어보시고 동의해주세요《사용 지침》");
      return;
    }
    setLoading(true);
    setError("");
    try {
      const { token } = await api.login("ARTEX", password);
      auth.setToken(token);
      window.location.replace("/function/tasks");
    } catch {
      setError("사용자 이름이나 비밀번호가 잘못되었습니다.");
    } finally {
      setLoading(false);
    }
  }

  if (checking) {
    return (
      <div role="status" className="flex min-h-dvh items-center justify-center text-muted-foreground">
        로그인 상태 확인 중…
      </div>
    );
  }

  return (
    <div className="flex h-dvh">
      {/* Left panel */}
      <div className="hidden flex-col items-center justify-center bg-primary p-12 text-center lg:flex lg:w-1/3">
        <div className="relative flex items-center justify-center">
          <div className="absolute size-80 rounded-full border border-primary-foreground/10" />
          <div className="absolute size-60 rounded-full border border-primary-foreground/15" />
          <div className="absolute size-40 rounded-full border border-primary-foreground/20" />
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src="/logo.png" alt="ARTEX" width={160} height={160} className="relative brightness-0 invert" />
        </div>
      </div>

      {/* Right panel */}
      <div className="flex w-full items-center justify-center bg-background p-8 lg:w-2/3">
        <div className="w-full max-w-md space-y-10 py-24 lg:py-32">
          <div className="space-y-4 text-center">
            <h2 className="text-2xl font-medium tracking-tight">로그인</h2>
            <p className="mx-auto max-w-xl text-muted-foreground">다시 오신 것을 환영합니다，계속 사용하시려면 비밀번호를 입력해주세요 ARTEX</p>
          </div>
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <div className="space-y-1.5">
              <Label htmlFor="username">사용자 이름</Label>
              <Input id="username" value="ARTEX" readOnly className="bg-muted text-muted-foreground" />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="password">비밀번호</Label>
              <Input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="비밀번호를 입력해주세요"
                autoFocus
                autoComplete="current-password"
              />
            </div>
            <div className="flex items-start gap-2">
              <Checkbox
                id="agree-terms"
                checked={agreed}
                onCheckedChange={(v) => setAgreed(v === true)}
                className="mt-0.5"
              />
              <Label htmlFor="agree-terms" className="text-sm font-normal leading-relaxed text-muted-foreground">
                읽고 동의했습니다.
                <button
                  type="button"
                  onClick={() => setTermsOpen(true)}
                  className="mx-0.5 font-medium text-primary underline-offset-4 hover:underline"
                >
                  《사용 지침》
                </button>
              </Label>
            </div>
            {error && <p className="text-sm text-destructive">{error}</p>}
            <Button type="submit" className="w-full" disabled={loading || !password || !agreed}>
              {loading ? "로그인 중..." : "로그인"}
            </Button>
          </form>
        </div>
      </div>

      <Dialog open={termsOpen} onOpenChange={setTermsOpen}>
        <DialogContent className="gap-0 p-0 sm:max-w-2xl">
          <DialogHeader className="flex-row items-center gap-3 border-b px-6 py-4">
            <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
              <ShieldCheck className="size-5" />
            </div>
            <div className="space-y-0.5">
              <DialogTitle className="text-base">ARTEX 사용 지침 및 면책 조항</DialogTitle>
              <p className="text-xs text-muted-foreground">
                버전 v1.0 · 시행일 2026-09-18 · 로그인하기 전에 다음 약관을 모두 읽어보십시오.
              </p>
            </div>
          </DialogHeader>

          <div
            ref={termsBodyRef}
            onScroll={handleTermsScroll}
            className="max-h-[60vh] space-y-5 overflow-y-auto px-6 py-5 text-sm leading-relaxed text-muted-foreground"
          >
            <p className="rounded-lg border bg-muted/40 p-3 text-foreground/80">
              벤《사용 지침 및 면책 조항》（이하 로 칭함"이 성명서는"）바로 당신과 ARTEX
              이 소프트웨어 사용에 관한 프로젝트 작성자와 기여자 간의 계약。사용 전 꼭 읽어주세요.、각 조항의 내용을 완전히 이해합니다.，특히 굵은 글씨나 컬러 블록으로 표시된 면책사항은、책임의 제한 및 금지。
              <span className="font-medium text-foreground">
                {" "}
                다운로드가 완료되면、설치、어떤 방식으로든 이 소프트웨어에 액세스하거나 사용합니다.，읽으신 것으로 간주됩니다.、이 진술을 완전히 이해하고 이에 동의합니다.。
              </span>
            </p>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  1
                </span>
                제1조 · 정의 및 오픈 소스 라이선스
              </h4>
              <p className="pl-7">
                이 소프트웨어（ARTEX）은 다음을 기반으로 하는 장치입니다. GNU Affero General Public License
                v3.0（AGPL-3.0）에서 출시한 오픈 소스 프로그램。본 약관에 따라 자유롭게 사용하셔도 됩니다.、복사、이 소프트웨어를 수정 및 배포합니다.；그러나 파생물은 모두（인터넷을 통해 제3자에게 제공되는 온라인 서비스를 포함합니다.）도 있어야 합니다.
                AGPL-3.0 프로토콜은 오픈 소스이며 해당 전체 소스 코드가 사용자에게 공개됩니다.。AGPL-3.0 전체 약관 첨부 LICENSE 문서가 우선합니다.。
              </p>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  2
                </span>
                제2조 · 승인된 사용 범위
              </h4>
              <p className="pl-7">
                이 소프트웨어는 개인 학습 전용입니다.、코드 연구、보안기술의 원리에 대한 논의，및 자체 로컬 격리 환경에서의 기술 검증용，학습에 적합、학술연구、코드 검토 및 기타 비공격적、비파괴적 사용。이 기사에서 명시적으로 허용하는 경우를 제외하고，이 소프트웨어를 다른 목적으로 사용할 수 없습니다.。
              </p>
            </section>

            <section className="space-y-2">
              <h4 className="flex items-center gap-2 font-medium text-destructive">
                <span className="flex size-5 items-center justify-center rounded-md bg-destructive/10 text-xs font-semibold text-destructive">
                  3
                </span>
                <AlertTriangle className="size-4" />
                제3조 · 금지행위
              </h4>
              <ul className="ml-7 list-decimal space-y-1.5 rounded-lg border border-destructive/20 bg-destructive/5 p-3 pl-8 text-foreground/80 marker:text-destructive/70">
                <li>
                  모든 웹사이트 이용을 엄격히 금지합니다.、온라인 서비스、다른 사람이나 제3자가 소유한 네트워크 시스템이 검색을 시작합니다.、탐지、악용 또는 공격（승인 여부、본인의 자산인가요?）；
                </li>
                <li>실제 침투 테스트에 이 소프트웨어를 사용하는 것은 엄격히 금지됩니다.、공격과 수비의 대결、빨간색과 파란색 드릴 또는 생산 환경；</li>
                <li>이 소프트웨어를 불법적인 침입에 사용하는 것은 엄격히 금지되어 있습니다.、데이터 도용、협박、서비스 거부（DoS/DDoS）또는 파괴적인 것、범죄행위；</li>
                <li>삭제는 엄격히 금지됩니다.、소프트웨어 및 그 결과물의 저작권을 조작하거나 회피하는 행위、권한 또는 보안 프롬프트 정보；</li>
                <li>해당 국가 또는 지역의 법률을 위반하는 행위는 엄격히 금지됩니다.、법령에 의해 요구되는 행위。</li>
              </ul>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  4
                </span>
                제4조 · 지적재산권
              </h4>
              <p className="pl-7">
                이 소프트웨어의 저작권 및 관련 지적 재산권은 프로젝트 작성자 및 기여자에게 있습니다.，그리고 AGPL-3.0
                계약에서 합의한 범위 내에서 해당 권리를 귀하에게 부여합니다.。본 계약에 의해 명시적으로 부여된 권리는 제외됩니다.，이 진술은 명시적 또는 묵시적으로 귀하에게 다른 권리를 부여하지 않습니다.。
              </p>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  5
                </span>
                제5조 · 데이터 및 개인정보 보호
              </h4>
              <p className="pl-7">
                이 소프트웨어는 직접 배포할 수 있는 오픈소스 프로그램입니다.，작성자는 중앙화된 서비스를 운영하지 않습니다.、은 귀하의 사용 데이터를 수집하거나 업로드하지 않습니다.。사용 중에 생성한 것입니다.、처리되거나 액세스되는 모든 데이터，적법성과 안전에 대한 통제권과 책임은 귀하에게 있습니다.；부적절한 데이터 처리로 인해 발생하는 모든 결과에 대한 책임은 귀하에게 있습니다.。
              </p>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  6
                </span>
                제6조 · 규정 준수 및 법적 책임
              </h4>
              <p className="pl-7">
                귀하가 위치한 국가 또는 지역의 네트워크 보안 규정을 준수해야 합니다.、데이터 보안 및 개인정보 보호、컴퓨터 범죄 등에 관한 모든 법률 및 규정（중국 본토(다음을 포함하되 이에 국한되지 않음)《사이버보안법》《데이터 보안법》《개인정보보호법》및 관련 사법 해석）。
                <span className="font-medium text-foreground">
                  {" "}
                  위의 법률 및 규정 또는 본 성명의 조항을 위반함으로써 발생하는 모든 법적 책임과 결과，이는 전적으로 귀하의 책임입니다.，은 이 소프트웨어의 작성자 및 기여자와 아무 관련이 없습니다.。
                </span>
              </p>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  7
                </span>
                제7조 · 면책조항 및 책임 제한
              </h4>
              <p className="pl-7">
                이 소프트웨어는"현재상황（AS IS）"그리고"기존（AS
                AVAILABLE）"상태 제공，어떠한 명시적 또는 묵시적 보증 없이，상품성에 대한 주장을 포함하되 이에 국한되지 않습니다.、특정 목적에 대한 적합성、정확성 및 비침해 보장。해당 법률이 허용하는 최대 한도 내에서，이 소프트웨어의 작성자 및 기여자는 이 소프트웨어의 사용 또는 사용 불능에 대해 책임을 지지 않습니다.（적절하게 사용하든 안하든 상관없이）직접적인 결과가 발생합니다.、간접、우연히、특별 또는 결과적 손실에 대한 책임，데이터 손실을 포함하되 이에 국한되지 않음、시스템 손상、업무 방해、수익 손실 또는 법적 분쟁。
              </p>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  8
                </span>
                제8조 · 약관 변경 및 최종 설명
              </h4>
              <p className="pl-7">
                저자는 법률, 규정 또는 프로젝트 개발 요구에 따라 이 성명을 수시로 업데이트할 권리가 있습니다.，업데이트 버전은 프로젝트와 함께 공개될 예정이며 발표일로부터 효력이 발생합니다.；이 소프트웨어를 계속 사용하면 개정된 약관에 동의하는 것으로 간주됩니다.。법률이 허용하는 한도 내에서，이 진술의 최종 해석권은 프로젝트 작성자에게 있습니다.。본 성명의 어느 조항이라도 유효하지 않다고 판단되는 경우，은 나머지 조항의 유효성에 영향을 미치지 않습니다.。
              </p>
            </section>
          </div>

          <DialogFooter className="mx-0 mb-0 flex-col items-stretch gap-2 rounded-b-xl px-6 sm:flex-row sm:items-center sm:justify-between">
            <p className="text-xs text-muted-foreground">
              {readToEnd ? "모든 약관을 확인하셨습니다." : "확인하기 전에 용어를 맨 아래로 스크롤하십시오."}
            </p>
            <DialogClose asChild>
              <Button
                type="button"
                disabled={!readToEnd}
                onClick={() => {
                  setAgreed(true);
                  setError("");
                }}
              >
                모든 약관을 읽었으며 이에 동의합니다.
              </Button>
            </DialogClose>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
