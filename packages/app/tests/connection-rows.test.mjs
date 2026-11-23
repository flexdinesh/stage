import assert from "node:assert/strict";
import { test } from "node:test";
import { addConnectionOptions, addConnectionReason, connectShortcuts, connectedRows, connectionRows, machineLive, machineRole } from "../src/lib/connectionRows.ts";

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

test("rows: set-up connections, and disconnected ones that were set up or hold history; a never-set-up one is not a row; unknown ones count", () => {
  const m = {
    connections: [
      { id: 1, harness: "t3", label: "controlbox" },
      { id: 2, harness: "opencode", label: "" },
      { id: 9, harness: "opencode", label: "vps:4096" },
      { id: 4, harness: "t3", label: "desk" },
    ],
  };
  const known = {
    1: { active: true, configured: true, label: "controlbox", tasks: 0, bindings: 0 },
    2: { active: true, configured: false, label: "", tasks: 0, bindings: 0 },
    4: { active: true, configured: false, label: "desk", tasks: 400, bindings: 27 },
  };
  const rows = connectionRows(m, (id) => known[id]);
  assert.deepEqual(rows, [
    { id: 1, harness: "t3", label: "controlbox", disconnected: false },
    { id: 9, harness: "opencode", label: "vps:4096", disconnected: false },
    { id: 4, harness: "t3", label: "desk", disconnected: true },
  ]);
  assert.deepEqual(connectedRows(rows).map((r) => r.id), [1, 9]);
  assert.deepEqual(connectionRows({ connections: [] }, () => undefined), []);
});

test("add options: connectable harnesses only; connected ones are listed disabled", () => {
  assert.deepEqual(addConnectionOptions(KINDS, [{ harness: "t3" }]), [
    { kind: "t3", name: "T3 Code", reason: "already connected" },
    { kind: "opencode", name: "OpenCode", reason: null },
  ]);
  assert.deepEqual(addConnectionOptions(KINDS, [{ harness: "t3", disconnected: true }])[0], { kind: "t3", name: "T3 Code", reason: "disconnected; reconnect it" });
  assert.deepEqual(connectShortcuts(addConnectionOptions(KINDS, [{ harness: "t3", disconnected: true }])), [{ kind: "opencode", label: "Connect OpenCode" }]);
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

test("empty machine: a Connect button per harness it can add, none while options load", () => {
  assert.deepEqual(connectShortcuts(addConnectionOptions(KINDS, [])), [
    { kind: "t3", label: "Connect T3 Code" },
    { kind: "opencode", label: "Connect OpenCode" },
  ]);
  assert.deepEqual(connectShortcuts(addConnectionOptions(KINDS, [{ harness: "t3" }])), [{ kind: "opencode", label: "Connect OpenCode" }], "a connected harness is not offered");
  assert.deepEqual(connectShortcuts(null), [], "options loading: text only");
  assert.deepEqual(connectShortcuts([]), []);
});
