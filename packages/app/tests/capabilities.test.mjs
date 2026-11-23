import { test } from "node:test";
import assert from "node:assert/strict";
import { newTaskConnection, optionsFor, prefillConfig } from "../src/lib/capabilities.ts";

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
const b = (connectionId, status = "available", connectionActive = true) => ({ connectionId, status, connectionActive });

test("new-task connection: default, else the only binding, else the first available", () => {
  const cases = [
    ["no default, one binding", [b(3)], null, 3],
    ["no default, one missing binding", [b(3, "missing")], null, 3],
    ["no default, two bindings", [b(1), b(3)], null, 0],
    ["replaced binding only", [b(1, "available", false)], null, 0],
    ["no bindings", [], 1, 0],
    ["default bound", [b(1), b(3)], 3, 3],
    ["default bound but missing", [b(1), b(3, "missing")], 3, 1],
    ["default unbound, one binding", [b(4, "missing")], 1, 4],
    ["default unbound, main machine first", [b(3), b(2), b(4)], 1, 2],
    ["default unbound, then by machine and id", [b(4), b(3)], 1, 3],
    ["default unbound, none available", [b(3, "missing"), b(4, "changed")], 1, 0],
    ["default on a replaced binding", [b(1, "available", false), b(3)], 1, 3],
  ];
  for (const [name, bindings, def, want] of cases) assert.equal(newTaskConnection(bindings, def, machineOf), want, name);
});
