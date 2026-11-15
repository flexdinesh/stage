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

const stacked = { type: "worktree", baseRef: "origin/main", branch: null, base: "predecessor" };
const task = (extra = {}) => ({ state: "backlog", afterTaskId: 12, afterThreadId: null, workspace: stacked, ...extra });
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

test("predecessor cell shows only when there is a predecessor to name", () => {
  assert.deepEqual(
    workspaceOptions(null).map((o) => o.value),
    ["root", "worktree"],
  );
  assert.deepEqual(workspaceOptions("#12").at(-1), { value: "predecessor", label: "#12's branch" });
  assert.equal(workspaceOptions("session").at(-1).label, "session's branch");
  assert.equal(predecessorName({ afterTaskId: 12, afterThreadId: null }), "#12");
  assert.equal(predecessorName({ afterTaskId: null, afterThreadId: "th-1" }), "session");
  assert.equal(predecessorName({ afterTaskId: null, afterThreadId: null }), null);
});

test("a predecessor choice reverts to worktree once Runs after is cleared", () => {
  assert.equal(effectiveChoice("predecessor", "#12"), "predecessor");
  assert.equal(effectiveChoice("predecessor", null), "worktree");
  assert.equal(effectiveChoice("root", null), "root");
});

test("form choice maps to and from the workspace", () => {
  assert.deepEqual(choiceToWorkspace("predecessor", " origin/main ", " "), stacked);
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
  assert.equal(predecessorWorkspaceText(task(), null), "worktree from #12's branch · fallback origin/main");
  assert.equal(predecessorWorkspaceText(task({ afterTaskId: null }), null), "worktree from predecessor's branch · fallback origin/main");
  assert.equal(predecessorWorkspaceText(task(), base()), "worktree from feat/api (#12)");
  assert.equal(
    predecessorWorkspaceText(task({ afterTaskId: null, afterThreadId: "th-1" }), base({ sourceTaskId: null, baseRef: "plan/x" })),
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
  assert.equal(pendingBaseNote(task()), "+ on launch: worktree from #12's branch (fallback origin/main)");
  assert.equal(pendingBaseNote(task({ state: "running" })), null);
  assert.equal(pendingBaseNote(task({ afterTaskId: null })), null);
  assert.equal(pendingBaseNote(task({ workspace: { ...stacked, base: "ref" } })), null);
});
