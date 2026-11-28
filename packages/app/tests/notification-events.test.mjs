import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";
import {
  NOTIFICATION_EVENTS,
  NOTIFICATION_EVENT_GROUPS,
  notificationEventLabel,
  notificationPath,
  notificationSubject,
} from "../src/lib/notification-events.ts";

const row = (extra = {}) => ({ event: "needs_input", taskId: null, treeId: null, scheduleId: null, url: "/?inbox", ...extra });

test("subject follows the event, then the task identity", () => {
  assert.equal(notificationSubject(row({ event: "tree_completed", taskId: 3 })), "tree");
  assert.equal(notificationSubject(row({ event: "schedule_started" })), "schedule");
  assert.equal(notificationSubject(row({ event: "schedule_skipped" })), "schedule");
  assert.equal(notificationSubject(row({ event: "completed", taskId: 4 })), "task");
  // A deleted task's row keeps its URL.
  assert.equal(notificationSubject(row({ event: "failed", url: "/tasks/9" })), "task");
  assert.equal(notificationSubject(row({ event: "done" })), "session");
});

test("done names its subject and never reads as completed", () => {
  assert.equal(notificationEventLabel("done"), "session finished");
  assert.equal(notificationEventLabel("done", "task"), "run finished");
  assert.equal(notificationEventLabel("completed", "task"), "completed");
  assert.equal(notificationEventLabel("tree_completed", "tree"), "tree completed");
  assert.equal(notificationEventLabel("schedule_skipped", "schedule"), "schedule skipped");
});

test("rows open their task, tree or schedule, else the inbox", () => {
  assert.equal(notificationPath(row({ taskId: 5, url: "/tasks/5" })), "/tasks/5");
  assert.equal(notificationPath(row({ event: "tree_completed", treeId: 6, url: "/trees/6" })), "/trees/6");
  assert.equal(notificationPath(row({ event: "schedule_skipped", scheduleId: 7, url: "/schedules/7" })), "/schedules/7");
  // Deleted trees and schedules keep their stored page; anything else opens the inbox.
  assert.equal(notificationPath(row({ event: "tree_completed", url: "/trees/6" })), "/trees/6");
  assert.equal(notificationPath(row({ event: "schedule_started", url: "/schedules/7" })), "/schedules/7");
  assert.equal(notificationPath(row({ event: "done", url: "/sessions/abc" })), "/?inbox");
  assert.equal(notificationPath(row({ url: "https://example.com/trees/1" })), "/?inbox");
});

test("settings groups list every server event once, in the server's order", () => {
  const source = readFileSync(new URL("../../../server/internal/db/store/notifications.go", import.meta.url), "utf8");
  const ids = new Map([...source.matchAll(/(Event\w+) NotificationEvent = "(\w+)"/g)].map((m) => [m[1], m[2]]));
  const list = source.match(/var NotificationEvents = \[\]NotificationEvent\{([^}]*)\}/);
  assert.ok(list);
  const server = list[1].split(",").map((s) => s.trim()).filter(Boolean).map((name) => ids.get(name));
  assert.deepEqual(NOTIFICATION_EVENTS, server);
  assert.deepEqual(NOTIFICATION_EVENT_GROUPS.map((g) => g.title), ["Tasks", "Trees", "Schedules", "Untracked sessions"]);
});
