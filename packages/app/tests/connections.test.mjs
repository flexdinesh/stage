import assert from "node:assert/strict";
import { test } from "node:test";
import { openCodeErrorText, openCodeRowDetail, openCodeSummary, statusRead, t3ErrorText, t3RowDetail, t3Summary } from "../src/lib/connections.ts";
import { statusRow } from "../src/lib/status.ts";
import { serviceHostText } from "../src/lib/opencode.ts";

const NOW = Date.parse("2026-11-20T10:00:00Z");

const t3Base = {
  connected: true,
  state: "connected",
  t3Url: "http://127.0.0.1:3773",
  publicUrl: "http://127.0.0.1:3773",
  t3UrlSource: "settings",
  environmentId: "env-1",
  clientId: "c",
  tokenExpiresAt: "2026-12-14T00:00:00Z",
  adminTokenConfigured: false,
  lastError: null,
  lastFullSyncAt: "2026-11-20T09:59:00Z",
  reachable: true,
  checkedAt: "2026-11-20T09:59:58Z",
  probeError: null,
  t3: { environmentId: "env-1", label: "controlbox", serverVersion: "0.1.10" },
  autoPair: { available: true, reason: null },
  sync: { running: false, startedAt: null, projects: 0, threads: 0 },
};
const ocBase = {
  connectionId: 2,
  state: "connected",
  checkedAt: "2026-11-20T09:59:00Z",
  endpoint: "http://127.0.0.1:49374",
  browserEndpoint: "",
  webClient: "",
  hasCredentials: false,
  auth: "local",
  local: { available: true, url: "http://127.0.0.1:49374", version: "2.3.2", reason: "" },
};
const loaded = (data) => ({ data, checking: false, error: null });
const t3 = (over) => loaded({ ...t3Base, ...over });
const oc = (over) => loaded({ ...ocBase, ...over });
const checking = { data: undefined, checking: true, error: null };
const failed = { data: undefined, checking: false, error: "Failed to fetch" };

test("t3: each state maps to its tone and label", () => {
  assert.deepEqual(t3Summary(t3({})), { harness: "t3", state: "connected", tone: "ok", label: "T3 connected" });
  assert.deepEqual(t3Summary(t3({ state: "unpaired", connected: false })), { harness: "t3", state: "unpaired", tone: "need", label: "T3 not paired" });
  assert.equal(t3Summary(t3({ state: "expired" })).tone, "need");
  assert.equal(t3Summary(t3({ state: "unreachable" })).tone, "bad");
  assert.equal(t3Summary(t3({ state: "rejected" })).tone, "bad");
  assert.deepEqual(t3Summary(checking), { harness: "t3", state: "checking", tone: "idle", label: "Checking T3" });
  assert.deepEqual(t3Summary(failed), { harness: "t3", state: "offline", tone: "bad", label: "T3 offline" });
});

test("opencode: states follow the Settings chip; a failed status call is offline", () => {
  assert.deepEqual(openCodeSummary(oc({}), NOW), { harness: "opencode", state: "connected", tone: "ok", label: "OpenCode connected" });
  assert.deepEqual(openCodeSummary(oc({ state: "unavailable" }), NOW), {
    harness: "opencode",
    state: "unavailable",
    tone: "bad",
    label: "OpenCode unavailable",
  });
  assert.equal(openCodeSummary(oc({ state: "rejected", auth: "basic" }), NOW).tone, "need");
  assert.equal(openCodeSummary(oc({ state: "rejected", auth: "paired", expiresAt: "2026-11-19T00:00:00Z" }), NOW).state, "expired");
  assert.deepEqual(openCodeSummary(checking, NOW), { harness: "opencode", state: "checking", tone: "idle", label: "Checking OpenCode" });
  assert.deepEqual(openCodeSummary(failed, NOW), { harness: "opencode", state: "offline", tone: "bad", label: "OpenCode offline" });
});

// The sidebar status row over real harness summaries (lib/status.ts owns the rules).
const row = (...summaries) =>
  statusRow(
    summaries.map((s) => ({ key: s.harness, group: "Harnesses", name: s.harness, tone: s.tone, state: s.state, label: s.label })),
    { checking: false, empty: "Connect a harness" },
  );

test("status row: the worst configured harness drives the tone", () => {
  const ok = t3Summary(t3({}));
  const need = t3Summary(t3({ state: "unpaired" }));
  const bad = openCodeSummary(oc({ state: "unavailable" }), NOW);
  const idle = openCodeSummary(checking, NOW);
  const okOc = openCodeSummary(oc({}), NOW);
  assert.equal(row(ok, okOc).tone, "ok");
  assert.equal(row(ok, bad).tone, "bad", "a broken OpenCode is not hidden by a connected T3");
  assert.equal(row(need, okOc).tone, "need", "an unpaired T3 is not hidden by a connected OpenCode");
  assert.equal(row(need, bad).tone, "bad");
  assert.equal(row(ok, idle).tone, "idle", "checking only outranks ok");
  assert.equal(row(need, idle).tone, "need");
  assert.equal(row(okOc).tone, "ok", "OpenCode alone drives the row when T3 is not configured");
});

test("status row: one problem names its harness; none configured asks to connect one", () => {
  assert.equal(row(t3Summary(t3({})), openCodeSummary(oc({ state: "unavailable" }), NOW)).text, "OpenCode unavailable");
  assert.equal(row(t3Summary(t3({ state: "expired" }))).text, "T3 pairing expired");
  assert.equal(row().text, "Connect a harness");
});

test("error lines: the reason for trouble, else the failed fetch, else none", () => {
  assert.equal(t3ErrorText(t3({})), null);
  assert.equal(t3ErrorText(t3({ state: "unreachable", probeError: "connection refused", lastError: "old" })), "connection refused");
  assert.equal(t3ErrorText(t3({ lastError: "sync failed" })), "sync failed");
  assert.equal(t3ErrorText(failed), "Failed to fetch");
  assert.equal(openCodeErrorText(oc({})), null);
  assert.equal(openCodeErrorText(oc({ state: "connected", reason: "stale" })), null);
  assert.equal(openCodeErrorText(oc({ state: "unavailable", reason: "dial tcp: connection refused" })), "dial tcp: connection refused");
  assert.equal(openCodeErrorText(oc({ state: "rejected" })), null);
  assert.equal(openCodeErrorText(failed), "Failed to fetch");
});

test("service host: host:port with the local version only when it is the one connected", () => {
  assert.equal(serviceHostText({ ...ocBase }), "127.0.0.1:49374 · v2.3.2");
  assert.equal(serviceHostText({ ...ocBase, endpoint: "http://studio.lan:4096", auth: "paired" }), "studio.lan:4096");
});

test("row detail: which T3 and when its token expires; falls back to the T3 URL before T3 answers", () => {
  assert.equal(t3RowDetail(t3Base, NOW), "controlbox · v0.1.10 · token expires in 23d");
  assert.equal(t3RowDetail({ ...t3Base, state: "expired", tokenExpiresAt: "2026-11-18T10:00:00Z" }, NOW), "controlbox · v0.1.10 · token expired 2d ago");
  assert.equal(t3RowDetail({ ...t3Base, t3: null, tokenExpiresAt: null }, NOW), "http://127.0.0.1:3773");
});

test("row detail: OpenCode's host and how Stage signs in", () => {
  assert.equal(openCodeRowDetail(ocBase, NOW), `${serviceHostText(ocBase)} · local service`);
  assert.equal(openCodeRowDetail({ ...ocBase, endpoint: "" }, NOW), "local service");
});

test("statusRead: only the first data-less fetch counts as checking", () => {
  assert.deepEqual(statusRead({ data: undefined, isPending: true, errorUpdateCount: 0, error: null }), { data: undefined, checking: true, error: null });
  assert.deepEqual(statusRead({ data: undefined, isPending: true, errorUpdateCount: 1, error: new Error("down") }), { data: undefined, checking: false, error: "down" });
});
