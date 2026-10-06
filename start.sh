#!/bin/sh
# ARTEX Guardian 시작 스크립트（Linux / macOS / Docker ENTRYPOINT）
#
# 사용법：
#   ./start.sh                       포그라운드 작업（Ctrl-C 그만하세요）
#   nohup ./start.sh >artex.log 2>&1 &   배경에 주민
#   ./start.sh -addr :9000           추가 매개변수는 그대로 전달됩니다. artex
#
# 한 가지 일만 합니다.：넣어보세요 artex 달려라，프로세스 종료 후 종료 코드를 눌러 다시 시작할지 결정하십시오.。
#
#   0      사용자가 정상적으로 중지되었습니다.        → 루프 종료
#   75     프로그램 재시작 요청        → 즉시 다시 실행（페이지를 클릭했습니다"원클릭 업데이트"또는"롤백"）
#   기타   충돌                → 후퇴 후 다시 달려라（1→2→4…대부분 60 초）
#
# 일부러 여기서 다운로드하지 마세요、SHA256 확인 또는 교체：그 논리는 sh 그리고 bat 두 세트를 작성해야 합니다.，
# 그리고 그것은 틀림없이 틀릴 수 없는 부분이다.——실행할 수 없는 바이너리로 교체되면，이 스크립트는 충실하게
# 반복해서 당겨주세요，사용자는 머신에 수동으로만 저장할 수 있습니다.。그럼 확인해 보세요/모든 변경 사항을 그대로 둡니다. Go 내부（selfupdate 패키지），
#  artex 시작시 저절로 완료됨，스크립트를 단순하게 유지하세요.。
set -u

cd "$(dirname "$0")" || exit 1

BIN=./artex
[ -x "$BIN" ] || { echo "[artex] 실행 파일을 찾을 수 없습니다. $BIN" >&2; exit 1; }

RESTART_CODE=75
MAX_DELAY=60

child=0
stopping=0

# 정지 신호를 다음으로 전달합니다. artex 몸。
#
# Docker 필수입니다：docker stop 그냥 넣어두세요 SIGTERM 보내기 PID 1（바로 이 스크립트입니다），
# 은 하위 프로세스로 전송되지 않습니다.。전달하지 않으면 artex 신호가 수신되지 않습니다.、정상적으로 종료할 수 없습니다.，10 초 후 SIGKILL
# 하드킬，실행 중인 작업이 중간에 중단되었습니다.。
forward() {
	stopping=1
	if [ "$child" -ne 0 ]; then
		kill -TERM "$child" 2>/dev/null || true
	fi
}
trap forward INT TERM

delay=1
while :; do
	"$BIN" "$@" &
	child=$!

	# 신호가 중단됩니다. wait 그리고 다시 돌려보내세요 >128。현재 하위 프로세스는 실제로 여전히 정상적으로 종료되고 있습니다.，
	# 꼭 다시 해야함 wait 한 번만 실제 종료 코드를 얻을 수 있습니다.。
	wait "$child"
	code=$?
	if [ "$code" -gt 128 ]; then
		wait "$child"
		code=$?
	fi
	child=0

	if [ "$stopping" -eq 1 ]; then
		echo "[artex] 중지됨"
		exit 0
	fi

	case "$code" in
		0)
			echo "[artex] 정상적으로 종료됩니다."
			exit 0
			;;
		"$RESTART_CODE")
			# 업데이트/롤백 준비 완료：다시 실행한 후 artex 시작 시 변경이 완료됩니다.（또 만나요 selfupdate.Bootstrap）。
			echo "[artex] 재시작 요청（새 버전 적용）…"
			delay=1
			;;
		*)
			echo "[artex] 비정상 종료 (code=$code)，${delay}s 그런 다음 다시 시작하세요." >&2
			sleep "$delay"
			delay=$((delay * 2))
			[ "$delay" -gt "$MAX_DELAY" ] && delay=$MAX_DELAY
			;;
	esac
done
