#!/usr/bin/env bash
# =============================================================================
# ARTEX 관리자 비밀번호 재설정 스크립트
#
# 로그인 사용자 이름은 다음과 같이 고정됩니다. ARTEX；비밀번호는 다음으로 시작합니다. bcrypt 해시가 데이터베이스에 저장됩니다. settings 테이블
# auth.password_hash 키。이 스크립트가 데이터베이스에 연결된 후，사용 pgcrypto 라이브러리에서 생성됨 bcrypt 해시
# 그리고 키를 다시 작성하세요.——백엔드 로그인으로 확인（golang.org/x/crypto/bcrypt）완전 호환。
#
# 두 가지 배포：
#   local （기본값）—— 호스트가 직접 사용 psql 데이터베이스에 연결。다음 우선순위에 따라 연결 정보를 얻습니다.：
#                    명령줄 매개변수 > --dsn/$ARTEX_PG_DSN > config.json 님 database.*
#   docker        —— 합격 `docker compose exec`（또는 `docker exec`）에 postgres
#                    컨테이너 내 실행 psql（compose 기본적으로 호스트에 노출되지 않음 5432，그럼 컨테이너 안으로 들어가세요）。
#
# 사용예：
#   ./reset-password.sh                          # 현지，자동읽기 config.json/환경，대화형으로 새 비밀번호를 입력하세요.
#   ./reset-password.sh -p 'NewPass!'            # 현지，새 비밀번호를 직접 알려주세요
#   ./reset-password.sh --dsn postgres://u:p@h:5432/artex
#   ./reset-password.sh -H 127.0.0.1 -P 5433 -U autopentest -W pass -d artex
#   ./reset-password.sh -m docker                # docker 배포（읽기 .env 님 POSTGRES_*）
#   ./reset-password.sh -m docker -c pg컨테이너 이름 --exec docker
#
# 보안：환경 변수를 통한 새 비밀번호 + psql \getenv 들어옴（프로세스에 들어가지 않습니다. argv），함께 사용하세요 :'var'
# 자동 탈출（수비 SQL 주사）；데이터베이스 비밀번호 PGPASSWORD 합격，역시 들어가지 않습니다. argv。
# =============================================================================
set -euo pipefail

PASS_KEY="auth.password_hash"
BCRYPT_COST=10

MODE=""            # local | docker（비어 있음=자동결정）
DSN=""
HOST="" PORT="" USER="" DBPASS="" DBNAME="" SSLMODE=""
CONFIG=""
CONTAINER=""       # docker 패턴 postgres 서비스/컨테이너 이름（기본값 postgres）
EXEC_KIND=""       # compose | docker（docker 어떤 모드를 사용하나요? exec；비어 있음=자동）
NEWPASS=""
ASSUME_YES=0

die() { echo "오류：$*" >&2; exit 1; }
info() { echo "· $*" >&2; }

usage() { sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//'; exit 0; }

# ---- 매개변수 분석 -------------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    -m|--mode)        MODE="${2:-}"; shift 2 ;;
    --dsn)            DSN="${2:-}"; shift 2 ;;
    -H|--host)        HOST="${2:-}"; shift 2 ;;
    -P|--port)        PORT="${2:-}"; shift 2 ;;
    -U|--user)        USER="${2:-}"; shift 2 ;;
    -W|--db-password) DBPASS="${2:-}"; shift 2 ;;
    -d|--dbname)      DBNAME="${2:-}"; shift 2 ;;
    --sslmode)        SSLMODE="${2:-}"; shift 2 ;;
    --config)         CONFIG="${2:-}"; shift 2 ;;
    -c|--container)   CONTAINER="${2:-}"; shift 2 ;;
    --exec)           EXEC_KIND="${2:-}"; shift 2 ;;
    -p|--new-password) NEWPASS="${2:-}"; shift 2 ;;
    -y|--yes)         ASSUME_YES=1; shift ;;
    -h|--help)        usage ;;
    *) die "알 수 없는 매개변수：$1（-h 사용량 보기）" ;;
  esac
done

# ---- 님으로부터 config.json 읽기 database.*（만 local 모드、명시적으로 연결이 제공되지 않습니다.）-----
# 우선순위 python3 분석（견고함）；누락 python3 일 때 반환됨 grep（config.json 은 일반 하위 필드입니다.）。
read_config_json() {
  local path="$1"
  [[ -f "$path" ]] || return 1
  if command -v python3 >/dev/null 2>&1; then
    python3 - "$path" <<'PY'
import json, sys
try:
    d = json.load(open(sys.argv[1])).get("database", {})
except Exception:
    sys.exit(1)
# 직접 지원 dsn，또는 하위 필드
if d.get("dsn"):
    print("DSN\t" + d["dsn"]); sys.exit(0)
for k in ("host","port","user","password","dbname","sslmode"):
    if d.get(k) is not None:
        print(k.upper() + "\t" + str(d[k]))
PY
  else
    # 미니멀리스트 백업：키별 키 grep（값은 문자열 또는 숫자입니다.）
    local k
    for k in host port user password dbname sslmode; do
      local v
      v=$(grep -oE "\"$k\"[[:space:]]*:[[:space:]]*(\"[^\"]*\"|[0-9]+)" "$path" 2>/dev/null \
            | head -1 | sed -E "s/.*:[[:space:]]*//; s/^\"//; s/\"$//") || true
      [[ -n "$v" ]] && echo -e "${k^^}\t$v"
    done
  fi
}

apply_config_fields() {
  local line key val
  while IFS=$'\t' read -r key val; do
    [[ -z "$key" ]] && continue
    case "$key" in
      DSN)      [[ -z "$DSN" ]] && DSN="$val" ;;
      HOST)     [[ -z "$HOST" ]] && HOST="$val" ;;
      PORT)     [[ -z "$PORT" ]] && PORT="$val" ;;
      USER)     [[ -z "$USER" ]] && USER="$val" ;;
      PASSWORD) [[ -z "$DBPASS" ]] && DBPASS="$val" ;;
      DBNAME)   [[ -z "$DBNAME" ]] && DBNAME="$val" ;;
      SSLMODE)  [[ -z "$SSLMODE" ]] && SSLMODE="$val" ;;
    esac
  done
}

# ---- 자동 판단 모드 ---------------------------------------------------------
if [[ -z "$MODE" ]]; then
  if [[ -n "$DSN$HOST$USER$DBNAME" || -n "${ARTEX_PG_DSN:-}" || -f "${CONFIG:-config.json}" ]]; then
    MODE="local"
  elif command -v docker >/dev/null 2>&1 && [[ -f docker-compose.yml ]]; then
    MODE="docker"
  else
    MODE="local"
  fi
fi
info "배포 모드：$MODE"

# ---- 새로운 비밀번호를 수집하세요 -----------------------------------------------------------
if [[ -z "$NEWPASS" ]]; then
  read -r -s -p "새 비밀번호를 입력하세요（사용자 이름은 다음과 같이 고정됩니다. ARTEX）：" NEWPASS; echo >&2
  [[ -n "$NEWPASS" ]] || die "비밀번호는 비워둘 수 없습니다."
  read -r -s -p "다시 입력하여 확인하세요.：" NEWPASS2; echo >&2
  [[ "$NEWPASS" == "$NEWPASS2" ]] || die "두 입력이 일치하지 않습니다."
fi
[[ -n "$NEWPASS" ]] || die "비밀번호는 비워둘 수 없습니다."

# 환경변수를 통해 비밀번호를 전달합니다. psql（\getenv 읽기，들어가지 않음 argv/ps）
export ARTEX_RESET_NEWPASS="$NEWPASS"

# 라이브러리에서 생성됨 bcrypt 그리고 upsert；비밀번호는 :'newpw' 자동 탈출。CREATE EXTENSION 멱등성，
# 데이터베이스 역할에 확장 권한이 없으면 여기에 오류가 보고됩니다.（팁은 아래를 참조하세요. run 의 실패한 분기）。
SQL=$(cat <<SQL
\\set ON_ERROR_STOP on
\\getenv newpw ARTEX_RESET_NEWPASS
CREATE EXTENSION IF NOT EXISTS pgcrypto;
INSERT INTO settings(key, value)
VALUES ('$PASS_KEY', crypt(:'newpw', gen_salt('bf', $BCRYPT_COST)))
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now();
SQL
)

# ---- 실행 -----------------------------------------------------------------
if [[ "$MODE" == "local" ]]; then
  # 연결정보 우선순위：명령줄 > --dsn/$ARTEX_PG_DSN > config.json
  if [[ -z "$DSN" && -z "$HOST$USER$DBNAME" ]]; then
    [[ -n "${ARTEX_PG_DSN:-}" ]] && DSN="$ARTEX_PG_DSN"
  fi
  if [[ -z "$DSN" && -z "$HOST$USER$DBNAME" ]]; then
    cfg="${CONFIG:-config.json}"
    if [[ -f "$cfg" ]]; then
      info "님으로부터 $cfg 데이터베이스 구성 읽기"
      apply_config_fields < <(read_config_json "$cfg")
    fi
  fi

  command -v psql >/dev/null 2>&1 || die "이 기계를 찾을 수 없습니다 psql（설치해주세요 postgresql-client，또는 대신 사용 -m docker）"

  declare -a PSQL_ARGS=()
  if [[ -n "$DSN" ]]; then
    PSQL_ARGS=("$DSN")
    target="$DSN"
  else
    [[ -n "$USER"   ]] || die "데이터베이스 사용자가 누락되었습니다.（-U）또는 유효 config.json/DSN"
    [[ -n "$DBNAME" ]] || die "데이터베이스 이름이 누락되었습니다.（-d）또는 유효 config.json/DSN"
    HOST="${HOST:-127.0.0.1}"; PORT="${PORT:-5432}"; SSLMODE="${SSLMODE:-disable}"
    PSQL_ARGS=(-h "$HOST" -p "$PORT" -U "$USER" -d "$DBNAME")
    [[ -n "$SSLMODE" ]] && export PGSSLMODE="$SSLMODE"
    [[ -n "$DBPASS" ]] && export PGPASSWORD="$DBPASS"
    target="$USER@$HOST:$PORT/$DBNAME"
  fi

  info "대상 데이터베이스：$target"
  if [[ "$ASSUME_YES" -ne 1 ]]; then
    read -r -p "이 라이브러리에서 재설정을 확인하세요. ARTEX 비밀번호？[y/N] " ans
    [[ "$ans" == "y" || "$ans" == "Y" ]] || die "취소됨"
  fi

  if ! printf '%s\n' "$SQL" | psql "${PSQL_ARGS[@]}" -v ON_ERROR_STOP=1 -q >/dev/null; then
    die "쓰기 실패。신고된 경우 pgcrypto 권한/없어짐，확장 프로그램 생성 권한이 있는 역할을 사용하세요.，아니면 수동으로 먼저 실행하세요 CREATE EXTENSION pgcrypto。"
  fi

else
  # ---- docker ----
  command -v docker >/dev/null 2>&1 || die "찾을 수 없음 docker"
  CONTAINER="${CONTAINER:-postgres}"

  # 선택 exec 방법：우선순위 docker compose exec（서비스 이름），그렇지 않으면 docker exec（컨테이너 이름）
  if [[ -z "$EXEC_KIND" ]]; then
    if docker compose version >/dev/null 2>&1 && [[ -f docker-compose.yml ]]; then
      EXEC_KIND="compose"
    else
      EXEC_KIND="docker"
    fi
  fi

  # 컨테이너 속 psql 자격 증명：우선순위 명령줄，둘째 .env 님 POSTGRES_*，다시 돌려주세요 compose 기본값(artex)
  if [[ -f .env ]]; then
    # shellcheck disable=SC1091
    set -a; . ./.env; set +a
  fi
  DUSER="${USER:-${POSTGRES_USER:-artex}}"
  DNAME="${DBNAME:-${POSTGRES_DB:-artex}}"
  [[ -n "$DBPASS" ]] && export PGPASSWORD="$DBPASS"
  [[ -z "${PGPASSWORD:-}" && -n "${POSTGRES_PASSWORD:-}" ]] && export PGPASSWORD="$POSTGRES_PASSWORD"

  info "대상：컨테이너 $CONTAINER 내부 psql -U $DUSER -d $DNAME（exec=$EXEC_KIND）"
  if [[ "$ASSUME_YES" -ne 1 ]]; then
    read -r -p "컨테이너 데이터베이스가 재설정되었는지 확인하세요. ARTEX 비밀번호？[y/N] " ans
    [[ "$ans" == "y" || "$ans" == "Y" ]] || die "취소됨"
  fi

  # -e 값 없이 이름만 → 현재 환경에서 상속됨，지금은 비밀번호가 나타나지 않습니다 docker 명령 argv 내부。
  declare -a EXEC_CMD
  if [[ "$EXEC_KIND" == "compose" ]]; then
    EXEC_CMD=(docker compose exec -T -e ARTEX_RESET_NEWPASS -e PGPASSWORD "$CONTAINER"
              psql -U "$DUSER" -d "$DNAME" -v ON_ERROR_STOP=1 -q)
  else
    EXEC_CMD=(docker exec -i -e ARTEX_RESET_NEWPASS -e PGPASSWORD "$CONTAINER"
              psql -U "$DUSER" -d "$DNAME" -v ON_ERROR_STOP=1 -q)
  fi

  if ! printf '%s\n' "$SQL" | "${EXEC_CMD[@]}" >/dev/null; then
    die "쓰기 실패。컨테이너 이름을 확인해 주세요.（-c）、데이터베이스 계정（.env 님 POSTGRES_*），그리고 등장인물은 pgcrypto 권한。"
  fi
fi

unset ARTEX_RESET_NEWPASS
echo "✓ 재설정 ARTEX 관리자 비밀번호。사용자 이름을 사용하세요. ARTEX + 새로운 비밀번호로 로그인하세요（서비스를 다시 시작할 필요가 없습니다.）。"
