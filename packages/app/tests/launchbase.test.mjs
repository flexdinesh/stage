import assert from "node:assert/strict";
import { test } from "node:test";
import {
  choiceToWorkspace,
  effectiveChoice,
  pendingBaseNote,
  predecessorChip,
  predecessorName,
  predecessorWorkspaceText,
  workspaceChoice,
  workspaceOptions,
} from "../src/lib/launchbase.ts";

const stacked = { type: "worktree", baseRef: "origin/main", branch: null, base: "predecessor", baseTaskId: null };
const task = (extra = {}) => ({ state: "backlog", needs: [12], workspace: stacked, ...extra });
const base = (extra = {}) => ({
  attempt: 1,
  baseRef: "feat/api",
  startFromOrigin: false,
  source: "predecessor",
  sourceTaskId: 12,
  sourceThreadId: "th-1",
  sourceTitle: "Build API",
  reason: null,
  at: "2026-11-14T10:00:00Z",
  ...extra,
});

test("need's-branch cell shows only when the task has needs", () => {
  assert.deepEqual(
    workspaceOptions(null).map((o) => o.value),
    ["root", "worktree"],
  );
  assert.deepEqual(workspaceOptions("#12").at(-1), { value: "predecessor", label: "#12's branch" });
  assert.equal(workspaceOptions("a need").at(-1).label, "a need's branch");
});

test("predecessor name: the picked need, else the sole need, else a need, else none", () => {
  assert.equal(predecessorName(task()), "#12");
  assert.equal(predecessorName(task({ needs: [3, 5], workspace: { ...stacked, baseTaskId: 5 } })), "#5");
  assert.equal(predecessorName(task({ needs: [3, 5] })), "a need");
  // A stale baseTaskId (no longer a need) does not name the base.
  assert.equal(predecessorName(task({ needs: [3], workspace: { ...stacked, baseTaskId: 9 } })), "#3");
  assert.equal(predecessorName(task({ needs: [] })), null);
  assert.equal(predecessorName(task({ needs: [3, 5], workspace: { type: "root" } })), "a need");
});

test("a predecessor choice remains explicit when its need disappears", () => {
  assert.equal(effectiveChoice("predecessor", "#12"), "predecessor");
  assert.equal(effectiveChoice("predecessor", null), "predecessor");
  assert.equal(effectiveChoice("root", null), "root");
});

test("form choice maps to and from the workspace", () => {
  assert.deepEqual(choiceToWorkspace("predecessor", " origin/main ", " "), stacked);
  assert.deepEqual(choiceToWorkspace("predecessor", "origin/main", "", 5), { ...stacked, baseTaskId: 5 });
  // baseTaskId applies only to a predecessor choice.
  assert.equal("baseTaskId" in choiceToWorkspace("worktree", "origin/main", "", 5), false);
  assert.deepEqual(choiceToWorkspace("worktree", "origin/main", "feat/x"), {
    type: "worktree",
    baseRef: "origin/main",
    branch: "feat/x",
    base: "ref",
  });
  assert.deepEqual(choiceToWorkspace("root", "origin/main", "x"), { type: "root" });
  assert.equal(workspaceChoice(stacked), "predecessor");
  assert.equal(workspaceChoice({ type: "worktree", baseRef: "origin/main", branch: null }), "worktree");
  assert.equal(workspaceChoice({ type: "worktree", baseRef: "origin/main", branch: null, base: "ref" }), "worktree");
  assert.equal(workspaceChoice({ type: "root" }), "root");
});

test("config line names the planned base, then the resolved one", () => {
  assert.equal(predecessorWorkspaceText(task(), null), "worktree from #12's branch · requires available branch");
  assert.equal(predecessorWorkspaceText(task({ needs: [3, 5] }), null), "worktree from a need's branch · requires available branch");
  assert.equal(predecessorWorkspaceText(task(), base()), "worktree from feat/api (#12)");
  assert.equal(
    predecessorWorkspaceText(task(), base({ sourceTaskId: null, baseRef: "plan/x" })),
    "worktree from plan/x (Build API)",
  );
  assert.equal(
    predecessorWorkspaceText(task(), base({ source: "fallback", baseRef: "origin/main", reason: "#12 ran at project root" })),
    "worktree from origin/main · fallback",
  );
  assert.equal(
    predecessorWorkspaceText(task({ workspace: { ...stacked, branch: "feat/next" } }), base()),
    "worktree from feat/api (#12) → feat/next",
  );
  assert.equal(predecessorWorkspaceText(task({ workspace: { type: "root" } }), null), null);
  assert.equal(predecessorWorkspaceText(task({ workspace: { ...stacked, base: "ref" } }), null), null);
});

test("chip and pending note", () => {
  assert.equal(predecessorChip(task(), null), "worktree from #12's branch");
  assert.equal(predecessorChip(task(), base()), "worktree from feat/api");
  assert.equal(predecessorChip(task({ workspace: { type: "root" } }), null), null);
  assert.equal(pendingBaseNote(task()), "+ on launch: worktree from #12's branch (requires available branch)");
  assert.equal(pendingBaseNote(task({ state: "running" })), null);
  assert.equal(pendingBaseNote(task({ needs: [] })), null);
  assert.equal(pendingBaseNote(task({ needs: [3, 5], workspace: { ...stacked, baseTaskId: 5 } })), "+ on launch: worktree from #5's branch (requires available branch)");
  assert.equal(pendingBaseNote(task({ workspace: { ...stacked, base: "ref" } })), null);
});
