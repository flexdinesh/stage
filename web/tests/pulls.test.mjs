import assert from "node:assert/strict";
import { test } from "node:test";
import { prLabel, prName, prTone, rowPulls, runModel, runQueued, runRan } from "../src/lib/pulls.ts";

const pr = (repository, number, state = "open") => ({ host: "github.com", repository, number, url: `https://github.com/${repository}/pull/${number}`, source: "created", state });

test("PR state tones stay inside the state palette", () => {
  assert.equal(prTone("open"), "run");
  assert.equal(prTone("merged"), "ok");
  assert.equal(prTone("MERGED"), "ok");
  assert.equal(prTone("closed"), "idle");
  assert.equal(prTone("draft"), "idle");
  assert.equal(prTone("something-new"), "idle");
});

test("PR labels drop the owner; names keep it", () => {
  assert.equal(prLabel(pr("deehomelab/k8s-cluster", 94)), "k8s-cluster#94");
  assert.equal(prLabel(pr("solo", 3)), "solo#3");
  assert.equal(prName(pr("flexdinesh/stage", 41)), "flexdinesh/stage #41");
});

test("rows show two PRs and count the rest", () => {
  assert.deepEqual(rowPulls([]), { shown: [], more: 0 });
  const three = [pr("o/a", 1), pr("o/b", 2), pr("o/c", 3)];
  const { shown, more } = rowPulls(three);
  assert.deepEqual(shown.map(prLabel), ["a#1", "b#2"]);
  assert.equal(more, 1);
  assert.equal(rowPulls(three.slice(0, 2)).more, 0);
});

const at = (s) => new Date(Date.UTC(2026, 10, 14) + s * 1000).toISOString();
const run = (over) => ({ runId: "r", ordinal: 1, status: "completed", providerInstanceId: "claudeAgent", model: "opus", executedModel: null, requestedAt: at(0), startedAt: at(0), completedAt: at(90), ...over });

test("run model shows the executed model, and both when they differ", () => {
  assert.equal(runModel(run({})), "opus");
  assert.equal(runModel(run({ executedModel: "opus" })), "opus");
  assert.equal(runModel(run({ executedModel: "opus-1101" })), "opus → opus-1101");
  assert.equal(runModel(run({ model: null, executedModel: "x" })), "x");
  assert.equal(runModel(run({ model: null })), "claudeAgent");
});

test("ran spans start to completion, or to now while running", () => {
  assert.equal(runRan(run({})), "1m");
  assert.equal(runRan(run({ completedAt: null }), Date.parse(at(45))), "45s");
  assert.equal(runRan(run({ startedAt: null, completedAt: null })), null);
});

test("queued is the wait before start, hidden under a second", () => {
  assert.equal(runQueued(run({ startedAt: at(0.4) })), null);
  assert.equal(runQueued(run({ startedAt: at(3 * 60 + 5) })), "3m");
  assert.equal(runQueued(run({ startedAt: at(7) })), "7s");
  assert.equal(runQueued(run({ startedAt: null })), null);
});
