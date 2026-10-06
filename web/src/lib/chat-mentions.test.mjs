import assert from "node:assert/strict";
import test from "node:test";
import { activeMention, mentionSearch, mentionToken, selectedMentions } from "./chat-mentions.ts";

test("mention trigger supports Korean and cursor placement without hijacking email", () => {
  assert.equal(activeMention("user@example.com", 16), null);
  assert.equal(activeMention("선택됨 @[취약점#1 X]", 12), null);
  assert.deepEqual(activeMention("보기@취약점 아래 내용은", 6), { start: 2, end: 6, query: "취약점" });
  assert.equal(activeMention("@취약점\n다음줄", 8), null);
});

test("categories, Korean aliases, IP and keyword search", () => {
  assert.equal(mentionSearch("").categories.length, 9);
  assert.equal(mentionSearch("취약").categories[0].kind, "finding");
  assert.equal(mentionSearch("취약점").kind, "finding");
  assert.equal(mentionSearch("취약점SQL주사").query, "SQL주사");
  assert.equal(mentionSearch("ip 192.0.2.1").kind, "ip");
  assert.equal(mentionSearch("인터페이스 GET /api").query, "GET /api");
  assert.equal(mentionSearch("acme.com").kind, "");
});

test("tokens roundtrip labels and removing one reference preserves its neighbors", () => {
  const first = mentionToken({ kind: "finding", id: 12, label: "제목[1]\n설명" });
  const second = mentionToken({ kind: "ip", id: 13, label: "192.0.2.1" });
  const value = `분석 ${first} 그리고 ${second}`;
  const selected = selectedMentions(value);
  assert.equal(selected.length, 2);
  assert.equal(selected[0].label, "취약점 #12 · 제목（1） 설명");
  const next = value.slice(0, selected[0].start) + value.slice(selected[0].start + selected[0].token.length);
  assert.equal(selectedMentions(next)[0].token, second);
  assert.equal(selectedMentions("@[\u6f0f\u6d1e#12 기존 기록]").length, 1);
});
