import assert from "node:assert/strict";
import { test } from "node:test";
import { addConnectionOptions, addConnectionReason, connectShortcuts, connectedRows, connectionRows, machineLive, machineRole } from "../src/lib/connectionRows.ts";

const main = { id: 1, label: "deebox", kind: "local", status: "online", connections: [] };
const studio = { id: 2, label: "studio", kind: "remote", status: "online", connections: [] };
const KINDS = [
  { kind: "t3", name: "T3 Code", localProcesses: false, sessions: "machine", available: true, connectable: true },
  { kind: "opencode", name: "OpenCode", localProcesses: false, sessions: "machine", available: true, connectable: true },
  { kind: "remote", name: "Remote", localProcesses: false, sessions: "cloud", available: true, connectable: false },
];
const WHY = "Proc runs only where Stage runs on your computer: use the desktop app, or start stage serve with --local-harnesses.";
const hosted = (over = {}) => ({ kind: "proc", name: "Proc", localProcesses: true, sessions: "machine", available: true, connectable: true, ...over });

test("the main machine is main, every other machine a worker", () => {
  assert.equal(machineRole(main), "main");
  assert.equal(machineRole(studio), "worker");
});

test("setup runs on the main machine and on online workers only", () => {
  assert.equal(machineLive(main), true);
  assert.equal(machineLive({ ...main, status: "offline" }), true);
  assert.equal(machineLive(studio), true);
  assert.equal(machineLive({ ...studio, status: "offline" }), false);
  assert.equal(machineLive({ ...studio, status: "negotiating" }), false);
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

test("add options: connectable harnesses are listed and existing ones are disabled", () => {
  assert.deepEqual(addConnectionOptions(KINDS, [{ harness: "t3" }]), [
    { kind: "t3", name: "T3 Code", reason: "already connected", detail: null },
    { kind: "opencode", name: "OpenCode", reason: null, detail: null },
  ]);
  assert.deepEqual(addConnectionOptions(KINDS, [{ harness: "t3", disconnected: true }])[0], { kind: "t3", name: "T3 Code", reason: "disconnected; reconnect it", detail: null });
  assert.deepEqual(connectShortcuts(addConnectionOptions(KINDS, [{ harness: "t3", disconnected: true }])), [{ kind: "opencode", label: "Connect OpenCode" }]);
});

test("Add connection is disabled when a machine cannot serve setup or has nothing to add", () => {
  const some = addConnectionOptions(KINDS, [{ harness: "t3" }]);
  const all = addConnectionOptions(KINDS, [{ harness: "t3" }, { harness: "opencode" }]);
  assert.equal(addConnectionReason(main, some), null);
  assert.equal(addConnectionReason(main, all), "every harness is connected");
  assert.equal(addConnectionReason(main, null), "checking harnesses");
  assert.equal(addConnectionReason(main, []), "no harness to connect");
  assert.equal(addConnectionReason({ ...studio, status: "offline" }, some), "studio is offline");
  assert.equal(addConnectionReason({ ...studio, status: "negotiating", statusReason: "Waiting for studio to finish connecting." }, some), "Waiting for studio to finish connecting.");
  assert.equal(addConnectionReason({ ...studio, status: "needs_update", statusReason: "Update Stage on the main machine." }, some), "Update Stage on the main machine.");
});

test("add options: harnesses requiring local processes are listed in either session placement", () => {
  assert.deepEqual(addConnectionOptions([hosted()], []), [{ kind: "proc", name: "Proc", reason: null, detail: null }], "allowed and set up: connect it");
  assert.deepEqual(addConnectionOptions([hosted({ sessions: "cloud", available: false, connectable: false, reason: WHY })], []), [{ kind: "proc", name: "Proc", reason: "unavailable here", detail: WHY }]);
  assert.deepEqual(
    addConnectionOptions([hosted({ available: false, connectable: false })], [])[0].detail,
    "Proc can't run on this machine.",
    "a missing server reason still explains it",
  );
  assert.deepEqual(addConnectionOptions([hosted({ connectable: false })], []), [{ kind: "proc", name: "Proc", reason: "no connection setup here", detail: null }]);
  // Unavailable wins over a row left from a machine profile that allowed it.
  assert.equal(addConnectionOptions([hosted({ available: false, connectable: false, reason: WHY })], [{ harness: "proc" }])[0].reason, "unavailable here");
  assert.deepEqual(connectShortcuts(addConnectionOptions([...KINDS, hosted({ available: false, connectable: false, reason: WHY })], [])), [
    { kind: "t3", label: "Connect T3 Code" },
    { kind: "opencode", label: "Connect OpenCode" },
  ], "an unavailable harness gets no shortcut");
});

test("Add connection stays openable while an unavailable harness explains itself", () => {
  const unavailable = hosted({ available: false, connectable: false, reason: WHY });
  const connected = [{ harness: "t3" }, { harness: "opencode" }];
  assert.equal(addConnectionReason(main, addConnectionOptions([...KINDS, unavailable], connected)), null);
  assert.equal(addConnectionReason(main, addConnectionOptions([...KINDS, hosted({ connectable: false })], connected)), "no harness to connect");
  assert.equal(addConnectionReason(main, addConnectionOptions(KINDS, connected)), "every harness is connected");
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
