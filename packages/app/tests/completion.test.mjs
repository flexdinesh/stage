import assert from "node:assert/strict";
import { test } from "node:test";
import { autoCompletedMeta, autoStarts, provenanceText, startsText, stateMove, stateSummary } from "../src/lib/completion.ts";

const dep = (taskId, claimed, startMode) => ({ taskId, title: `t${taskId}`, claimed, startMode });
const preview = { dependents: [dep(12, false, "auto"), dep(13, false, "manual"), dep(14, true, "auto"), dep(15, false, null), dep(16, false, "auto")] };

test("autoStarts lists unclaimed auto dependents while the tree is open or absent", () => {
  assert.deepEqual(autoStarts(preview, { treeStatus: "open" }).map((d) => d.taskId), [12, 16]);
  assert.deepEqual(autoStarts(preview, { treeStatus: null }).map((d) => d.taskId), [12, 16]);
});

test("autoStarts is empty when the tree is closed or nothing auto-starts", () => {
  assert.deepEqual(autoStarts(preview, { treeStatus: "done" }), []);
  assert.deepEqual(autoStarts(preview, { treeStatus: "archived" }), []);
  assert.deepEqual(autoStarts({ dependents: [dep(13, false, "manual")] }, { treeStatus: "open" }), []);
});

test("startsText names each started task once", () => {
  assert.equal(startsText([]), "");
  assert.equal(startsText([{ taskId: 12 }]), "Starts #12");
  assert.equal(startsText([{ taskId: 12 }, { taskId: 13 }, { taskId: 12 }]), "Starts #12, #13");
});

// Closure copy comes from the task's harness descriptor (GET /harnesses), never from a harness list in the app.
const settles = { closures: [{ signal: "settled", provenance: "harness_settled", setting: "", completed: "Completed automatically: settled in Box.", via: "via Box settle" }] };
const archives = { closures: [{ signal: "archived", provenance: "harness_archived", setting: "", completed: "Completed automatically: archived in Crate.", via: "via Crate archive" }] };

test("provenanceText describes how the result was accepted", () => {
  assert.match(provenanceText("user"), /^Completed in Stage\./);
  assert.match(provenanceText("historical_settlement"), /^Migrated from historical T3 settlement\./);
  assert.equal(provenanceText("harness_settled", settles), "Completed automatically: settled in Box.");
  assert.equal(provenanceText("harness_archived", archives), "Completed automatically: archived in Crate.");
  // A descriptor that does not declare the closure (or none loaded yet) reads generically.
  assert.match(provenanceText("harness_archived", settles), /^Completed automatically when its harness closed the session\./);
  assert.match(provenanceText("harness_settled"), /^Completed automatically when its harness closed the session\./);
});

test("a new provenance uses its declared copy or the generic fallback", () => {
  const provenance = "harness_published";
  const descriptor = { closures: [{ provenance, signal: "published", setting: "", completed: "Completed when published.", via: "via publish" }] };
  assert.equal(provenanceText(provenance, descriptor), "Completed when published.");
  assert.equal(autoCompletedMeta(provenance, descriptor), "via publish");
  assert.equal(provenanceText(provenance, settles), "Completed automatically when its harness closed the session. This result stays unchanged when execution continues.");
  assert.equal(autoCompletedMeta(provenance, settles), "via harness");
});

test("autoCompletedMeta labels inferred completions only", () => {
  assert.equal(autoCompletedMeta("harness_settled", settles), "via Box settle");
  assert.equal(autoCompletedMeta("harness_archived", archives), "via Crate archive");
  assert.equal(autoCompletedMeta("harness_archived"), "via harness");
  assert.equal(autoCompletedMeta("user", settles), null);
  assert.equal(autoCompletedMeta("historical_settlement", settles), null);
  assert.equal(autoCompletedMeta(null), null);
});

const st = (extra = {}) => ({
  state: "backlog", readiness: null, acceptanceId: null, needs: [], startAt: null, completedAt: null, finishedAt: null, completionProvenance: null,
  treeId: null, treeStatus: null, adopted: false, effectiveStartMode: null, ...extra,
});
const NONE = new Set();
const NOW = Date.parse("2026-11-20T12:00:00Z");
const HOUR_AGO = "2026-11-20T11:00:00Z";

test("stateMove offers Completed for open work, with what accepting means", () => {
  assert.deepEqual(stateMove(st(), NONE), { kind: "complete", to: "completed", detail: "accept without a run", disabled: false });
  assert.equal(stateMove(st({ readiness: "ready", needs: [3] }), NONE).detail, "accept without a run");
  assert.equal(stateMove(st({ state: "done" }), NONE).detail, "review, then accept");
  assert.equal(stateMove(st({ state: "failed" }), NONE).detail, "accept as it ended");
});

test("stateMove disables Completed while execution is active", () => {
  for (const state of ["launching", "running", "needs_input"]) {
    assert.deepEqual(stateMove(st({ state }), NONE), { kind: "complete", to: "completed", detail: "stop execution first", disabled: true });
  }
});

const back = (extra, accepted = NONE) => stateMove(st({ acceptanceId: 7, ...extra }), accepted);
const accepted = (extra) => stateSummary(st({ state: "done", acceptanceId: 7, completedAt: HOUR_AGO, ...extra }), NOW, settles);

test("stateMove takes completed work back to the status it shows without acceptance", () => {
  assert.deepEqual(back({ state: "done" }), { kind: "uncomplete", to: "to_review", detail: "clears acceptance", disabled: false });
  assert.equal(back({}).to, "backlog");
  assert.equal(back({ state: "running", treeId: 1, treeStatus: "open" }).to, "running");
});

test("stateMove derives a completed tree member's readiness the way the server does once acceptance clears", () => {
  const member = { treeId: 1, treeStatus: "open", effectiveStartMode: "auto" };
  assert.equal(back({ ...member }).to, "ready", "a root reads ready");
  assert.equal(back({ ...member, needs: [3] }).to, "blocked", "a need not accepted blocks it");
  assert.equal(back({ ...member, needs: [3] }, new Set([3])).to, "backlog", "auto with accepted needs launches; no readiness");
  assert.equal(back({ ...member, needs: [3], effectiveStartMode: "manual" }, new Set([3])).to, "ready");
  assert.equal(back({ ...member, startAt: "2026-11-21T09:00:00Z" }).to, "backlog", "a scheduled root waits for its time");
  assert.equal(back({ ...member, treeStatus: "done" }).to, "backlog", "a closed tree releases nothing");
  assert.equal(back({ ...member, adopted: true }).to, "backlog");
});

test("stateSummary says what the current state means", () => {
  assert.equal(stateSummary(st(), NOW), "Not run yet.");
  assert.equal(stateSummary(st({ startAt: HOUR_AGO }), NOW), "Scheduled. Not run yet.");
  assert.equal(stateSummary(st({ readiness: "ready", needs: [3] }), NOW), "Needs met. Not run yet.");
  assert.equal(stateSummary(st({ readiness: "ready" }), NOW), "Not run yet.");
  assert.equal(stateSummary(st({ readiness: "blocked", needs: [3, 5] }), NOW), "Waiting on #3, #5.");
  assert.equal(stateSummary(st({ state: "launching" }), NOW), "Launch not confirmed yet.");
  assert.equal(stateSummary(st({ state: "running" }), NOW), "Running. Stop it before completing.");
  assert.equal(stateSummary(st({ state: "needs_input" }), NOW), "Waiting for your input in the harness.");
  assert.equal(stateSummary(st({ state: "done", finishedAt: HOUR_AGO }), NOW), "Run finished 1h ago.");
  assert.equal(stateSummary(st({ state: "done" }), NOW), "Run finished.");
  assert.equal(stateSummary(st({ state: "interrupted" }), NOW), "Run interrupted.");
});

test("stateSummary for completed work names when and how it was accepted", () => {
  assert.equal(accepted({ completionProvenance: "user" }), "Accepted 1h ago.");
  assert.equal(accepted({ completionProvenance: "harness_settled" }), "Accepted 1h ago via Box settle.");
  assert.equal(accepted({ state: "running" }), "Accepted 1h ago. Execution moved on since.");
});
