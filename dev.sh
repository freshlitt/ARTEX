#!/usr/bin/env bash
# 개발 모드：백엔드(:8787) + 트래픽 프록시(:8788) 그리고 프런트엔드 next dev(:5173) 같이 달려요。
# 프런트엔드 /api 백엔드에서 백엔드로；Ctrl-C 같이 나가자。
#
# 단일 바이너리（프런트엔드 임베디드）가는 길에 뵙겠습니다 README「단일 바이너리」섹션，이 스크립트를 따르지 마세요。
set -euo pipefail
cd "$(dirname "$0")"

# 종료 시 이 프로세스 그룹의 모든 하위 프로세스를 종료합니다.（백엔드 + 프런트엔드）。
cleanup() { kill 0 2>/dev/null || true; }
trap cleanup EXIT INT TERM

# 백엔드（보통 go run，프런트엔드를 삽입하지 마세요.）；동시성 work agent 번호는「시스템 설정」구성。
go run ./cmd/artex -addr :8787 -proxy 127.0.0.1:8788 &

# 프런트엔드 핫 업데이트（Vite/Next dev server，/api 역세대를 :8787）。
( cd web && npm run dev ) &

echo "[dev] 백엔드 :8787 / 대리인 :8788 / 프런트엔드 http://localhost:5173  (Ctrl-C 종료)"
wait
