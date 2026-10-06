# `/btw` 확인기록

날짜：2026-09-10。지점：`codex/btw-side-question`。기준선：`8dae851b9b622f2ff2631f332fde9719d0b16fba`。

독립적인 PostgreSQL 테스트 라이브러리와 데이터 디렉터리를 사용합니다. 실제 모델 자격 증명은 독립 테스트 환경에만 주입되며 코드나 이 기록은 작성되지 않으며 제품 기본 모델은 수정되지 않습니다. Go 1.26.3, 노르마 v0.3.6, Next.js 16.2.9.

실제 모델 대화, 반환 개체, 엔지니어링 주장 및 Qwen 원본 리뷰 텍스트는 API 자격 증명이 없는 [validation-2026-09-10.json](validation-2026-09-10.json)에 저장됩니다.

## 엔지니어링 검사

| 범위 | 결과 | 증거 |
| --- | --- | --- |
| 구조화된 메시지、공구 매개변수의 전체 사본 | 합격 | `TestCheckpointDeepCopyAndBoundaries` |
| 요약 / 압축요청은 해당되지 않습니다.、전체 답변 및 최종 공개、반문단 답변 제외 | 합격 | `TestCheckpointDeepCopyAndBoundaries`、`TestSnapshotExcludesPartialStreamAndSelectsPoolMember` |
| 실제 모델풀 멤버십 | 합격 | `TestSnapshotExcludesPartialStreamAndSelectsPoolMember` |
| 도구 페어링、20 그룹 재생、예산 클리핑 및 초과 실행 오류 | 합격 | `TestBuildRequestCompactionToolPairingAndBudget` |
| 메인 바이패스 병렬、양방향 격리 취소 | 합격 | 차단 Provider，`TestMainSideConcurrencyAndIndependentCancellation` |
| 도구 실행 없음、스트리밍 / 비스트리밍、실패시 이미 사용됨 | 합격 | `TestServiceNoToolsAndUsageOnFailure` |
| 진짜 norma ChatAgent + 현지 Read 도구、스승님 transcript / 활동 격리 | 합격 | `TestSideActualChatCheckpointToolResultAndTranscriptIsolation`，스트리밍 및 비스트리밍 하위 사용 사례 |
| 끈기、페이징、멱등성、다시 시작하고 답변을 유지하세요 | 합격 | `TestSideHistoryIdempotencyPagingAndRecovery` |
| 명확하고 늦은 쓰기 경합、상위 리소스 삭제、버전 비교 | 합격 | `TestSideClearLateWritersAndDeletedParent` |
| MainAgent / Worker 보관 및 복원，v1/v2/v3 | 합격 | `TestSideTaskArchiveVersions` |
| 3개의 상위 인터페이스、인증、리소스 소유권、Worker 논리적 삭제 | 합격 | `TestSideHTTPGlobalLimitTaskWorkerAndDeletion`、`TestSideCheckpointPersistsBeforeAdmissionAndRestart` |
| 바쁜 메인 세션을 우회할 수 있습니다.、독립 SSE 다시 연결 / 연결 끊기、취소、클리어 | 합격 | `TestSideHTTPBusyIsolationClearAndReconnect` |
| 학부모 세션당 1 / 글로벌 4 동시성 | 합격 | 둘 `TestSideHTTP…` 사용 사례 |
| 제출 전 스냅샷이 저장되었습니다.、다시 시작하고 질문을 계속하세요、위조할 수 없는 이전 세션의 스냅샷 | 합격 | `TestSideCheckpointPersistsBeforeAdmissionAndRestart` |
| 캐시의 구성이 삭제되거나 모델이 변경된 후 계속을 거부합니다. | 합격 | `TestSideRejectsDeletedOrChangedCachedProfile` |
| 보관하기 전에 취소하고 최종 답변과 사용량이 기록될 때까지 기다리세요. | 합격 | `TestSideTaskDrainPersistsBeforeArchive` |
| 스트리밍 소비자가 사전에 취소할 경우 사용량 및 바이패스 어트리뷰션은 1회만 기록됩니다. | 합격 | `TestSideUsageRecordedOnceOnConsumerCancellation` |
| 다시 시작하고 자동으로 복원 Worker / deadline 실행 중인 컨텍스트가 계속해서 새 스냅샷을 게시합니다. | 합격 | `TestSideRestoredWorkerRuntimePublishesNewCheckpoint` |
| 관련 패키지 race 확인 | 합격 | 다음 명령 |
| TypeScript 프로덕션 빌드 포함 | 합격 | `npx tsc --noEmit`、`npm run build` |
| 새로운 프런트엔드 모듈이 추가되었습니다. Biome | 합격 | `biome check`，3 새 모듈 |

폐기 가능한 별도의 데이터베이스에 구성 `ARTEX_PG_DSN` 이후，자동검사 재현 가능（프로덕션 라이브러리를 가리키지 마세요.）：

```sh
go test -race ./agent ./db ./server ./sidequestion ./llmrec ./llmpool \
  -run 'Test(Side|Checkpoint|Snapshot|BuildRequest|Service|MainSide|CaptureRun|TaskArchive|CompleteForwards|StopIntent|CancelIntent)' -count=1
cd web
npx tsc --noEmit
npx biome check src/lib/side-questions.ts src/hooks/use-side-questions.ts src/components/side-question-workspace.tsx
npm run build
```

전액 Go 반품이 모두 녹색이 아닙니다.：`server` 패키지에는 임시 디렉터리 정리 단계에서 실패한 두 가지 기존 테스트가 있습니다.，모두 신고했습니다 `TempDir RemoveAll … directory not empty`：

- `TestInheritedActivityDetailAndRelationDeletion`
- `TestTaskMetadataPatchReturnsRenameAndPin`

위의 수정되지 않은 베이스라인에서 소스코드를 익스포트한 후，동일한 격리 환경에서 다시 실행 `server` 패키지，이 두 가지 정리 실패도 발생합니다.。또 다른 기준선 실행 발생 `TestCoreTaskLifecyclePG` 의 대상 노드 번호 어설션이 실패했습니다.；최종 수정됨 `server` 어설션 없이 회귀에 실패했습니다.。기타 패키지 통과，이 우회 관련 사용 사례와 race 확인 통과。기본 문제는 이 승인에 대해 통과된 것으로 표시되지 않았습니다.，숨겨진 문제를 해결하기 위해 수정된 기존 어설션이 없습니다.。

Next.js 빌드는 기존의 여러 잠금 파일/작업공간 루트 추론 경고를 출력합니다. 빌드가 완료되고 모든 페이지가 성공적으로 생성됩니다.

## 브라우저 확인

Codex In-app Browser를 사용하여 독립형 로컬 Go 서비스와 Next.js 개발 서버를 연결하세요. 데스크탑 및 390 × 844 좁은 화면은 다음 수동 자동화 작업을 완료하고 스크린샷 및 브라우저 로그를 확인합니다.

- 일반 채팅 중에 입장 `/btw`，메인컨텐츠와 바이패스가 동시에 표시됩니다.；데스크톱 사이드바는 정상입니다.。
- 지속적인 질문; 바이패스가 중지된 후에도 생성된 부분을 유지합니다. 주요 프로세스가 계속됩니다.
- 패널을 닫을 때 계속 요청，재시작 후 복구 완료 답변；페이지 새로 고침 후 비어 있음 `/btw` 복원 기록。
- 좁은 화면 서랍의 입력, 버튼, 히스토리, 닫는 동작은 정상이며 가로 오버플로우도 없습니다.
- 사용확인 팝업창을 지웁니다. 삭제 후에는 기록이 사라지고 주요 기록과 스냅샷이 유지됩니다.
- Task MainAgent와 두 명의 Worker가 질문을 하고 따로 전환했습니다. 상담원 라벨과 내역이 혼선되지 않았습니다.
- 차단된 로컬 모델 고정 장치 유지 Worker 달려라；님으로부터 Worker 메인 입력란 제출 `/btw`，바이패스 정지 후 Worker 은 여전히 실시간 실행 및 자체 일시중지 버튼을 표시합니다.，부분 답변 저장 우회。
- 브라우저 오류/경고 로그가 비어 있습니다.

제어 가능한 고정 장치는 실제 모델의 출력 속도에 의존하지 않고 동시 타이밍을 정확하게 확인하는 데 사용됩니다. 디버깅하는 동안 두 작업자 런타임 검사가 유효한 동시성 창(작업 종료/조기 응답 종료)을 형성하지 않았으며, 픽스처를 수정하고 다시 실행하여 통과했습니다. 이러한 초기 작업은 유효한 패스로 기록되지 않습니다.

## 실제 모델 대사

우선순위 감지 `grok-4.6`，OpenAI 호환 인터페이스 `http://127.0.0.1:12580/tingly/openai`。탐지 HTTP 200，모델명을 반환합니다. `grok-4.6` 그리고 `READY`，시간이 많이 걸린다 2.82 초。선호 가능，따라서 활성화되지 않습니다. Tingly `glm` 또는 지혜 스펙트럼 `glm-5.3` 백업 체인；이 두 가지 백업 서비스는 이번에 검증되지 않았습니다.。

| 장면 | 실제 결과 |
| --- | --- |
| 메인 세션이 실행되는 동안 자산을 요청합니다.、대상、태그 | 복귀 `redhaze.top`、홈페이지 읽기 및 요약 목표、`BTW-REAL-0910`；바이패스 완료，16.97 초 |
| 메인 세션이 홈 페이지 읽기를 마친 후 기초에 대한 도구를 요청하십시오. | 정확한 인용문 WebFetch 200、curl 점프 301 → 302 → 200、페이지 제목；7.24 초 |
| 우회 요구 사항 Bash 테스트 파일 생성 | 집행거부，대상 파일이 생성되지 않았습니다.；7.74 초 |
| 우회가 완료되어도 기본 컨텍스트가 변경되지 않습니다. | 스승님 transcript SHA-256 주요활동기록과 일치；바이패스 도구의 실행 횟수는 다음과 같습니다. 0 |
| 진짜 그만해 / 다시 시작 Go 서비스 후속 질문 | 이전 유지 3 우회 이력，영구 스냅샷에서 직접 자산에 응답、태그 및 제목，반복되지 않는 주자 Agent |
| 새로운 세션 사용법 Grok 비스트리밍 구성 | 정답은 자산과 `ATOMIC-0910`；반환 및 사용량 저장：input 11734、output 138、cache_read 11520 |

자산 사례의 주요 세션 사용 WebFetch 그리고 Bash/curl 공개홈페이지 읽기，랜딩페이지는 `https://id.redhaze.top/home`，제목은“빨간 커튼 기술 RedHaze Group · 글로벌 통합 그룹 포털”。Bash 로컬 테스트 파일에 응답을 임시 저장합니다.；원격측에는 쓰기가 수행되지 않습니다.。이 사실은 다음과 일치합니다.“우회 실행 도구가 없습니다.”별도로 확인하세요。

스승님 transcript 값 확인：`e7e61f135a4a120954b539f357e8c4205d7d5cd7460dcaf3dc0fd066463e1d00`。

**사용 제한：** Tingly 님 Grok 스트리밍 응답이 반환되지 않습니다. usage。따로 직접 보내주세요 `stream_options.include_usage=true` 확인，HTTP 200、12 데이터 프레임、0  usage 프레임。그래서 스트리밍 테스트에서는 0 은 엔드포인트가 사용량을 제공하지 않음을 의미합니다.，과금 없음으로 해석할 수 없습니다.。흘러내리지 않는 용량 및 고정 장치 실패 / 취소된 모든 사용 내역이 올바르게 저장되었습니다.。

## 퀀 리뷰

리뷰모델 `qwen-flash`，OpenAI 호환 인터페이스 `https://dashscope.aliyuncs.com/compatible-mode/v1`，HTTP 200。은 처음 세 가지 실제 사이드 대화를 제공합니다.、메인 세션 도구 기반 및 프로젝트 주장；복귀 `verdict: accept`、`concerns: []`，답과 자산을 생각하세요、태그、페이지 읽기 증거가 일관됩니다，우회 도구가 제약 조건 준수를 거부함。복용량 검토：prompt 6625、completion 312、total 6937。

이번 Qwen의 검토 범위에는 나중에 추가된 서비스 재시작 및 비스트리밍 테스트는 포함되지 않습니다. Qwen의 "쓰기 없음" 일반화는 너무 광범위합니다. 기본 세션 컬은 위에 명시적으로 문서화된 로컬 응답 임시 파일을 생성합니다. 동시성, 제로 도구 실행 및 성적표 격리는 엔지니어링 주장에 따라 판단되며 모델 검토는 답변 품질 평가에만 도움이 됩니다.
