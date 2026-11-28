import assert from "node:assert/strict";
import { test } from "node:test";
import { composerDelivery, isSendKey, MAX_MESSAGE_BYTES, sendable, sessionMessage } from "../src/lib/sessionComposer.ts";

const clear = { ready: true, count: 0 };
const offered = { available: true };
const ops = { send: offered, steer: offered, queue: offered };
const modes = (state, operations = ops, requests = clear) => composerDelivery(state, operations, requests, "Fixture");

test("running exposes only native delivery modes with Steer as primary", () => {
  assert.deepEqual(modes("running").buttons, ["steer", "queue"]);
  assert.deepEqual(modes("running", {
    queue: offered, send: offered, steer: { available: false, reason: "No turn to steer." },
  }), { buttons: ["queue"], reasons: ["No turn to steer."] });
  assert.deepEqual(modes("running", { send: offered }).buttons, []);
});

test("ended turns send, and a needs-input reply waits for known requests", () => {
  for (const state of ["done", "idle", "needs_input"]) assert.deepEqual(modes(state).buttons, ["send"]);
  for (const requests of [{ ready: false, count: 0 }, { ready: true, count: 1 }, { ...clear, unlisted: "Approve in Fixture." }]) {
    assert.deepEqual(modes("needs_input", ops, requests).buttons, []);
  }
  for (const state of ["launching", "failed", "interrupted", "cancelled", "backlog"]) {
    assert.deepEqual(modes(state).buttons, []);
  }
  assert.deepEqual(modes("done", { send: { available: false, reason: "Resume the session." } }).reasons, ["Resume the session."]);
});

test("only a Send reply guards the needs-input revision", () => {
  assert.deepEqual(sessionMessage("answer", "send", "needs_input", 7), {
    text: "answer", delivery: "send", expectedStateRevision: 7,
  });
  assert.deepEqual(sessionMessage("next", "send", "done", 7), { text: "next", delivery: "send" });
  assert.deepEqual(sessionMessage("next", "queue", "running", 7), { text: "next", delivery: "queue" });
});

test("the limit is UTF-8 bytes and shortcuts leave normal Enter and IME alone", () => {
  const exact = "é".repeat(MAX_MESSAGE_BYTES / 2);
  assert.equal(sendable(exact), true);
  assert.equal(sendable(exact + "x"), false);
  assert.equal(sendable(" \n\t"), false);
  for (const [ctrlKey, metaKey, isComposing, expected] of [
    [true, false, false, true], [false, true, false, true], [false, false, false, false], [true, false, true, false],
  ]) {
    assert.equal(isSendKey({ key: "Enter", ctrlKey, metaKey, isComposing }), expected);
  }
});
