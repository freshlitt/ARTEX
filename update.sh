#!/usr/bin/env bash
# ARTEX 업데이트 스크립트：① Docker 업데이트（새 이미지를 가져와서 다시 빌드）  ② 로컬 컴파일 업데이트（바이너리 재구축）
# 그리고 install.sh 해당：install 첫 상륙 담당，update 새 버전으로 업그레이드를 담당합니다。
# DB 마이그레이션을 수동으로 수행할 필요가 없습니다.——artex 시작할 때마다 멱등성이 있고 다시 실행됩니다. schema.sql（포함 ADD COLUMN/CREATE
# INDEX IF NOT EXISTS），그래서“다시 시작 및 마이그레이션”。데이터（pgdata 볼륨、./data、./skills）영향을 받지 않음。
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"

info(){ printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok(){   printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn(){ printf '\033[33m[!]\033[0m %s\n' "$*"; }
die(){  printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }
ask(){  local p="$1" d="${2:-}" a; read -rp "$p${d:+ [$d]}: " a; echo "${a:-$d}"; }

# ── 선택사항：창고를 최신 코드로 동기화（compose/스크립트/로컬 컴파일 소스 코드는 이에 의존하여 업데이트됩니다.）───────
sync_repo(){
  [ -d .git ] && command -v git >/dev/null 2>&1 || { warn "아니요 git 작업 카피，건너뛰기 git pull"; return; }
  [ "$(ask '최신 코드를 가져옵니다. (git pull --ff-only)? (y/n)' y)" = y ] || return
  if ! git pull --ff-only; then
    warn "git pull 빨리감기 실패（로컬 변경이나 분기 포크가 있습니다.）——수동으로 처리하시고 다시 시도해주세요，이번에는 현재 코드를 사용하겠습니다."
  fi
}

# ── ① Docker 업데이트 ───────────────────────────────
update_docker(){
  command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1 \
    || die "감지되지 않음 docker / docker compose，먼저 사용해 보세요 ./install.sh 설치 및 배포"
  [ -f .env ] || die "찾을 수 없음 .env，먼저 달려주세요 ./install.sh 1차 배포 완료"

  # 선택사항：지정된 버전으로 업그레이드 tag（비워두면 사용 .env 에 ARTEX_TAG，기본값은 latest）
  local tag; tag="$(ask '대상 이미지 tag（입력하고 사용하세요 .env / latest）' '')"
  if [ -n "$tag" ]; then
    if grep -q '^ARTEX_TAG=' .env; then
      sed -i.bak "s|^ARTEX_TAG=.*|ARTEX_TAG=${tag}|" .env && rm -f .env.bak
    else
      printf '\nARTEX_TAG=%s\n' "$tag" >> .env
    fi
    ok "이미 ARTEX_TAG 다음으로 설정 ${tag}"
  fi

  # 움직이기만 하면 된다 artex：postgres 이 수정되었습니다. 16-alpine，업그레이드를 따를 필요가 없습니다.（당기는 것은 대역폭 낭비다.，
  # 그리고 주요 버전 변경으로 인해 호환성 위험이 있을 것입니다.）。artex 선언됨 depends_on postgres，그럼 서비스명을 가져와주세요
  # up 루오 시 pg 올라가지 않으면 자동으로 당겨집니다.，이미 실행 중이라면 그대로 유지하세요.、재구축 없음。
  info "새 이미지 가져오기（만 artex）…"
  docker compose pull artex
  info "다시 빌드하고 시작하세요.（artex 재시작 시 자동 마이그레이션 schema）…"
  docker compose up -d artex
  ok "업데이트 완료 → http://localhost:8787"
  info "로그 보기：docker compose logs -f artex"
  info "오래된 이미지 정리（선택사항）：docker image prune -f"
}

# ── ② 로컬 컴파일 업데이트 ──────────────────────────────
update_local(){
  command -v go >/dev/null 2>&1 || die "감지되지 않음 Go（>=1.26）：https://go.dev/dl/"
  [ -f config.json ] || warn "찾을 수 없음 config.json——처음 배포하는 경우 ./install.sh"
  ok "Go: $(go version)"

  if command -v npm >/dev/null 2>&1; then
    info "프런트엔드 정적 제품 재구축…"
    ( cd web && npm ci && npm run build:static )
    rm -rf server/webui/dist && cp -r web/out server/webui/dist
    info "내장된 단일 바이너리를 다시 컴파일합니다.…"
    CGO_ENABLED=0 go build -tags embedui -trimpath -o artex ./cmd/artex
  else
    warn "감지되지 않음 npm：컴파일**프런트엔드를 삽입하지 마세요.**용 백엔드（프론트엔드를 별도로 실행해야 합니다. npm run dev）"
    CGO_ENABLED=0 go build -o artex ./cmd/artex
  fi
  ok "편집 완료 → ./artex"
  warn "런닝을 다시 시작해주세요 artex 프로세스가 적용됩니다.（다시 시작하면 자동으로 마이그레이션됩니다. schema）"
}

echo "=============================="
echo "  ARTEX 업데이트"
echo "  1) Docker 업데이트（새 이미지를 가져와서 다시 빌드）"
echo "  2) 로컬 업데이트（go 재컴파일）"
echo "=============================="
case "$(ask '선택' 1)" in
  1) sync_repo; update_docker ;;
  2) sync_repo; update_local ;;
  *) die "잘못된 선택입니다." ;;
esac
