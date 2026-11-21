import assert from "node:assert/strict";
import { test } from "node:test";
import {
  connectionOptions,
  countdown,
  machineStatusLine,
  machineTag,
  machineTone,
  pairingPhase,
  removePrompt,
  serverVersion,
  versionDiffers,
  withMachine,
} from "../src/lib/machines.ts";

const NOW = Date.parse("2026-11-22T10:00:00Z");
const kindName = (k) => ({ t3: "T3", opencode: "OpenCode" })[k] ?? k;
const local = { id: 1, label: "This computer", kind: "local", online: true, status: "online", lastSeenAt: null, version: "v1.6.0", connections: [] };
const desk = { id: 2, label: "desk", kind: "remote", online: false, status: "offline", lastSeenAt: "2026-11-22T09:56:00Z", version: "v1.5.0", connections: [] };
const conn = (id, harness, machineId) => ({ id, harness, machineId });

test("status line: online local, offline remote with last seen, never seen", () => {
  assert.equal(machineStatusLine(local, NOW), "online · v1.6.0");
  assert.equal(machineStatusLine(desk, NOW), "offline · seen 4m ago · v1.5.0");
  assert.equal(machineStatusLine({ ...desk, lastSeenAt: null, version: null }, NOW), "offline · never seen");
  assert.equal(machineTone(local), "ok");
  assert.equal(machineTone(desk), "idle");
});

test("version differs only for a remote machine with a known, different build", () => {
  const server = serverVersion([local, desk]);
  assert.equal(server, "v1.6.0");
  assert.equal(versionDiffers(desk, server), true);
  assert.equal(versionDiffers({ ...desk, version: "v1.6.0" }, server), false);
  assert.equal(versionDiffers({ ...desk, version: null }, server), false);
  assert.equal(versionDiffers(local, "v0"), false);
  assert.equal(serverVersion([desk]), null);
});

test("countdown is m:ss and stops at 0:00", () => {
  assert.equal(countdown("2026-11-22T10:10:00Z", NOW), "10:00");
  assert.equal(countdown("2026-11-22T10:00:59.200Z", NOW), "1:00");
  assert.equal(countdown("2026-11-22T10:00:05Z", NOW), "0:05");
  assert.equal(countdown("2026-11-22T09:59:00Z", NOW), "0:00");
  assert.equal(countdown("not a date", NOW), "0:00");
});

test("pairing phase: server joined/expired win; waiting expires by the clock", () => {
  const exp = "2026-11-22T10:05:00Z";
  assert.equal(pairingPhase(undefined, exp, NOW), "waiting");
  assert.equal(pairingPhase({ state: "waiting" }, exp, NOW), "waiting");
  assert.equal(pairingPhase({ state: "waiting" }, exp, Date.parse(exp)), "expired");
  assert.equal(pairingPhase({ state: "joined" }, exp, Date.parse(exp) + 1000), "joined");
  assert.equal(pairingPhase({ state: "expired" }, exp, NOW), "expired");
});

test("remove prompt names each connection's usage and what happens to tasks", () => {
  const lines = removePrompt(desk, [
    { harness: "t3", label: "env a", tasks: 12, bindings: 2 },
    { harness: "opencode", label: "", tasks: 1, bindings: 0 },
  ], kindName);
  assert.equal(lines[0], "Remove desk? Its worker is signed out at its next request and stops.");
  assert.match(lines[1], /^T3 env a \(12 tasks · 2 bindings\), OpenCode \(1 task · 0 bindings\) become read-only history/);
  assert.match(lines[1], /no longer launches on or follows them, including tasks running there/);
  assert.match(lines[2], /Backlog tasks and schedules .* stay until you move them/);
  assert.match(lines.at(-1), /Pair the machine again/);
  const one = removePrompt(desk, [{ harness: "t3", label: "", tasks: 0, bindings: 1 }], kindName);
  assert.match(one[1], /^T3 \(0 tasks · 1 binding\) becomes read-only history: .* follows it,/);
  const unknown = removePrompt(desk, [{ harness: "t3", label: "env b" }], kindName);
  assert.match(unknown[1], /^T3 env b becomes read-only history/);
  const none = removePrompt(desk, [], kindName);
  assert.deepEqual(none.slice(1, 2), ["It has no harness connections."]);
});

test("machine tag only with more than one machine", () => {
  assert.equal(machineTag([local], 1), undefined);
  assert.equal(machineTag([local, desk], 2), "desk");
  assert.equal(machineTag([local, desk], 9), undefined);
  assert.equal(withMachine("T3", undefined), "T3");
  assert.equal(withMachine("T3", "desk"), "T3 · desk");
});

test("connection options: one machine reads as today (no tag, no reorder)", () => {
  const opts = connectionOptions([conn(4, "opencode", 1), conn(3, "t3", 1)], [local], kindName);
  assert.deepEqual(opts, [
    { value: "4", label: "OpenCode", tag: undefined, chip: undefined },
    { value: "3", label: "T3", tag: undefined, chip: undefined },
  ]);
});

test("connection options: several machines tag each by machine, local first", () => {
  const opts = connectionOptions([conn(7, "t3", 2), conn(1, "t3", 1), conn(5, "opencode", 1)], [local, desk], kindName);
  assert.deepEqual(opts.map((o) => [o.value, o.label, o.tag]), [
    ["1", "T3", "This computer"],
    ["5", "OpenCode", "This computer"],
    ["7", "T3", "desk"],
  ]);
});

test("connection options: a chosen project's bindings list first; the rest are marked not bound", () => {
  const opts = connectionOptions([conn(1, "t3", 1), conn(5, "opencode", 1), conn(7, "t3", 2)], [local, desk], kindName, new Set([7]));
  assert.deepEqual(opts.map((o) => [o.value, o.tag, o.chip]), [
    ["7", "desk", undefined],
    ["1", "This computer", "not bound"],
    ["5", "This computer", "not bound"],
  ]);
});
