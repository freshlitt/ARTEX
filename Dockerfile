# syntax=docker/dockerfile:1
#
# 이미지 실행（이미지에는 컴파일되지 않았습니다.）：일반 도구만 설치됨，넣어주세요**사전 컴파일됨 Linux 단일 바이너리**。
# 바이너리 CI 님 binaries job 크로스컴파일（순수 Go、없음 QEMU），대상 아키텍처에 따라 배치
# 컨텍스트 구축 dist/<TARGETARCH>/artex。이렇게 여러 아키텍처를 구축할 때 arm64 시뮬레이션만 해보세요 apt 레이어，
# 더 이상 시뮬레이션되지 않습니다. Next/Go 컴파일，훨씬 빨라요。
#
# 로컬에서 이미지를 수동으로 빌드하는 경우，먼저 바이너리를 직접 준비하세요：
#   cd web && npm run build:static && cd ..
#   cp -r web/out server/webui/dist
#   CGO_ENABLED=0 GOARCH=amd64 go build -tags embedui -o dist/amd64/artex ./cmd/artex
#   docker build -t artex:local .
FROM python:3.12-slim-bookworm
ARG TARGETARCH
# 공통 도구：ripgrep / curl / vim，배치 추가 recon 예비 부품（필요에 따라 추가 또는 삭제하세요.）。
# Node 님으로부터 NodeSource 척 20.x：bookworm 같이와요 apt nodejs 네 18，Playwright 요청 >=20。
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates ripgrep curl wget vim git jq unzip \
      dnsutils iputils-ping netcat-openbsd inetutils-telnet whois nmap \
    && curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*
# 사전 설치됨 Playwright MCP 그리고 CLI（글로벌），더 이상 실행되지 않습니다. npx 온라인 다운로드。
# @playwright/mcp：browser MCP 직접 `npx @playwright/mcp`（이 전역적으로 설치되었습니다.，필요없어요 -y/@latest）。
# @playwright/cli：제공 playwright-cli，그런데 설치하고 나면 --help 확인이 실행 가능합니다.。
# 재설치 playwright（브라우저 관리 제공），설치 후 사용 --with-deps 프리셋 chromium 및 해당 시스템 종속성，
# 이 컨테이너에는 MCP/CLI 처음 시작 시 사용 가능，브라우저를 다운로드하기 위해 더 이상 인터넷에 연결되어 있지 않습니다.。
RUN npm install -g @playwright/mcp@latest @playwright/cli@latest playwright@latest \
    && playwright-cli --help \
    && playwright install --with-deps chromium \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
# 사전 컴파일된 해당 아키텍처 바이너리（dist/amd64/artex 또는 dist/arm64/artex）
COPY dist/${TARGETARCH}/artex /app/artex
# Guardian 시작 스크립트：프로세스 종료 후 종료 코드를 눌러 다시 시작할지 여부를 결정하십시오.，클릭 한 번으로 페이지 업데이트에 사용하세요。
# 또한 책임이 있습니다 SIGTERM 앞으로 artex —— docker stop 다음으로만 신호를 보냅니다. PID 1，
# 전달하지 않으면 artex 받지 못함、정상적으로 종료할 수 없습니다.，10 초 후 SIGKILL 하드킬。
COPY start.sh /app/start.sh
RUN chmod +x /app/artex /app/start.sh
COPY skills/ /app/skills/
# data/（SQLite + jwt.key）지속성 지점
VOLUME ["/app/data"]
EXPOSE 8787 8788
ENTRYPOINT ["/app/start.sh"]
CMD ["-addr", ":8787", "-proxy", ":8788"]
