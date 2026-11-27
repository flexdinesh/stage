import assert from "node:assert/strict";
import { test } from "node:test";
import { normalizeT3Status, t3Api } from "../src/harnesses/t3/api.ts";
import { codeReason, disconnectAvailability, firstSyncLanded, primaryAction, savedBrowserUrl, setupSteps, t3Configured, t3DisconnectPrompt, t3SettingsChanges } from "../src/harnesses/t3/onboarding.ts";
import { fakeApi } from "./fake-transport.mjs";

const SYNCED = "2026-11-17T10:00:00Z";

const base = {
  connected: false,
  state: "unpaired",
  t3Url: "http://127.0.0.1:3773",
  publicUrl: "http://127.0.0.1:3773",
  t3UrlSource: "default",
  environmentId: null,
  clientId: null,
  tokenExpiresAt: null,
  adminTokenConfigured: false,
  lastError: null,
  lastFullSyncAt: null,
  reachable: true,
  checkedAt: "2026-11-17T09:59:58Z",
  probeError: null,
  t3: { environmentId: "env-1", label: "controlbox", serverVersion: "0.1.9" },
  autoPair: { available: true, reason: null },
  sync: { running: false, startedAt: null, projects: 0, threads: 0 },
};
const status = (over) => ({ ...base, ...over });
const unreachable = status({ state: "unreachable", reachable: false, t3: null, probeError: "connection refused" });
const connected = status({ state: "connected", connected: true, clientId: "c", tokenExpiresAt: "2026-12-17T00:00:00Z" });
const notes = (s, checking) => setupSteps(s, checking).map((x) => `${x.key}:${x.status}:${x.note}`);

test("first sync refresh fires only on null to set, not on the first load or later syncs", () => {
  const synced = status({ lastFullSyncAt: SYNCED });
  assert.equal(firstSyncLanded(undefined, synced), false);
  assert.equal(firstSyncLanded(status({}), synced), true);
  assert.equal(firstSyncLanded(synced, status({ lastFullSyncAt: "2026-11-17T10:05:00Z" })), false);
  assert.equal(firstSyncLanded(status({}), status({})), false);
});

test("setup steps: unreachable T3", () => {
  assert.deepEqual(notes(unreachable), ["t3:bad:not found at http://127.0.0.1:3773", "pairing:idle:not paired", "first sync:idle:waiting"]);
  assert.deepEqual(notes(unreachable, true)[0], "t3:active:checking", "a retry in flight shows checking");
  assert.deepEqual(notes(status({ ...unreachable, connected: true }))[1], "pairing:ok:paired", "a valid token stays paired while T3 is away");
  assert.deepEqual(notes(status({ ...unreachable, tokenExpiresAt: "2026-11-01T00:00:00Z" }))[1], "pairing:idle:waiting");
  assert.deepEqual(notes(status({ ...unreachable, checkedAt: null }))[0], "t3:active:checking", "never probed yet");
});

test("setup steps: pairing states", () => {
  assert.deepEqual(notes(status({})), ["t3:ok:controlbox · v0.1.9", "pairing:idle:not paired", "first sync:idle:waiting"]);
  assert.equal(notes(status({ state: "expired" }))[1], "pairing:need:expired");
  assert.equal(notes(status({ state: "rejected" }))[1], "pairing:bad:rejected");
  assert.equal(notes(status({ t3: null }))[0], "t3:ok:reachable", "T3 did not identify itself");
  assert.equal(notes(status({ t3: { environmentId: "e", label: "box", serverVersion: "v1.0.0" } }))[0], "t3:ok:box · v1.0.0");
  assert.equal(notes(status({}), true)[0], "t3:ok:controlbox · v0.1.9", "checking only matters while unreachable");
});

test("setup steps: first sync counts tick while it runs", () => {
  const running = (projects, threads) => status({ ...connected, sync: { running: true, startedAt: SYNCED, projects, threads } });
  assert.deepEqual(notes(running(0, 0)), ["t3:ok:controlbox · v0.1.9", "pairing:ok:paired", "first sync:active:starting"]);
  assert.equal(notes(running(12, 776))[2], "first sync:active:12 projects · 776 threads");
  assert.equal(notes(running(1, 1))[2], "first sync:active:1 project · 1 thread");
  assert.equal(notes(status({ ...connected, lastFullSyncAt: SYNCED }))[2], "first sync:ok:done");
});

test("one primary action per state", () => {
  assert.equal(primaryAction(unreachable), "retry");
  assert.equal(primaryAction(status({})), "autoPair");
  assert.equal(primaryAction(status({ autoPair: { available: false, reason: "not_local" } })), "code");
  assert.equal(primaryAction(status({ state: "expired" })), "autoPair");
  assert.equal(primaryAction(status({ state: "rejected", autoPair: { available: false, reason: "cli_not_found" } })), "code");
  assert.equal(primaryAction(connected), "sync", "paired but no sync running: start it");
  assert.equal(primaryAction(status({ ...connected, sync: { running: true, startedAt: SYNCED, projects: 0, threads: 0 } })), null);
  assert.equal(primaryAction(status({ ...connected, lastFullSyncAt: SYNCED })), null);
});

test("code reasons name why auto-pair is unavailable", () => {
  assert.match(codeReason("not_local"), /another machine/);
  assert.match(codeReason("cli_not_found"), /t3 CLI was not found/);
  assert.match(codeReason("fake"), /any code/);
  assert.match(codeReason(null), /pairing code/);
});

test("an older server without onboarding fields gets a derived state and safe defaults", () => {
  const now = Date.parse("2026-11-17T10:00:00Z");
  const {
    state: _state,
    t3UrlSource: _src,
    reachable: _r,
    checkedAt: _c,
    probeError: _p,
    t3: _t3,
    autoPair: _a,
    sync: _s,
    ...legacy
  } = base;
  const s = normalizeT3Status(legacy, now);
  assert.equal(s.state, "unpaired");
  assert.equal(s.reachable, true);
  assert.equal(s.t3, null);
  assert.deepEqual(s.autoPair, { available: false, reason: null });
  assert.deepEqual(s.sync, { running: false, startedAt: null, projects: 0, threads: 0 });
  assert.equal(primaryAction(s), "code", "no auto-pair on an older server");
  assert.equal(normalizeT3Status({ ...legacy, connected: true }, now).state, "connected");
  assert.equal(normalizeT3Status({ ...legacy, tokenExpiresAt: "2026-11-17T09:00:00Z" }, now).state, "expired");
  assert.equal(normalizeT3Status({ ...legacy, tokenExpiresAt: "2026-11-18T09:00:00Z" }, now).state, "unpaired");
  assert.deepEqual(normalizeT3Status(base, now), base, "a current server passes through unchanged");
});

test("T3 is set up once paired: a stored token or a saved admin token; unpaired it is not", () => {
  assert.equal(t3Configured(status({})), false, "never paired");
  assert.equal(t3Configured(status({ t3UrlSource: "settings", t3Url: "http://box:3773" })), false, "a saved URL alone is not set up");
  assert.equal(t3Configured(connected), true);
  assert.equal(t3Configured(status({ adminTokenConfigured: true })), true, "an admin token pairs on its own");
});

test("disconnect shows whenever T3 is set up (token or admin token), never while unpaired", () => {
  assert.equal(disconnectAvailability(status({})), "hidden", "never paired");
  assert.equal(disconnectAvailability(connected), "ready");
  assert.equal(disconnectAvailability(status({ state: "expired", tokenExpiresAt: "2026-11-01T00:00:00Z" })), "ready");
  assert.equal(disconnectAvailability({ ...unreachable, tokenExpiresAt: "2026-12-17T00:00:00Z" }), "ready", "T3 away, token still stored");
  assert.equal(disconnectAvailability({ ...connected, adminTokenConfigured: true }), "ready", "an admin token no longer locks it");
  assert.equal(disconnectAvailability(status({ adminTokenConfigured: true })), "ready", "admin token without a stored token");
});

test("T3 settings send only what changed: URL (empty ignored), Browser URL (empty clears), an entered admin token", () => {
  const draft = { url: base.t3Url, browserUrl: "", adminToken: "" };
  assert.equal(savedBrowserUrl(base), "", "publicUrl equal to the T3 URL means none is saved");
  assert.equal(savedBrowserUrl(status({ publicUrl: "https://t3.tail" })), "https://t3.tail");
  assert.deepEqual(t3SettingsChanges(base, draft), {}, "nothing changed");
  assert.deepEqual(t3SettingsChanges(base, { ...draft, url: " http://box:3773 " }), { url: "http://box:3773" });
  assert.deepEqual(t3SettingsChanges(base, { ...draft, url: "  " }), {}, "an emptied URL is not sent; Reset restores the default");
  assert.deepEqual(t3SettingsChanges(base, { ...draft, browserUrl: " https://t3.tail " }), { publicUrl: "https://t3.tail" });
  assert.deepEqual(t3SettingsChanges(status({ publicUrl: "https://t3.tail" }), { ...draft, browserUrl: "" }), { publicUrl: null }, "emptied Browser URL clears it");
  assert.deepEqual(t3SettingsChanges(base, { ...draft, adminToken: "secret" }), { adminToken: "secret" });
});

const isoDate = (iso) => iso.slice(0, 10);

test("disconnect prompt names the T3, what stays, and the token's validity only while T3 still accepts it", () => {
  const date = isoDate;
  assert.equal(
    t3DisconnectPrompt(connected, date),
    "Disconnect from controlbox? Stage forgets its token, URL, browser URL and admin token and stops syncing. Tasks and bindings stay; pair the same T3 again to resume. It stays listed as disconnected until you remove it. The token stays valid in T3 until 2026-12-17.",
  );
  const expired = status({ state: "expired", t3: null, tokenExpiresAt: "2026-11-01T00:00:00Z" });
  assert.equal(
    t3DisconnectPrompt(expired, date),
    "Disconnect T3? Stage forgets its token, URL, browser URL and admin token and stops syncing. Tasks and bindings stay; pair the same T3 again to resume. It stays listed as disconnected until you remove it.",
  );
});

const reply = (details, replaced) => ({ kind: "t3", name: "T3", connectionId: 1, configured: true, connected: details.connected, state: details.state ?? "unpaired", fields: [], actions: [], details, ...(replaced ? { replaced } : {}) });

test("T3 status reads generic sync progress and results using thread vocabulary", async () => {
  const { sync: _progress, lastError: _error, lastFullSyncAt: _lastSync, ...details } = connected;
  const startedAt = "2026-11-17T09:59:30Z";
  let sync = { running: true, startedAt, projects: 1, sessions: 4 };
  const { api } = fakeApi(async () => Response.json({ ...reply(details), sync }));

  const running = await t3Api(api).status();
  assert.deepEqual(running.sync, { running: true, startedAt, projects: 1, threads: 4 });
  assert.equal(running.lastFullSyncAt, null);
  assert.equal(running.lastError, null);
  assert.equal(notes(running)[2], "first sync:active:1 project · 4 threads");
  assert.equal(primaryAction(running), null);

  sync = { running: false, projects: 0, sessions: 0, lastError: "sync failed", lastFullSyncAt: SYNCED };
  const finished = await t3Api(api).status();
  assert.deepEqual(finished.sync, { running: false, startedAt: null, projects: 0, threads: 0 });
  assert.equal(finished.lastError, "sync failed");
  assert.equal(finished.lastFullSyncAt, SYNCED);
  assert.equal(firstSyncLanded(running, finished), true);
  assert.equal(notes(finished)[2], "first sync:ok:done");
});

test("generic sync clears older T3 details, which still work when sync is absent", async () => {
  const details = {
    ...connected,
    lastError: "old sync error",
    lastFullSyncAt: SYNCED,
    sync: { running: true, startedAt: SYNCED, projects: 2, threads: 5 },
  };
  let sync;
  const { api } = fakeApi(async () => Response.json({ ...reply(details), sync }));

  const legacy = await t3Api(api).status();
  assert.equal(legacy.lastError, details.lastError);
  assert.equal(legacy.lastFullSyncAt, details.lastFullSyncAt);
  assert.deepEqual(legacy.sync, details.sync);

  sync = { running: false, startedAt: null, projects: 0, sessions: 0, lastError: null, lastFullSyncAt: null };
  const current = await t3Api(api).status();
  assert.equal(current.lastError, null);
  assert.equal(current.lastFullSyncAt, null);
  assert.deepEqual(current.sync, { running: false, startedAt: null, projects: 0, threads: 0 });
});

test("disconnect sends DELETE /harnesses/t3/connection and returns the normalized details", async () => {
  const seen = [];
  const { api } = fakeApi(async (path, init) => {
    seen.push({ path, method: init.method, body: init.body });
    return Response.json(reply(base));
  });
  const s = await t3Api(api).disconnect();
  assert.equal(s.state, "unpaired");
  assert.equal(s.tokenExpiresAt, null);
  assert.deepEqual(seen, [{ path: "/harnesses/t3/connection", method: "DELETE", body: undefined }]);
});

test("auto-pair and T3 settings hit their harness endpoints and return the normalized details", async () => {
  const seen = [];
  const replaced = { id: 1, harness: "t3", label: "box a", active: false };
  const { api } = fakeApi(async (path, init) => {
    seen.push({ path, method: init.method, body: init.body });
    return Response.json(reply({ ...base, connected: true, state: "connected" }, path.includes("auto-pair") ? replaced : undefined));
  });
  const paired = await t3Api(api).autoPair();
  assert.equal(paired.state, "connected");
  assert.deepEqual(paired.replaced, replaced, "the replaced connection rides along with the details");
  assert.equal((await t3Api(api).saveSettings({ url: "http://127.0.0.1:4000" })).state, "connected");
  await t3Api(api).saveSettings({ url: null, publicUrl: null, adminToken: "tok" });
  assert.deepEqual(seen, [
    { path: "/harnesses/t3/actions/auto-pair", method: "POST", body: undefined },
    { path: "/harnesses/t3/settings", method: "PUT", body: JSON.stringify({ url: "http://127.0.0.1:4000" }) },
    { path: "/harnesses/t3/settings", method: "PUT", body: JSON.stringify({ url: null, publicUrl: null, adminToken: "tok" }) },
  ]);
});
