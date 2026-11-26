import assert from "node:assert/strict";
import { test } from "node:test";
import { MAX_MESSAGE_BYTES, busyHint, followUpView, isSendKey, mayFollowUp, replyPrompt, sendable, tooLong } from "../src/lib/followup.ts";

const task = (state, more = {}) => ({ state, externalSessionId: "3923bfbc-e1ce-4823-a36c-cc0922208074", connectionActive: true, ...more });
const offered = { offered: true, requestsSettled: true, requestCount: 0 };

test("a finished session takes a follow-up under Last word", () => {
  assert.deepEqual(followUpView(task("done"), offered), { placement: "after-reply", title: "Follow up", busy: false });
});

test("a turn that ended asking you leads with Reply", () => {
  assert.deepEqual(followUpView(task("needs_input"), offered), { placement: "lead", title: "Reply", busy: false });
});

test("open requests, or requests not read yet, leave needs input to the Requests panel", () => {
  assert.equal(followUpView(task("needs_input"), { ...offered, requestCount: 1 }), null);
  assert.equal(followUpView(task("needs_input"), { ...offered, requestsSettled: false }), null);
});

test("a working session shows the composer with Send waiting", () => {
  for (const state of ["running", "launching"]) {
    assert.deepEqual(followUpView(task(state), offered), { placement: "after-reply", title: "Follow up", busy: true }, state);
  }
});

test("states that can't take a message have no composer", () => {
  for (const state of ["backlog", "failed", "interrupted", "cancelled", "settled"]) {
    assert.equal(followUpView(task(state), offered), null, state);
    assert.equal(mayFollowUp(state), false, state);
  }
  for (const state of ["needs_input", "done", "running", "launching"]) assert.equal(mayFollowUp(state), true, state);
});

test("no composer without send, on a retired connection, or without a session", () => {
  assert.equal(followUpView(task("done"), { ...offered, offered: false }), null);
  assert.equal(followUpView(task("done", { connectionActive: false }), offered), null);
  assert.equal(followUpView(task("done", { externalSessionId: null }), offered), null);
});

test("copy names the harness", () => {
  assert.equal(replyPrompt("Claude Code"), "Claude Code is waiting for your reply.");
  assert.equal(busyHint("Claude Code"), "Claude Code is working. Send once it finishes.");
});

test("a message is sendable when nonblank and within 32 KiB of UTF-8", () => {
  assert.equal(sendable(""), false);
  assert.equal(sendable("  \n "), false);
  assert.equal(sendable("-teal"), true);
  assert.equal(sendable("a".repeat(MAX_MESSAGE_BYTES)), true);
  assert.equal(tooLong("a".repeat(MAX_MESSAGE_BYTES + 1)), true);
  // Bytes, not characters: 3 bytes each.
  assert.equal(tooLong("€".repeat(Math.floor(MAX_MESSAGE_BYTES / 3) + 1)), true);
});

test("Ctrl or Cmd with Enter sends; Enter alone is a newline", () => {
  assert.equal(isSendKey({ key: "Enter", ctrlKey: true, metaKey: false }), true);
  assert.equal(isSendKey({ key: "Enter", ctrlKey: false, metaKey: true }), true);
  assert.equal(isSendKey({ key: "Enter", ctrlKey: false, metaKey: false }), false);
  assert.equal(isSendKey({ key: "a", ctrlKey: true, metaKey: false }), false);
});
