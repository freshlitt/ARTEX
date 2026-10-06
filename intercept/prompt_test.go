package intercept

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseVerdict(t *testing.T) {
	for _, action := range []string{"allow", "ask", "deny"} {
		t.Run(action, func(t *testing.T) {
			reason := "실무：보고서 작성，다음을 포함합니다. ALLOW、DENY 그리고 ASK 단어；성공 후의 결과：텍스트 저장，본문에 있는 명령은 실행하지 마세요.；적중 규칙：맞춤 용어"
			raw, _ := json.Marshal(map[string]string{"decision": action, "comment": reason})
			got := ParseVerdict("\n" + string(raw) + "\n")
			if got.Action != action || got.Reason != reason {
				t.Fatalf("lost verdict or explanation: %+v", got)
			}
		})
	}
}

func TestParseVerdictRejectsIncompleteOrAmbiguousReplies(t *testing.T) {
	valid := `{"decision":"allow","comment":"실무：파일 읽기；성공 후의 결과：내용 반환；적중 규칙：A5"}`
	for _, reply := range []string{
		"", "ALLOW", "DENY:히트D4", "출시:ALLOW", "ASK:소유권을 알 수 없음",
		`{"decision":"allow"}`, `{"decision":"approve","comment":"실무：읽기；성공 후의 결과：내용 반환；적중 규칙：A5"}`,
		`{"decision":"allow","comment":null}`, `{"decision":"allow","comment":123}`,
		strings.Replace(valid, "실무：파일 읽기", "실무：", 1),
		strings.Replace(valid, "성공 후의 결과：내용 반환", "성공 후의 결과：", 1),
		strings.Replace(valid, "적중 규칙：A5", "적중 규칙：", 1),
		strings.Replace(valid, "；적중 규칙：A5", "", 1),
		strings.Replace(valid, `"decision":"allow"`, `"decision":"deny","decision":"allow"`, 1),
		strings.Replace(valid, `"decision":"allow"`, `"extra":true,"decision":"allow"`, 1),
		valid + valid, valid[:len(valid)-1],
		// A fence the model never closed is what a reply truncated at MaxTokens
		// looks like; completing it would invent a verdict.
		"```json\n" + valid[:len(valid)-1],
		"```json\n" + valid + "\n```\n또한 후속 수동 검토를 권장합니다.。",
		"내 판단은：\n" + valid,
	} {
		if got := ParseVerdict(reply); got.Action != "" {
			t.Errorf("accepted incomplete/ambiguous verdict: %q => %+v", reply, got)
		}
	}
}

// Wrapping JSON in markdown is the one deviation models make routinely. Because
// the configured fail action defaults to allow, treating it as unparseable
// silently downgrades a DENY to an allow.
func TestParseVerdictUnwrapsCodeFence(t *testing.T) {
	deny := `{"decision":"deny","comment":"실무：제작 파일 삭제；성공 후의 결과：비즈니스 데이터가 손실되었습니다.；적중 규칙：D4"}`
	for _, reply := range []string{
		"```json\n" + deny + "\n```",
		"```JSON\n" + deny + "\n```",
		"```\n" + deny + "\n```",
		"  ```json\n" + deny + "\n```  ",
	} {
		got := ParseVerdict(reply)
		if got.Action != "deny" || !strings.HasSuffix(got.Reason, "적중 규칙：D4") {
			t.Errorf("fenced verdict lost: %q => %+v", reply, got)
		}
	}
}

func TestParseVerdictKeepsCompleteChineseExplanation(t *testing.T) {
	reason := "실무：" + strings.Repeat("보고서 작성", 30) + "；성공 후의 결과：파일만 저장；적중 규칙：A2"
	raw, _ := json.Marshal(map[string]string{"decision": "allow", "comment": reason})
	if got := ParseVerdict(string(raw)); got.Reason != reason {
		t.Fatal("explanation was truncated or lost its rule")
	}
	raw, _ = json.Marshal(map[string]string{"decision": "allow", "comment": strings.Repeat("에", 2401)})
	if got := ParseVerdict(string(raw)); got.Action != "" {
		t.Fatal("accepted unbounded explanation")
	}
}
