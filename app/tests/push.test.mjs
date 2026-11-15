import assert from "node:assert/strict";
import { test } from "node:test";
import { pushFooterNote } from "../src/lib/push.ts";

test("drawer footer names the HTTPS reason only for an insecure context", () => {
  assert.equal(pushFooterNote("insecure"), "Push needs HTTPS or localhost");
  for (const state of ["unsupported", "ios-install", "blocked", "off"]) {
    assert.equal(pushFooterNote(state), "Push is off on this device");
  }
  assert.equal(pushFooterNote("on"), null);
  assert.equal(pushFooterNote(undefined), null);
});
