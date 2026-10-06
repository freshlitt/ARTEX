import packageJson from "../../package.json";

const currentYear = new Date().getFullYear();

export const APP_CONFIG = {
  name: "ARTEX",
  version: packageJson.version,
  copyright: `© ${currentYear}, ARTEX.`,
  meta: {
    title: "ARTEX — 독립적인 침투 테스트 콘솔",
    description: "LLM 구동 독립 침투 테스트 시스템 콘솔",
  },
};
