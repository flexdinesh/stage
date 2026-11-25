import assert from "node:assert/strict";
import { test } from "node:test";
import { ApiError } from "../src/api/client.ts";
import {
  allAnswered,
  answeredText,
  answersResponse,
  draftOf,
  emptyDraft,
  expiredText,
  optionNote,
  optionResponse,
  pick,
  pickOther,
  previewLines,
  requestSummary,
  requestsPath,
  respondFailure,
  rowRequestAction,
} from "../src/lib/requests.ts";

const opt = (id, label = id, available = true) => ({ id, label, available });
const approval = (id, options = [opt("allow", "Allow"), opt("always:0", "Always allow `Bash(npm test)` in this project"), opt("deny", "Deny")]) => ({
  id,
  kind: "approval",
  title: "Run a command",
  preview: "npm test\n",
  options,
  textOption: "deny",
  createdAt: "2026-11-25T00:00:00Z",
});
const question = { id: "q", kind: "question", title: "Claude Code asks", questions: [], createdAt: "2026-11-25T00:00:00Z" };

test("row: one approval answers inline with its first (proceed) and last (decline) option", () => {
  const a = rowRequestAction([approval("r1")]);
  assert.equal(a?.kind, "inline");
  assert.equal(a.proceed.id, "allow");
  assert.equal(a.decline?.id, "deny");
});

test("row: a single-option approval has no decline", () => {
  const a = rowRequestAction([approval("r1", [opt("ok", "OK")])]);
  assert.equal(a?.kind, "inline");
  assert.equal(a.decline, null);
});

test("row: a question, several requests or an approval without options link to the task", () => {
  assert.deepEqual(rowRequestAction([question]), { kind: "answer", count: 1 });
  assert.deepEqual(rowRequestAction([approval("a"), approval("b")]), { kind: "answer", count: 2 });
  assert.deepEqual(rowRequestAction([approval("a", [])]), { kind: "answer", count: 1 });
  assert.equal(rowRequestAction([]), null);
});

test("summary: title then the preview's first non-blank line", () => {
  assert.equal(requestSummary({ title: "Run a command", preview: "\n  touch a && echo b\nmore" }), "Run a command · touch a && echo b");
  assert.equal(requestSummary({ title: "Edit a file" }), "Edit a file");
  assert.equal(requestSummary({ title: "Edit a file", preview: "  \n" }), "Edit a file");
});

test("preview: long past eight lines, trailing newlines ignored", () => {
  assert.deepEqual(previewLines("a\nb\n\n"), { lines: ["a", "b"], long: false });
  assert.equal(previewLines(Array.from({ length: 8 }, () => "x").join("\n")).long, false);
  assert.equal(previewLines(Array.from({ length: 9 }, () => "x").join("\n")).long, true);
});

test("option response carries text only when not blank", () => {
  assert.deepEqual(optionResponse("deny"), { option: "deny" });
  assert.deepEqual(optionResponse("deny", "   "), { option: "deny" });
  assert.deepEqual(optionResponse("deny", " use a temp dir "), { option: "deny", text: "use a temp dir" });
});

test("single select: picking replaces, Other drops the pick", () => {
  const q = { id: "0", prompt: "Color?", options: [{ label: "Red" }, { label: "Blue" }], custom: true };
  let d = pick(q, emptyDraft(), "Red");
  d = pick(q, d, "Blue");
  assert.deepEqual(d.picked, ["Blue"]);
  d = pickOther(q, d);
  assert.deepEqual(d, { picked: [], otherOn: true, other: "" });
  d = pick(q, { ...d, other: "teal" }, "Red");
  assert.equal(d.otherOn, false);
});

test("multi select toggles picks and Other independently", () => {
  const q = { id: "0", prompt: "Which?", multi: true, custom: true };
  let d = pick(q, emptyDraft(), "A");
  d = pick(q, d, "B");
  d = pick(q, d, "A");
  d = pickOther(q, d);
  assert.deepEqual(d, { picked: ["B"], otherOn: true, other: "" });
  assert.equal(pickOther(q, d).otherOn, false);
});

test("every question needs an answer; blank Other does not count", () => {
  const qs = [
    { id: "0", prompt: "Color?", options: [{ label: "Red" }], custom: true },
    { id: "1", prompt: "Size?", options: [{ label: "S" }, { label: "M" }], multi: true },
  ];
  assert.equal(allAnswered(qs, {}), false);
  assert.equal(allAnswered(qs, { 0: { picked: [], otherOn: true, other: "  " }, 1: { picked: ["S"], otherOn: false, other: "" } }), false);
  const draft = { 0: { picked: [], otherOn: true, other: " teal " }, 1: { picked: ["S", "M"], otherOn: false, other: "" } };
  assert.equal(allAnswered(qs, draft), true);
  assert.deepEqual(answersResponse(qs, draft), { answers: { 0: ["teal"], 1: ["S", "M"] } });
  assert.equal(allAnswered([], {}), false);
});

test("Other text is ignored where free answers are not allowed", () => {
  const q = { id: "0", prompt: "Pick", options: [{ label: "A" }] };
  assert.equal(allAnswered([q], { 0: { picked: [], otherOn: true, other: "x" } }), false);
  assert.deepEqual(draftOf({}, "0"), emptyDraft());
});

test("a 404 means answered elsewhere or expired; anything else is an error to show", () => {
  assert.equal(respondFailure(new ApiError(404, "request \"r\" is no longer pending")), "expired");
  assert.equal(respondFailure(new ApiError(400, "bad")), "error");
  assert.equal(respondFailure(new Error("network")), "error");
});

test("copy and anchor", () => {
  assert.equal(answeredText("Claude Code"), "Answered · waiting for Claude Code");
  assert.equal(expiredText("Claude Code"), "Already answered in Claude Code or expired");
  assert.equal(requestsPath(12), "/tasks/12#requests");
});

test("an option's note hides a blank description or one repeating its label", () => {
  assert.equal(optionNote({ label: "Red", description: "Red" }), null);
  assert.equal(optionNote({ label: "Red", description: " red " }), null);
  assert.equal(optionNote({ label: "Red" }), null);
  assert.equal(optionNote({ label: "Red", description: "Choose red" }), "Choose red");
});
