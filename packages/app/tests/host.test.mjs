import assert from "node:assert/strict";
import { test } from "node:test";
import {
  gateView,
  isServerSwitch,
  primaryAction,
  serverStatus,
  serverTestSteps,
  serverWhere,
  stepNote,
  stepStatus,
  visibleSteps,
} from "../src/lib/host.ts";

const base = {
  phase: "starting",
  step: "service",
  title: "Starting Stage",
  message: "",
  fix: "",
  command: "",
  problems: [],
  logTail: [],
  app: { name: "Stage", version: "1.2.0", identity: "stage", apiVersion: 1 },
  server: null,
  foreground: false,
  versionSkew: false,
  target: { id: "local", name: "This machine", kind: "local", url: "" },
  epoch: 0,
  serverEpoch: 0,
  busy: false,
  dev: false,
};
const state = (over) => ({ ...base, ...over });
const steps = (s) => ["service", "server", "api"].map((k) => stepStatus(s, k));

test("starting steps progress in order", () => {
  assert.deepEqual(steps(state({ step: "service" })), ["active", "idle", "idle"]);
  assert.deepEqual(steps(state({ step: "server" })), ["ok", "active", "idle"]);
  assert.deepEqual(steps(state({ step: "api" })), ["ok", "ok", "active"]);
  assert.deepEqual(steps(state({ step: null })), ["idle", "idle", "idle"]);
});

test("failures mark the step that failed", () => {
  assert.deepEqual(steps(state({ phase: "unsupported" })), ["bad", "idle", "idle"]);
  assert.deepEqual(steps(state({ phase: "misconfigured" })), ["bad", "idle", "idle"]);
  assert.deepEqual(steps(state({ phase: "failed" })), ["ok", "bad", "idle"]);
  assert.deepEqual(steps(state({ phase: "conflict" })), ["ok", "bad", "idle"]);
  assert.deepEqual(steps(state({ phase: "incompatible" })), ["ok", "ok", "bad"]);
  assert.deepEqual(steps(state({ phase: "unreachable" })), ["ok", "bad", "idle"]);
  assert.deepEqual(steps(state({ phase: "ready" })), ["ok", "ok", "ok"]);
});

test("step notes name versions once ready", () => {
  const ready = state({ phase: "ready", server: { version: "1.2.0", apiVersion: 1, identity: "stage", publicUrl: null, socket: "/s", managed: true, pid: 4 } });
  assert.equal(stepNote(ready, "server", "ok"), "1.2.0");
  assert.equal(stepNote(ready, "api", "ok"), "v1");
  assert.equal(stepNote(state({}), "api", "active"), "checking");
  assert.equal(stepNote(state({ phase: "unsupported" }), "service", "bad"), "unavailable");
  assert.equal(stepNote(state({ phase: "unreachable" }), "server", "bad"), "unreachable");
});

test("each phase offers one primary recovery", () => {
  assert.equal(primaryAction("stopped"), "start");
  assert.equal(primaryAction("failed"), "start");
  assert.equal(primaryAction("incompatible"), "restart");
  for (const p of ["unsupported", "misconfigured", "conflict", "reconnecting", "unreachable"]) assert.equal(primaryAction(p), "retry");
  assert.equal(primaryAction("starting"), null);
  assert.equal(primaryAction("ready"), null);
});

test("gate: app once ready, blank briefly while starting, screen otherwise", () => {
  assert.equal(gateView(null, false), "app");
  assert.equal(gateView(state({}), false), "blank");
  assert.equal(gateView(state({}), true), "screen");
  assert.equal(gateView(state({ phase: "ready", epoch: 1, serverEpoch: 1 }), false), "app");
  // After the first ready, trouble keeps the app mounted (banner instead).
  assert.equal(gateView(state({ phase: "stopped", epoch: 1, serverEpoch: 1 }), false), "app");
  assert.equal(gateView(state({ phase: "reconnecting", epoch: 2, serverEpoch: 2 }), false), "app");
  assert.equal(gateView(state({ phase: "failed", epoch: 0 }), false), "screen");
  // Another API version blocks even after the app ran.
  assert.equal(gateView(state({ phase: "incompatible", epoch: 3, serverEpoch: 3 }), false), "screen");
});

const studio = { id: "s-1a2b3c4d", name: "Studio", kind: "remote", url: "http://192.168.1.20:7380" };
const remote = (over) => state({ target: studio, step: "server", ...over });

test("gate: a switch starts over on the new server (serverEpoch, not epoch)", () => {
  // The app ran on the old server (epoch 4), the new one was never ready.
  assert.equal(gateView(remote({ epoch: 4, serverEpoch: 0 }), false), "blank");
  assert.equal(gateView(remote({ epoch: 4, serverEpoch: 0 }), true), "screen");
  assert.equal(gateView(remote({ phase: "unreachable", epoch: 4, serverEpoch: 0 }), false), "screen");
  assert.equal(gateView(remote({ phase: "ready", epoch: 5, serverEpoch: 1 }), false), "app");
  // Once ready there, later trouble keeps the app mounted.
  assert.equal(gateView(remote({ phase: "unreachable", epoch: 5, serverEpoch: 1 }), false), "app");
});

test("a remote lists only server and api steps, and offers only Retry", () => {
  assert.deepEqual(visibleSteps(state({})), ["service", "server", "api"]);
  assert.deepEqual(visibleSteps(remote({})), ["server", "api"]);
  assert.deepEqual(["server", "api"].map((k) => stepStatus(remote({ phase: "unreachable" }), k)), ["bad", "idle"]);
  assert.deepEqual(["server", "api"].map((k) => stepStatus(remote({ step: "api" }), k)), ["ok", "active"]);
  for (const p of ["unreachable", "reconnecting", "incompatible"]) assert.equal(primaryAction(p, "remote"), "retry");
  assert.equal(primaryAction("starting", "remote"), null);
  assert.equal(primaryAction("ready", "remote"), null);
  assert.equal(primaryAction("incompatible", "local"), "restart");
});

test("a server switch is a target change after the app ran on the old one", () => {
  const ranLocal = state({ phase: "ready", epoch: 1, serverEpoch: 1 });
  assert.equal(isServerSwitch(ranLocal, remote({ epoch: 1, serverEpoch: 0 })), true);
  // The host's first state replacing the placeholder is not a switch (a launch deep link survives).
  assert.equal(isServerSwitch(state({}), remote({})), false);
  // Same server: readies and outages are not switches.
  assert.equal(isServerSwitch(ranLocal, state({ phase: "reconnecting", epoch: 1, serverEpoch: 1 })), false);
  // Away from a server that never became ready: nothing of it is cached.
  assert.equal(isServerSwitch(remote({ phase: "unreachable", epoch: 1, serverEpoch: 0 }), state({ epoch: 1 })), false);
});

const localEntry = { id: "local", name: "This machine", kind: "local", url: "", active: true, needsInput: 2 };
const studioEntry = { ...studio, active: false, needsInput: null };

test("server rows: only the active server has a known state", () => {
  assert.deepEqual(serverStatus(localEntry, null), { tone: "idle", note: "" });
  assert.deepEqual(serverStatus(localEntry, state({ phase: "ready", serverEpoch: 1 })), { tone: "ok", note: "connected" });
  assert.deepEqual(serverStatus(localEntry, state({})), { tone: "run", note: "connecting" });
  assert.deepEqual(serverStatus(studioEntry, state({ phase: "ready" })), { tone: "idle", note: "" });
  const active = { ...studioEntry, active: true };
  assert.deepEqual(serverStatus(active, remote({ phase: "unreachable" })), { tone: "bad", note: "unreachable" });
  assert.deepEqual(serverStatus(active, remote({ phase: "reconnecting" })), { tone: "run", note: "reconnecting" });
  // A list from before the switch landed: the state's target decides.
  assert.deepEqual(serverStatus(active, state({ phase: "ready" })), { tone: "idle", note: "" });
});

test("server rows name this machine (with its socket while active) or the remote URL", () => {
  const sock = { id: "local", version: "1", apiVersion: 1, identity: "stage", publicUrl: null, socket: "/run/stage.sock", managed: true, pid: 1 };
  assert.equal(serverWhere(localEntry, state({ server: sock })), "this machine · /run/stage.sock");
  assert.equal(serverWhere(localEntry, remote({})), "this machine");
  assert.equal(serverWhere(localEntry, null), "this machine");
  assert.equal(serverWhere(studioEntry, state({})), "http://192.168.1.20:7380");
});

const result = (over) => ({ reachable: true, apiVersion: 1, identity: "stage", version: "1.4.0", error: "", hint: "", command: "", ...over });
const testRows = (t, testing = false) => serverTestSteps(t, testing).map((s) => `${s.key}:${s.status}:${s.note}`);

test("Test connection rows: reachable, api, identity", () => {
  assert.deepEqual(testRows(null), ["reachable:idle:not tested", "api:idle:waiting", "identity:idle:waiting"]);
  assert.deepEqual(testRows(null, true), ["reachable:active:checking", "api:idle:waiting", "identity:idle:waiting"]);
  assert.deepEqual(testRows(result({})), ["reachable:ok:answers", "api:ok:v1", "identity:ok:stage · 1.4.0"]);
  assert.deepEqual(testRows(result({ reachable: false, apiVersion: 0, identity: "", version: "", error: "dial tcp: refused" })), [
    "reachable:bad:no answer",
    "api:idle:-",
    "identity:idle:-",
  ]);
  // The origin policy refused this hostname: something answers, but not as a usable Stage server.
  assert.deepEqual(
    testRows(result({ apiVersion: 0, identity: "", version: "", error: "host not allowed", command: "STAGE_ALLOWED_ORIGINS=http://box.lan:7380" })),
    ["reachable:ok:answers", "api:bad:refused", "identity:idle:-"],
  );
  assert.deepEqual(testRows(result({ apiVersion: 0, identity: "", version: "", error: "not a Stage server" })), [
    "reachable:ok:answers",
    "api:bad:not a Stage server",
    "identity:idle:-",
  ]);
  assert.deepEqual(testRows(result({ apiVersion: 3, error: "it speaks API v3; this app needs v1" })), [
    "reachable:ok:answers",
    "api:bad:v3",
    "identity:ok:stage · 1.4.0",
  ]);
});
