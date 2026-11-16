import assert from "node:assert/strict";
import { test } from "node:test";
import { desktopHost } from "../src/platform/host.ts";
import { desktopLinks } from "../src/platform/links.ts";
import { INITIAL_STATE } from "../src/platform/native.ts";
import { desktopNotifications, toPushState } from "../src/platform/notifications.ts";

function fakeNative(over = {}) {
  const stateListeners = new Set();
  const navListeners = new Set();
  const calls = [];
  const native = {
    state: () => Promise.resolve({ ...INITIAL_STATE, phase: "ready", epoch: 1 }),
    onState: (l) => (stateListeners.add(l), () => stateListeners.delete(l)),
    onNavigate: (l) => (navListeners.add(l), () => navListeners.delete(l)),
    takeNavigation: () => Promise.resolve(""),
    retry: async () => void calls.push("retry"),
    startServer: async () => void calls.push("start"),
    restartServer: async () => void calls.push("restart"),
    diagnostics: async () => "report",
    copyText: async (t) => void calls.push(["copy", t]),
    openExternal: async (u) => void calls.push(["external", u]),
    openApp: async (u) => void calls.push(["app", u]),
    loginStartup: async () => true,
    setLoginStartup: async (on) => void calls.push(["login", on]),
    ...over,
  };
  return { native, calls, emit: (s) => stateListeners.forEach((l) => l(s)), navigate: (p) => navListeners.forEach((l) => l(p)) };
}

const tick = () => new Promise((r) => setTimeout(r, 0));

test("host: starts in starting, takes the snapshot, then follows events", async () => {
  const f = fakeNative();
  const host = desktopHost(f.native);
  assert.equal(host.connection.current().phase, "starting");
  const seen = [];
  host.connection.subscribe((s) => seen.push(s.phase));
  await tick();
  assert.equal(host.connection.current().phase, "ready");
  const next = { ...INITIAL_STATE, phase: "reconnecting", epoch: 1 };
  f.emit(next);
  assert.equal(host.connection.current(), next);
  assert.deepEqual(seen, ["ready", "reconnecting"]);
});

test("host: an event before the snapshot wins over the stale snapshot", async () => {
  let resolve;
  const f = fakeNative({ state: () => new Promise((r) => (resolve = r)) });
  const host = desktopHost(f.native);
  f.emit({ ...INITIAL_STATE, phase: "failed" });
  resolve({ ...INITIAL_STATE, phase: "starting" });
  await tick();
  assert.equal(host.connection.current().phase, "failed");
});

test("host: launch deep link and later navigations reach the app", async () => {
  const f = fakeNative({ takeNavigation: () => Promise.resolve("/tasks/12") });
  const host = desktopHost(f.native);
  const paths = [];
  const off = host.onNavigate((p) => paths.push(p));
  await tick();
  f.navigate("/sessions/t-1");
  off();
  f.navigate("/");
  assert.deepEqual(paths, ["/tasks/12", "/sessions/t-1"]);
});

test("host: actions and login startup go to the shell", async () => {
  const f = fakeNative();
  const host = desktopHost(f.native);
  await host.connection.retry();
  await host.connection.startServer();
  await host.connection.restartServer();
  await host.loginStartup.set(false);
  assert.equal(await host.loginStartup.get(), true);
  assert.equal(await host.connection.diagnostics(), "report");
  assert.deepEqual(f.calls, ["retry", "start", "restart", ["login", false]]);
});

function fakeServers() {
  const calls = [];
  const listeners = new Set();
  const local = { id: "local", name: "This machine", kind: "local", url: "", active: true, needsInput: 1 };
  const native = {
    servers: async () => [local],
    addServer: async (input) => (calls.push(["add", input]), { id: "s-1", ...input, kind: "remote", active: false, needsInput: null }),
    updateServer: async (id, input) => (calls.push(["update", id, input]), { id, ...input, kind: "remote", active: false, needsInput: null }),
    removeServer: async (id) => void calls.push(["remove", id]),
    switchServer: async (id) => void calls.push(["switch", id]),
    testServer: async (url) => (calls.push(["test", url]), { reachable: false, apiVersion: 0, identity: "", version: "", error: "refused", hint: "h", command: "" }),
    onServers: (l) => (listeners.add(l), () => listeners.delete(l)),
  };
  return { native, calls, emit: (list) => listeners.forEach((l) => l(list)), listeners };
}

test("host: no servers service without the shell's server calls", () => {
  assert.equal(desktopHost(fakeNative().native).servers, undefined);
});

test("host: servers go to the shell; subscribe follows servers:changed until unsubscribed", async () => {
  const s = fakeServers();
  const { servers } = desktopHost(fakeNative().native, s.native);
  assert.ok(servers);
  assert.deepEqual((await servers.list()).map((e) => e.id), ["local"]);
  const input = { name: "Box", url: "http://10.0.0.2:7380" };
  assert.equal((await servers.add(input)).id, "s-1");
  assert.equal((await servers.update("s-1", { ...input, name: "Big box" })).name, "Big box");
  await servers.switch("s-1");
  await servers.remove("s-1");
  assert.equal((await servers.test("http://x")).error, "refused");
  assert.deepEqual(s.calls, [
    ["add", input],
    ["update", "s-1", { ...input, name: "Big box" }],
    ["switch", "s-1"],
    ["remove", "s-1"],
    ["test", "http://x"],
  ]);
  const seen = [];
  const off = servers.subscribe((list) => seen.push(list.length));
  s.emit([1, 2]);
  off();
  s.emit([1]);
  assert.deepEqual(seen, [2]);
  assert.equal(s.listeners.size, 0);
});

test("host: the initial state targets this machine, before any ready", () => {
  const host = desktopHost(fakeNative({ state: () => new Promise(() => {}) }).native);
  const s = host.connection.current();
  assert.deepEqual(s.target, { id: "local", name: "This machine", kind: "local", url: "" });
  assert.equal(s.serverEpoch, 0);
});

test("links: public URL when the server has one, else the app's deep link, never mygo://", () => {
  const f = fakeNative();
  const server = { version: "1", apiVersion: 1, identity: "stage", publicUrl: null, socket: "/s", managed: true, pid: 1 };
  let state = { ...INITIAL_STATE, app: { ...INITIAL_STATE.app, identity: "stage-dev" }, server };
  const links = desktopLinks(f.native, () => state);
  assert.equal(links.stageLink("/tasks/12"), "stage-dev://tasks/12");
  assert.equal(links.stageLink("/sessions/a%3Ab"), "stage-dev://sessions/a%3Ab");
  state = { ...state, server: { ...server, publicUrl: "https://stage.example.ts.net/" } };
  assert.equal(links.stageLink("/tasks/12"), "https://stage.example.ts.net/tasks/12");
  state = { ...INITIAL_STATE };
  assert.equal(links.stageLink("/tasks/3"), "stage://tasks/3");
});

test("links: opens go through the shell", async () => {
  const f = fakeNative();
  const links = desktopLinks(f.native, () => INITIAL_STATE);
  links.openExternal("https://github.com/o/r/pull/1");
  links.openApp("t3code://threads/e/t");
  await tick();
  assert.deepEqual(f.calls, [["external", "https://github.com/o/r/pull/1"], ["app", "t3code://threads/e/t"]]);
});

function fakeNotifications(over = {}) {
  const calls = [];
  const delivered = new Set();
  const native = {
    notificationState: async () => "on",
    enableNotifications: async () => void calls.push("enable"),
    disableNotifications: async () => void calls.push("disable"),
    testNotification: async () => void calls.push("test"),
    closeNotifications: async (tag) => void calls.push(["close", tag]),
    syncNotifications: async () => void calls.push("sync"),
    openNotificationSettings: async () => false,
    onNotificationDelivered: (l) => (delivered.add(l), () => delivered.delete(l)),
    ...over,
  };
  return { native, calls, deliver: () => delivered.forEach((l) => l()), listeners: delivered };
}

test("notifications: native channel, state names map, unknown reads as unsupported", async () => {
  for (const [name, want] of [["on", "on"], ["off", "off"], ["blocked", "blocked"], ["unsupported", "unsupported"], ["weird", "unsupported"]]) {
    const n = desktopNotifications(fakeNotifications({ notificationState: async () => name }).native);
    assert.equal(n.channel, "native");
    assert.equal(await n.state(), want);
  }
  assert.equal(toPushState(""), "unsupported");
});

test("notifications: controls reach the shell; closeShown() closes all with an empty tag", async () => {
  const f = fakeNotifications();
  const n = desktopNotifications(f.native);
  await n.enable();
  await n.test();
  await n.disable();
  await n.closeShown("task-12");
  await n.closeShown();
  await n.syncShown();
  assert.deepEqual(f.calls, ["enable", "test", "disable", ["close", "task-12"], ["close", ""], "sync"]);
  assert.equal(n.openSystemSettings === undefined ? null : await n.openSystemSettings(), false);
});

const fail = async () => {
  throw new Error("down");
};

test("notifications: closeShown and syncShown never reject; enable errors surface", async () => {
  const n = desktopNotifications(fakeNotifications({ closeNotifications: fail, syncNotifications: fail, enableNotifications: fail }).native);
  await n.closeShown("task-1");
  await n.syncShown();
  await assert.rejects(n.enable(), /down/);
});

test("notifications: onDelivered follows the shell's event until unsubscribed", () => {
  const f = fakeNotifications();
  const n = desktopNotifications(f.native);
  let count = 0;
  const off = n.onDelivered(() => count++);
  f.deliver();
  off();
  f.deliver();
  assert.equal(count, 1);
  assert.equal(f.listeners.size, 0);
});
