import assert from "node:assert/strict";
import { test } from "node:test";
import { idQuery, paletteHits } from "../src/lib/palette.ts";

test("idQuery reads a bare or #-prefixed positive ID", () => {
  assert.equal(idQuery("409"), 409);
  assert.equal(idQuery("#409"), 409);
  assert.equal(idQuery("  #7 "), 7);
  for (const q of ["", "#", "0", "#0", "#abc", "12a", "4 09", "##4", "-3", "1.5", "99999999999999999999"]) assert.equal(idQuery(q), null, q);
});

const list = [
  { id: 1, title: "Fix login" },
  { id: 40, title: "Phase 409 rollout" },
  { id: 409, title: "DB adapters" },
  { id: 410, title: "Retry 409 conflicts" },
];
const hits = (q, limit = 6) => paletteHits(list, q, limit).map((h) => [h.item.id, h.byId]);

test("an exact ID match comes first, then title matches", () => {
  assert.deepEqual(hits("409"), [
    [409, true],
    [40, false],
    [410, false],
  ]);
  assert.deepEqual(hits("#409"), [[409, true]]);
});

test("a partial ID does not match by ID", () => {
  assert.deepEqual(hits("40"), [
    [40, true],
    [410, false],
  ]);
  assert.deepEqual(hits("#40"), [[40, true]]);
  assert.deepEqual(hits("#4"), []);
  assert.deepEqual(hits("#41"), []);
});

test("a record whose ID and title both match appears once", () => {
  assert.deepEqual(paletteHits([{ id: 5, title: "Step 5" }], "5", 6), [{ item: { id: 5, title: "Step 5" }, byId: true }]);
});

test("title search is case-insensitive and the limit holds with the ID hit first", () => {
  assert.deepEqual(hits("LOGIN"), [[1, false]]);
  assert.deepEqual(hits("409", 2), [
    [409, true],
    [40, false],
  ]);
  assert.deepEqual(hits(""), []);
});
