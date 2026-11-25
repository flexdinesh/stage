import assert from "node:assert/strict";
import { test } from "node:test";
import { codexAccountText, codexDaemonText, codexDetails, codexState, codexVersionText, parseCodexStatus } from "../src/lib/codex.ts";
import { codexErrorText, codexRowDetail, codexStatusView, codexSummary, liveChip } from "../src/lib/connections.ts";
import { rowSessionAction } from "../src/lib/harnesses.ts";

const NOW = Date.parse("2026-11-25T10:00:00Z");

const details = {
  binary: "",
  cliVersion: "0.80.0",
  appServerVersion: "0.80.1",
  codexHome: "/home/u/.codex",
  daemon: "running",
  account: { type: "chatgpt", email: "dee@example.com", plan: "pro" },
  fix: "",
  checkedAt: "2026-11-25T09:58:00Z",
};

const wire = (over = {}, d = details) => ({
  kind: "codex",
  name: "Codex",
  connectionId: 7,
  configured: true,
  connected: true,
  state: "connected",
  fields: [{ id: "binary", value: "", set: false, source: "default" }],
  actions: [{ id: "connect", available: true }],
  details: d,
  ...over,
});

const loaded = (over, d) => ({ data: parseCodexStatus(wire(over, d)), checking: false, error: null });
const down = (state, reason, d = details) => loaded({ state, connected: false, reason }, d);

test("parseCodexStatus keeps a well-formed details object and the generic fields", () => {
  const s = parseCodexStatus(wire());
  assert.deepEqual(s.details, details);
  assert.equal(s.state, "connected");
  assert.equal(s.connectionId, 7);
  assert.deepEqual(s.actions, [{ id: "connect", available: true }]);
});

test("codexDetails reads omitted empties: absent strings as empty, absent account and checkedAt as null, absent daemon as unknown", () => {
  assert.deepEqual(codexDetails({}), { binary: "", cliVersion: "", appServerVersion: "", codexHome: "", daemon: "unknown", account: null, fix: "", checkedAt: null });
  assert.deepEqual(codexDetails({ ...details, account: { type: "apiKey" } }).account, { type: "apiKey", email: null, plan: null });
});

test("codexDetails: malformed details read as none", () => {
  for (const bad of [
    undefined,
    null,
    "codex",
    [],
    { ...details, cliVersion: 80 },
    { ...details, daemon: "sleeping" },
    { ...details, account: "chatgpt" },
    { ...details, account: { type: "github", email: null, plan: null } },
    { ...details, account: { type: "chatgpt", email: 1, plan: null } },
    { ...details, checkedAt: 0 },
    { ...details, fix: null },
  ]) {
    assert.equal(codexDetails(bad), null, JSON.stringify(bad));
  }
  assert.equal(parseCodexStatus(wire({}, { ...details, daemon: 1 })).details, null);
  const { details: _omit, ...noDetails } = wire();
  assert.equal(parseCodexStatus(noDetails).details, null);
});

test("codexState knows Codex's states and nothing else", () => {
  for (const s of ["connected", "not_connected", "signed_out", "unreachable", "not_installed"]) assert.equal(codexState(s), s);
  assert.equal(codexState("unpaired"), null);
});

test("codexSummary: each state's chip, tone and label", () => {
  assert.deepEqual(codexSummary(loaded()), { harness: "codex", state: "connected", tone: "ok", label: "Codex connected" });
  assert.deepEqual(codexSummary(down("signed_out")), { harness: "codex", state: "signed out", tone: "need", label: "Codex signed out" });
  assert.deepEqual(codexSummary(down("unreachable")), { harness: "codex", state: "unreachable", tone: "bad", label: "Codex unreachable" });
  assert.deepEqual(codexSummary(down("not_installed")), { harness: "codex", state: "not installed", tone: "bad", label: "Codex not installed" });
  assert.deepEqual(codexSummary(loaded({ state: "not_connected", connected: false, configured: false })), {
    harness: "codex",
    state: "not connected",
    tone: "idle",
    label: "Codex not connected",
  });
  assert.deepEqual(codexSummary({ data: undefined, checking: true, error: null }), { harness: "codex", state: "checking", tone: "idle", label: "Checking Codex" });
  assert.deepEqual(codexSummary({ data: undefined, checking: false, error: "Failed to fetch" }), { harness: "codex", state: "offline", tone: "bad", label: "Codex offline" });
  // A state this build does not know reads as a generic harness.
  assert.deepEqual(codexSummary(down("rate_limited")), { harness: "codex", state: "rate_limited", tone: "bad", label: "Codex rate_limited" });
});

test("codexRowDetail: CLI version and who it is signed in as", () => {
  assert.equal(codexRowDetail(loaded().data), "codex-cli 0.80.0 · dee@example.com");
  assert.equal(codexRowDetail(loaded({}, { ...details, account: { type: "apiKey", email: null, plan: null } }).data), "codex-cli 0.80.0 · API key");
  assert.equal(codexRowDetail(loaded({}, { ...details, account: { type: "chatgpt", email: null, plan: null } }).data), "codex-cli 0.80.0 · ChatGPT");
  assert.equal(codexRowDetail(down("signed_out", "Run codex login.", { ...details, account: null }).data), "codex-cli 0.80.0");
  assert.equal(codexRowDetail(down("not_installed", "codex not found", { ...details, cliVersion: "", account: null }).data), "");
  assert.equal(codexRowDetail(loaded({}, null).data), "");
});

test("codexStatusView and codexErrorText: detail lines, and the reason while not connected", () => {
  assert.deepEqual(codexStatusView(loaded(), NOW), {
    summary: codexSummary(loaded()),
    detail: { lines: ["codex-cli 0.80.0 · dee@example.com", "checked 2m ago"], error: null },
  });
  const signedOut = down("signed_out", "Codex isn't signed in. Run codex login, then connect again.", { ...details, account: null, checkedAt: null });
  assert.deepEqual(codexStatusView(signedOut, NOW).detail, { lines: ["codex-cli 0.80.0"], error: "Codex isn't signed in. Run codex login, then connect again." });
  assert.equal(codexErrorText(loaded({ connected: false, state: "unreachable", lastError: "app-server exited" })), "app-server exited");
  assert.equal(codexErrorText(down("not_connected", "")), null);
  assert.equal(codexErrorText({ data: undefined, checking: false, error: "Failed to fetch" }), "Failed to fetch");
  assert.deepEqual(codexStatusView({ data: undefined, checking: false, error: "Failed to fetch" }, NOW).detail, { lines: [], error: "Failed to fetch" });
});

test("a Config panel chip from Codex's summary links a problem to Settings", () => {
  const read = down("signed_out", "Run codex login.");
  const chip = liveChip(codexSummary(read), codexRowDetail(read.data), codexErrorText(read));
  assert.deepEqual(chip, { tone: "need", label: "signed out", title: "Codex signed out\ncodex-cli 0.80.0 · dee@example.com\nRun codex login.", fixable: true });
});

test("panel fact lines: versions, account and daemon", () => {
  assert.equal(codexVersionText(details), "codex-cli 0.80.0 · app-server 0.80.1");
  assert.equal(codexVersionText({ cliVersion: "", appServerVersion: "0.80.1" }), "app-server 0.80.1");
  assert.equal(codexVersionText({ cliVersion: "", appServerVersion: "" }), "");
  assert.equal(codexAccountText(details.account, "connected"), "dee@example.com · pro");
  assert.equal(codexAccountText({ type: "chatgpt", email: "dee@example.com", plan: null }, "connected"), "dee@example.com");
  assert.equal(codexAccountText({ type: "apiKey", email: null, plan: null }, "connected"), "API key");
  assert.equal(codexAccountText({ type: "amazonBedrock", email: null, plan: null }, "connected"), "Amazon Bedrock");
  assert.equal(codexAccountText({ type: "gcp", email: null, plan: null }, "connected"), "Google Cloud");
  assert.equal(codexAccountText(null, "signed_out"), "Not signed in");
  assert.equal(codexAccountText(null, "not_installed"), null);
  assert.equal(codexDaemonText("running"), "daemon running");
  assert.equal(codexDaemonText("stopped"), "daemon stopped");
  assert.equal(codexDaemonText("unknown"), null);
});

test("rowSessionAction: open the destination, else copy the resume command; primary while it needs input", () => {
  const cli = { links: { command: "codex resume 019a" } };
  const shown = { needsInput: false, hidden: false };
  assert.deepEqual(rowSessionAction("t3://thread/1", cli, shown), { kind: "open", href: "t3://thread/1", primary: false });
  assert.deepEqual(rowSessionAction(null, cli, shown), { kind: "resume", command: "codex resume 019a", primary: false });
  assert.deepEqual(rowSessionAction(null, cli, { needsInput: true, hidden: false }), { kind: "resume", command: "codex resume 019a", primary: true });
  assert.deepEqual(rowSessionAction("t3://thread/1", cli, { needsInput: true, hidden: false }), { kind: "open", href: "t3://thread/1", primary: true });
  // Neither a destination nor a command; a replaced connection offers no command; a confirming or just-completed row hides it.
  assert.equal(rowSessionAction(null, { links: {} }, shown), null);
  assert.equal(rowSessionAction(null, { ...cli, connectionActive: false }, shown), null);
  assert.equal(rowSessionAction(null, cli, { needsInput: true, hidden: true }), null);
  assert.equal(rowSessionAction("t3://thread/1", cli, { needsInput: false, hidden: true }), null);
});
