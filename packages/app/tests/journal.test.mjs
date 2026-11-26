import assert from "node:assert/strict";
import { test } from "node:test";
import { ApiError } from "../src/api/client.ts";
import { isNoteCategory, journalEntryId, journalEntryPath, MAX_NOTE_BYTES, noteBytes, noteRequest } from "../src/lib/journal.ts";
import { fakeApi } from "./fake-transport.mjs";

const entry = {
  id: 41, kind: "note", taskId: 7, treeId: null, sourceTaskId: null,
  attempt: null, category: "decision", text: "Keep the record", eventType: "", fromTreeId: null,
  toTreeId: null, createdAt: "2026-11-16T00:00:00Z", truncated: false,
};

test("journal endpoints preserve ownership, provenance and literal filters", async () => {
  const calls = [];
  const { api } = fakeApi(async (path, init) => {
    calls.push({ path, method: init.method, body: init.body === undefined ? undefined : JSON.parse(init.body) });
    return Response.json({ entries: [entry], nextCursor: "next+=" });
  });
  const page = await api.journal({ treeId: 3 }, { kind: "all", category: "finding", q: "%_ literal", taskId: 7, limit: 20, cursor: "next+=" });
  assert.deepEqual(page.entries, [entry]);
  const url = new URL(calls[0].path, "https://stage.test");
  assert.equal(url.pathname, "/trees/3/journal");
  assert.deepEqual(Object.fromEntries(url.searchParams), { kind: "all", category: "finding", q: "%_ literal", taskId: "7", limit: "20", cursor: "next+=" });
  await api.journal({ taskId: 7 });
  assert.equal(calls[1].path, "/tasks/7/journal");
  await api.journalEntry(41);
  assert.equal(calls[2].path, "/journal/41");
  const body = { text: "Keep the record", category: "decision", requestId: "request-1", sourceTaskId: 9, sourceSession: { connectionId: 2, externalSessionId: "session/9" } };
  await api.appendNote({ treeId: 3 }, body);
  assert.deepEqual(calls[3], { path: "/trees/3/journal", method: "POST", body });
  await api.appendNote({ taskId: 7 }, body);
  assert.equal(calls[4].path, "/tasks/7/journal");
});

test("an ambiguous append failure retries the same key and content", async () => {
  const writes = [];
  const { api } = fakeApi(async (_path, init) => {
    writes.push(JSON.parse(init.body));
    if (writes.length === 1) throw new TypeError("connection dropped after commit");
    return Response.json(entry, { status: 201 });
  });
  const first = noteRequest(null, "Keep the record", "decision");
  await assert.rejects(api.appendNote({ taskId: 7 }, first));
  const retry = noteRequest(first, "Keep the record", "decision");
  assert.deepEqual(await api.appendNote({ taskId: 7 }, retry), entry);
  assert.deepEqual(writes[0], writes[1]);
  assert.equal(first.requestId, retry.requestId);
  assert.notEqual(noteRequest(first, "Changed", "decision").requestId, first.requestId);
  assert.notEqual(noteRequest(first, "Keep the record", "finding").requestId, first.requestId);
  assert.notEqual(noteRequest(null, "Keep the record", "decision").requestId, first.requestId);
});

test("journal conflicts expose the status needed to restart stale pagination", async () => {
  const { api } = fakeApi(async () => Response.json({ error: "tree membership changed", code: "stale_cursor" }, { status: 409 }));
  await assert.rejects(api.journal({ treeId: 3 }, { cursor: "old" }), (error) => error instanceof ApiError && error.status === 409);
});

test("note limits measure UTF-8 bytes, including multi-byte text", () => {
  assert.equal(noteBytes("a".repeat(MAX_NOTE_BYTES)), MAX_NOTE_BYTES);
  assert.equal(noteBytes("界".repeat(5461)), MAX_NOTE_BYTES - 1);
  assert.ok(noteBytes("界".repeat(5462)) > MAX_NOTE_BYTES);
  assert.equal(noteBytes("😀"), 4);
});

test("categories are optional and restricted to the four supported meanings", async () => {
  for (const category of ["", "finding", "decision", "blocker", "outcome"]) assert.equal(isNoteCategory(category), true);
  for (const category of ["context", "design", "none", "all", "Decision"]) assert.equal(isNoteCategory(category), false);
  const calls = [];
  const { api } = fakeApi(async (path, init) => { calls.push({ path, body: init.body }); return Response.json({}); });
  await api.journal({ taskId: 7 }, { category: "" });
  await api.appendNote({ taskId: 7 }, { text: "No category", category: "", requestId: "uncategorized-1" });
  assert.equal(calls[0].path, "/tasks/7/journal");
  assert.equal(JSON.parse(calls[1].body).category, "");
});

test("entry links retain the immutable ID and use the task even after moving trees", () => {
  assert.equal(journalEntryPath(entry), "/tasks/7?entry=41#journal-41");
  assert.equal(journalEntryPath({ ...entry, kind: "event", treeId: 3 }), "/tasks/7?entry=41#journal-41");
  assert.equal(journalEntryPath({ ...entry, taskId: null, treeId: 3 }), "/trees/3?view=notes&entry=41#journal-41");
  assert.equal(journalEntryId("41"), 41);
  for (const value of [null, "", "0", "-1", "1.5", "1e3", "9007199254740992", "bad"]) assert.equal(journalEntryId(value), null);
});

test("task creation and adoption use initialNote; schedules retain notes", async () => {
  const bodies = [];
  const { api } = fakeApi(async (_path, init) => { bodies.push(JSON.parse(init.body)); return Response.json({}); });
  await api.createTask({ projectId: 1, title: "Task", prompt: "Work", initialNote: "Initial context" });
  await api.adoptSession({ connectionId: 1, externalSessionId: "thread-1" }, { initialNote: "Tracked context" });
  await api.createSchedule({ projectId: 1, title: "Schedule", prompt: "Work", cron: "0 9 * * *", tz: "UTC", notes: "Template context" });
  assert.equal(bodies[0].initialNote, "Initial context");
  assert.equal(bodies[0].notes, undefined);
  assert.deepEqual(bodies[1], { initialNote: "Tracked context" });
  assert.equal(bodies[2].notes, "Template context");
  assert.equal(bodies[2].initialNote, undefined);
});
