import assert from "node:assert/strict";
import { test } from "node:test";
import { SETTINGS_PATH, runningAs, settingsTab, settingsTabLabel, settingsTabs, versionLine } from "../src/lib/settings.ts";

test("personal settings has the original tabs and shared settings adds Account", () => {
  assert.deepEqual(
    settingsTabs(false).map((t) => [t.tab, t.label, t.to]),
    [
      ["general", "General", "/settings"],
      ["connections", "Connections", "/settings/connections"],
      ["notifications", "Notifications", "/settings/notifications"],
    ],
  );
  assert.deepEqual(settingsTabs(true).at(-1), { tab: "account", label: "Account", to: "/settings/account" });
  assert.equal(SETTINGS_PATH.general, "/settings");
});

test("a path picks its tab; unknown subpaths and the old machine pages fall back sensibly", () => {
  assert.equal(settingsTab("/settings"), "general");
  assert.equal(settingsTab("/settings/"), "general");
  assert.equal(settingsTab("/settings/connections"), "connections");
  assert.equal(settingsTab("/settings/connections/"), "connections");
  assert.equal(settingsTab("/settings/notifications"), "notifications");
  assert.equal(settingsTab("/settings/account"), "account");
  assert.equal(settingsTab("/settings/connectionsx"), "general");
  assert.equal(settingsTab("/settings/machines/2"), "general");
  assert.equal(settingsTabLabel("connections"), "Connections");
});

test("Info version line: version, short commit, API version; empty parts left out", () => {
  assert.equal(versionLine({ version: "v0.9.4", commit: "65bac1c785599de3", apiVersion: 7 }), "v0.9.4 · 65bac1c · api 7");
  assert.equal(versionLine({ version: "", commit: "", apiVersion: 7 }), "api 7");
});

test("Info running as: always the main machine, with how the desktop app's server runs when known", () => {
  assert.equal(runningAs(null), "main");
  assert.equal(runningAs("per-user service"), "main · per-user service");
});
