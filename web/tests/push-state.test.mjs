import assert from "node:assert/strict";
import { test } from "node:test";
import { pushStateFor } from "../src/platform/webPush.ts";

/** A secure desktop browser with push support and no permission decision yet. */
const base = { secure: true, ios: false, standalone: false, supported: true, permission: "default" };

test("insecure context reports insecure, not unsupported", () => {
  // Browsers hide the push APIs outside a secure context.
  assert.equal(pushStateFor({ ...base, secure: false, supported: false }), "insecure");
});

test("insecure wins over every other condition", () => {
  assert.equal(pushStateFor({ ...base, secure: false, ios: true }), "insecure");
  assert.equal(pushStateFor({ ...base, secure: false, permission: "denied" }), "insecure");
  assert.equal(pushStateFor({ ...base, secure: false, permission: "granted" }), "insecure");
});

test("secure context without push APIs is unsupported", () => {
  assert.equal(pushStateFor({ ...base, supported: false }), "unsupported");
});

test("iOS browser tab must install first; the Home Screen app proceeds", () => {
  assert.equal(pushStateFor({ ...base, ios: true, supported: false }), "ios-install");
  assert.equal(pushStateFor({ ...base, ios: true, standalone: true }), "off");
});

test("permission maps to blocked, off, or a subscription lookup", () => {
  assert.equal(pushStateFor({ ...base, permission: "denied" }), "blocked");
  assert.equal(pushStateFor(base), "off");
  assert.equal(pushStateFor({ ...base, permission: "granted" }), null);
});
