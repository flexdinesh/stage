import assert from "node:assert/strict";
import { test } from "node:test";
import {
  backList,
  canGoBack,
  canGoForward,
  forwardList,
  historyIndex,
  labelCurrent,
  recordNavigation,
  routeKind,
  routeLabel,
  startHistory,
} from "../src/lib/history.ts";

/** Dashboard -> Tasks -> task 12, as pushes from a fresh tab. */
function visited() {
  let h = startHistory(0, "/");
  h = recordNavigation(h, 1, "PUSH", "/tasks");
  h = recordNavigation(h, 2, "PUSH", "/tasks/12");
  return h;
}

test("a fresh tab can go neither back nor forward", () => {
  const h = startHistory(0, "/tasks/5");
  assert.equal(canGoBack(h), false);
  assert.equal(canGoForward(h), false);
  assert.deepEqual(backList(h), []);
});

test("pushes go back but not forward", () => {
  const h = visited();
  assert.equal(canGoBack(h), true);
  assert.equal(canGoForward(h), false);
  assert.deepEqual(
    backList(h).map((j) => [j.delta, j.label]),
    [
      [-1, "Tasks"],
      [-2, "Dashboard"],
    ],
  );
});

test("going back keeps the pages ahead", () => {
  let h = visited();
  h = recordNavigation(h, 0, "POP", "/");
  assert.equal(canGoBack(h), false);
  assert.equal(canGoForward(h), true);
  assert.deepEqual(
    forwardList(h).map((j) => [j.delta, j.path]),
    [
      [1, "/tasks"],
      [2, "/tasks/12"],
    ],
  );
  h = recordNavigation(h, 1, "POP", "/tasks");
  assert.equal(canGoBack(h), true);
  assert.equal(canGoForward(h), true);
});

test("a push after going back drops the pages ahead", () => {
  let h = visited();
  h = recordNavigation(h, 1, "POP", "/tasks");
  h = recordNavigation(h, 2, "PUSH", "/schedules");
  assert.equal(canGoForward(h), false);
  assert.deepEqual(
    backList(h).map((j) => j.path),
    ["/tasks", "/"],
  );
  h = recordNavigation(h, 1, "POP", "/tasks");
  assert.deepEqual(
    forwardList(h).map((j) => j.path),
    ["/schedules"],
  );
});

test("a replace swaps the current page and keeps what lies ahead", () => {
  let h = visited();
  h = recordNavigation(h, 1, "POP", "/tasks");
  h = recordNavigation(h, 1, "REPLACE", "/tasks?view=closed");
  assert.equal(canGoForward(h), true);
  assert.equal(h.entries.get(1)?.path, "/tasks?view=closed");
});

test("after a reload Back works from the index; unknown earlier pages stay out of the list", () => {
  const h = startHistory(3, "/tasks/12");
  assert.equal(canGoBack(h), true);
  assert.equal(canGoForward(h), false);
  assert.deepEqual(backList(h), []);
});

test("a page's own name replaces the fallback, and survives going back and forth", () => {
  let h = visited();
  h = labelCurrent(h, "/tasks/12", "Fix login");
  h = recordNavigation(h, 1, "POP", "/tasks");
  assert.deepEqual(forwardList(h)[0], { delta: 1, path: "/tasks/12", label: "Fix login", kind: "task" });
  h = recordNavigation(h, 2, "POP", "/tasks/12");
  assert.equal(h.entries.get(2)?.label, "Fix login");
});

test("labelling a page that is no longer current changes nothing", () => {
  const h = visited();
  assert.equal(labelCurrent(h, "/tasks", "Tasks"), h);
  assert.equal(labelCurrent(labelCurrent(h, "/tasks/12", "A"), "/tasks/12", "A").entries.get(2)?.label, "A");
});

test("the list stops at its limit", () => {
  let h = startHistory(0, "/");
  for (let i = 1; i <= 14; i++) h = recordNavigation(h, i, "PUSH", `/tasks/${i}`);
  assert.equal(backList(h).length, 10);
  assert.equal(backList(h)[9]?.path, "/tasks/4");
});

test("historyIndex reads the router's index and tolerates anything else", () => {
  assert.equal(historyIndex({ usr: null, key: "k", idx: 4 }), 4);
  assert.equal(historyIndex(null), 0);
  assert.equal(historyIndex({ idx: "2" }), 0);
  assert.equal(historyIndex({ idx: -1 }), 0);
  assert.equal(historyIndex(undefined), 0);
});

test("route names and kinds", () => {
  assert.equal(routeLabel("/"), "Dashboard");
  assert.equal(routeLabel("/tasks?project=2"), "Tasks");
  assert.equal(routeLabel("/tasks/12"), "task 12");
  assert.equal(routeLabel("/trees/3?task=9"), "tree 3");
  assert.equal(routeLabel("/projects/4"), "project 4");
  assert.equal(routeLabel("/schedules/7"), "schedule 7");
  assert.equal(routeLabel("/settings"), "Settings");
  assert.equal(routeLabel("/settings/connections"), "Connections");
  assert.equal(routeLabel("/nowhere"), "/nowhere");
  assert.equal(routeKind("/tasks/12"), "task");
  assert.equal(routeKind("/tasks"), null);
});
