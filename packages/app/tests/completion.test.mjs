import assert from "node:assert/strict";
import { test } from "node:test";
import { autoCompletedMeta, autoStarts, provenanceText, startsText } from "../src/lib/completion.ts";

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

test("provenanceText says who completed the result", () => {
  assert.match(provenanceText("user"), /^Completed by you in Stage\./);
  assert.match(provenanceText("historical_settlement"), /^Migrated from historical T3 settlement\./);
  assert.equal(provenanceText("harness_settled"), "Completed automatically: settled in T3. This result stays unchanged when execution continues.");
  assert.equal(provenanceText("harness_archived"), "Completed automatically: archived in OpenCode. This result stays unchanged when execution continues.");
});

test("autoCompletedMeta labels inferred completions only", () => {
  assert.equal(autoCompletedMeta("harness_settled"), "via T3 settle");
  assert.equal(autoCompletedMeta("harness_archived"), "via OpenCode archive");
  assert.equal(autoCompletedMeta("user"), null);
  assert.equal(autoCompletedMeta(null), null);
});
