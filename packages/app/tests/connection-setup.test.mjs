import { test } from "node:test";
import assert from "node:assert/strict";
import { actionBody, availableSetupActions, clearSecrets, initialValues, settingsBody, splitToolbarActions } from "../src/lib/connectionSetup.ts";

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

test("settings preserve a saved Boolean and send explicit false when turned off", () => {
  const saved = [...current, { id: "newInstance", value: "true", set: true, source: "saved" }];
  const values = initialValues(fields, saved);
  assert.equal(values.newInstance, true);
  assert.deepEqual(settingsBody(fields, saved, values), {});
  assert.deepEqual(settingsBody(fields, saved, { ...values, newInstance: false }), { newInstance: false });
});

test("action input sends declared choices and required false and clears only secrets", () => {
  const input = [
    { id: "profile", kind: "choice", choices: [{ id: "review", label: "Review" }] },
    { id: "token", kind: "secret" },
    { id: "verify", kind: "bool", required: true },
  ];
  const values = { profile: "review", token: "one-time-code", verify: false };
  assert.deepEqual(actionBody(input, values), values);
  assert.deepEqual(clearSecrets(input, values), { profile: "review", token: "", verify: false });
});

test("a generic setup uses both declaration and live availability, for users only", () => {
  const base = { target: "connection", label: "Action", callers: ["user"], availability: { available: true } };
  const actions = [
    { ...base, id: "start" },
    { ...base, id: "repair" },
    { ...base, id: "export", availability: { available: false, reason: "Disabled by policy" } },
    { ...base, id: "agent", callers: ["agent"] },
  ];
  assert.deepEqual(availableSetupActions(actions, [
    { id: "repair", available: false, reason: "Start the relay first" },
    { id: "export", available: true },
  ]).map(({ id, availability }) => [id, availability]), [
    ["start", { available: true }],
    ["repair", { available: false, reason: "Start the relay first" }],
    ["export", { available: false, reason: "Disabled by policy" }],
  ]);
});

const sessionAction = (id, extra = {}) => ({ id, label: id, target: "session", destructive: false, callers: ["user", "agent"], availability: { available: true }, ...extra });

test("task toolbar: user actions without input become buttons, actions with input keep their form", () => {
  const action = sessionAction;
  const settle = action("settle", { confirm: "T3 settles this session." });
  const archive = action("archive", { availability: { available: false, reason: "Already archived in T3." } });
  const label = action("label", { input: [{ id: "name", label: "Name", kind: "text" }] });
  const noInput = action("refresh", { input: [] });
  const agentOnly = action("agent", { callers: ["agent"] });
  const { buttons, forms } = splitToolbarActions([settle, archive, label, noInput, agentOnly]);
  assert.deepEqual(buttons.map((a) => a.id), ["settle", "archive", "refresh"], "unavailable stays visible with its reason");
  assert.deepEqual(forms.map((a) => a.id), ["label"]);
});
