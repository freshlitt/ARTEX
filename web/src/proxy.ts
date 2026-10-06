import { NextResponse } from "next/server";
import type { NextRequest } from "next/server";

const AUTH_PAGES = ["/login", "/setup"];

export function proxy(request: NextRequest) {
  // Mock demo：실제 로그인이 되지 않습니다.，모든 페이지 공개（클라이언트 auth 경비원도 당신을 놓아줄 것입니다）。
  if (process.env.NEXT_PUBLIC_MOCK === "1") return NextResponse.next();

  const { pathname } = request.nextUrl;
  const token = request.cookies.get("artex_token")?.value;
  const isAuthPage = AUTH_PAGES.some((p) => pathname === p || pathname.startsWith(`${p}/`));

  // 로그인 안됨 → 로그인 페이지로 이동
  if (!token && !isAuthPage) {
    return NextResponse.redirect(new URL("/login", request.url));
  }

  // 로그인 시 로그인 접속/초기화 페이지 → 메인 인터페이스로 이동
  if (token && isAuthPage) {
    return NextResponse.redirect(new URL("/function/tasks", request.url));
  }

  return NextResponse.next();
}

export const config = {
  // 건너뛰기 Next.js 내부 라우팅、API 라우팅、favicon 그리고 public/ 아래의 정적 파일（사진과 함께、폰트 등）
  matcher: ["/((?!_next/static|_next/image|favicon\\.ico|api/|.*\\.(?:png|jpg|jpeg|gif|webp|svg|ico|woff2?|ttf|otf)$).*)"],
};
