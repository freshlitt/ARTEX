#!/usr/bin/env bash
# ARTEX 설치 스크립트：① 모두 Docker  ② 로컬에서 컴파일 및 실행
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"

info(){ printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok(){   printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn(){ printf '\033[33m[!]\033[0m %s\n' "$*"; }
die(){  printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }
ask(){  local p="$1" d="${2:-}" a; read -rp "$p${d:+ [$d]}: " a; echo "${a:-$d}"; }
rand(){ head -c 18 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 24; }

# ── docker 환경 테스트 / 자동 설치 ───────────────────
ensure_docker(){
  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    ok "감지됨 docker 그리고 docker compose"; return
  fi
  warn "감지되지 않음 docker / docker compose"
  case "$(uname -s)" in
    Linux)
      if [ "$(ask '자동 설치 Docker? (y/n)' y)" = y ]; then
        curl -fsSL https://get.docker.com | sh
        sudo usermod -aG docker "$USER" || true
        ok "Docker 설치 완료（사용자 그룹을 변경하려면 다시 로그인해야 합니다. sudo）"
      else
        die "직접 설치해 보세요 docker 나중에 다시 시도해 보세요."
      fi ;;
    Darwin) die "macOS 설치해주세요 Docker Desktop：https://www.docker.com/products/docker-desktop/" ;;
    *)      die "직접 설치해 보세요 docker 나중에 다시 시도해 보세요." ;;
  esac
}

# ── ① 모두 Docker ───────────────────────────────
install_docker(){
  ensure_docker
  if [ ! -f .env ]; then
    cp .env.example .env 2>/dev/null || true
    local pw key
    pw="$(ask 'Postgres 비밀번호（무작위로 생성된 내용을 입력하세요.）' "$(rand)")"
    key="$(ask 'ANTHROPIC_API_KEY（비워둘 수 있습니다.，후속 조치 UI 일치）' '')"
    sed -i.bak "s|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD=${pw}|" .env
    sed -i.bak "s|^ANTHROPIC_API_KEY=.*|ANTHROPIC_API_KEY=${key}|" .env
    rm -f .env.bak
    ok "생성됨 .env（POSTGRES_PASSWORD 세트）"
  else
    info "기존 것을 상속 .env"
  fi
  info "이미지를 당겨서 시작하세요.…"
  docker compose pull || true
  docker compose up -d
  ok "시동 완료 → http://localhost:8787"
  info "로그 보기：docker compose logs -f artex"
}

# ── ② 로컬에서 컴파일 및 실행 ──────────────────────────────
install_local(){
  echo "데이터베이스 설치 방법："
  echo "  1) 연결이 이미 존재합니다. PostgreSQL"
  echo "  2) 사용 Docker 하나 사세요 PostgreSQL（필수 docker）"
  case "$(ask '선택' 1)" in
    2)
      ensure_docker
      local pw; pw="$(ask 'Postgres 비밀번호（무작위로 입력하세요）' "$(rand)")"
      docker run -d --name artex-pg -p 5432:5432 \
        -e POSTGRES_USER=artex -e POSTGRES_PASSWORD="$pw" -e POSTGRES_DB=artex \
        -v artex-pg:/var/lib/postgresql/data postgres:16-alpine
      DB_HOST=127.0.0.1 DB_PORT=5432 DB_USER=artex DB_PASS="$pw" DB_NAME=artex DB_SSL=disable ;;
    *)
      DB_HOST="$(ask '데이터베이스 주소' 127.0.0.1)"
      DB_PORT="$(ask '포트' 5432)"
      DB_USER="$(ask '계정' artex)"
      DB_PASS="$(ask '비밀번호' '')"
      DB_NAME="$(ask '데이터베이스 이름' artex)"
      DB_SSL="$(ask 'sslmode (disable/require)' disable)" ;;
  esac

  # 생성 config.json
  cat > config.json <<JSON
{
  "database": {
    "host": "${DB_HOST}",
    "port": ${DB_PORT},
    "user": "${DB_USER}",
    "password": "${DB_PASS}",
    "dbname": "${DB_NAME}",
    "sslmode": "${DB_SSL}"
  }
}
JSON
  ok "생성됨 config.json"

  # go 환경점검
  command -v go >/dev/null 2>&1 || die "감지되지 않음 Go，먼저 설치해주세요 Go（>=1.26）：https://go.dev/dl/"
  ok "Go: $(go version)"

  # 임베디드 프런트엔드에 필요 node 정적 제품 생산
  if command -v npm >/dev/null 2>&1; then
    info "프런트엔드 정적 제품 구축…"
    ( cd web && npm ci && npm run build:static )
    rm -rf server/webui/dist && cp -r web/out server/webui/dist
    info "내장된 단일 바이너리 컴파일…"
    CGO_ENABLED=0 go build -tags embedui -trimpath -o artex ./cmd/artex
  else
    warn "감지되지 않음 npm：컴파일됩니다**프런트엔드를 삽입하지 마세요.**용 백엔드（프론트엔드를 별도로 실행해야 합니다. npm run dev）"
    CGO_ENABLED=0 go build -o artex ./cmd/artex
  fi
  ok "편집 완료 → ./artex"

  info "시작…（Ctrl-C 종료）"
  ./artex
}

echo "=============================="
echo "  ARTEX 설치"
echo "  1) 모두 Docker 설치"
echo "  2) 로컬에서 실행（go 컴파일）"
echo "=============================="
case "$(ask '선택' 1)" in
  1) install_docker ;;
  2) install_local ;;
  *) die "잘못된 선택입니다." ;;
esac
