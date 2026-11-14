import assert from "node:assert/strict";
import { test } from "node:test";
import { api } from "../src/api/client.ts";
import {
  advanceCursor,
  emptyTranscript,
  newSince,
  parseJson,
  parseTranscriptItem,
  parseTranscriptState,
  transcriptReducer,
  upsertItem,
} from "../src/lib/transcript.ts";

const item = (position, over = {}) => ({
  position,
  itemId: `item-${position}`,
  sourceThreadId: "th",
  inherited: false,
  kind: "assistant",
  status: "completed",
  streaming: false,
  createdBy: "agent",
  runId: "run-1",
  text: `text ${position}`,
  truncated: false,
  updatedAt: "2026-11-14T10:00:00Z",
  ...over,
});
const page = (items, over = {}) => ({ items, hasEarlier: false, cursor: -1, active: false, status: "completed", ...over });
const positions = (model) => model.items.map((it) => it.position);

test("cursor stops before the first streaming item", () => {
  assert.equal(advanceCursor(-1, [item(1), item(4), item(7, { streaming: true }), item(9)]), 4);
  assert.equal(advanceCursor(5, [item(1), item(4)]), 5, "never moves backwards");
  assert.equal(advanceCursor(-1, []), -1);
});

test("tail page sorts, dedupes and advances the REST cursor", () => {
  const m = transcriptReducer(emptyTranscript, { type: "tail", page: page([item(5), item(2), item(9, { streaming: true })], { cursor: 2, active: true, status: "running" }) });
  assert.deepEqual(positions(m), [2, 5, 9]);
  assert.equal(m.cursor, 5);
  assert.equal(m.active, true);
  assert.equal(m.status, "running");
});

test("earlier pages prepend without duplicating loaded items", () => {
  let m = transcriptReducer(emptyTranscript, { type: "tail", page: page([item(10), item(12)], { hasEarlier: true, cursor: 12 }) });
  m = transcriptReducer(m, { type: "earlier", page: page([item(3), item(7), item(10)], { cursor: null, hasEarlier: false }) });
  assert.deepEqual(positions(m), [3, 7, 10, 12]);
  assert.equal(m.hasEarlier, false);
  assert.equal(m.cursor, 12, "earlier pages leave the cursor alone");
});

test("stream upserts replace by position, append, and settle the cursor", () => {
  let m = transcriptReducer(emptyTranscript, { type: "tail", page: page([item(1), item(3, { streaming: true, text: "par" })], { cursor: 1, active: true }) });
  assert.equal(m.cursor, 1);
  m = transcriptReducer(m, { type: "item", item: item(3, { streaming: true, text: "partial", updatedAt: "2026-11-14T10:00:02Z" }) });
  assert.equal(m.items[1]?.text, "partial");
  assert.equal(m.cursor, 1);
  m = transcriptReducer(m, { type: "item", item: item(3, { text: "partial done", updatedAt: "2026-11-14T10:00:04Z" }) });
  m = transcriptReducer(m, { type: "item", item: item(6, { kind: "plan", updatedAt: "2026-11-14T10:00:05Z" }) });
  assert.deepEqual(positions(m), [1, 3, 6]);
  assert.equal(m.cursor, 6);
});

test("an older copy never overwrites a newer one", () => {
  const newer = item(3, { text: "newer", updatedAt: "2026-11-14T10:00:05Z" });
  const older = item(3, { text: "older", updatedAt: "2026-11-14T10:00:01Z" });
  assert.equal(upsertItem([newer], older)[0]?.text, "newer");
  assert.equal(upsertItem([older], newer)[0]?.text, "newer");
  assert.equal(upsertItem([newer], { ...newer, text: "same time" })[0]?.text, "same time", "same updatedAt replaces");
});

test("items older than the loaded window wait behind Load earlier", () => {
  let m = transcriptReducer(emptyTranscript, { type: "tail", page: page([item(20)], { hasEarlier: true, cursor: 20 }) });
  m = transcriptReducer(m, { type: "item", item: item(4) });
  assert.deepEqual(positions(m), [20]);
  m = transcriptReducer(emptyTranscript, { type: "tail", page: page([item(20)], { cursor: 20 }) });
  m = transcriptReducer(m, { type: "item", item: item(4) });
  assert.deepEqual(positions(m), [4, 20], "whole history loaded: insert in order");
});

test("state events set activity and error", () => {
  const m = transcriptReducer(emptyTranscript, { type: "state", state: { active: true, status: "running", error: "T3 read failed" } });
  assert.equal(m.active, true);
  assert.equal(m.error, "T3 read failed");
});

test("SSE payloads are validated, not cast", () => {
  assert.deepEqual(parseTranscriptItem(item(2)), item(2));
  assert.equal(parseTranscriptItem({ ...item(2), kind: "command" }), null);
  assert.equal(parseTranscriptItem({ ...item(2), position: "2" }), null);
  assert.equal(parseTranscriptItem(null), null);
  assert.deepEqual(parseTranscriptState({ active: false, status: "idle", error: null }), { active: false, status: "idle", error: null });
  assert.equal(parseTranscriptState({ active: false, status: "nope", error: null }), null);
  assert.equal(parseJson("{bad"), null);
});

test("new item count since the reader left the bottom", () => {
  assert.equal(newSince([item(1), item(4), item(8)], 1), 2);
  assert.equal(newSince([], 1), 0);
});

test("transcript URLs encode ids as single segments", () => {
  assert.equal(api.transcriptStreamUrl("a/b ?#", -1), "/api/threads/a%2Fb%20%3F%23/transcript/stream?after=-1");
});
