import assert from "node:assert/strict";
import { test } from "node:test";
import { fakeApi } from "./fake-transport.mjs";
import {
  PALETTE_MESSAGE_CAP, PALETTE_SESSION_CAP, matchLabel, mergePaletteSessions, t3SearchNote,
} from "../src/lib/paletteSearch.ts";

const thread = (id, title = id) => ({ t3ThreadId: id, title, projectTitle: "stage", t3DeepLink: `t3code://threads/env/${id}` });
const hit = (id, matchSource, extra = {}) => ({
  t3ThreadId: id, title: `T3 ${id}`, projectTitle: "stage", matchSource,
  snippet: null, messageCreatedAt: null, t3DeepLink: null, thread: null, ...extra,
});
const ids = (rows) => rows.map((r) => r.id);
const many = (n, source) => Array.from({ length: n }, (_, i) => hit(`${source}-${i}`, source));

test("local title matches lead; T3 hits append without reordering them", () => {
  const local = [thread("a"), thread("b")];
  const before = mergePaletteSessions(local, [], new Set());
  const after = mergePaletteSessions(local, [hit("c", "branch"), hit("a", "title"), hit("d", "user", { snippet: "x" })], new Set());
  assert.deepEqual(ids(before.sessions), ["a", "b"]);
  assert.deepEqual(ids(after.sessions), ["a", "b", "c"]);
  assert.deepEqual(ids(after.messages), ["d"]);
  assert.equal(after.sessions[0].source, "title");
  assert.equal(after.sessions[0].thread, local[0]);
  assert.equal(after.sessions[2].source, "branch");
});

test("dedupes by thread id: a local title hit wins over a T3 message hit", () => {
  const { sessions, messages } = mergePaletteSessions([thread("a")], [hit("a", "assistant", { snippet: "said" }), hit("a", "user")], new Set());
  assert.deepEqual(ids(sessions), ["a"]);
  assert.deepEqual(messages, []);
});

test("skips threads already listed as tasks", () => {
  const { sessions, messages } = mergePaletteSessions([thread("a"), thread("b")], [hit("c", "user")], new Set(["a", "c", null]));
  assert.deepEqual(ids(sessions), ["b"]);
  assert.deepEqual(messages, []);
});

test("collapses snippet whitespace and keeps unsynced hits", () => {
  const { messages } = mergePaletteSessions([], [hit("m", "user", { snippet: "## Plan\n\n  the palette  should" })], new Set());
  assert.equal(messages[0].snippet, "## Plan the palette should");
  assert.equal(messages[0].thread, null);
  assert.equal(messages[0].title, "T3 m");
});

test("caps appended T3 rows per group", () => {
  const { sessions, messages } = mergePaletteSessions([], [...many(20, "title"), ...many(20, "assistant")], new Set());
  assert.equal(sessions.length, PALETTE_SESSION_CAP);
  assert.equal(messages.length, PALETTE_MESSAGE_CAP);
});

test("labels why a session matched; title needs no label", () => {
  assert.equal(matchLabel("title"), null);
  assert.equal(matchLabel("branch"), "branch");
  assert.equal(matchLabel("project"), "project");
  assert.equal(matchLabel("pr"), "PR");
  assert.equal(matchLabel("user"), "you said");
  assert.equal(matchLabel("assistant"), "agent said");
});

test("quiet note only when T3 could not search", () => {
  assert.equal(t3SearchNote(undefined), null);
  assert.equal(t3SearchNote("ok"), null);
  assert.equal(t3SearchNote("timeout"), "T3 slow · titles only");
  assert.equal(t3SearchNote("offline"), "T3 offline · titles only");
  assert.equal(t3SearchNote("error"), "T3 offline · titles only");
});

test("searchThreads calls the search endpoint with query, project and limit", async () => {
  const calls = [];
  const { api } = fakeApi(async (url) => {
    calls.push(String(url));
    return new Response(JSON.stringify({ results: [], t3: "ok" }), { headers: { "content-type": "application/json" } });
  });
  assert.deepEqual(await api.searchThreads("what was said", { project: 3, limit: 20 }), { results: [], t3: "ok" });
  assert.equal(calls.length, 1);
  assert.match(calls[0], /^\/threads\/search\?/);
  const params = new URL(calls[0], "http://x").searchParams;
  assert.equal(params.get("q"), "what was said");
  assert.equal(params.get("project"), "3");
  assert.equal(params.get("limit"), "20");
});
