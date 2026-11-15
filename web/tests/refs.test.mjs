import assert from "node:assert/strict";
import { test } from "node:test";
import { parseStageRef, refPath, sessionMarkdown, sessionUrl, splitStageRefs, taskMarkdown, taskUrl } from "../src/lib/refs.ts";

const origin = "http://localhost:7380";

test("copied links name themselves and escape brackets in titles", () => {
  assert.equal(taskUrl(origin, 12), "http://localhost:7380/tasks/12");
  assert.equal(taskMarkdown(origin, 12, "Fix [login] flake"), "[stage task #12: Fix \\[login\\] flake](http://localhost:7380/tasks/12)");
  assert.equal(taskMarkdown(origin, 3, "a\\b\n  c"), "[stage task #3: a\\\\b c](http://localhost:7380/tasks/3)");
  assert.equal(sessionUrl(origin, "codex:ab%c"), "http://localhost:7380/sessions/codex%3Aab%25c");
  assert.equal(sessionMarkdown(origin, "t-1", "Plan auth"), "[stage session: Plan auth](http://localhost:7380/sessions/t-1)");
});

test("stage links match by path on any host", () => {
  assert.deepEqual(parseStageRef("http://localhost:7380/tasks/12"), { kind: "task", id: 12 });
  assert.deepEqual(parseStageRef("https://stage.tail.ts.net/tasks/7?view=transcript#answer"), { kind: "task", id: 7 });
  assert.deepEqual(parseStageRef("http://10.0.0.2:7380/sessions/codex%3Aab%25c"), { kind: "session", id: "codex:ab%c" });
  assert.deepEqual(parseStageRef("https://proxy.lan/stage/tasks/9"), { kind: "task", id: 9 });
  for (const bad of ["http://x/tasks/abc", "http://x/tasks/0", "http://x/projects/1", "http://x/", "/tasks/1", "t3code://threads/e/t", "nope", "http://x/sessions/%E0%A4%A"]) {
    assert.equal(parseStageRef(bad), null, bad);
  }
});

test("refPath is the in-app route", () => {
  assert.equal(refPath({ kind: "task", id: 4 }), "/tasks/4");
  assert.equal(refPath({ kind: "session", id: "a:b" }), "/sessions/a%3Ab");
});

test("splitStageRefs pulls markdown and bare stage links out of text", () => {
  const md = taskMarkdown(origin, 12, "Fix [login]");
  const text = `See ${md} and http://phone.lan:7380/sessions/t-1. Also https://github.com/o/r/pull/3 stays.`;
  assert.deepEqual(splitStageRefs(text), [
    { kind: "text", text: "See " },
    { kind: "ref", ref: { kind: "task", id: 12 }, text: md, href: "http://localhost:7380/tasks/12" },
    { kind: "text", text: " and " },
    { kind: "ref", ref: { kind: "session", id: "t-1" }, text: "http://phone.lan:7380/sessions/t-1", href: "http://phone.lan:7380/sessions/t-1" },
    { kind: "text", text: ". Also https://github.com/o/r/pull/3 stays." },
  ]);
  assert.deepEqual(splitStageRefs("no links"), [{ kind: "text", text: "no links" }]);
  assert.deepEqual(splitStageRefs("[docs](https://example.com/a)"), [{ kind: "text", text: "[docs](https://example.com/a)" }]);
});
