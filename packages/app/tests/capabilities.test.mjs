import { test } from "node:test";
import assert from "node:assert/strict";
import { optionsFor } from "../src/lib/capabilities.ts";

test("options are the harness-wide ones, then the model's own", () => {
  const sandbox = { id: "sandbox", values: ["read-only", "workspace"] };
  const effort = { id: "effort", values: ["low", "high"] };
  assert.deepEqual(optionsFor({ options: [sandbox] }, { options: [effort] }), [sandbox, effort]);
  assert.deepEqual(optionsFor({ options: [sandbox] }, undefined), [sandbox]);
  assert.deepEqual(optionsFor({}, { options: null }), []);
  assert.deepEqual(optionsFor(undefined, { options: [effort] }), [effort]);
});
