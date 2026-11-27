import assert from "node:assert/strict";
import { test } from "node:test";
import { newToastEvents, reconcileToasts } from "../src/lib/notification-toasts.ts";

const notification = (id, extra = {}) => ({
  id,
  event: "needs_input",
  taskId: 7,
  externalSessionId: "session-a",
  title: `Event ${id}`,
  projectTitle: "Original project",
  url: "/tasks/7",
  createdAt: "2026-11-17T00:00:00Z",
  readAt: null,
  currentState: "needs_input",
  links: { native: "t3://session-a" },
  ...extra,
});

test("new read events stay in shared history without producing a toast", () => {
  const read = notification(3, { readAt: "2026-11-17T00:00:00Z" });
  const first = notification(2);
  const latest = notification(4);
  const items = [latest, read, first, notification(1)];
  assert.deepEqual(newToastEvents(items, 1, false), [first, latest]);
  assert.deepEqual(newToastEvents(items, 1, true), [first, read, latest]);
  assert.equal(items.length, 4);
});

for (const currentState of ["running", "done", "failed", "cancelled"]) {
  test(`queued needs_input toast disappears when the entity is ${currentState}`, () => {
    const queued = notification(1);
    const latest = notification(1, { currentState,});
    // Polls can change live fields without introducing a new notification ID.
    assert.deepEqual(reconcileToasts([queued], [latest]), []);
  });

  test(`new needs_input arrival already ${currentState} is suppressed`, () => {
    const arrival = notification(2, { currentState,});
    assert.deepEqual(reconcileToasts([arrival], [arrival]), []);
  });
}

test("still-waiting toasts retain waiting state while preserving the event snapshot", () => {
  const queued = notification(1, {});
  const latest = notification(2, {
    title: "Renamed task",
    projectTitle: "New project",
    url: "/sessions/session-b",
    createdAt: "2026-11-17T01:00:00Z",
    readAt: "2026-11-17T01:01:00Z",
    externalSessionId: "session-b",
    links: { native: "t3://session-b" },
  });
  assert.deepEqual(reconcileToasts([queued], [latest]), [
    { ...queued,},
  ]);
  assert.deepEqual(reconcileToasts([queued], [{ ...latest,}]), [
    { ...queued,},
  ]);
});

test("unknown live state and unmatched entities keep their needs_input toasts", () => {
  const unknown = notification(1);
  const unmatched = notification(2, { taskId: 8 });
  const alreadyUnknown = notification(3, { taskId: 9, currentState: null,});
  const latest = notification(1, { currentState: null,});
  assert.deepEqual(reconcileToasts([unknown, unmatched, alreadyUnknown], [latest]), [
    { ...unknown, currentState: null,},
    unmatched,
    alreadyUnknown,
  ]);
  assert.deepEqual(reconcileToasts([unknown], []), [unknown]);
});

test("the highest notification ID supplies live state regardless of latest row order", () => {
  const queued = notification(1);
  const older = notification(2, { currentState: "running",});
  const newest = notification(10, {});
  const middle = notification(5, { currentState: "done",});
  for (const latest of [[older, newest, middle], [newest, middle, older], [older, middle, newest]]) {
    assert.deepEqual(reconcileToasts([queued], latest), [{ ...queued,}]);
  }
  assert.deepEqual(reconcileToasts([queued], [newest, notification(11, {
    event: "done", currentState: "done",
  })]), []);
});

test("external sessions reconcile by session ID and keep unrelated sessions", () => {
  const resolved = notification(1, { taskId: null });
  const waiting = notification(2, { taskId: null, externalSessionId: "session-b" });
  const unmatched = notification(3, { taskId: null, externalSessionId: "session-c" });
  const latest = [
    notification(10, { taskId: null, currentState: "done",}),
    notification(8, { taskId: null }),
    notification(11, { taskId: null, externalSessionId: "session-b",}),
  ];
  assert.deepEqual(reconcileToasts([resolved, waiting, unmatched], latest), [
    { ...waiting,}, unmatched,
  ]);
});

test("task identity wins over overlapping session IDs, including external sessions", () => {
  const task = notification(1);
  const otherTask = notification(2, { taskId: 8 });
  const external = notification(3, { taskId: null });
  const latest = [
    notification(10, { externalSessionId: "replacement-session",}),
    notification(20, { taskId: 8, currentState: "done",}),
    notification(30, { taskId: null,}),
  ];
  assert.deepEqual(reconcileToasts([task, otherTask, external], latest), [
    { ...task,},
    { ...external,},
  ]);
});

test("notifications without a task or session match only their own notification ID", () => {
  const queued = notification(1, { taskId: null, externalSessionId: null });
  const unmatched = notification(2, { taskId: null, externalSessionId: null });
  const latest = [
    notification(1, { taskId: null, externalSessionId: null, currentState: "running",}),
    notification(3, { taskId: null, externalSessionId: null, currentState: "done",}),
  ];
  assert.deepEqual(reconcileToasts([queued, unmatched], latest), [unmatched]);
});

test("task, external session and notification ID namespaces remain distinct", () => {
  const task = notification(1);
  const external = notification(2, { taskId: null, externalSessionId: "7" });
  const unbound = notification(7, { taskId: null, externalSessionId: null });
  const latest = [notification(20, { taskId: 7, currentState: "done",})];
  assert.deepEqual(reconcileToasts([task, external, unbound], latest), [external, unbound]);
});

test("other event types remain and refresh live fields when current state changes", () => {
  for (const event of ["ready", "done", "failed"]) {
    const queued = notification(1, { event, currentState: "done",});
    for (const currentState of ["running", "done", "failed", "cancelled", "needs_input", null]) {
      const latest = notification(2, { currentState,});
      assert.deepEqual(reconcileToasts([queued], [latest]), [
        { ...queued, currentState,},
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
    Object.freeze(notification(10, { taskId: 9,})),
    Object.freeze(notification(11, { taskId: 8, currentState: "running",})),
    Object.freeze(notification(12, { currentState: "done",})),
    Object.freeze(notification(13, { taskId: 10 })),
  ]);
  const beforeQueued = structuredClone(queued);
  const beforeLatest = structuredClone(latest);
  assert.deepEqual(reconcileToasts(queued, latest), [
    { ...queued[0], currentState: "running",},
    { ...queued[2],},
  ]);
  assert.deepEqual(queued, beforeQueued);
  assert.deepEqual(latest, beforeLatest);
  assert.deepEqual(reconcileToasts([], latest), []);
});
