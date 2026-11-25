import assert from "node:assert/strict";
import { test } from "node:test";
import { claudeChip, claudeDetails, claudeFix, claudeIdentity, claudeRowDetail, claudeStatusView, claudeSummary, commandText, configText, versionText } from "../src/lib/claude.ts";

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
  assert.deepEqual(claudeDetails(status()), details);
  assert.equal(claudeDetails(status({ details: undefined })), null);
  assert.deepEqual(claudeDetails(status({ details: { loggedIn: "yes", version: 2 } })), { binary: "", version: "", configDirectory: "", loggedIn: false, scope: false });
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
  const down = claudeStatusView(read(status({ connected: false, state: "error", reason: "claude --version didn't answer." })), "Claude Code");
  assert.deepEqual(down.detail, { lines: [], error: "claude --version didn't answer." });
  assert.equal(down.summary.tone, "bad");
  assert.deepEqual(claudeStatusView(read(undefined, { error: "fetch failed" }), "Claude Code").detail, { lines: [], error: "fetch failed" });
});
