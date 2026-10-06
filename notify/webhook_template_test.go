package notify

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// 이 파일은 일반 용도로 잠겨 있습니다. Webhook 템플릿**능력 경계**。
//
// 이것이 유일한 올인클루시브다.「사용자가 제공한 문자열은 코드로 평가됩니다.」장소，그래서 우리는 그것이 무엇을 할 수 있는지 명확하게 해야 합니다.、
// 할 수 없는 일，그리고 테스트를 통해 이러한 속성을 수정하세요.——그렇지 않으면 나중에 누군가 편리하게 템플릿 컨텍스트를 추가할 것입니다.
// 방법、또는 FuncMap 추가하세요 readFile，기능이 자동으로 확장되었습니다.，그리고 diff 닮음
// 그냥 무해한 작은 기능。

// TestTemplateContextHasNoMethods 이 제일 중요해요。
//
// text/template 은 내보내기 메소드를 호출합니다.（{{.Foo}} 필드를 검색하고 메서드를 조정할 수 있습니다.）。따라서 템플릿 컨텍스트
// 닿을 수 있는 한**아무거나**내보낸 메서드로 입력，해당 메소드를 템플릿 작성자에게 노출하는 것과 같습니다.。
// 이 함수의 컨텍스트는 의도적으로 순수 데이터입니다.（필드만 내보내기、제로 방식）。
//
// 이것이 실패하면：누군가가 주었다고 설명해보세요 webhookTemplateData / webhookItem 추가된 방법。
// 발매를 결정하기 전，먼저 노출하고 싶지 않은 내용을 읽기 위해 템플릿에서 해당 메서드를 사용할 수 있는지 생각해 보세요.。
func TestTemplateContextHasNoMethods(t *testing.T) {
	for _, v := range []any{webhookTemplateData{}, webhookItem{}} {
		typ := reflect.TypeOf(v)
		if n := typ.NumMethod(); n != 0 {
			var names []string
			for i := 0; i < n; i++ {
				names = append(names, typ.Method(i).Name)
			}
			t.Fatalf("%s 노출됨 %d 방법（%s）：text/template 전화할 수 있어요，"+
				"템플릿 작성자에게 이러한 방법의 기능을 공개하는 것과 같습니다.", typ.Name(), n, strings.Join(names, ", "))
		}
	}
}

// TestTemplateFuncsAreMinimal 템플릿에 노출된 기능 모음을 잠급니다.。
//
// FuncMap 기능이 추가될 때마다 능력이 하나 더 추가됩니다.。현재만 json / jsons，값을 직렬화하는 기능입니다.
// 쳉 JSON 단편——파일을 읽을 수 없습니다、요청을 보낼 수 없습니다.、명령을 실행할 수 없습니다.。
func TestTemplateFuncsAreMinimal(t *testing.T) {
	var got []string
	for name := range webhookTemplateFuncs {
		got = append(got, name)
	}
	sort.Strings(got)
	want := []string{"json", "jsons"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("템플릿 함수 모음이 변경되었습니다.：받았어요 %v，기대 %v。기능을 추가하기 전에 기능이 확장되지 않는지 확인하십시오."+
			"（파일을 읽거나 쓸 수 없습니다.、네트워크 요청을 시작할 수 없습니다.、명령을 실행할 수 없습니다.）", got, want)
	}
}

// TestTemplateCannotReachUnknownData 템플릿에서 범위를 벗어난 액세스를 재정의합니다.：
// 존재하지 않는 것에 접근하는 것은 실패해야 한다，뭔가를 울리는 대신；그리고 실패 정보가 내부 데이터를 꺼내서는 안 됩니다.。
func TestTemplateCannotReachUnknownData(t *testing.T) {
	_, err := renderWebhookBody(`{"x": {{.Environment}}, "y": {{.Env}}}`, singleMsg())
	if err == nil {
		t.Fatal("존재하지 않는 필드에 액세스하면 오류가 보고되어야 합니다.")
	}
	// 템플릿 컨텍스트의 실제 콘텐츠는 오류에 나타날 수 없습니다.（취약점 제목/요약）。
	for _, leak := range []string{"SQL주사", "매개변수 id"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("템플릿 오류로 인해 메시지 내용이 드러남 %q: %v", leak, err)
		}
	}
}

// TestTemplateRenderFailsPermanently 잘못된 템플릿은 구성 오류입니다.，재시도 자체가 복구되지 않음。
// 유죄 판결을 받은 경우 다시 시도할 수 있습니다.，잘못된 템플릿은 모든 배송에서 세 번의 회피를 헛되이 만들 것입니다.。
func TestTemplateRenderFailsPermanently(t *testing.T) {
	cfg := map[string]any{
		"url":           "https://example.com/hook",
		"body_template": `{{.Items.`,
	}
	if err := (webhookChannel{}).Validate(cfg); err == nil {
		t.Fatal("저장 시 템플릿 구문 오류를 차단해야 합니다.")
	}
	// 인증을 우회해서 직접 전달하더라도，또한 반복적으로 재시도하는 것이 아니라 영구실패로 판단해야 합니다.。
	_, err := (webhookChannel{}).Send(context.Background(), cfg, singleMsg())
	if err == nil || !IsPermanent(err) {
		t.Fatalf("잘못된 템플릿은 영구적으로 실패해야 합니다.，받았어요 %v", err)
	}
}

// TestTemplateCanOnlyProduceJSON 재정의「템플릿 렌더링 결과가 합법적이어야 합니다. JSON」이 제약은。
// 그런데 막았네요「템플릿을 사용하여 다른 프로토콜을 트리거하는 일반 텍스트를 생성합니다.」그런 사용법。
func TestTemplateCanOnlyProduceJSON(t *testing.T) {
	// 법적 템플릿을 통과할 수 있습니다.。
	ok := map[string]any{"url": "https://example.com/hook", "body_template": `{"t":{{json .Title}}}`}
	if err := (webhookChannel{}).Validate(ok); err != nil {
		t.Fatalf("법적 템플릿이 확인을 통과해야 합니다.: %v", err)
	}
	// 은 비 렌더링 JSON 은 거부되어야 합니다（그대로 보내는 대신）。
	bad := map[string]any{"url": "http://127.0.0.1:1/hook", "body_template": `not json {{.Count}}`}
	_, err := (webhookChannel{}).Send(context.Background(), bad, singleMsg())
	if err == nil || !IsPermanent(err) {
		t.Fatalf("은 비 렌더링 JSON 영구실패를 선고해야 한다，받았어요 %v", err)
	}
	if !strings.Contains(err.Error(), "올바른 JSON이 아닙니다") {
		t.Errorf("오류 메시지에는 다음이 명시되어야 합니다. JSON 질문，받았어요 %v", err)
	}
}
