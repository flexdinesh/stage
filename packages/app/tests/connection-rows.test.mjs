import assert from "node:assert/strict";
import { test } from "node:test";
import { addConnectionOptions, addConnectionReason, connectionRows, machineLive, machineRole } from "../src/lib/connectionRows.ts";

const main = { id: 1, label: "deebox", kind: "local", status: "online", connections: [] };
const studio = { id: 2, label: "studio", kind: "remote", status: "online", connections: [] };
const KINDS = [
  { kind: "t3", name: "T3 Code", connectable: true },
  { kind: "opencode", name: "OpenCode", connectable: true },
  { kind: "codex", name: "Codex", connectable: false },
];

test("the main machine is main, every other machine a worker", () => {
  assert.equal(machineRole(main), "main");
  assert.equal(machineRole(studio), "worker");
});

test("setup runs on the main machine and on online workers only", () => {
  assert.equal(machineLive(main), true);
  assert.equal(machineLive({ ...main, status: "offline" }), true);
  assert.equal(machineLive(studio), true);
  assert.equal(machineLive({ ...studio, status: "offline" }), false);
  assert.equal(machineLive({ ...studio, status: "needs_update" }), false);
});

test("rows: active connections that are set up; one planned before setup is not a row; unknown ones count", () => {
  const m = {
    connections: [
      { id: 1, harness: "t3", label: "controlbox" },
      { id: 2, harness: "opencode", label: "" },
      { id: 9, harness: "opencode", label: "vps:4096" },
    ],
  };
  const known = { 1: { configured: true }, 2: { configured: false } };
  assert.deepEqual(connectionRows(m, (id) => known[id]), [
    { id: 1, harness: "t3", label: "controlbox" },
    { id: 9, harness: "opencode", label: "vps:4096" },
  ]);
  assert.deepEqual(connectionRows({ connections: [] }, () => undefined), []);
});

test("add options: connectable harnesses only; connected ones are listed disabled", () => {
  assert.deepEqual(addConnectionOptions(KINDS, [{ harness: "t3" }]), [
    { kind: "t3", name: "T3 Code", reason: "already connected" },
    { kind: "opencode", name: "OpenCode", reason: null },
  ]);
});

test("Add connection is disabled offline, on another API version, while checking, and when nothing is left", () => {
  const some = addConnectionOptions(KINDS, [{ harness: "t3" }]);
  const all = addConnectionOptions(KINDS, [{ harness: "t3" }, { harness: "opencode" }]);
  assert.equal(addConnectionReason(main, some), null);
  assert.equal(addConnectionReason(main, all), "every harness is connected");
  assert.equal(addConnectionReason(main, null), "checking harnesses");
  assert.equal(addConnectionReason(main, []), "no harness to connect");
  assert.equal(addConnectionReason({ ...studio, status: "offline" }, some), "studio is offline");
  assert.equal(addConnectionReason({ ...studio, status: "needs_update" }, some), "studio needs an update");
});
