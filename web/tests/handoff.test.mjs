import assert from "node:assert/strict";
import { test } from "node:test";
import { handoffHint, handoffSource, handoffSummary, pendingHandoffNote, recordedSource } from "../src/lib/handoff.ts";

const task = (extra = {}) => ({ state: "backlog", handoff: true, afterTaskId: null, afterThreadId: null, afterThreadTitle: null, ...extra });

test("handoff source names the predecessor task, else the session title, else its id", () => {
  assert.equal(handoffSource(task({ afterTaskId: 12 })), "#12");
  assert.equal(handoffSource(task({ afterThreadId: "th-1", afterThreadTitle: "Plan auth" })), "Plan auth");
  assert.equal(handoffSource(task({ afterThreadId: "th-1" })), "th-1");
  assert.equal(handoffSource(task()), null);
});

test("hint names whose context is appended", () => {
  assert.equal(handoffHint({ afterTaskId: 12, afterThreadId: null }), "Appends #12's final reply, PRs, branch and thread link at launch.");
  assert.equal(handoffHint({ afterTaskId: null, afterThreadId: "th-1" }), "Appends the session's final reply, PRs, branch and thread link at launch.");
  assert.equal(handoffHint({ afterTaskId: null, afterThreadId: null }), "Appends the predecessor's final reply, PRs, branch and thread link at launch.");
});

test("backlog note shows only for a waiting backlog task with handoff on", () => {
  assert.equal(pendingHandoffNote(task({ afterTaskId: 12 })), "+ on launch: handoff from #12 (final reply, PRs, branch, thread link)");
  assert.equal(
    pendingHandoffNote(task({ afterThreadId: "th-1", afterThreadTitle: "Plan auth" })),
    "+ on launch: handoff from Plan auth (final reply, PRs, branch, thread link)",
  );
  assert.equal(pendingHandoffNote(task()), null);
  assert.equal(pendingHandoffNote(task({ afterTaskId: 12, handoff: false })), null);
  assert.equal(pendingHandoffNote(task({ afterTaskId: 12, state: "running" })), null);
});

test("recorded source prefers the task number, then the title, then the thread id", () => {
  assert.equal(recordedSource({ sourceTaskId: 12, sourceThreadId: "th-1", sourceTitle: "Build" }), "#12");
  assert.equal(recordedSource({ sourceTaskId: null, sourceThreadId: "th-1", sourceTitle: "Plan auth" }), "Plan auth");
  assert.equal(recordedSource({ sourceTaskId: null, sourceThreadId: "th-1", sourceTitle: "" }), "th-1");
});

test("summary counts the appended characters", () => {
  assert.equal(handoffSummary({ text: "x".repeat(1234) }), "1,234 chars appended to the prompt");
});
