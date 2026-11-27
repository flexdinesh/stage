import { test } from "node:test";
import assert from "node:assert/strict";
import { OPERATION_LABELS, capabilityLines, launchParts, newTaskConnection, optionsFor, prefillConfig } from "../src/lib/capabilities.ts";

test("options are the harness-wide ones, then the model's own", () => {
  const sandbox = { id: "sandbox", values: ["read-only", "workspace"] };
  const effort = { id: "effort", values: ["low", "high"] };
  assert.deepEqual(optionsFor({ options: [sandbox] }, { options: [effort] }), [sandbox, effort]);
  assert.deepEqual(optionsFor({ options: [sandbox] }, undefined), [sandbox]);
  assert.deepEqual(optionsFor({}, { options: null }), []);
  assert.deepEqual(optionsFor(undefined, { options: [effort] }), [effort]);
});

test("prefill is the connection's pin: model and options, never mode or permission", () => {
  assert.equal(prefillConfig(undefined), undefined);
  assert.equal(prefillConfig({ newTaskConfig: null }), undefined);
  const pin = { model: '["claude","haiku"]', options: [{ id: "effort", value: "high" }], permission: "auto", mode: "plan" };
  assert.deepEqual(prefillConfig({ newTaskConfig: pin }), { model: pin.model, options: pin.options, permission: "", mode: "" });
});

// Connections 1 and 2 on the main machine (1), 3 and 4 on machine 2.
const machineOf = (id) => (id >= 3 ? 2 : 1);
const b = (connectionId, status = "available", connectionActive = true, primary = true) => ({ connectionId, status, connectionActive, primary });

test("new-task connection: default, else the only binding, else the first available", () => {
  const cases = [
    ["no default, one binding", [b(3)], null, 3],
    ["no default, several workspaces on one connection", [b(3), b(3, "available", true, false)], null, 3],
    ["no default, one missing binding", [b(3, "missing")], null, 3],
    ["no default, two bindings", [b(1), b(3)], null, 0],
    ["replaced binding only", [b(1, "available", false)], null, 0],
    ["no bindings", [], 1, 0],
    ["default bound", [b(1), b(3)], 3, 3],
    ["default bound but missing", [b(1), b(3, "missing")], 3, 1],
    ["default's secondary can't replace its missing primary", [b(1), b(3, "missing"), b(3, "available", true, false)], 3, 1],
    ["default unbound, one binding", [b(4, "missing")], 1, 4],
    ["default unbound, main machine first", [b(3), b(2), b(4)], 1, 2],
    ["default unbound, then by machine and id", [b(4), b(3)], 1, 3],
    ["default unbound, none available", [b(3, "missing"), b(4, "changed")], 1, 0],
    ["default on a replaced binding", [b(1, "available", false), b(3)], 1, 3],
  ];
  for (const [name, bindings, def, want] of cases) assert.equal(newTaskConnection(bindings, def, machineOf), want, name);
});

test("capability lines: declared operations in Stage's order and words, with the harness's reason when unavailable", () => {
  const lines = capabilityLines({
    send: { available: false, reason: "Sessions take follow-ups in their own client." },
    launch: { available: true },
    images: { available: false },
    stop: { available: true, reason: "ignored while available" },
  });
  assert.deepEqual(lines, [
    { op: "launch", label: OPERATION_LABELS.launch, available: true, reason: null },
    { op: "stop", label: OPERATION_LABELS.stop, available: true, reason: null },
    { op: "images", label: OPERATION_LABELS.images, available: false, reason: null },
    { op: "send", label: OPERATION_LABELS.send, available: false, reason: "Sessions take follow-ups in their own client." },
  ]);
  assert.deepEqual(capabilityLines({}), []);
});

test("launch parts show labelled choices and explicit boolean overrides in declaration order", () => {
  const caps = {
    models: [{ id: "m1", label: "Model One", available: true, options: [{ id: "effort", label: "Reasoning effort", kind: "choice", values: ["low", "high"], valueLabels: { high: "High effort" }, default: "low" }] }],
    options: [{ id: "fast", label: "Fast mode", kind: "boolean", values: [true, false], default: false }],
    permissions: [{ id: "full", label: "Full access", available: true }],
    modes: [{ id: "plan", label: "Plan", available: true }],
    modeLabel: "Agent mode",
  };
  const config = { model: "m1", options: [{ id: "effort", value: "high" }, { id: "fast", value: true }, { id: "extra", value: "x" }], permission: "full", mode: "plan" };
  assert.deepEqual(launchParts(config, caps), [
    { label: "Model", value: "Model One" },
    { label: "Fast mode", value: "On" },
    { label: "Reasoning effort", value: "High effort" },
    { label: "extra", value: "x" },
    { label: "Permission", value: "Full access" },
    { label: "Agent mode", value: "Plan" },
  ]);
  const defaults = { model: "m1", options: [{ id: "effort", value: "low" }, { id: "fast", value: false }], permission: "", mode: "" };
  assert.deepEqual(launchParts(defaults, caps), [
    { label: "Model", value: "Model One" },
    { label: "Fast mode", value: "Off" },
  ], "an explicit Off survives even when Off is the harness default");
  assert.deepEqual(launchParts({ model: "", options: [], permission: "", mode: "" }, caps), []);
  assert.deepEqual(launchParts(null, caps), []);
  assert.deepEqual(launchParts({ model: "adopted", options: null, permission: "", mode: "" }, caps), [
    { label: "Model", value: "adopted" },
  ]);
  assert.deepEqual(launchParts({ model: "gone", options: [], permission: "auto", mode: "" }, undefined), [
    { label: "Model", value: "gone" },
    { label: "Permission", value: "auto" },
  ], "without capabilities: the saved ids");
});
