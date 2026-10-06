@echo off
rem 콘솔 스위치 UTF-8，그렇지 않으면 이 문서의 중국어는 GBK 단말기가 깨졌습니다.。
chcp 65001 >nul 2>&1
rem ARTEX Guardian 시작 스크립트（Windows）
rem
rem 사용법：
rem   start.bat                  포그라운드 작업（Ctrl-C 그만하세요）
rem   start.bat -addr :9000      추가 매개변수는 그대로 전달됩니다. artex
rem
rem 한 가지 일만 합니다.：넣어보세요 artex.exe 달려라，프로세스 종료 후 종료 코드를 눌러 다시 시작할지 결정하십시오.。
rem
rem   0      사용자가 정상적으로 중지되었습니다.     -> 루프 종료
rem   75     프로그램 재시작 요청     -> 즉시 다시 실행（페이지를 클릭했습니다"원클릭 업데이트"또는"롤백"）
rem   기타   충돌             -> 후퇴 후 다시 달려라（1->2->4…대부분 60 초）
rem
rem 다운로드、SHA256 확인、의상변경은 여기에 없습니다，올바이 artex 시작시 저절로 완료됨
rem （selfupdate 패키지）。스크립트를 단순하게 유지하세요.，자세히 보기 start.sh 상단 설명。

setlocal enabledelayedexpansion
cd /d "%~dp0"

set "BIN=artex.exe"
if not exist "%BIN%" (
	echo [artex] 실행 파일을 찾을 수 없습니다. %BIN% 1>&2
	exit /b 1
)

set "RESTART_CODE=75"
set "MAX_DELAY=60"
set /a delay=1

:loop
"%BIN%" %*
set "code=!ERRORLEVEL!"

if "!code!"=="0" (
	echo [artex] 정상적으로 종료됩니다.
	exit /b 0
)

if "!code!"=="%RESTART_CODE%" (
	rem 업데이트/롤백 준비 완료：다시 실행한 후 artex 시작 시 변경이 완료됩니다.。
	echo [artex] 재시작 요청（새 버전 적용）…
	set /a delay=1
	goto loop
)

echo [artex] 비정상 종료 ^(code=!code!^)，!delay!s 그런 다음 다시 시작하세요. 1>&2
rem timeout 리디렉션된 콘솔에서는 실패합니다.，사용 ping 사실대로 말해주세요（지연 N 초 필요 N+1 회）。
set /a pings=!delay!+1
ping -n !pings! 127.0.0.1 >nul 2>&1
set /a delay=!delay!*2
if !delay! gtr %MAX_DELAY% set /a delay=%MAX_DELAY%
goto loop
