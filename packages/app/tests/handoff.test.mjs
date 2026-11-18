import assert from "node:assert/strict";
import { test } from "node:test";
import { handoffHint, handoffSource, handoffSummary, pendingHandoffNote, recordedSource } from "../src/lib/handoff.ts";

const task = (extra = {}) => ({ state: "backlog", handoff: true, needs: [], ...extra });

test("handoff source lists the needs, or null without any", () => {
  assert.equal(handoffSource(task({ needs: [12] })), "#12");
  assert.equal(handoffSource(task({ needs: [3, 5] })), "#3, #5");
  assert.equal(handoffSource(task()), null);
});

test("hint names whose context is appended", () => {
  assert.equal(handoffHint({ needs: [12] }), "Appends #12's accepted result and available references from Stage at launch.");
  assert.equal(handoffHint({ needs: [3, 5] }), "Appends each need's accepted result and available references from Stage at launch.");
  assert.equal(handoffHint({ needs: [] }), "Appends each need's accepted result and available references from Stage at launch.");
});

test("backlog note shows only for a backlog task with needs and handoff on", () => {
  assert.equal(pendingHandoffNote(task({ needs: [12] })), "+ on launch: handoff from #12 (accepted result and available references)");
  assert.equal(pendingHandoffNote(task({ needs: [3, 5] })), "+ on launch: handoff from #3, #5 (accepted result and available references)");
  assert.equal(pendingHandoffNote(task()), null);
  assert.equal(pendingHandoffNote(task({ needs: [12], handoff: false })), null);
  assert.equal(pendingHandoffNote(task({ needs: [12], state: "running" })), null);
});

test("recorded source prefers the task number, then the title, then the thread id", () => {
  assert.equal(recordedSource({ sourceTaskId: 12, sourceThreadId: "th-1", sourceTitle: "Build" }), "#12");
  assert.equal(recordedSource({ sourceTaskId: null, sourceThreadId: "th-1", sourceTitle: "Plan auth" }), "Plan auth");
  assert.equal(recordedSource({ sourceTaskId: null, sourceThreadId: "th-1", sourceTitle: "" }), "th-1");
});

test("summary counts the appended characters", () => {
  assert.equal(handoffSummary({ text: "x".repeat(1234) }), "1,234 chars appended to the prompt");
});
