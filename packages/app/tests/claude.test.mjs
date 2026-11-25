import assert from "node:assert/strict";
import { test } from "node:test";
import { approvalsText, claudeChip, claudeDetails, claudeFix, claudeIdentity, claudeRowDetail, claudeStatusView, claudeSummary, commandText, configText, updatesText, versionText } from "../src/lib/claude.ts";

const details = { binary: "/home/dee/.local/bin/claude", version: "2.1.332 (Claude Code)", configDirectory: "/home/dee/.claude", loggedIn: true, scope: true };
const status = (extra = {}) => ({
  kind: "claude",
  name: "Claude Code",
  connectionId: 7,
  configured: true,
  connected: true,
  state: "connected",
  fields: [{ id: "binary", value: "", set: false, source: "default" }],
  actions: [{ id: "connect", available: true }],
  details,
  ...extra,
});
const read = (data, extra = {}) => ({ data, checking: false, error: null, ...extra });

test("details are read defensively from the status", () => {
  assert.deepEqual(claudeDetails(status()), { ...details, events: null });
  assert.equal(claudeDetails(status({ details: undefined })), null);
  assert.deepEqual(claudeDetails(status({ details: { loggedIn: "yes", version: 2 } })), { binary: "", version: "", configDirectory: "", loggedIn: false, scope: false, events: null });
  const events = (e) => claudeDetails(status({ details: { ...details, events: e } }))?.events;
  assert.deepEqual(events({ mode: "push", lastEventAt: "2026-11-25T07:00:00Z" }), { mode: "push", reason: "", lastEventAt: "2026-11-25T07:00:00Z" });
  assert.deepEqual(events({ mode: "polling", reason: "No port.", lastEventAt: null }), { mode: "polling", reason: "No port.", lastEventAt: null });
  assert.equal(events({ mode: "webhook" }), null);
  assert.equal(events("push"), null);
});

test("updates: instant while hooks push (last hook or none yet), every few seconds with the reason while polling", () => {
  const now = Date.parse("2026-11-25T07:02:00Z");
  assert.deepEqual(updatesText({ mode: "push", reason: "", lastEventAt: "2026-11-25T07:00:00Z" }, now), { text: "instant · last hook 2m ago", reason: null });
  assert.deepEqual(updatesText({ mode: "push", reason: "", lastEventAt: null }, now), { text: "instant · no hook yet", reason: null });
  const why = "Stage couldn't open its local hook port (address in use). It checks Claude Code every few seconds instead.";
  assert.deepEqual(updatesText({ mode: "polling", reason: why, lastEventAt: null }, now), { text: "every few seconds", reason: why });
  assert.deepEqual(updatesText({ mode: "polling", reason: "", lastEventAt: null }, now), { text: "every few seconds", reason: null });
});

test("chip: connected ok, not connected idle, a problem bad only once set up", () => {
  assert.deepEqual(claudeChip(status()), { tone: "ok", label: "connected" });
  assert.deepEqual(claudeChip(status({ configured: false, connected: false, state: "disconnected" })), { tone: "idle", label: "not connected" });
  assert.deepEqual(claudeChip(status({ connected: false, state: "logged_out" })), { tone: "bad", label: "logged out" });
  assert.deepEqual(claudeChip(status({ configured: false, connected: false, state: "not_found" })), { tone: "idle", label: "not found" });
  assert.deepEqual(claudeChip(status({ connected: false, state: "error" })), { tone: "bad", label: "not answering" });
  assert.deepEqual(claudeChip(status({ connected: false, state: "rebooting" })), { tone: "bad", label: "rebooting" });
});

test("config folder: ~ only for Claude's default folder under a home", () => {
  assert.equal(configText("/home/dee/.claude"), "~/.claude");
  assert.equal(configText("/Users/dee/.claude"), "~/.claude");
  assert.equal(configText("/srv/claude-work"), "/srv/claude-work");
  assert.equal(versionText("2.1.332 (Claude Code)"), "2.1.332");
  assert.equal(versionText("2.1.332"), "2.1.332");
});

test("row detail: which Claude when connected, the reason when not", () => {
  assert.equal(claudeRowDetail(status()), "~/.claude · 2.1.332");
  assert.equal(claudeIdentity(status({ details: { ...details, version: "" } })), "~/.claude");
  const loggedOut = status({ connected: false, state: "logged_out", reason: "Claude Code isn't logged in." });
  assert.equal(claudeRowDetail(loggedOut), "Claude Code isn't logged in.");
  assert.equal(claudeRowDetail(status({ configured: false, connected: false, state: "disconnected", details: undefined })), "");
});

test("command: the saved Claude command, the one found on PATH, or the default name", () => {
  assert.deepEqual(commandText(status()), { command: "/home/dee/.local/bin/claude", source: "found on PATH" });
  const saved = { fields: [{ id: "binary", value: "/opt/claude", set: true, source: "saved" }] };
  assert.deepEqual(commandText(status({ ...saved, details: { ...details, binary: "/opt/claude" } })), { command: "/opt/claude", source: "saved" });
  assert.deepEqual(commandText(status({ ...saved, details: { ...details, binary: "" } })), { command: "/opt/claude", source: "saved" });
  assert.deepEqual(commandText(status({ details: undefined })), { command: "claude", source: "default" });
});

test("only a logged-out Claude is fixed in a terminal", () => {
  assert.equal(claudeFix({ state: "logged_out" }), "claude auth login");
  assert.equal(claudeFix({ state: "not_found" }), null);
  assert.equal(claudeFix({ state: "connected" }), null);
});

test("status panel: summary, identity line, reason as the error", () => {
  assert.deepEqual(claudeSummary(read(undefined, { checking: true }), "Claude Code"), { harness: "claude", state: "checking", tone: "idle", label: "Checking Claude Code" });
  assert.deepEqual(claudeSummary(read(undefined), "Claude Code"), { harness: "claude", state: "offline", tone: "bad", label: "Claude Code offline" });
  assert.deepEqual(claudeStatusView(read(status()), "Claude Code"), {
    summary: { harness: "claude", state: "connected", tone: "ok", label: "Claude Code connected" },
    detail: { lines: ["~/.claude · 2.1.332"], error: null },
  });
  const now = Date.parse("2026-11-25T07:02:00Z");
  const pushing = status({ details: { ...details, events: { mode: "push", lastEventAt: "2026-11-25T07:00:00Z" } } });
  assert.deepEqual(claudeStatusView(read(pushing), "Claude Code", now).detail, { lines: ["~/.claude · 2.1.332", "updates instant · last hook 2m ago"], error: null });
  const polling = status({ details: { ...details, events: { mode: "polling", reason: "No folder." } } });
  assert.deepEqual(claudeStatusView(read(polling), "Claude Code", now).detail, { lines: ["~/.claude · 2.1.332", "updates every few seconds"], error: null });
  assert.equal(claudeStatusView(read(polling), "Claude Code").summary.tone, "ok");
  const down = claudeStatusView(read(status({ connected: false, state: "error", reason: "claude --version didn't answer." })), "Claude Code");
  assert.deepEqual(down.detail, { lines: [], error: "claude --version didn't answer." });
  assert.equal(down.summary.tone, "bad");
  assert.deepEqual(claudeStatusView(read(undefined, { error: "fetch failed" }), "Claude Code").detail, { lines: [], error: "fetch failed" });
});

test("approvals: on the task while hooks push, else in Claude Code", () => {
  assert.equal(approvalsText({ mode: "push", reason: "", lastEventAt: null }), "on the task in Stage, or in Claude Code");
  assert.equal(approvalsText({ mode: "polling", reason: "No port.", lastEventAt: null }), "in Claude Code");
});
