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

test("drawer footer on the native channel says notifications, never push or HTTPS", () => {
  for (const state of ["unsupported", "blocked", "off"]) {
    assert.equal(pushFooterNote(state, "native"), "Notifications are off on this device");
  }
  assert.equal(pushFooterNote("on", "native"), null);
  assert.equal(pushFooterNote(undefined, "native"), null);
  assert.equal(pushFooterNote("off", "web-push"), "Push is off on this device");
  assert.equal(pushFooterNote("insecure", "web-push"), "Push needs HTTPS or localhost");
});
