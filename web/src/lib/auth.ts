const TOKEN_KEY = "artex_token";
const COOKIE_MAX_AGE = 7 * 24 * 60 * 60; // 7 일（초）

export interface CurrentUser {
  id: string;
  name: string;
  username: string;
  email: string;
  avatar: string;
  role: string;
}

export const auth = {
  getToken(): string | null {
    if (typeof window === "undefined") return null;
    // Mock demo：실제 로그인이 되지 않습니다.，false를 반환합니다. token 라우팅 가드를 통과시키세요、메인 인터페이스에 직접 들어가십시오.。
    return localStorage.getItem(TOKEN_KEY) ?? (process.env.NEXT_PUBLIC_MOCK === "1" ? "mock-demo" : null);
  },

  setToken(token: string): void {
    localStorage.setItem(TOKEN_KEY, token);
    // 동기 쓰기 cookie， Next.js middleware 서버 읽기
    document.cookie = `${TOKEN_KEY}=${encodeURIComponent(token)}; path=/; max-age=${COOKIE_MAX_AGE}; SameSite=Lax`;
  },

  clearToken(): void {
    localStorage.removeItem(TOKEN_KEY);
    document.cookie = `${TOKEN_KEY}=; path=/; max-age=0`;
  },

  // 님으로부터 JWT payload 님 sub 필드 구문 분석 현재 사용자，표시 전용，서명 확인 없음。
  getCurrentUser(): CurrentUser | null {
    const token = this.getToken();
    if (!token) return null;
    try {
      const parts = token.split(".");
      if (parts.length !== 3) return null;
      // base64url → base64
      const payload = JSON.parse(atob(parts[1].replace(/-/g, "+").replace(/_/g, "/")));
      const username: string = payload.sub ?? "ARTEX";
      return { id: "1", name: username, username, email: "", avatar: "", role: "operator" };
    } catch {
      return null;
    }
  },
};
