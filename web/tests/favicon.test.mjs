import assert from "node:assert/strict";
import { test } from "node:test";
import { FAVICON_IDLE, FAVICON_NEED, faviconHref } from "../src/lib/favicon.ts";

test("favicon is idle while the count is unknown or zero", () => {
  assert.equal(faviconHref(undefined), FAVICON_IDLE);
  assert.equal(faviconHref(0), FAVICON_IDLE);
});

test("favicon shows the yellow step while anything needs input", () => {
  assert.equal(faviconHref(1), FAVICON_NEED);
  assert.equal(faviconHref(12), FAVICON_NEED);
});
