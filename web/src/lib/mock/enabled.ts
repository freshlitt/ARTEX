// Mock 스위치。구축 중 공용 변수 주입（NEXT_PUBLIC_ 접두사는 브라우저에서만 읽을 수 있습니다.）。
// Vercel 상부장비 NEXT_PUBLIC_MOCK=1 역 전체를 걸어보세요 mock、백엔드가 필요하지 않습니다.。
export const MOCK = process.env.NEXT_PUBLIC_MOCK === "1";
