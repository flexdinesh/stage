import assert from "node:assert/strict";
import { test } from "node:test";
import { activityView, liveActivity } from "../src/lib/sessionActivity.ts";

const blank = { items: [], plan: [], files: [], model: "", truncated: false, at: "" };

test("an activity view may contain a plan or changed files before it has feed items", () => {
  assert.equal(activityView(blank).empty, true);
  assert.equal(activityView({ ...blank, plan: [{ text: "Review", status: "pending" }] }).empty, false);
  const files = [{ path: "main.go", additions: 0, deletions: 4, status: "modified" }];
  assert.deepEqual(activityView({ ...blank, files }), {
    empty: false, items: [], files: [{ ...files[0], added: "+0", removed: "-4" }],
  });
});

test("the feed keeps order and uses adapter titles and previews with readable fallbacks", () => {
  const items = [
    { id: "1", kind: "command", title: "Run checks", preview: " go test ", text: "Ignored message", status: "done", at: "2026-11-28T01:00:00Z" },
    { id: "2", kind: "message_agent", title: "", preview: "", text: " Review complete. ", status: "", at: "2026-11-28T02:00:00Z" },
  ];
  const view = activityView({ ...blank, items, details: { opaque: "slot only" }, truncated: true });
  assert.deepEqual(view.items.map(({ id, title, preview, status, at }) => [id, title, preview, status, at]), [
    ["1", "Run checks", "go test", "done", items[0].at],
    ["2", "Assistant message", "Review complete.", "", items[1].at],
  ]);
  assert.equal("details" in view, false);
});

test("activity polls only a current running or needs-input session", () => {
  for (const state of ["running", "needs_input"]) {
    assert.equal(liveActivity(true, state), true);
    assert.equal(liveActivity(false, state), false);
  }
  for (const state of ["done", "idle", "launching", "failed"]) assert.equal(liveActivity(true, state), false);
});
