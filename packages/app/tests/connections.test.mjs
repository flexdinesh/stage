import assert from "node:assert/strict";
import { test } from "node:test";
import { connectionGate, connectionStatusChip, connectionStatusItem, harnessStatusView, harnessSummary, liveChip, statusRead, statusTargets, unreachableDetail } from "../src/lib/connections.ts";
import { statusRow } from "../src/lib/status.ts";
import { serviceHostText } from "../src/harnesses/opencode/opencode.ts";
import { openCodeErrorText, openCodeRowDetail, openCodeStatusView, openCodeSummary } from "../src/harnesses/opencode/status.ts";
import { t3ErrorText, t3RowDetail, t3StatusView, t3Summary } from "../src/harnesses/t3/status.ts";

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

// ---- Config panel Harness row chip ----

const local = { kind: "local", status: "online", label: "controlbox" };
const worker = (status) => ({ kind: "remote", status, label: "studio" });
const conn = (over = {}) => ({ active: true, configured: true, ...over });

test("connectionGate: no chip for a replaced connection or one not loaded", () => {
  assert.equal(connectionGate(conn({ active: false }), local), null);
  assert.equal(connectionGate(undefined, local), null);
  assert.equal(connectionGate(conn(), undefined), null);
});

test("connectionGate: a worker that cannot answer reads as Settings records it, no Settings link", () => {
  assert.deepEqual(connectionGate(conn(), worker("offline")), { tone: "idle", label: "offline", title: "studio is offline", fixable: false });
  assert.deepEqual(connectionGate(conn(), { ...worker("negotiating"), statusReason: "Waiting for studio to finish connecting." }), { tone: "idle", label: "Negotiating", title: "Waiting for studio to finish connecting.", fixable: false });
  assert.deepEqual(connectionGate(conn(), { ...worker("needs_update"), statusReason: "Update Stage on the main machine." }), { tone: "idle", label: "needs update", title: "Update Stage on the main machine.", fixable: false });
});

test("connectionGate: disconnected links to Settings; a set-up connection on a ready machine is read live", () => {
  assert.deepEqual(connectionGate(conn({ configured: false }), local), { tone: "idle", label: "disconnected", title: "Not connected. Connect it in Settings.", fixable: true });
  assert.equal(connectionGate(conn(), local), "live");
  assert.equal(connectionGate(conn(), worker("online")), "live");
  // The local machine is always ready, whatever its recorded status.
  assert.equal(connectionGate(conn(), { ...local, status: "offline" }), "live");
});

test("liveChip: healthy T3 shows its state, titled with the Settings detail, no link", () => {
  const read = t3();
  const chip = liveChip(t3Summary(read), t3RowDetail(read.data, NOW), t3ErrorText(read));
  assert.equal(chip.tone, "ok");
  assert.equal(chip.label, "connected");
  assert.equal(chip.fixable, false);
  assert.equal(chip.title, "T3 connected\ncontrolbox · v0.1.10 · token expires in 23d");
});

test("liveChip: a T3 problem carries its tone and error and links to Settings", () => {
  const expired = t3({ state: "expired" });
  const e = liveChip(t3Summary(expired), "", t3ErrorText(expired));
  assert.equal(e.tone, "need");
  assert.equal(e.label, "expired");
  assert.equal(e.fixable, true);
  const down = t3({ state: "unreachable", probeError: "dial tcp: refused" });
  const d = liveChip(t3Summary(down), "", t3ErrorText(down));
  assert.equal(d.tone, "bad");
  assert.equal(d.label, "unreachable");
  assert.equal(d.title, "T3 unreachable\ndial tcp: refused");
  assert.equal(d.fixable, true);
});

test("liveChip: OpenCode rejected links to Settings; checking does not", () => {
  const rejected = loaded({ ...ocBase, state: "rejected", reason: "401" });
  const r = liveChip(openCodeSummary(rejected, NOW), "", openCodeErrorText(rejected));
  assert.equal(r.fixable, true);
  assert.match(r.title, /401/);
  const pending = liveChip(t3Summary({ data: undefined, checking: true, error: null }), "", null);
  assert.equal(pending.fixable, false);
  assert.equal(pending.label, "checking");
});

const status = (over) => loaded({ kind: "x", name: "X", connectionId: 9, configured: true, connected: true, state: "connected", fields: [], actions: [], ...over });

test("connection phases pair a human state label with a shared tone", () => {
  assert.deepEqual(connectionStatusChip({ phase: "needs_setup", state: "signed_out" }), { tone: "need", label: "Signed out" });
  assert.deepEqual(connectionStatusChip({ phase: "unavailable", state: "not_installed" }), { tone: "bad", label: "Not installed" });
  assert.deepEqual(connectionStatusChip({ phase: "connected", state: "" }), { tone: "ok", label: "Connected" });
});

test("harnessSummary: a generic harness reads as its Settings row does", () => {
  assert.deepEqual(harnessSummary(status({}), "x", "X"), { harness: "x", state: "connected", tone: "ok", label: "X connected" });
  assert.equal(harnessSummary(status({ connected: false, state: "unavailable" }), "x", "X").tone, "bad");
  assert.equal(harnessSummary(status({ connected: false, configured: false, state: "not connected" }), "x", "X").tone, "idle");
  assert.equal(harnessSummary({ data: undefined, checking: false, error: "boom" }, "x", "X").tone, "bad");
  assert.equal(harnessSummary({ data: undefined, checking: true, error: null }, "x", "X").state, "checking");
});

test("status views: T3 and OpenCode slots carry their summary and panel detail", () => {
  const t = t3StatusView(t3({}), NOW);
  assert.deepEqual(t.summary, t3Summary(t3({})));
  assert.deepEqual(t.detail, { lines: ["controlbox · v0.1.10 · http://127.0.0.1:3773", "synced 1m ago · token expires in 23d"], error: null });
  assert.deepEqual(t3StatusView(failed, NOW).detail, { lines: [], error: "Failed to fetch" });
  const o = openCodeStatusView(oc({ state: "unavailable", reason: "service.json missing" }), NOW);
  assert.equal(o.summary.state, "unavailable");
  assert.equal(o.detail.error, "service.json missing");
  assert.equal(o.detail.lines.length, 2);
});

test("status views: any other kind reads the generic status (reason, else last error, as its detail)", () => {
  const v = harnessStatusView(status({ connected: false, state: "unavailable", reason: "claude not found" }), "claude", "Claude Code");
  assert.deepEqual(v.summary, { harness: "claude", state: "unavailable", tone: "bad", label: "Claude Code unavailable" });
  assert.deepEqual(v.detail, { lines: ["claude not found"], error: null });
  assert.deepEqual(harnessStatusView(status({ lastError: "exit 1" }), "claude", "Claude Code").detail.lines, ["exit 1"]);
  assert.deepEqual(harnessStatusView(status({}), "claude", "Claude Code").detail, { lines: [], error: null });
  assert.deepEqual(harnessStatusView(failed, "claude", "Claude Code").detail, { lines: [], error: "Failed to fetch" });
});

// The status button reads every machine's set-up connections, not just the main machine's.
// The main machine's T3 was disconnected (its Settings row stays, as Disconnected); its OpenCode was never set up.
const statusMain = { id: 1, kind: "local", label: "stage.local", status: "online", lastSeenAt: null, version: "dev", connections: [{ id: 1, harness: "t3", label: "controlbox" }, { id: 2, harness: "opencode", label: "" }] };
const statusWorker = { id: 2, kind: "remote", label: "controlbox", status: "online", lastSeenAt: "2026-01-01T00:00:00Z", version: "dev", connections: [{ id: 4, harness: "t3", label: "controlbox" }] };
const statusConn = (configured, label, tasks) => ({ active: true, configured, label, tasks, bindings: 0 });
const configured = new Map([[1, statusConn(false, "controlbox", 400)], [2, statusConn(false, "", 0)], [4, statusConn(true, "controlbox", 6)]]);

test("statusTargets lists a worker's connection when the main machine has none set up; a disconnected one is not a target", () => {
  const targets = statusTargets([statusMain, statusWorker], (id) => configured.get(id));
  assert.deepEqual(
    targets.map((t) => [t.connectionId, t.harness, t.machine.label]),
    [[4, "t3", "controlbox"]],
  );
});

test("a worker's connected T3 makes the row All connected, in the worker's section", () => {
  const [target] = statusTargets([statusMain, statusWorker], (id) => configured.get(id));
  const item = connectionStatusItem(target, "T3", { tone: "ok", state: "connected", label: "T3 connected" }, true);
  assert.deepEqual(item, { key: "4", group: "controlbox", name: "T3", tone: "ok", state: "connected", label: "T3 connected on controlbox" });
  const summary = statusRow([item], { checking: false, empty: "Connect a harness" });
  assert.deepEqual([summary.kind, summary.text], ["ok", "All connected"]);
});

test("one machine keeps the Harnesses section and unqualified sentence", () => {
  const single = { ...statusMain, connections: [{ id: 1, harness: "t3", label: "" }] };
  const [target] = statusTargets([single], () => ({ configured: true }));
  const item = connectionStatusItem(target, "T3", { tone: "need", state: "expired", label: "T3 pairing expired" }, false);
  assert.deepEqual([item.group, item.label], ["Harnesses", "T3 pairing expired"]);
});

test("an offline or outdated worker's connection is a red problem naming the machine", () => {
  const offline = { ...statusWorker, status: "offline", online: false };
  const [target] = statusTargets([statusMain, offline], (id) => configured.get(id));
  const item = connectionStatusItem(target, "T3", undefined, true);
  assert.deepEqual([item.tone, item.state, item.label], ["bad", "offline", "controlbox offline"]);
  assert.deepEqual(statusRow([item], { checking: false, empty: "" }).text, "controlbox offline");
  const outdated = connectionStatusItem({ ...target, machine: { ...statusWorker, status: "needs_update" } }, "T3", undefined, true);
  assert.deepEqual([outdated.tone, outdated.state, outdated.label], ["bad", "needs update", "Stage update required for controlbox's connection"]);
});

test("negotiating connections and ready connections waiting for a status read count as checking", () => {
  const [target] = statusTargets([statusMain, statusWorker], (id) => configured.get(id));
  const item = connectionStatusItem(target, "T3", undefined, true);
  assert.deepEqual([item.tone, item.state], ["idle", "checking"]);
  const negotiating = connectionStatusItem({ ...target, machine: { ...statusWorker, status: "negotiating" } }, "T3", undefined, true);
  assert.deepEqual([negotiating.tone, negotiating.state], ["idle", "Negotiating"]);
  assert.equal(statusRow([negotiating], { checking: false, empty: "" }).kind, "checking");
});

test("unreachableDetail reports last contact or the server's protocol status reason", () => {
  const now = Date.parse("2026-01-01T00:05:00Z");
  assert.deepEqual(unreachableDetail({ ...statusWorker, status: "offline" }, now), ["seen 5m ago"]);
  assert.deepEqual(unreachableDetail({ ...statusWorker, status: "offline", lastSeenAt: null }, now), ["never seen"]);
  assert.deepEqual(unreachableDetail({ ...statusWorker, status: "negotiating", statusReason: "Waiting for the worker to finish connecting." }, now), ["Waiting for the worker to finish connecting."]);
  assert.deepEqual(unreachableDetail({ ...statusWorker, status: "needs_update", statusReason: "Update Stage on the main machine." }, now), ["Update Stage on the main machine."]);
  assert.deepEqual(unreachableDetail(statusWorker, now), []);
});
