import assert from "node:assert/strict";
import { test } from "node:test";
import { sessionPromptSource } from "../src/lib/prompt.ts";

const thread = { t3ThreadId: "thread-1" };

test("a session with a stage task shows the task prompt and images", () => {
  const attachments = [{ id: 3, name: "shot.png", mimeType: "image/png", sizeBytes: 10 }];
  const task = { id: 91, prompt: "make stage oss", attachments };
  assert.deepEqual(sessionPromptSource({ thread, task }), { kind: "task", taskId: 91, prompt: "make stage oss", attachments });
});

test("an external session reads its first message from T3", () => {
  assert.deepEqual(sessionPromptSource({ thread, task: null }), { kind: "t3", threadId: "thread-1" });
});

test("an adopted task's session reads its first message from T3 (the task has no prompt)", () => {
  const task = { id: 92, prompt: "", attachments: [], adopted: true };
  assert.deepEqual(sessionPromptSource({ thread, task }), { kind: "t3", threadId: "thread-1" });
});
