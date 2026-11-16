import assert from "node:assert/strict";
import { test } from "node:test";
import { gateView, primaryAction, stepNote, stepStatus } from "../src/lib/host.ts";

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
  epoch: 0,
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
  assert.deepEqual(steps(state({ phase: "ready" })), ["ok", "ok", "ok"]);
});

test("step notes name versions once ready", () => {
  const ready = state({ phase: "ready", server: { version: "1.2.0", apiVersion: 1, identity: "stage", publicUrl: null, socket: "/s", managed: true, pid: 4 } });
  assert.equal(stepNote(ready, "server", "ok"), "1.2.0");
  assert.equal(stepNote(ready, "api", "ok"), "v1");
  assert.equal(stepNote(state({}), "api", "active"), "checking");
  assert.equal(stepNote(state({ phase: "unsupported" }), "service", "bad"), "unavailable");
});

test("each phase offers one primary recovery", () => {
  assert.equal(primaryAction("stopped"), "start");
  assert.equal(primaryAction("failed"), "start");
  assert.equal(primaryAction("incompatible"), "restart");
  for (const p of ["unsupported", "misconfigured", "conflict", "reconnecting"]) assert.equal(primaryAction(p), "retry");
  assert.equal(primaryAction("starting"), null);
  assert.equal(primaryAction("ready"), null);
});

test("gate: app once ready, blank briefly while starting, screen otherwise", () => {
  assert.equal(gateView(null, false), "app");
  assert.equal(gateView(state({}), false), "blank");
  assert.equal(gateView(state({}), true), "screen");
  assert.equal(gateView(state({ phase: "ready", epoch: 1 }), false), "app");
  // After the first ready, trouble keeps the app mounted (banner instead).
  assert.equal(gateView(state({ phase: "stopped", epoch: 1 }), false), "app");
  assert.equal(gateView(state({ phase: "reconnecting", epoch: 2 }), false), "app");
  assert.equal(gateView(state({ phase: "failed", epoch: 0 }), false), "screen");
  // Another API version blocks even after the app ran.
  assert.equal(gateView(state({ phase: "incompatible", epoch: 3 }), false), "screen");
});
