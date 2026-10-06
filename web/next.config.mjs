import { fileURLToPath } from "node:url";

// 정적 내보내기：`NEXT_EXPORT=1 next build` 순수 정적 디렉터리를 다음으로 출력합니다. web/out，직접 던질 수 있습니다.
// nginx web 루트 디렉터리에서 실행。개발(next dev)이 변수는 설정되지 않았습니다.，예약됨 /api 역생성 및 핫 업데이트。
const isExport = process.env.NEXT_EXPORT === "1";
// Vercel demo：역 전체를 걸어보세요 mock，백엔드 없음，필요없어요 /api 안티세대。
const isMock = process.env.NEXT_PUBLIC_MOCK === "1";

/** @type {import('next').NextConfig} */
const nextConfig = {
  // 상위 디렉토리 피하기 lockfile 루트 디렉터리 추론 및 리소스 경로 생성에 영향을 줍니다.。
  turbopack: { root: fileURLToPath(new URL(".", import.meta.url)) },
  reactCompiler: true,
  // LAN에서 액세스 허용 IP 방문 dev 리소스（HMR），필요에 따라 추가 또는 삭제하세요.。
  // dev 아무 무대나 공개해 IPv4 소스 액세스 /_next/* 그리고 HMR（랜 IP 변경사항은 영향을 받지 않습니다.）。
  // 주의：Next 안전상의 이유로 과도한 노출은 금지됩니다. "*"，분할된 와일드카드를 사용해야 합니다.；"*.*.*.*" 은 다음과 일치합니다. IPv4。
  allowedDevOrigins: ["*.*.*.*"],
  compiler: {
    removeConsole: process.env.NODE_ENV === "production",
  },
  ...(isExport
    ? {
        // 순수 정적 내보내기：없음 Node 런타임；사진이 최적화되지 않았습니다；각 경로 출력 <route>/index.html。
        output: "export",
        images: { unoptimized: true },
        trailingSlash: true,
      }
    : isMock
      ? {
          // Vercel mock demo：백엔드 없음，필요하지 않음 /api 안티세대。
          images: { unoptimized: true },
        }
      : {
          // 개발：넣어보세요 /api/* 역세대를 Go 백엔드（기본값 :8787，가능 AUTOPENTEST_API 재정의）。
          async rewrites() {
            const backend = process.env.AUTOPENTEST_API ?? "http://localhost:8787";
            return [{ source: "/api/:path*", destination: `${backend}/api/:path*` }];
          },
        }),
};

export default nextConfig;
