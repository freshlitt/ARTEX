package agent

import (
	"log"
	"path/filepath"

	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/noaadapter"
)

// noaWarn returns a diagnostics sink tagging non-fatal noa messages with the
// session, routed through the package logger (agents have no per-instance one).
func noaWarn(session string) func(string) {
	return func(msg string) { log.Printf("[noa] %s: %s", session, msg) }
}

// noa 네 norma v0.4.0 소개됨「모델 기반 컨텍스트 압축」메커니즘,플랫폼 실험 기능으로 사용자는
// 시스템 설정으로 전환。내장과 비슷합니다. compaction 상호 배타적:noaadapter.Enable 이 유일한 입구입니다,일단 끊으세요
// 컨텍스트 수용자(Compactor)、Compress 도구 및 세 가지 영구 알림 단어,호출되지 않음 Enable 폐쇄됨
// (내장 compaction 평소대로 일하세요)。스위치는 각각 agent 주입됨 noaEnabledFn 분석,매 run 읽기
// 한번,따라서 전환은 나중에 시작된 경우에만 영향을 미칩니다. run,재구축 필요 없음 agent。

// enableNoa 넣어주세요 noa 접속 opts。archiveRoot 은 압축된 원본 텍스트의 지속성 기본 디렉터리입니다.
// (글로벌을 노려라 workDir,각각 agent 통일가을 <workDir>/noa 다음,작업을 따르지 않음/인텐트 디렉터리가 분산되어 있습니다.),sessionID
// 그 아래의 아카이브 하위 디렉터리 이름을 지정하세요.(전역적으로 고유함,따라서 동일한 베이스 디렉터리에서는 충돌이 발생하지 않습니다.)。
//
// noa 은 실험적인 함수입니다.:액세스 실패로 인해 실제 작업이 중단되어서는 안 됩니다.。오류가 발생한 시간 onWarn 내장 압축 보고 및 롤백。
// 성공적으로 활성화되면 지워짐 opts.Compaction,피하세요 agentcore 왜냐하면「두 개의 컨텍스트 관리자가 동시에 설정되었습니다.」알람。
func enableNoa(opts *agentcore.Options, enabled func() bool, archiveRoot, sessionID string, onWarn func(string)) {
	if enabled == nil || !enabled() {
		return
	}
	if opts.OnWarn == nil {
		opts.OnWarn = onWarn
	}
	if err := noaadapter.Enable(opts, noaadapter.Options{
		ArchiveBaseDir: filepath.Join(archiveRoot, "noa"),
		SessionID:      sessionID,
		OnWarn:         onWarn,
	}); err != nil {
		if onWarn != nil {
			onWarn("noa 압축 활성화 실패,대체 압축 내장:" + err.Error())
		}
		return
	}
	// Compactor 재정의 Compaction,하지만 둘 다 공존하는 경우 agentcore 매번 알람이 울립니다.;명확하게 지우세요。
	opts.Compaction = nil
}
