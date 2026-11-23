import assert from "node:assert/strict";
import { test } from "node:test";
import { ApiError, normalizeOpenCodeStatus } from "../src/api/client.ts";
import {
  advancedChanges,
  isAuthRequired,
  localNote,
  openCodeChip,
  parseTarget,
  serviceHost,
  serviceText,
  signInText,
  targetReadBack,
} from "../src/lib/opencode.ts";
import { fakeApi } from "./fake-transport.mjs";

const NOW = Date.parse("2026-11-19T12:00:00Z");

const base = {
  connectionId: 2,
  state: "connected",
  checkedAt: "2026-11-19T11:59:00Z",
  endpoint: "http://127.0.0.1:4096",
  browserEndpoint: "",
  webClient: "",
  hasCredentials: false,
  auth: "local",
  local: { available: true, url: "http://127.0.0.1:4096", version: "2.3.0", reason: "" },
};
const status = (over) => ({ ...base, ...over });

test("field: a pairing link reads back as a link to its service", () => {
  const link = parseTarget("  http://192.168.1.2:49374/auth/connect/x53T6-T4sS  ");
  assert.deepEqual(link, { kind: "link", service: "192.168.1.2:49374" });
  assert.equal(targetReadBack(link), "pairing link · service 192.168.1.2:49374 · single use, 5 min");
  assert.equal(parseTarget("https://studio.lan/auth/connect/abc").kind, "link");
});

test("field: a plain http(s) address reads back as an address", () => {
  const address = parseTarget("http://studio.lan:49374");
  assert.deepEqual(address, { kind: "address", service: "studio.lan:49374" });
  assert.equal(targetReadBack(address), "address · studio.lan:49374");
  assert.equal(parseTarget("http://studio.lan:49374/auth/connect/").kind, "address", "no code is not a link");
  assert.equal(parseTarget("http://studio.lan:49374/auth/connect/a/b").kind, "address", "extra segments are not a link");
});

test("field: anything else has no read-back", () => {
  for (const text of ["", "   ", "studio.lan:49374", "ftp://studio.lan/auth/connect/x", "not a url"]) {
    assert.equal(parseTarget(text), null, text);
  }
  assert.equal(targetReadBack(null), null);
});

test("client: pair, connect-local, connect address and disconnect hit the contract", async () => {
  const seen = [];
  const { api } = fakeApi(async (path, init) => {
    seen.push({ path, method: init.method, body: init.body === undefined ? undefined : JSON.parse(init.body) });
    return new Response(null, { status: 204 });
  });
  await api.pairOpenCode("http://192.168.1.2:49374/auth/connect/abc");
  await api.connectLocalOpenCode();
  await api.saveOpenCode({ endpoint: "http://studio.lan:49374" });
  await api.saveOpenCode({ endpoint: "http://studio.lan:49374", username: "opencode", password: "pw" });
  await api.disconnectOpenCode();
  assert.deepEqual(seen, [
    { path: "/harnesses/opencode/actions/pair", method: "POST", body: { link: "http://192.168.1.2:49374/auth/connect/abc" } },
    { path: "/harnesses/opencode/actions/connect-local", method: "POST", body: undefined },
    { path: "/harnesses/opencode/settings", method: "PUT", body: { endpoint: "http://studio.lan:49374" } },
    { path: "/harnesses/opencode/settings", method: "PUT", body: { endpoint: "http://studio.lan:49374", username: "opencode", password: "pw" } },
    { path: "/harnesses/opencode/connection", method: "DELETE", body: undefined },
  ]);
});

test("auth required: only the 409 opencode_auth_required reveals the password", async () => {
  const { api } = fakeApi(async () => Response.json({ error: "OpenCode requires a password.", code: "opencode_auth_required" }, { status: 409 }));
  const error = await api.saveOpenCode({ endpoint: "http://studio.lan:49374" }).catch((e) => e);
  assert.ok(error instanceof ApiError);
  assert.equal(isAuthRequired(error), true);
  assert.equal(isAuthRequired(new ApiError(409, "expired", "opencode_pairing_expired")), false);
  assert.equal(isAuthRequired(new Error("x")), false);
  assert.equal(isAuthRequired(null), false);
});

test("pairing errors keep the server's message and code", async () => {
  const { api } = fakeApi(async () => Response.json({ error: "Pairing link expired.", code: "opencode_pairing_expired" }, { status: 409 }));
  const error = await api.pairOpenCode("http://h:1/auth/connect/x").catch((e) => e);
  assert.equal(error.message, "Pairing link expired.");
  assert.equal(error.code, "opencode_pairing_expired");
});

test("connected rows: service shows the local version only when it is the connected service", () => {
  assert.equal(serviceText(status({})), "http://127.0.0.1:4096 · v2.3.0");
  assert.equal(serviceText(status({ local: { ...base.local, version: "v2.3.0" } })), "http://127.0.0.1:4096 · v2.3.0");
  assert.equal(serviceText(status({ endpoint: "http://studio.lan:4096", auth: "paired" })), "http://studio.lan:4096");
  assert.equal(serviceText(status({ local: { ...base.local, version: "" } })), "http://127.0.0.1:4096");
  assert.equal(serviceText(status({ local: { available: false, url: "", version: "", reason: "missing" } })), "http://127.0.0.1:4096");
});

test("connected rows: sign-in per auth mode", () => {
  assert.equal(signInText(status({ auth: "local" }), NOW), "local service");
  assert.equal(signInText(status({ auth: "paired", expiresAt: "2026-11-26T12:00:00Z" }), NOW), "paired · renews automatically, expires Nov 26");
  assert.equal(signInText(status({ auth: "paired" }), NOW), "paired · renews automatically");
  assert.equal(signInText(status({ auth: "basic" }), NOW), "password");
  assert.equal(signInText(status({ auth: "none" }), NOW), "none");
});

test("chip: states and sign-in lost", () => {
  assert.deepEqual(openCodeChip(status({}), NOW), { label: "connected", tone: "ok" });
  assert.deepEqual(openCodeChip(status({ state: "unconfigured" }), NOW), { label: "not connected", tone: "idle" });
  assert.deepEqual(openCodeChip(status({ state: "unavailable" }), NOW), { label: "unavailable", tone: "bad" });
  assert.deepEqual(openCodeChip(status({ state: "rejected", auth: "paired", expiresAt: "2026-11-19T11:00:00Z" }), NOW), { label: "expired", tone: "need" });
  assert.deepEqual(openCodeChip(status({ state: "rejected", auth: "paired", expiresAt: "2026-11-20T11:00:00Z" }), NOW), { label: "rejected", tone: "need" });
  assert.deepEqual(openCodeChip(status({ state: "rejected", auth: "local" }), NOW), { label: "rejected", tone: "need" });
  assert.deepEqual(openCodeChip(status({ state: "rejected", auth: "basic", expiresAt: "2026-11-19T11:00:00Z" }), NOW), { label: "rejected", tone: "need" });
});

test("sign-in lost names the service host", () => {
  assert.equal(serviceHost("http://192.168.1.2:49374"), "192.168.1.2:49374");
  assert.equal(serviceHost("https://studio.lan"), "studio.lan");
  assert.equal(serviceHost("garbage"), "garbage");
});

test("local note: shown when nothing local can connect, never when the server did not look", () => {
  const local = (over) => status({ state: "unconfigured", local: { available: false, url: "", version: "", ...over } });
  for (const reason of ["missing", "unreachable", "invalid"]) assert.equal(localNote(local({ reason })), "No OpenCode service on this machine.", reason);
  assert.equal(localNote(local({ reason: "" })), null);
  assert.equal(localNote(status({ state: "unconfigured" })), null, "available shows Connect instead");
});

test("advanced: sends only changed, trimmed fields", () => {
  const draft = { endpoint: base.endpoint, browserEndpoint: "", webClient: "" };
  assert.deepEqual(advancedChanges(status({}), draft, null), {}, "nothing changed");
  assert.deepEqual(
    advancedChanges(status({}), { ...draft, browserEndpoint: "  http://box.tail:4096 " }, null),
    { browserEndpoint: "http://box.tail:4096" },
    "browser address alone keeps the endpoint (and so the auth mode)",
  );
  assert.deepEqual(advancedChanges(status({}), { ...draft, endpoint: ` ${base.endpoint} ` }, null), {}, "whitespace is no change");
  assert.deepEqual(advancedChanges(status({}), { ...draft, endpoint: "http://studio.lan:4096" }, null), { endpoint: "http://studio.lan:4096" });
  assert.deepEqual(advancedChanges(status({}), { ...draft, webClient: "http://client" }, null), { webClient: "http://client" });
  assert.deepEqual(advancedChanges(status({}), draft, { username: " opencode ", password: "pw" }), { username: "opencode", password: "pw" });
  assert.deepEqual(advancedChanges(status({}), draft, { username: "", password: "" }), { username: "", password: "" }, "empty pair clears");
});

test("older servers without pairing fields read as Basic or none, with no local service", () => {
  const { auth, local, ...legacy } = base;
  assert.equal(normalizeOpenCodeStatus(legacy).auth, "none");
  assert.equal(normalizeOpenCodeStatus({ ...legacy, hasCredentials: true }).auth, "basic");
  assert.deepEqual(normalizeOpenCodeStatus(legacy).local, { available: false, url: "", version: "", reason: "" });
  assert.deepEqual(normalizeOpenCodeStatus(base), base, "a current server passes through");
  assert.equal(auth, "local");
  assert.equal(local.available, true);
});
