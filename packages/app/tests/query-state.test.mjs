import assert from "node:assert/strict";
import { test } from "node:test";
import { queryStateView } from "../src/lib/queryState.ts";

const boom = new Error("server unreachable");

test("a failed background refetch keeps the cached view with the error above it", () => {
  assert.deepEqual(queryStateView({ isPending: false, error: boom, hasData: true }), { view: "content", banner: true });
  assert.deepEqual(queryStateView({ isPending: false, error: boom, hasData: true, empty: true }), { view: "empty", banner: true });
});

test("an error with nothing cached replaces the view", () => {
  assert.deepEqual(queryStateView({ isPending: false, error: boom, hasData: false }), { view: "error", banner: false });
});

test("pending, empty and loaded views carry no banner", () => {
  assert.deepEqual(queryStateView({ isPending: true, error: null, hasData: false }), { view: "pending", banner: false });
  assert.deepEqual(queryStateView({ isPending: false, error: null, hasData: true, empty: true }), { view: "empty", banner: false });
  assert.deepEqual(queryStateView({ isPending: false, error: undefined, hasData: true }), { view: "content", banner: false });
});
