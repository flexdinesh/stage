import assert from "node:assert/strict";
import { test } from "node:test";
import { reconcileToasts } from "../src/lib/notification-toasts.ts";

const notification = (id, extra = {}) => ({
  id,
  event: "needs_input",
  taskId: 7,
  t3ThreadId: "thread-a",
  title: `Event ${id}`,
  projectTitle: "Original project",
  url: "/tasks/7",
  createdAt: "2026-11-17T00:00:00Z",
  readAt: null,
  currentState: "needs_input",
  pendingQuestionCount: 1,
  t3DeepLink: "t3://thread-a",
  ...extra,
});

for (const currentState of ["running", "done", "failed", "cancelled", "settled"]) {
  test(`queued needs_input toast disappears when the entity is ${currentState}`, () => {
    const queued = notification(1);
    const latest = notification(1, { currentState, pendingQuestionCount: 0 });
    // Polls can change live fields without introducing a new notification ID.
    assert.deepEqual(reconcileToasts([queued], [latest]), []);
  });

  test(`new needs_input arrival already ${currentState} is suppressed`, () => {
    const arrival = notification(2, { currentState, pendingQuestionCount: 0 });
    assert.deepEqual(reconcileToasts([arrival], [arrival]), []);
  });
}

test("still-waiting toasts refresh question counts while preserving the event snapshot", () => {
  const queued = notification(1, { pendingQuestionCount: 3 });
  const latest = notification(2, {
    title: "Renamed task",
    projectTitle: "New project",
    url: "/sessions/thread-b",
    createdAt: "2026-11-17T01:00:00Z",
    readAt: "2026-11-17T01:01:00Z",
    t3ThreadId: "thread-b",
    t3DeepLink: "t3://thread-b",
    pendingQuestionCount: 0,
  });
  assert.deepEqual(reconcileToasts([queued], [latest]), [
    { ...queued, pendingQuestionCount: 0 },
  ]);
  assert.deepEqual(reconcileToasts([queued], [{ ...latest, pendingQuestionCount: 2 }]), [
    { ...queued, pendingQuestionCount: 2 },
  ]);
});

test("unknown live state and unmatched entities keep their needs_input toasts", () => {
  const unknown = notification(1);
  const unmatched = notification(2, { taskId: 8 });
  const alreadyUnknown = notification(3, { taskId: 9, currentState: null, pendingQuestionCount: 0 });
  const latest = notification(1, { currentState: null, pendingQuestionCount: 0 });
  assert.deepEqual(reconcileToasts([unknown, unmatched, alreadyUnknown], [latest]), [
    { ...unknown, currentState: null, pendingQuestionCount: 0 },
    unmatched,
    alreadyUnknown,
  ]);
  assert.deepEqual(reconcileToasts([unknown], []), [unknown]);
});

test("the highest notification ID supplies live state regardless of latest row order", () => {
  const queued = notification(1);
  const older = notification(2, { currentState: "running", pendingQuestionCount: 0 });
  const newest = notification(10, { pendingQuestionCount: 4 });
  const middle = notification(5, { currentState: "done", pendingQuestionCount: 0 });
  for (const latest of [[older, newest, middle], [newest, middle, older], [older, middle, newest]]) {
    assert.deepEqual(reconcileToasts([queued], latest), [{ ...queued, pendingQuestionCount: 4 }]);
  }
  assert.deepEqual(reconcileToasts([queued], [newest, notification(11, {
    event: "done", currentState: "done", pendingQuestionCount: 0,
  })]), []);
});

test("external threads reconcile by thread ID and keep unrelated threads", () => {
  const resolved = notification(1, { taskId: null });
  const waiting = notification(2, { taskId: null, t3ThreadId: "thread-b" });
  const unmatched = notification(3, { taskId: null, t3ThreadId: "thread-c" });
  const latest = [
    notification(10, { taskId: null, currentState: "settled", pendingQuestionCount: 0 }),
    notification(8, { taskId: null }),
    notification(11, { taskId: null, t3ThreadId: "thread-b", pendingQuestionCount: 3 }),
  ];
  assert.deepEqual(reconcileToasts([resolved, waiting, unmatched], latest), [
    { ...waiting, pendingQuestionCount: 3 }, unmatched,
  ]);
});

test("task identity wins over overlapping thread IDs, including external threads", () => {
  const task = notification(1);
  const otherTask = notification(2, { taskId: 8 });
  const external = notification(3, { taskId: null });
  const latest = [
    notification(10, { t3ThreadId: "replacement-thread", pendingQuestionCount: 2 }),
    notification(20, { taskId: 8, currentState: "done", pendingQuestionCount: 0 }),
    notification(30, { taskId: null, pendingQuestionCount: 4 }),
  ];
  assert.deepEqual(reconcileToasts([task, otherTask, external], latest), [
    { ...task, pendingQuestionCount: 2 },
    { ...external, pendingQuestionCount: 4 },
  ]);
});

test("notifications without a task or thread match only their own notification ID", () => {
  const queued = notification(1, { taskId: null, t3ThreadId: null });
  const unmatched = notification(2, { taskId: null, t3ThreadId: null });
  const latest = [
    notification(1, { taskId: null, t3ThreadId: null, currentState: "running", pendingQuestionCount: 0 }),
    notification(3, { taskId: null, t3ThreadId: null, currentState: "done", pendingQuestionCount: 0 }),
  ];
  assert.deepEqual(reconcileToasts([queued, unmatched], latest), [unmatched]);
});

test("task, external thread and notification ID namespaces remain distinct", () => {
  const task = notification(1);
  const external = notification(2, { taskId: null, t3ThreadId: "7" });
  const unbound = notification(7, { taskId: null, t3ThreadId: null });
  const latest = [notification(20, { taskId: 7, currentState: "done", pendingQuestionCount: 0 })];
  assert.deepEqual(reconcileToasts([task, external, unbound], latest), [external, unbound]);
});

test("other event types remain and refresh live fields when current state changes", () => {
  for (const event of ["ready", "done", "failed", "settled"]) {
    const queued = notification(1, { event, currentState: "done", pendingQuestionCount: 0 });
    for (const currentState of ["running", "done", "failed", "cancelled", "settled", "needs_input", null]) {
      const latest = notification(2, { currentState, pendingQuestionCount: 2 });
      assert.deepEqual(reconcileToasts([queued], [latest]), [
        { ...queued, currentState, pendingQuestionCount: 2 },
      ]);
    }
  }
});

test("reconciliation preserves queue order and inputs and never adds latest-only IDs", () => {
  const queued = Object.freeze([
    Object.freeze(notification(3, { taskId: 8, event: "done", currentState: "done" })),
    Object.freeze(notification(1)),
    Object.freeze(notification(2, { taskId: 9 })),
  ]);
  const latest = Object.freeze([
    Object.freeze(notification(10, { taskId: 9, pendingQuestionCount: 4 })),
    Object.freeze(notification(11, { taskId: 8, currentState: "running", pendingQuestionCount: 0 })),
    Object.freeze(notification(12, { currentState: "done", pendingQuestionCount: 0 })),
    Object.freeze(notification(13, { taskId: 10 })),
  ]);
  const beforeQueued = structuredClone(queued);
  const beforeLatest = structuredClone(latest);
  assert.deepEqual(reconcileToasts(queued, latest), [
    { ...queued[0], currentState: "running", pendingQuestionCount: 0 },
    { ...queued[2], pendingQuestionCount: 4 },
  ]);
  assert.deepEqual(queued, beforeQueued);
  assert.deepEqual(latest, beforeLatest);
  assert.deepEqual(reconcileToasts([], latest), []);
});
