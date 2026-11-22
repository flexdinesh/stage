import assert from "node:assert/strict";
import { test } from "node:test";
import {
  DEFAULT_REPEAT,
  buildCron,
  cronSummary,
  lastRun,
  defaultStartLocal,
  localToInstant,
  parseCron,
  parsePreviewKey,
  previewKey,
  repeatSummary,
  toLocalInput,
  until,
  wallTime,
} from "../src/lib/schedules.ts";

const spec = (patch) => ({ ...DEFAULT_REPEAT, ...patch });

test("presets build standard cron", () => {
  assert.equal(buildCron(spec({ preset: "hourly", minute: 15 })), "15 * * * *");
  assert.equal(buildCron(spec({ preset: "daily", minute: 0, hour: 2 })), "0 2 * * *");
  assert.equal(buildCron(spec({ preset: "weekdays", minute: 30, hour: 9 })), "30 9 * * 1-5");
  assert.equal(buildCron(spec({ preset: "weekly", minute: 0, hour: 2, weekday: 1 })), "0 2 * * 1");
  assert.equal(buildCron(spec({ preset: "cron", cron: "  0  */6 * * * " })), "0 */6 * * *");
});

test("parseCron round-trips every preset", () => {
  for (const s of [
    spec({ preset: "hourly", minute: 15 }),
    spec({ preset: "daily", minute: 5, hour: 23 }),
    spec({ preset: "weekdays", minute: 30, hour: 9 }),
    spec({ preset: "weekly", minute: 0, hour: 2, weekday: 6 }),
  ]) {
    const expr = buildCron(s);
    const back = parseCron(expr);
    assert.equal(back.preset, s.preset, expr);
    assert.equal(buildCron(back), expr);
  }
});

test("parseCron falls back to raw cron for anything else", () => {
  for (const expr of ["0 */6 * * *", "0 2 1 * *", "0 2 * 1 *", "0 2 * * 1,3", "@daily", "61 2 * * *", "0 24 * * *", "0 2 * *"]) {
    const s = parseCron(expr);
    assert.equal(s.preset, "cron", expr);
    assert.equal(s.cron, expr);
  }
  assert.equal(parseCron("0 2 * * 7").weekday, 0, "7 is Sunday");
});

test("summaries read as plain language", () => {
  assert.equal(repeatSummary(spec({ preset: "daily", minute: 0, hour: 2 })), "every day at 02:00");
  assert.equal(repeatSummary(spec({ preset: "weekdays", minute: 30, hour: 9 })), "weekdays at 09:30");
  assert.equal(repeatSummary(spec({ preset: "hourly", minute: 15 })), "every hour at :15");
  assert.equal(repeatSummary(spec({ preset: "weekly", minute: 0, hour: 2, weekday: 1 })), "Mondays at 02:00");
  assert.equal(cronSummary("0 */6 * * *"), "cron 0 */6 * * *");
});

test("wall time converts both ways in a zone", () => {
  const iso = "2026-11-11T20:30:00Z";
  assert.equal(toLocalInput(iso, "Asia/Kolkata"), "2026-11-12T02:00");
  assert.equal(localToInstant("2026-11-12T02:00", "Asia/Kolkata"), Date.parse(iso));
  assert.equal(localToInstant("2026-07-01T09:00", "America/New_York"), Date.parse("2026-07-01T13:00:00Z"));
  assert.equal(localToInstant("2026-12-01T09:00", "America/New_York"), Date.parse("2026-12-01T14:00:00Z"));
  assert.equal(localToInstant("nope", "UTC"), null);
});

test("default start is the next full hour of wall time", () => {
  const now = Date.parse("2026-11-11T20:10:30Z"); // 01:40:30 IST
  assert.equal(defaultStartLocal("Asia/Kolkata", now), "2026-11-12T02:00");
  assert.equal(defaultStartLocal("UTC", now), "2026-11-11T21:00");
});

test("wallTime shortens to the clock on the same day", () => {
  const now = Date.parse("2026-11-12T00:00:00Z");
  assert.equal(wallTime("2026-11-12T02:00:00Z", "UTC", { short: true, now }), "02:00");
  assert.equal(wallTime("2026-11-13T02:00:00Z", "UTC", { short: true, now }), "Nov 13 02:00");
  assert.equal(wallTime("2026-11-13T02:00:00Z", "UTC", { now }), "Nov 13 02:00");
});

test("until formats remaining time", () => {
  const now = 0;
  assert.equal(until(-5, now), "now");
  assert.equal(until((9 * 60 + 12) * 60_000, now), "9h 12m");
  assert.equal(until((3 * 24 + 4) * 3_600_000, now), "3d 4h");
});

test("lastRun flags only failed or fire-blocking runs", () => {
  assert.deepEqual(lastRun(131, "done"), { kind: "quiet", word: "finished" });
  assert.deepEqual(lastRun(7, "settled"), { kind: "quiet", word: "settled" });
  assert.deepEqual(lastRun(7, "cancelled"), { kind: "quiet", word: "cancelled" });
  assert.deepEqual(lastRun(7, "backlog"), { kind: "quiet", word: "pending" });
  assert.deepEqual(lastRun(208, "running"), { kind: "flag", skip: "skips while #208 runs" });
  assert.deepEqual(lastRun(208, "launching"), { kind: "flag", skip: "skips while #208 runs" });
  assert.deepEqual(lastRun(214, "needs_input"), { kind: "flag", skip: "skips until #214 is answered" });
  assert.deepEqual(lastRun(190, "failed"), { kind: "flag", skip: null });
  assert.deepEqual(lastRun(190, "interrupted"), { kind: "flag", skip: null });
});

test("a preview key is stable by value and round-trips its input", () => {
  // The debounced preview input compares by value; an object made each render would never settle.
  assert.equal(previewKey("0 9 * * *", "Europe/Paris"), previewKey("0 9 * * *", "Europe/Paris"));
  assert.deepEqual(parsePreviewKey(previewKey("*/5 * * * *", "UTC")), { cron: "*/5 * * * *", tz: "UTC" });
  assert.equal(parsePreviewKey(null), null);
});
