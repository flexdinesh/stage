import { test } from "node:test";
import assert from "node:assert/strict";
import { actionBody, initialValues, settingsBody } from "../src/lib/connectionSetup.ts";

const fields = [
  { id: "endpoint", label: "Server URL", kind: "url" },
  { id: "browser", label: "Browser URL", kind: "url" },
  { id: "password", label: "Password", kind: "secret" },
  { id: "newInstance", label: "New instance", kind: "bool" },
];
const current = [
  { id: "endpoint", value: "http://default:1", set: false, source: "default" },
  { id: "browser", value: "http://saved:2", set: true, source: "saved" },
  { id: "password", value: "", set: true, source: "saved" },
];

test("form starts from current settings, never echoing secrets", () => {
  assert.deepEqual(initialValues(fields, current), { endpoint: "http://default:1", browser: "http://saved:2", password: "", newInstance: false });
});

test("settings body sends only what changed; every field is editable, defaults included", () => {
  const start = initialValues(fields, current);
  assert.deepEqual(settingsBody(fields, current, start), {}, "nothing changed");
  assert.deepEqual(settingsBody(fields, current, { ...start, endpoint: "http://other:1", browser: "http://new:3" }), { endpoint: "http://other:1", browser: "http://new:3" });
  assert.deepEqual(settingsBody(fields, current, { ...start, browser: "" }), { browser: null }, "cleared resets to the default");
  assert.deepEqual(settingsBody(fields, current, { ...start, password: "pw", newInstance: true }), { password: "pw", newInstance: true });
});

test("action body sends entered values and checked flags", () => {
  const input = [{ id: "link", label: "Link", kind: "secret" }, fields[3]];
  assert.deepEqual(actionBody(input, initialValues(input, [])), {});
  assert.deepEqual(actionBody(input, { link: "http://x/auth/connect/c", newInstance: true }), { link: "http://x/auth/connect/c", newInstance: true });
});
