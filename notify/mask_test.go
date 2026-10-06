package notify

import (
	"errors"
	"strings"
	"testing"
)

func TestMaskedValueHidesBodyButKeepsTailHint(t *testing.T) {
	const secret = "https://oapi.dingtalk.com/robot/send?access_token=abcdef123456"
	got := MaskedValue(secret)
	if strings.Contains(got, "abcdef123456") {
		t.Fatalf("마스크 값은 전체 자격 증명을 나타냅니다.: %q", got)
	}
	if strings.Contains(got, "oapi.dingtalk.com") {
		t.Fatalf("마스크 값은 주소 본문을 노출해서는 안 됩니다.: %q", got)
	}
	// 끝 6 비트를 예약해야 합니다.，어떤 로봇인지는 사용자만이 알 수 있습니다.。
	if !strings.HasSuffix(got, "123456") {
		t.Fatalf("은 마지막에 보관해야 합니다. 6 비트를 식별 프롬프트로 사용: %q", got)
	}
	if !IsMasked(got) {
		t.Fatalf("마스크 값은 다음과 같아야 합니다. IsMasked 신분증: %q", got)
	}
}

func TestMaskedValueShortSecretGivesNoHint(t *testing.T) {
	// Short Credential도 노출된다면, 6 비트，이는 전체 자격 증명을 노출하는 것과 같습니다.。
	for _, s := range []string{"abc", "abcdef", ""} {
		got := MaskedValue(s)
		if got != MaskedPrefix {
			t.Fatalf("길이 %d 에 대한 자격 증명은 후행 힌트를 제공해서는 안 됩니다.，받았어요 %q", len(s), got)
		}
		if s != "" && strings.Contains(got, s) {
			t.Fatalf("마스크 값에 원래 값이 포함되어 있습니다.: %q", got)
		}
	}
}

func TestMaskConfigMasksOnlySecrets(t *testing.T) {
	cfg := map[string]any{
		"webhook": "https://example.com/hook?token=SECRETVALUE",
		"secret":  "SECtest123456",
		"port":    float64(587),
		"host":    "smtp.example.com",
	}
	masked := MaskConfig(KindDingTalk, cfg)
	for _, k := range []string{"webhook", "secret"} {
		s, _ := masked[k].(string)
		if !IsMasked(s) {
			t.Errorf("%s 을 마스킹해야 합니다.，받았어요 %q", k, s)
		}
	}
	// 자격 증명이 아닌 필드는 그대로 두어야 합니다.，그렇지 않으면 UI 표시할 수 없습니다.。
	if masked["port"] != float64(587) {
		t.Errorf("비자격 필드 port 변경하면 안 됩니다.: %v", masked["port"])
	}
}

func TestMaskConfigUnknownKindReturnsEmpty(t *testing.T) {
	// 채널타입을 인식할 수 없는 경우，차라리 포기하고 싶어요 UI 빈 구성 표시，자격 증명이 포함될 수 있는 원본 콘텐츠를 뱉어내지 마세요.。
	got := MaskConfig("nope", map[string]any{"webhook": "https://x/y?token=LEAK"})
	if len(got) != 0 {
		t.Fatalf("알 수 없는 채널 유형은 빈 구성을 반환해야 합니다.，받았어요 %v", got)
	}
}

func TestMaskConfigDoesNotMutateInput(t *testing.T) {
	// 마스크는 표시 레이어 동작입니다.，라이브러리의 참값을 반대로 바꿀 수는 없습니다.。
	cfg := map[string]any{"webhook": "https://example.com/hook", "secret": "SECtest123456"}
	_ = MaskConfig(KindDingTalk, cfg)
	if IsMasked(cfg["secret"].(string)) {
		t.Fatal("MaskConfig 입력 매개변수를 수정했습니다.，은 마스크 값으로 실제 자격 증명을 덮어쓰게 됩니다.")
	}
}

func TestMergeConfigKeepsStoredOnMaskedIncoming(t *testing.T) {
	stored := map[string]any{"webhook": "https://real/hook", "secret": "REALSECRET", "method": "POST"}
	// 사용자만 변경됨 method，브라우저가 마스크 값을 제출했습니다.+새로운 method。
	incoming := map[string]any{
		"webhook": MaskedValue("https://real/hook"),
		"secret":  MaskedValue("REALSECRET"),
		"method":  "PUT",
	}
	got := MergeConfig(stored, incoming)
	if got["webhook"] != "https://real/hook" || got["secret"] != "REALSECRET" {
		t.Fatalf("마스크 필드는 라이브러리의 원래 값을 유지해야 합니다.，받았어요 %v", got)
	}
	if got["method"] != "PUT" {
		t.Fatalf("수정된 필드가 적용됩니다.，받았어요 %v", got["method"])
	}
}

func TestMergeConfigEmptyStringClears(t *testing.T) {
	stored := map[string]any{"webhook": "https://real/hook", "secret": "REALSECRET"}
	got := MergeConfig(stored, map[string]any{"secret": ""})
	if _, ok := got["secret"]; ok {
		t.Fatalf("빈 문자열은 이 필드를 지워야 합니다.，받았어요 %v", got)
	}
	// 언급되지 않은 필드는 예약되어 있습니다.（로컬 업데이트 의미）。
	if got["webhook"] != "https://real/hook" {
		t.Fatalf("언급되지 않은 필드는 유지되어야 합니다.，받았어요 %v", got)
	}
}

func TestMergeConfigKeepsUnmentionedStoredKeys(t *testing.T) {
	stored := map[string]any{"host": "smtp.example.com", "port": float64(587), "password": "pw"}
	got := MergeConfig(stored, map[string]any{"port": float64(465)})
	if got["host"] != "smtp.example.com" || got["password"] != "pw" {
		t.Fatalf("언급되지 않은 필드는 유지되어야 합니다.，받았어요 %v", got)
	}
	if got["port"] != float64(465) {
		t.Fatalf("언급된 필드를 업데이트해야 합니다.，받았어요 %v", got["port"])
	}
}

// TestPrepareConfigUpdateBlocksDestinationSwap 은 이 패키지의 가장 중요한 보안 불변입니다.：
// **대상 주소 변경 시 기존 자격 증명을 가져올 수 없습니다.**。
//
// 이러한 사용 사례는 정확히 공격 형태의 입력을 사용합니다.（주소만 바꾸세요、자격 증명에 대해 이야기하지 마십시오.），
// 대신「방어논리의 올바른 입력」——후자만 테스트한다면，방어가 안 먹혀도 다 푸르름。
func TestPrepareConfigUpdateBlocksDestinationSwap(t *testing.T) {
	cases := []struct {
		name     string
		kind     string
		stored   map[string]any
		incoming map[string]any
		// wantMissing 은 이름이 지정될 자격 증명 키입니다.。
		wantMissing string
	}{
		{
			name: "일반 Webhook 주소를 변경했는데 그대로 유지하고 싶어요 Authorization 머리",
			kind: KindWebhook,
			stored: map[string]any{
				"url":     "https://legit.example.com/hook",
				"headers": map[string]any{"Authorization": "Bearer REAL-TOKEN"},
			},
			incoming:    map[string]any{"url": "https://attacker.tld/c"},
			wantMissing: "headers",
		},
		{
			name:        "Telegram 변경 base_url 하고싶다 Bot Token 자신의 엔드포인트로 보내기",
			kind:        KindTelegram,
			stored:      map[string]any{"bot_token": "123456:REAL", "chat_id": "1", "base_url": "https://api.telegram.org"},
			incoming:    map[string]any{"base_url": "https://attacker.tld"},
			wantMissing: "bot_token",
		},
		{
			name:        "이메일이 변경되었습니다. SMTP 호스트가 비밀번호를 넘겨주려고 합니다",
			kind:        KindEmail,
			stored:      map[string]any{"host": "smtp.corp.com", "port": 587, "password": "REALPW", "from": "a@b.c", "to": []any{"d@e.f"}},
			incoming:    map[string]any{"host": "smtp.attacker.tld"},
			wantMissing: "password",
		},
		{
			name:        "이메일이 닫혔습니다. TLS 또한 비밀번호를 다시 입력해야 합니다.",
			kind:        KindEmail,
			stored:      map[string]any{"host": "smtp.corp.com", "port": 587, "tls": false, "password": "REALPW", "from": "a@b.c", "to": []any{"d@e.f"}},
			incoming:    map[string]any{"tls": true},
			wantMissing: "password",
		},
		{
			// 마스크 값 = 「기존 자격 증명 유지」，도 거부되어야 합니다.。
			name:        "마스킹된 자격 증명 반환 + 새 주소",
			kind:        KindTelegram,
			stored:      map[string]any{"bot_token": "123456:REAL", "chat_id": "1", "base_url": "https://api.telegram.org"},
			incoming:    map[string]any{"base_url": "https://attacker.tld", "bot_token": MaskedValue("123456:REAL")},
			wantMissing: "bot_token",
		},
		{
			name:        "딩딩수정 Webhook 서명키를 계속 사용하고 싶어요",
			kind:        KindDingTalk,
			stored:      map[string]any{"webhook": "https://oapi.dingtalk.com/robot/send?access_token=OLD", "secret": "REALSEC"},
			incoming:    map[string]any{"webhook": "https://attacker.tld/hook"},
			wantMissing: "secret",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			merged, err := PrepareConfigUpdate(tc.kind, tc.stored, tc.incoming)
			if err == nil {
				t.Fatalf("주소를 변경했지만 자격 증명을 다시 명시하지 않았습니다.，은 거부되어야 합니다；구성 가져오기 %v", merged)
			}
			var target *ErrDestinationChangedWithoutCredentials
			if !errors.As(err, &target) {
				t.Fatalf("인터페이스가 실행 가능한 프롬프트를 제공할 수 있도록 특수 오류 유형이 반환되어야 합니다.，받았어요 %T: %v", err, err)
			}
			found := false
			for _, m := range target.Missing {
				if m == tc.wantMissing {
					found = true
				}
			}
			if !found {
				t.Fatalf("누락된 자격 증명 키의 이름을 지정해야 합니다. %q，받았어요 %v", tc.wantMissing, target.Missing)
			}
			// 오류 메시지는 운영자에게 오류 수정 방법을 안내할 수 있어야 합니다.。
			if !strings.Contains(err.Error(), tc.wantMissing) {
				t.Errorf("오류 메시지에 언급되어야 합니다. %q: %v", tc.wantMissing, err)
			}
		})
	}
}

// TestPrepareConfigUpdateAllowsLegitimateEdits 역 사용 사례：일반 편집은 실수로 막을 수 없습니다，
// 그렇지 않으면 이 보호는「너무 짜증나」및 우회되거나 삭제되었습니다.。
func TestPrepareConfigUpdateAllowsLegitimateEdits(t *testing.T) {
	cases := []struct {
		name     string
		kind     string
		stored   map[string]any
		incoming map[string]any
	}{
		{
			name:     "이름만 바꾸세요（구성을 그대로 반환합니다.）",
			kind:     KindWebhook,
			stored:   map[string]any{"url": "https://legit.example.com/hook", "headers": map[string]any{"Authorization": "Bearer REAL"}},
			incoming: map[string]any{"url": MaskedValue("https://legit.example.com/hook")},
		},
		{
			name:     "요청방식만 변경，주소나 자격 증명이 모두 변경되지 않았습니다.",
			kind:     KindWebhook,
			stored:   map[string]any{"url": "https://legit.example.com/hook", "method": "POST"},
			incoming: map[string]any{"method": "PUT"},
		},
		{
			name:     "주소를 변경하고**동시에**새 자격 증명 제공",
			kind:     KindWebhook,
			stored:   map[string]any{"url": "https://old.example.com/hook", "headers": map[string]any{"Authorization": "Bearer OLD"}},
			incoming: map[string]any{"url": "https://new.example.com/hook", "headers": map[string]any{"Authorization": "Bearer NEW"}},
		},
		{
			name:     "주소를 변경하고 자격 증명이 더 이상 필요하지 않음을 명시적으로 명시합니다.",
			kind:     KindWebhook,
			stored:   map[string]any{"url": "https://old.example.com/hook", "headers": map[string]any{"Authorization": "Bearer OLD"}},
			incoming: map[string]any{"url": "https://new.example.com/hook", "headers": ""},
		},
		{
			name:     "Telegram 변경 chat_id（목적지가 아님）",
			kind:     KindTelegram,
			stored:   map[string]any{"bot_token": "t", "chat_id": "1", "base_url": "https://api.telegram.org"},
			incoming: map[string]any{"chat_id": "-100200"},
		},
		{
			name:     "이메일 수신자 변경（목적지가 아님）",
			kind:     KindEmail,
			stored:   map[string]any{"host": "smtp.corp.com", "port": 587, "password": "PW", "from": "a@b.c", "to": []any{"x@y.z"}},
			incoming: map[string]any{"to": []any{"new@y.z"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			merged, err := PrepareConfigUpdate(tc.kind, tc.stored, tc.incoming)
			if err != nil {
				t.Fatalf("법률편집자가 실수로 차단되었습니다: %v", err)
			}
			if merged == nil {
				t.Fatal("병합된 결과가 반환되어야 합니다.")
			}
		})
	}
}

// TestPrepareConfigUpdatePortTypeTolerance 오해받기 쉬운 부분을 가려라：
// 프런트 엔드에서 제출한 포트는 다음과 같습니다. JSON number（float64），커리도 다시 읽어봤어요 float64，
// 하지만 두 값의 유형이 다를 수 있습니다.（ int vs float64）。사용 == 비교「변화 없음」종료됨「변경됨」，
// 이름만 바꾼 유저가 나오도록「비밀번호를 다시 입력해주세요」——잘못된 경보로 인해 사람들은 더 이상 이 보호 기능을 신뢰하지 않게 됩니다.。
func TestPrepareConfigUpdatePortTypeTolerance(t *testing.T) {
	stored := map[string]any{"host": "smtp.corp.com", "port": float64(587), "password": "PW"}
	// 같은 포트，에게 int 양식 제출。
	if _, err := PrepareConfigUpdate(KindEmail, stored, map[string]any{"port": 587}); err != nil {
		t.Fatalf("포트 값이 동일합니다.（종류만 다릅니다）주소 변경으로 간주되어서는 안 됩니다.: %v", err)
	}
	// 포트가 정말 변경된 경우에는 차단해야 합니다.。
	if _, err := PrepareConfigUpdate(KindEmail, stored, map[string]any{"port": 25}); err == nil {
		t.Fatal("포트 변경을 차단해야 합니다.")
	}
}

// TestPrepareConfigUpdateSurvivesRepeatedSaveWithBlankDestination 재정의「비워둘 수 있습니다.
// 대상 필드」이 경로：Telegram 님 base_url 공식을 사용하려면 공백으로 남겨두세요. API 주소。
//
// 이는 채널이 두 번째 저장부터 영구적으로 저장되지 못하게 하는 데 사용됩니다.：
//
//	새로 만들때 라이브러리에 저장해두세요 base_url:""（생성된 경로는 프런트엔드 제출물에 직접 저장됩니다. config，떠나지 않음 MergeConfig）
//	→ 첫 번째 저장，MergeConfig 빈 문자열을 명시적 지우기로 처리합니다.、delete 이 키를 제거하세요.
//	→ 두 번째 저장，incoming 그래도 ""、그리고 stored 에는 더 이상 이 키가 없습니다.，형을 선고받았습니다「주소가 바뀌었어요」
//	→ bot_token 은 마스크 에코 값입니다. → 400「목적지 주소가 변경되었습니다，자격 증명 필드도 다시 작성해 주세요.」
//
// 사용자가 아무것도 변경하지 않았습니다.，그런데 더 이상 저장이 안되네요，다시 붙여넣지 않는 이상 Bot Token。
func TestPrepareConfigUpdateSurvivesRepeatedSaveWithBlankDestination(t *testing.T) {
	stored := map[string]any{"bot_token": "123:ABC", "chat_id": "-100", "base_url": ""}

	// 프런트엔드 buildConfig() 이 채널의 각 필드 정의에 대한 값을 제출하세요.：자격 증명 백필 마스크，
	// 빈 텍스트 상자가 빈 문자열을 제출합니다.。다음은 출력의 완전한 재현입니다.，그냥 제출하는 대신「키가 변경되었습니다.」。
	submit := func() map[string]any {
		return map[string]any{
			"bot_token": MaskedValue("123:ABC"),
			"chat_id":   "-100",
			"base_url":  "",
		}
	}

	// 첫 번째 저장：채널명만 바뀌었어요，config 그대로 반환。
	merged, err := PrepareConfigUpdate(KindTelegram, stored, submit())
	if err != nil {
		t.Fatalf("첫 번째 저장이 실수로 차단되었습니다.: %v", err)
	}
	if _, ok := merged["base_url"]; ok {
		t.Fatal("전제가 바뀌었다：빈 문자열은 다음과 같아야 합니다. MergeConfig 삭제——이 사용 사례에서 다루는 내용은 정확히「키가 사라진 후」그 걸음")
	}

	// 두 번째 저장：제출하신 내용은 지난번과 똑같습니다.，사용자가 아무것도 변경하지 않았습니다.。
	merged2, err := PrepareConfigUpdate(KindTelegram, merged, submit())
	if err != nil {
		t.Fatalf("실수로 두 번째 저장이 차단되었습니다.（사용자가 아무것도 변경하지 않았습니다.）: %v", err)
	}
	// 세번째，확인되지 않음「실수 딱 하나」하지만 안정적이고 저장이 가능합니다。
	if _, err := PrepareConfigUpdate(KindTelegram, merged2, submit()); err != nil {
		t.Fatalf("세 번째 저장이 실수로 차단되었습니다.: %v", err)
	}
	// 자격 증명은 도중에 보관되어야 합니다.，은 빈 문자열 논리에 의해 지워지지 않습니다.。
	if got := merged2["bot_token"]; got != "123:ABC" {
		t.Fatalf("Bot Token 원래 값을 사용해야 합니다.，받았어요 %v", got)
	}
}

// TestPrepareConfigUpdateStillGuardsBlankDestinationChanges 은 이전 사용 사례와 일치합니다.
// 주장：빈 문자열을 다음과 결합합니다.「키가 존재하지 않습니다.」동등한 것으로 간주，**안돼요**실제 주소 변경도 무시。
// 이 두 방향은 실제 자격 증명 나가는 경로입니다.——Telegram 님 Bot Token 걸어들어가다 URL 경로에，
// 변경됨 base_url 은 다음과 같습니다. Token 새 주소로 보내기。
func TestPrepareConfigUpdateStillGuardsBlankDestinationChanges(t *testing.T) {
	// 방향 1：님으로부터「비어 있음」（공식주소）자체작성주소로 변경。
	official := map[string]any{"bot_token": "123:ABC", "chat_id": "-100"}
	if _, err := PrepareConfigUpdate(KindTelegram, official, map[string]any{
		"bot_token": MaskedValue("123:ABC"),
		"base_url":  "https://tg-proxy.attacker.tld",
	}); err == nil {
		t.Fatal("공식 주소에서 자체 생성 주소로 변경하는 경우에는 다시 작성해야 합니다. Token")
	}

	// 방향 2：직접 만든 주소를 삭제하세요.（= 공식으로 다시 변경 API）주소변경과 동일。
	proxied := map[string]any{"bot_token": "123:ABC", "base_url": "https://proxy.internal/bot"}
	if _, err := PrepareConfigUpdate(KindTelegram, proxied, map[string]any{
		"bot_token": MaskedValue("123:ABC"),
		"base_url":  "",
	}); err == nil {
		t.Fatal("직접 만든 주소 지우기（공식으로 다시 변경 API）주소변경과 동일，리필 요청 필수 Token")
	}
}

func TestDestinationKeysDeclaredForEveryKind(t *testing.T) {
	// 그리고 SecretKeys 같은 이유：채널이 대상 키 선언을 잊어버린 경우，PrepareConfigUpdate 지키지 못해요。
	for kind, ch := range registry {
		if len(ch.DestinationKeys()) == 0 {
			t.Errorf("채널 %s 대상 키가 선언되지 않았습니다.，주소 변경 및 자격 증명 가져오기 보호가 유효하지 않습니다.", kind)
		}
		if len(ch.SecretKeys()) == 0 {
			t.Errorf("채널 %s 자격 증명 키가 선언되지 않았습니다.", kind)
		}
	}
}

func TestSecretKeysDeclaredForEveryKind(t *testing.T) {
	// 컴파일러는 각 채널을 강제로 구현했습니다. SecretKeys，여기에서 다시 확인하세요.「마스크에 채널이 없습니다.
	// 빈칸으로 손을 들어주세요」——빈 조각을 반환하는 채널은 해당 자격 증명이 브라우저에 일반 형식으로 표시된다는 의미입니다.。
	expect := map[string]bool{
		KindDingTalk: true, KindFeishu: true, KindWeCom: true,
		KindWebhook: true, KindTelegram: true, KindEmail: true,
	}
	for kind, ch := range registry {
		if !expect[kind] {
			t.Errorf("채널 %s 마스크 기대치가 테스트에 등록되지 않았습니다.", kind)
			continue
		}
		if len(ch.SecretKeys()) == 0 {
			t.Errorf("채널 %s 자격 증명 필드가 선언되지 않았습니다.，구성이 일반 텍스트로 표시됩니다.", kind)
		}
	}
}

// TestPrepareConfigUpdateRejectsMaskedInContainer 커버리지 감사에서 지적된 공백：
// 마스크 센티넬을 넣어주세요**문자열이 아님**구조（ webhook.headers 은 객체입니다.）시간，
// MergeConfig 인식만 가능「접두사가 포함된 문자열」는 마스크입니다，말 그대로 "__masked__" 은 다음과 같이 간주됩니다.
// 실제 헤더 값은 라이브러리에 저장됩니다.——후속 인증이 자동으로 실패함，오류가 보고되지 않았습니다.。
func TestPrepareConfigUpdateRejectsMaskedInContainer(t *testing.T) {
	stored := map[string]any{
		"url":     "https://legit.example.com/hook",
		"headers": map[string]any{"Authorization": "Bearer REAL"},
	}
	// 개체 내부 동반 마스크 감시자。
	incoming := map[string]any{
		"headers": map[string]any{"Authorization": MaskedPrefix},
	}
	if _, err := PrepareConfigUpdate(KindWebhook, stored, incoming); err == nil {
		t.Fatal("구조물 내부에 탑승한 가면을 쓴 보초는 거부되어야 합니다.（그렇지 않으면 리터럴이 라이브러리에 저장됩니다.）")
	}
	// 종합제출 대상（진정한 새로운 가치）평소대로 받아들이세요。
	ok := map[string]any{"headers": map[string]any{"Authorization": "Bearer NEW"}}
	if _, err := PrepareConfigUpdate(KindWebhook, stored, ok); err != nil {
		t.Fatalf("새로운 요청 헤더의 정상적인 제출을 차단해서는 안 됩니다.: %v", err)
	}
}
