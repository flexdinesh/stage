import assert from "node:assert/strict";
import { test } from "node:test";
import { ApiError, createApi } from "../src/api/client.ts";
import { grantKind, returnToWork } from "../src/lib/accessPaths.ts";
import { observeAccess } from "../src/lib/accessState.ts";
import { createSharedSession } from "../src/lib/sharedSession.ts";

const to = (pathname, search = "", hash = "") => returnToWork({ pathname, search, hash });
const tick = () => new Promise((resolve) => setImmediate(resolve));

test("sign-in restores Stage paths while rejecting authority and auth-route redirects", () => {
  assert.equal(to("/trees/15", "?task=20", "#activity"), "/trees/15?task=20#activity");
  for (const path of [
    "//evil.test", "/\\evil.test", "/\t/evil.test", "https://evil.test", "/work/..//evil.test",
    "/auth/login", "/AUTH/SETUP", "/tasks/../auth/invite",
  ]) assert.equal(to(path), "/", path);
  assert.equal(grantKind("/auth/invite/"), "invite");
  assert.equal(grantKind("/tasks/15"), null);
});

test("a second account starts with an empty cache and cannot receive the prior session's in-flight response", async () => {
  const preferences = new Map([["stage-theme", "dark"], ["stage-scope", "personal-only"]]);
  const closed = [];
  const attention = [];
  let release;
  let requestInit;
  const platform = {
    transport: {
      request: (_path, init) => {
        requestInit = init;
        return new Promise((resolve) => { release = resolve; });
      },
      assetUrl: (path) => path,
      subscribe: (path) => ({ close: () => closed.push(path) }),
    },
    storage: {
      get: (key) => preferences.get(key) ?? null,
      set: (key, value) => preferences.set(key, value),
    },
    attention: { setNeedsInput: (value) => attention.push(value) },
  };
  const owner = createSharedSession(platform, () => {}, () => {});
  const ownerAPI = createApi(owner.platform.transport);
  owner.client.setQueryData(["admin", "invitations"], ["owner-only invitation"]);
  owner.client.setQueryData(["device", "push"], "on");
  owner.platform.storage.set("stage-scope", "owner-project");
  assert.equal(owner.platform.storage.get("stage-theme"), "dark");

  const stream = owner.platform.transport.subscribe("/notifications/stream", { events: {} });
  const pending = ownerAPI.account();
  owner.close();
  stream.close();
  assert.deepEqual(closed, ["/notifications/stream"]);
  assert.deepEqual(attention, [0]);
  assert.equal(requestInit.cache, "no-store");
  assert.equal(requestInit.signal.aborted, true);

  const member = createSharedSession(platform, () => {}, () => {});
  release(Response.json({ id: 1, email: "owner@example.test", role: "admin" }));
  await assert.rejects(pending, { name: "AbortError" });
  assert.equal(owner.client.getQueryData(["admin", "invitations"]), undefined);
  assert.equal(member.client.getQueryData(["admin", "invitations"]), undefined);
  assert.equal(member.client.getQueryData(["device", "push"]), undefined);
  assert.equal(member.platform.storage.get("stage-scope"), null);
  assert.equal(preferences.get("stage-scope"), "personal-only");
  member.close();
});

test("cross-tab identity changes retire the owner view before the member is verified", async (t) => {
  let mode = "personal";
  let account = null;
  let onLifecycle;
  let accountReads = 0;
  let hostResets = 0;
  const views = [];
  const platform = {
    sessionLifecycle: {
      clearPrivateState() { hostResets++; },
      subscribe: (listener) => { onLifecycle = listener; return () => {}; },
    },
    storage: { get: () => null, set() {} },
    attention: { setNeedsInput() {} },
  };
  const api = {
    async accessStatus() { return { mode, installationId: "installation" }; },
    async account() {
      accountReads++;
      if (account === null) throw new ApiError(401, "Sign in to Stage.", "authentication_required");
      return account;
    },
  };
  const observation = observeAccess(api, platform, (view) => views.push(view));
  t.after(() => observation.close());
  await tick();
  assert.equal(views.at(-1).kind, "personal");
  assert.equal(accountReads, 0);

  mode = "setup_pending";
  onLifecycle("resume");
  await tick();
  assert.equal(views.at(-1).kind, "setup_pending");
  assert.equal(accountReads, 0);

  mode = "claimed";
  await observation.check();
  assert.equal(views.at(-1).kind, "signed_out");
  account = { id: 1, role: "admin", email: "owner@example.test", status: "active" };
  await observation.check();
  const owner = views.at(-1);
  assert.equal(owner.account.role, "admin");
  owner.session.client.setQueryData(["admin", "invitations"], "owner-only");

  account = { id: 2, role: "member", email: "member@example.test", status: "active" };
  onLifecycle("changed");
  assert.equal(views.at(-1).kind, "checking");
  assert.equal(hostResets, 1);
  assert.equal(owner.session.client.getQueryData(["admin", "invitations"]), undefined);
  await tick();
  const member = views.at(-1);
  assert.equal(member.account.role, "member");
  assert.notEqual(member.session, owner.session);
  assert.equal(member.session.client.getQueryData(["admin", "invitations"]), undefined);
  onLifecycle("suspend");
  assert.equal(views.at(-1).kind, "checking");
  assert.equal(hostResets, 2);
  account = null;
  onLifecycle("resume");
  await tick();
  assert.equal(views.at(-1).kind, "signed_out");
});

test("a missing status route only falls back for a healthy personal Stage server", async (t) => {
  const views = [];
  const health = { status: "ok", service: "stage", instanceId: "stage-instance" };
  const api = {
    async accessStatus() { throw new ApiError(404, "Not found."); },
    async health() { return health; },
  };
  const observation = observeAccess(api, {}, (view) => views.push(view));
  t.after(() => observation.close());
  await tick();
  assert.equal(views.at(-1).kind, "personal");
  health.sharedAccess = true;
  await observation.check();
  assert.equal(views.at(-1).kind, "error");
});
