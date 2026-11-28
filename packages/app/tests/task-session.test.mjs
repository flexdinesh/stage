import assert from "node:assert/strict";
import { test } from "node:test";
import { matchRoutes } from "react-router";
import { parseAttempt, requestsPath, taskPath, taskSessionPath } from "../src/lib/taskSession.ts";

test("Task, current Session and historical reference paths share the task layout", () => {
  const routes = [{
    path: "/tasks/:id",
    children: [{ index: true, id: "task" }, { path: "session/:attempt?", id: "session" }],
  }];
  for (const [path, tab, attempt] of [
    [taskPath(533), "task", undefined],
    [taskSessionPath(533), "session", undefined],
    [taskSessionPath(533, 0), "session", 0],
    [taskSessionPath(533, 2), "session", 2],
    [requestsPath(533), "session", undefined],
  ]) {
    const match = matchRoutes(routes, path)?.at(-1);
    assert.equal(match?.route.id, tab);
    assert.equal(match?.params.id, "533");
    assert.equal(parseAttempt(match?.params.attempt), attempt);
  }
});

test("attempt parsing rejects non-decimal and unsafe indices", () => {
  for (const invalid of ["", "-1", "2.5", "1e2", "0x2", "9007199254740992"]) {
    assert.equal(parseAttempt(invalid), null);
  }
});
