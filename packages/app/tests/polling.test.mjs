import assert from "node:assert/strict";
import { test } from "node:test";
import { POLL, pollFirstPage } from "../src/lib/polling.ts";

test("an infinite list polls only while one page is loaded", () => {
  // A timed refetch re-requests every loaded page in sequence; after Load more it waits for invalidation.
  assert.equal(pollFirstPage(undefined, POLL.live), POLL.live);
  assert.equal(pollFirstPage(1, POLL.live), POLL.live);
  assert.equal(pollFirstPage(2, POLL.live), false);
  assert.equal(pollFirstPage(9, POLL.slow), false);
});
